package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/vimutter/rowpress-lab/csvpdf"
	"github.com/vimutter/rowpress-lab/pdfcsv"
)

const (
	maxCSVBytes       = 10 << 20
	maxMessageBytes   = 32 << 20
	maxTitleRunes     = 200
	requestTimeout    = 45 * time.Second
	heartbeatInterval = 25 * time.Second
	heartbeatTimeout  = 10 * time.Second
)

//go:embed static/index.html
var indexHTML []byte

type convertRequest struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Title      string `json:"title"`
	Delimiter  string `json:"delimiter"`
	CSV        string `json:"csv"`
	LogoBase64 string `json:"logo_base64"`
	PDFBase64  string `json:"pdf_base64"`
}

type serverMessage struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
	PDFToCSV bool   `json:"pdf_to_csv,omitempty"`
}

type basicAuthConfig struct {
	username string
	password string
}

type socket interface {
	Read(context.Context) (websocket.MessageType, []byte, error)
	Write(context.Context, websocket.MessageType, []byte) error
	Ping(context.Context) error
	CloseNow() error
	SetReadLimit(int64)
}

func newHandler(appContext context.Context, auth basicAuthConfig, logger *slog.Logger) http.Handler {
	return newHandlerWithExtractor(appContext, auth, logger, nil)
}

func newHandlerWithExtractor(appContext context.Context, auth basicAuthConfig, logger *slog.Logger, extractor pdfcsv.Extractor) http.Handler {
	protected := http.NewServeMux()
	protected.HandleFunc("GET /{$}", serveIndex)
	protected.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		serveWebSocket(appContext, logger, w, r, extractor)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", serveHealth)
	mux.Handle("/", basicAuth(auth, logger, protected))
	return securityHeaders(mux)
}

func serveIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(indexHTML)
}

func serveHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func basicAuth(config basicAuthConfig, logger *slog.Logger, next http.Handler) http.Handler {
	if config.username == "" && config.password == "" {
		return next
	}

	wantUsername := sha256.Sum256([]byte(config.username))
	wantPassword := sha256.Sum256([]byte(config.password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		gotUsername := sha256.Sum256([]byte(username))
		gotPassword := sha256.Sum256([]byte(password))
		credentialsMatch := subtle.ConstantTimeCompare(gotUsername[:], wantUsername[:]) &
			subtle.ConstantTimeCompare(gotPassword[:], wantPassword[:])
		if !ok || credentialsMatch != 1 {
			logger.Warn("authentication_failed", "path", r.URL.Path)
			w.Header().Set("WWW-Authenticate", `Basic realm="Rowpress", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func serveWebSocket(appContext context.Context, logger *slog.Logger, w http.ResponseWriter, r *http.Request, extractor pdfcsv.Extractor) {
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		logger.Warn("websocket_accept_failed", "error", err)
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(maxMessageBytes)
	started := time.Now()
	logger.Info("websocket_connected")
	runSession(appContext, logger, connection, extractor)
	logger.Info("websocket_disconnected", "duration_ms", time.Since(started).Milliseconds())
}

func runSession(appContext context.Context, logger *slog.Logger, connection socket, extractor pdfcsv.Extractor) {
	ctx, cancel := context.WithCancel(appContext)
	heartbeatFinished := make(chan struct{})
	go func() {
		defer close(heartbeatFinished)
		_ = keepAlive(ctx, heartbeatInterval, heartbeatTimeout, connection.Ping)
		_ = connection.CloseNow()
	}()
	defer func() {
		cancel()
		<-heartbeatFinished
	}()

	if err := writeServerMessage(ctx, connection, serverMessage{
		Type:     "ready",
		Message:  "Connected and ready",
		PDFToCSV: extractor != nil,
	}); err != nil {
		return
	}

	// Keep reading control frames while a conversion runs. Permit one queued
	// request; reject excess work rather than buffering uploads indefinitely.
	queued := &queuedSocket{socket: connection, messages: make(chan socketMessage, 1)}
	readerFinished := make(chan struct{})
	go func() {
		defer close(readerFinished)
		defer cancel()
		for {
			kind, data, err := connection.Read(ctx)
			if err != nil {
				return
			}
			select {
			case queued.messages <- socketMessage{kind, data}:
			default:
				return
			}
		}
	}()
	defer func() {
		cancel()
		_ = connection.CloseNow()
		<-readerFinished
	}()
	for {
		keepGoing, err := handleNextRequest(ctx, logger, queued, extractor)
		if err != nil || !keepGoing {
			return
		}
	}
}

type socketMessage struct {
	kind websocket.MessageType
	data []byte
}

type queuedSocket struct {
	socket
	messages chan socketMessage
}

func (s *queuedSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case message := <-s.messages:
		return message.kind, message.data, nil
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func handleNextRequest(appContext context.Context, logger *slog.Logger, connection socket, extractor pdfcsv.Extractor) (bool, error) {
	messageType, data, err := connection.Read(appContext)
	if err != nil {
		return false, nil
	}
	if messageType != websocket.MessageText {
		return true, writeServerMessage(appContext, connection, serverMessage{
			Type:  "error",
			Error: "requests must be JSON text messages",
		})
	}

	var request convertRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return true, writeServerMessage(appContext, connection, serverMessage{
			Type:  "error",
			Error: "request is not valid JSON",
		})
	}
	if request.Type != "convert" && request.Type != "pdf-to-csv" {
		return true, writeServerMessage(appContext, connection, serverMessage{
			Type:  "error",
			ID:    request.ID,
			Error: "unsupported request type",
		})
	}
	if request.ID == "" || len(request.ID) > 64 {
		return true, writeServerMessage(appContext, connection, serverMessage{
			Type:  "error",
			Error: "request id must contain 1 to 64 characters",
		})
	}
	status := "Converting CSV to PDF"
	if request.Type == "pdf-to-csv" {
		status = "Extracting table from PDF…"
	}
	if err := writeServerMessage(appContext, connection, serverMessage{
		Type:    "status",
		ID:      request.ID,
		Message: status,
	}); err != nil {
		return false, err
	}

	started := time.Now()
	logger.Info(
		"conversion_started",
		"request_id", request.ID,
		"direction", request.Type,
		"csv_bytes", len(request.CSV),
		"logo_base64_bytes", len(request.LogoBase64),
	)
	var output []byte
	if request.Type == "pdf-to-csv" {
		output, err = convertCSV(appContext, request, extractor)
	} else {
		output, err = convertPDF(appContext, request)
	}
	if err != nil {
		logger.Warn(
			"conversion_failed",
			"request_id", request.ID,
			"duration_ms", time.Since(started).Milliseconds(),
			"error", err,
		)
		return true, writeServerMessage(appContext, connection, serverMessage{
			Type:  "error",
			ID:    request.ID,
			Error: err.Error(),
		})
	}
	logger.Info(
		"conversion_completed",
		"request_id", request.ID,
		"duration_ms", time.Since(started).Milliseconds(),
		"output_bytes", len(output),
	)
	return true, connection.Write(appContext, websocket.MessageBinary, output)
}

func convertCSV(appContext context.Context, request convertRequest, extractor pdfcsv.Extractor) ([]byte, error) {
	if err := appContext.Err(); err != nil {
		return nil, err
	}
	if extractor == nil {
		return nil, errors.New("PDF to CSV is unavailable: configure OPENAI_API_KEY on the server")
	}
	// Validate before spending an API request. Reuse delimiter validation.
	options, err := (convertRequest{Delimiter: request.Delimiter}).options()
	if err != nil {
		return nil, err
	}
	if options.Comma == '"' || options.Comma == '\r' || options.Comma == '\n' || options.Comma == 0 || options.Comma == utf8.RuneError {
		return nil, errors.New("invalid CSV delimiter")
	}
	if len(request.PDFBase64) > base64.StdEncoding.EncodedLen(pdfcsv.MaxPDFBytes) {
		return nil, errors.New("PDF must be 10 MiB or smaller")
	}
	pdf, err := base64.StdEncoding.DecodeString(request.PDFBase64)
	if err != nil {
		return nil, errors.New("PDF is not valid base64")
	}
	ctx, cancel := context.WithTimeout(appContext, 3*time.Minute)
	defer cancel()
	var output bytes.Buffer
	if err := pdfcsv.Convert(ctx, &output, bytes.NewReader(pdf), extractor, pdfcsv.Options{Comma: options.Comma}); err != nil {
		// Provider errors can include request or account details. Keep them out
		// of browser messages and application logs.
		return nil, errors.New("PDF extraction failed. Check the PDF and server API configuration, then try again")
	}
	return output.Bytes(), nil
}

func convertPDF(appContext context.Context, request convertRequest) ([]byte, error) {
	ctx, cancel := context.WithTimeout(appContext, requestTimeout)
	defer cancel()

	options, err := request.options()
	if err != nil {
		return nil, err
	}
	var pdf bytes.Buffer
	if err := csvpdf.Convert(ctx, &pdf, strings.NewReader(request.CSV), options); err != nil {
		return nil, err
	}
	return pdf.Bytes(), nil
}

func (request convertRequest) options() (csvpdf.Options, error) {
	if len(request.CSV) > maxCSVBytes {
		return csvpdf.Options{}, fmt.Errorf("CSV exceeds %d bytes", maxCSVBytes)
	}
	if utf8.RuneCountInString(request.Title) > maxTitleRunes {
		return csvpdf.Options{}, fmt.Errorf("title exceeds %d characters", maxTitleRunes)
	}

	delimiter := ','
	if request.Delimiter != "" {
		if utf8.RuneCountInString(request.Delimiter) != 1 {
			return csvpdf.Options{}, errors.New("delimiter must be exactly one character")
		}
		delimiter, _ = utf8.DecodeRuneInString(request.Delimiter)
	}

	return csvpdf.Options{
		Title: request.Title,
		Comma: delimiter,
		Logo: csvpdf.Logo{
			Base64: request.LogoBase64,
		},
	}, nil
}

func writeServerMessage(ctx context.Context, connection socket, message serverMessage) error {
	data, _ := json.Marshal(message)
	return connection.Write(ctx, websocket.MessageText, data)
}

func keepAlive(
	ctx context.Context,
	interval time.Duration,
	timeout time.Duration,
	ping func(context.Context) error,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			pingContext, cancel := context.WithTimeout(ctx, timeout)
			err := ping(pingContext)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}
