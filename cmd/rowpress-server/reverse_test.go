package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/vimutter/rowpress-lab/pdfcsv"
)

type extractFunc func(context.Context, []byte, string) ([][]string, error)

func (f extractFunc) Extract(ctx context.Context, data []byte, name string) ([][]string, error) {
	return f(ctx, data, name)
}

func TestReverseConversionValidation(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := convertCSV(canceled, convertRequest{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request = %v", err)
	}
	extractor := extractFunc(func(context.Context, []byte, string) ([][]string, error) {
		return [][]string{{"name", "score"}, {"Ada", "10"}}, nil
	})
	valid := base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 test"))
	for _, test := range []struct{ name, pdf, delimiter, want string }{
		{"success", valid, ";", ""},
		{"delimiter length", valid, "ab", "delimiter"},
		{"newline delimiter", valid, "\n", "delimiter"},
		{"quote delimiter", valid, "\"", "delimiter"},
		{"oversize", strings.Repeat("A", base64.StdEncoding.EncodedLen(pdfcsv.MaxPDFBytes)+1), "", "10 MiB"},
		{"base64", "!", "", "base64"},
		{"empty", "", "", "extraction failed"},
		{"not PDF", base64.StdEncoding.EncodeToString([]byte("hello")), "", "extraction failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := convertCSV(context.Background(), convertRequest{PDFBase64: test.pdf, Delimiter: test.delimiter}, extractor)
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil || string(data) != "name;score\nAda;10\n" {
				t.Fatalf("result = %q, %v", data, err)
			}
		})
	}
	if _, err := convertCSV(context.Background(), convertRequest{}, nil); err == nil {
		t.Fatal("missing extractor accepted")
	}
	secret := "provider error with private account information"
	failing := extractFunc(func(context.Context, []byte, string) ([][]string, error) { return nil, errors.New(secret) })
	_, err := convertCSV(context.Background(), convertRequest{PDFBase64: valid}, failing)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("provider error exposed: %v", err)
	}
}

func TestExtractorEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if extractorFromEnvironment() != nil {
		t.Fatal("enabled without key")
	}
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_MODEL", "test-model")
	if extractorFromEnvironment() == nil {
		t.Fatal("disabled with key")
	}
}

func TestWebSocketRejectsExcessQueuedUploads(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	extractor := extractFunc(func(ctx context.Context, _ []byte, _ string) ([][]string, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	})
	server := httptest.NewServer(newHandlerWithExtractor(context.Background(), basicAuthConfig{}, newLogger(io.Discard), extractor))
	defer server.Close()
	connection := dialWebSocket(t, server.URL)
	defer connection.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	readServerMessage(t, ctx, connection)
	request := marshal(t, convertRequest{Type: "pdf-to-csv", ID: "slow", PDFBase64: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7"))})
	if err := connection.Write(ctx, websocket.MessageText, request); err != nil {
		t.Fatal(err)
	}
	readServerMessage(t, ctx, connection)
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("extraction did not start")
	}
	for range 2 {
		if err := connection.Write(ctx, websocket.MessageText, request); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("excess uploads did not cancel extraction")
	}
}

func TestWebSocketBothDirectionsAndReverseErrorRecovery(t *testing.T) {
	extractor := extractFunc(func(ctx context.Context, data []byte, name string) ([][]string, error) {
		if !bytes.HasPrefix(data, []byte("%PDF-")) || name != "document.pdf" {
			t.Error("incorrect PDF input")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing extraction deadline")
		}
		return [][]string{{"name", "score"}, {"Ada", "10"}}, nil
	})
	server := httptest.NewServer(newHandlerWithExtractor(context.Background(), basicAuthConfig{}, newLogger(io.Discard), extractor))
	defer server.Close()
	connection := dialWebSocket(t, server.URL)
	defer connection.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !readServerMessage(t, ctx, connection).PDFToCSV {
		t.Fatal("reverse capability missing")
	}
	pdf := convertOverSocket(t, ctx, connection, convertRequest{Type: "convert", ID: "forward", CSV: "name,score\nAda,10\n"})
	for _, payload := range []string{"!", base64.StdEncoding.EncodeToString(pdf), base64.StdEncoding.EncodeToString(pdf)} {
		request := convertRequest{Type: "pdf-to-csv", ID: "reverse", PDFBase64: payload}
		if err := connection.Write(ctx, websocket.MessageText, marshal(t, request)); err != nil {
			t.Fatal(err)
		}
		if status := readServerMessage(t, ctx, connection); status.Type != "status" || status.ID != "reverse" {
			t.Fatalf("status = %+v", status)
		}
		if payload == "!" {
			if message := readServerMessage(t, ctx, connection); message.Type != "error" {
				t.Fatalf("message = %+v", message)
			}
			continue
		}
		kind, data, err := connection.Read(ctx)
		if err != nil || kind != websocket.MessageBinary || string(data) != "name,score\nAda,10\n" {
			t.Fatalf("result = %q, %v", data, err)
		}
	}
}

func TestWebSocketReadsPingsAndCancelsExtractionOnDisconnect(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	extractor := extractFunc(func(ctx context.Context, _ []byte, _ string) ([][]string, error) {
		close(started)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	})
	server := httptest.NewServer(newHandlerWithExtractor(context.Background(), basicAuthConfig{}, newLogger(io.Discard), extractor))
	defer server.Close()
	connection := dialWebSocket(t, server.URL)
	defer connection.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	readServerMessage(t, ctx, connection)
	request := convertRequest{Type: "pdf-to-csv", ID: "slow", PDFBase64: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7"))}
	if err := connection.Write(ctx, websocket.MessageText, marshal(t, request)); err != nil {
		t.Fatal(err)
	}
	readServerMessage(t, ctx, connection)
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("extraction did not start")
	}
	// A reader must run on the client too, to process the server's pong.
	readerDone := make(chan struct{})
	go func() { defer close(readerDone); _, _, _ = connection.Read(ctx) }()
	if err := connection.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	connection.CloseNow()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("disconnect did not cancel extraction")
	}
	<-readerDone
}
