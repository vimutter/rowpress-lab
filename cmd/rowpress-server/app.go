package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/vimutter/rowpress-lab/csvpdf"
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
}

type serverMessage struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
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

func newHandler(appContext context.Context, auth basicAuthConfig) http.Handler {
	protected := http.NewServeMux()
	protected.HandleFunc("GET /{$}", serveIndex)
	protected.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		serveWebSocket(appContext, w, r)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", serveHealth)
	mux.Handle("/", basicAuth(auth, protected))
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

func basicAuth(config basicAuthConfig, next http.Handler) http.Handler {
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

func serveWebSocket(appContext context.Context, w http.ResponseWriter, r *http.Request) {
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(maxMessageBytes)
	runSession(appContext, connection)
}

func runSession(appContext context.Context, connection socket) {
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
		Type:    "ready",
		Message: "Connected and ready",
	}); err != nil {
		return
	}

	for {
		keepGoing, err := handleNextRequest(ctx, connection)
		if err != nil || !keepGoing {
			return
		}
	}
}

func handleNextRequest(appContext context.Context, connection socket) (bool, error) {
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
	if request.Type != "convert" {
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
	if err := writeServerMessage(appContext, connection, serverMessage{
		Type:    "status",
		ID:      request.ID,
		Message: "Converting CSV to PDF",
	}); err != nil {
		return false, err
	}

	pdf, err := convertPDF(appContext, request)
	if err != nil {
		return true, writeServerMessage(appContext, connection, serverMessage{
			Type:  "error",
			ID:    request.ID,
			Error: err.Error(),
		})
	}
	return true, connection.Write(appContext, websocket.MessageBinary, pdf)
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
