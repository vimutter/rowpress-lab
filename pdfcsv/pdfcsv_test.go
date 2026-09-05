package pdfcsv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type extractorFunc func(context.Context, []byte, string) ([][]string, error)

func (function extractorFunc) Extract(ctx context.Context, pdf []byte, filename string) ([][]string, error) {
	return function(ctx, pdf, filename)
}

func TestConvertWritesCanonicalCSV(t *testing.T) {
	var output bytes.Buffer
	extractor := extractorFunc(func(_ context.Context, pdf []byte, filename string) ([][]string, error) {
		if !bytes.Equal(pdf, []byte("%PDF-demo")) || filename != "document.pdf" {
			t.Fatalf("extract input = %q, %q", pdf, filename)
		}
		return [][]string{{"name", "note"}, {"Ada", "hello; world"}}, nil
	})

	err := Convert(
		context.Background(),
		&output,
		strings.NewReader("%PDF-demo"),
		extractor,
		Options{Comma: ';'},
	)
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != "name;note\nAda;\"hello; world\"\n" {
		t.Fatalf("CSV = %q", output.String())
	}
}

func TestConvertRejectsNilArguments(t *testing.T) {
	extractor := extractorFunc(func(context.Context, []byte, string) ([][]string, error) { return nil, nil })
	tests := []struct {
		name      string
		ctx       context.Context
		dst       io.Writer
		src       io.Reader
		extractor Extractor
	}{
		{name: "context", dst: io.Discard, src: strings.NewReader("%PDF"), extractor: extractor},
		{name: "destination", ctx: context.Background(), src: strings.NewReader("%PDF"), extractor: extractor},
		{name: "source", ctx: context.Background(), dst: io.Discard, extractor: extractor},
		{name: "extractor", ctx: context.Background(), dst: io.Discard, src: strings.NewReader("%PDF")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Convert(test.ctx, test.dst, test.src, test.extractor, Options{}); err == nil {
				t.Fatal("Convert() error = nil")
			}
		})
	}
}

func TestConvertRejectsInvalidPDFInput(t *testing.T) {
	extractor := extractorFunc(func(context.Context, []byte, string) ([][]string, error) {
		t.Fatal("extractor should not be called")
		return nil, nil
	})
	tests := []struct {
		name string
		src  io.Reader
		want string
	}{
		{name: "read error", src: errorReader{}, want: "read PDF"},
		{name: "empty", src: strings.NewReader(""), want: "empty"},
		{name: "too large", src: bytes.NewReader(make([]byte, MaxPDFBytes+1)), want: "exceeds"},
		{name: "wrong type", src: strings.NewReader("not a PDF"), want: "PDF header"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Convert(context.Background(), io.Discard, test.src, extractor, Options{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Convert() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestConvertReportsExtractionAndCancellation(t *testing.T) {
	want := errors.New("model unavailable")
	extractor := extractorFunc(func(context.Context, []byte, string) ([][]string, error) {
		return nil, want
	})
	err := Convert(context.Background(), io.Discard, strings.NewReader("%PDF-demo"), extractor, Options{})
	if !errors.Is(err, want) {
		t.Fatalf("Convert() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	extractor = extractorFunc(func(context.Context, []byte, string) ([][]string, error) {
		cancel()
		return [][]string{{"a"}}, nil
	})
	err = Convert(ctx, io.Discard, strings.NewReader("%PDF-demo"), extractor, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Convert() error = %v, want context.Canceled", err)
	}
}

func TestConvertRejectsInvalidExtractedTables(t *testing.T) {
	manyRows := make([][]string, maxRows+1)
	for index := range manyRows {
		manyRows[index] = []string{"value"}
	}
	manyColumns := make([]string, maxColumns+1)
	tests := []struct {
		name string
		rows [][]string
		want string
	}{
		{name: "empty", want: "no table"},
		{name: "too many rows", rows: manyRows, want: "rows"},
		{name: "empty header", rows: [][]string{{}}, want: "no columns"},
		{name: "too many columns", rows: [][]string{manyColumns}, want: "columns"},
		{name: "uneven", rows: [][]string{{"a", "b"}, {"one"}}, want: "row 2"},
		{name: "large cell", rows: [][]string{{"a"}, {strings.Repeat("x", maxCellBytes+1)}}, want: "column 1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extractor := extractorFunc(func(context.Context, []byte, string) ([][]string, error) {
				return test.rows, nil
			})
			err := Convert(context.Background(), io.Discard, strings.NewReader("%PDF-demo"), extractor, Options{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Convert() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestConvertReportsCSVWriteErrors(t *testing.T) {
	extractor := extractorFunc(func(context.Context, []byte, string) ([][]string, error) {
		return [][]string{{"a"}, {"1"}}, nil
	})

	err := Convert(context.Background(), io.Discard, strings.NewReader("%PDF-demo"), extractor, Options{Comma: '\n'})
	if err == nil || !strings.Contains(err.Error(), "write CSV") {
		t.Fatalf("invalid delimiter error = %v", err)
	}
	err = Convert(context.Background(), failingWriter{}, strings.NewReader("%PDF-demo"), extractor, Options{})
	if err == nil || !strings.Contains(err.Error(), "write CSV") {
		t.Fatalf("writer error = %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
