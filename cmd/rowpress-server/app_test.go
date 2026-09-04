package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestHTTPRoutesAndSecurityHeaders(t *testing.T) {
	handler := newHandler(context.Background(), basicAuthConfig{}, newLogger(io.Discard))

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), "CSV → polished PDF") {
		t.Fatalf("GET / = %d %q", index.Code, index.Body.String())
	}
	if index.Header().Get("Content-Security-Policy") == "" || index.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers missing: %v", index.Header())
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || health.Body.String() != "ok\n" {
		t.Fatalf("GET /healthz = %d %q", health.Code, health.Body.String())
	}

	upgradeRequired := httptest.NewRecorder()
	handler.ServeHTTP(upgradeRequired, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if upgradeRequired.Code == http.StatusSwitchingProtocols {
		t.Fatal("ordinary HTTP request unexpectedly upgraded")
	}
}

func TestBasicAuthProtectsAppButNotHealthCheck(t *testing.T) {
	handler := newHandler(context.Background(), basicAuthConfig{username: "demo", password: "secret"}, newLogger(io.Discard))

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/", nil))
	if unauthorized.Code != http.StatusUnauthorized || unauthorized.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthenticated GET / = %d, headers %v", unauthorized.Code, unauthorized.Header())
	}

	wrong := httptest.NewRequest(http.MethodGet, "/", nil)
	wrong.SetBasicAuth("demo", "wrong")
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusUnauthorized {
		t.Fatalf("wrong credentials GET / = %d", wrongResponse.Code)
	}

	authorized := httptest.NewRequest(http.MethodGet, "/", nil)
	authorized.SetBasicAuth("demo", "secret")
	authorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(authorizedResponse, authorized)
	if authorizedResponse.Code != http.StatusOK {
		t.Fatalf("authenticated GET / = %d", authorizedResponse.Code)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("unauthenticated GET /healthz = %d", health.Code)
	}
}

func TestConvertRequestOptions(t *testing.T) {
	options, err := (convertRequest{Title: "Report", CSV: "a;b\n1;2\n", Delimiter: ";"}).options()
	if err != nil {
		t.Fatal(err)
	}
	if options.Title != "Report" || options.Comma != ';' {
		t.Fatalf("options = %#v", options)
	}

	options, err = (convertRequest{CSV: "a\n1\n"}).options()
	if err != nil || options.Comma != ',' {
		t.Fatalf("default options = %#v, %v", options, err)
	}
}

func TestConvertRequestRejectsLimitsAndDelimiter(t *testing.T) {
	tests := []struct {
		name    string
		request convertRequest
		want    string
	}{
		{name: "CSV", request: convertRequest{CSV: strings.Repeat("x", maxCSVBytes+1)}, want: "CSV exceeds"},
		{name: "title", request: convertRequest{Title: strings.Repeat("é", maxTitleRunes+1)}, want: "title exceeds"},
		{name: "delimiter", request: convertRequest{Delimiter: "||"}, want: "delimiter"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.request.options(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("options() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestConvertPDFRejectsInvalidOptions(t *testing.T) {
	_, err := convertPDF(context.Background(), convertRequest{Delimiter: "||"})
	if err == nil || !strings.Contains(err.Error(), "delimiter") {
		t.Fatalf("convertPDF() error = %v", err)
	}
}

func TestPersistentWebSocketConvertsMultipleFiles(t *testing.T) {
	server := httptest.NewServer(newHandler(context.Background(), basicAuthConfig{}, newLogger(io.Discard)))
	defer server.Close()
	connection := dialWebSocket(t, server.URL)
	defer connection.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ready := readServerMessage(t, ctx, connection)
	if ready.Type != "ready" {
		t.Fatalf("first message = %#v, want ready", ready)
	}

	first := convertRequest{
		Type:       "convert",
		ID:         "job-1",
		Title:      "Scores",
		CSV:        "name,score\nAda,10\n",
		LogoBase64: testLogoBase64(t),
	}
	firstPDF := convertOverSocket(t, ctx, connection, first)
	if !bytes.Contains(firstPDF, []byte("/Subtype /Image")) {
		t.Fatal("first PDF does not contain the logo")
	}

	second := convertRequest{Type: "convert", ID: "job-2", CSV: "city,country\nBerlin,Germany\n"}
	secondPDF := convertOverSocket(t, ctx, connection, second)
	if !bytes.HasPrefix(secondPDF, []byte("%PDF-")) {
		t.Fatal("second PDF is invalid")
	}
}

func TestBasicAuthProtectsWebSocketUpgrade(t *testing.T) {
	server := httptest.NewServer(newHandler(context.Background(), basicAuthConfig{username: "demo", password: "secret"}, newLogger(io.Discard)))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"

	unauthorized, response, err := websocket.Dial(context.Background(), url, nil)
	if unauthorized != nil {
		unauthorized.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated dial response = %#v, error = %v", response, err)
	}
	response.Body.Close()

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.SetBasicAuth("demo", "secret")
	connection, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: request.Header,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if ready := readServerMessage(t, ctx, connection); ready.Type != "ready" {
		t.Fatalf("first message = %#v, want ready", ready)
	}
}

func TestPersistentWebSocketReportsErrorsAndContinues(t *testing.T) {
	server := httptest.NewServer(newHandler(context.Background(), basicAuthConfig{}, newLogger(io.Discard)))
	defer server.Close()
	connection := dialWebSocket(t, server.URL)
	defer connection.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = readServerMessage(t, ctx, connection)

	tests := []struct {
		messageType websocket.MessageType
		payload     []byte
		want        string
	}{
		{messageType: websocket.MessageBinary, payload: []byte("binary"), want: "JSON text"},
		{messageType: websocket.MessageText, payload: []byte("{"), want: "valid JSON"},
		{messageType: websocket.MessageText, payload: marshal(t, convertRequest{Type: "unknown", ID: "bad-type"}), want: "unsupported"},
		{messageType: websocket.MessageText, payload: marshal(t, convertRequest{Type: "convert"}), want: "request id"},
		{messageType: websocket.MessageText, payload: marshal(t, convertRequest{Type: "convert", ID: strings.Repeat("x", 65)}), want: "request id"},
	}

	for _, test := range tests {
		if err := connection.Write(ctx, test.messageType, test.payload); err != nil {
			t.Fatal(err)
		}
		response := readServerMessage(t, ctx, connection)
		if response.Type != "error" || !strings.Contains(response.Error, test.want) {
			t.Fatalf("response = %#v, want error containing %q", response, test.want)
		}
	}

	conversionError := convertRequest{Type: "convert", ID: "empty-csv"}
	if err := connection.Write(ctx, websocket.MessageText, marshal(t, conversionError)); err != nil {
		t.Fatal(err)
	}
	status := readServerMessage(t, ctx, connection)
	response := readServerMessage(t, ctx, connection)
	if status.Type != "status" || response.Type != "error" || !strings.Contains(response.Error, "CSV input is empty") {
		t.Fatalf("status = %#v, response = %#v", status, response)
	}

	valid := convertRequest{Type: "convert", ID: "after-errors", CSV: "a\n1\n"}
	_ = convertOverSocket(t, ctx, connection, valid)
}

func TestHandleNextRequestReturnsWriteErrors(t *testing.T) {
	request := marshal(t, convertRequest{Type: "convert", ID: "job", CSV: "a\n1\n"})
	connection := &fakeSocket{
		reads:        []fakeRead{{messageType: websocket.MessageText, data: request}},
		writeErrorAt: 1,
	}
	keepGoing, err := handleNextRequest(context.Background(), newLogger(io.Discard), connection)
	if keepGoing || err == nil {
		t.Fatalf("handleNextRequest() = %v, %v; want write error", keepGoing, err)
	}

	connection = &fakeSocket{
		reads:        []fakeRead{{messageType: websocket.MessageText, data: request}},
		writeErrorAt: 2,
	}
	keepGoing, err = handleNextRequest(context.Background(), newLogger(io.Discard), connection)
	if !keepGoing || err == nil {
		t.Fatalf("handleNextRequest() = %v, %v; want PDF write error", keepGoing, err)
	}
}

func TestConversionLogsMetadataNotCSVContents(t *testing.T) {
	request := convertRequest{Type: "convert", ID: "logged-job", CSV: "name\nprivate-cell-value\n"}
	connection := &fakeSocket{
		reads: []fakeRead{{messageType: websocket.MessageText, data: marshal(t, request)}},
	}
	var logs strings.Builder

	keepGoing, err := handleNextRequest(context.Background(), newLogger(&logs), connection)
	if err != nil || !keepGoing {
		t.Fatalf("handleNextRequest() = %v, %v", keepGoing, err)
	}
	if !strings.Contains(logs.String(), `"msg":"conversion_started"`) ||
		!strings.Contains(logs.String(), `"msg":"conversion_completed"`) ||
		!strings.Contains(logs.String(), `"request_id":"logged-job"`) {
		t.Fatalf("conversion metadata missing from logs: %s", logs.String())
	}
	if strings.Contains(logs.String(), "private-cell-value") {
		t.Fatalf("CSV contents leaked into logs: %s", logs.String())
	}
}

func TestRunSessionStopsWhenReadyCannotBeWritten(t *testing.T) {
	connection := &fakeSocket{writeErrorAt: 1}
	runSession(context.Background(), newLogger(io.Discard), connection)
	if connection.closeCalls == 0 {
		t.Fatal("runSession() did not close the connection")
	}
}

func TestKeepAlive(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := keepAlive(canceled, time.Millisecond, time.Millisecond, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("keepAlive(canceled) = %v", err)
	}

	ctx, stop := context.WithCancel(context.Background())
	pings := 0
	err := keepAlive(ctx, time.Millisecond, time.Second, func(context.Context) error {
		pings++
		stop()
		return nil
	})
	if err != nil || pings != 1 {
		t.Fatalf("keepAlive(success) = %v, pings = %d", err, pings)
	}

	want := errors.New("pong timeout")
	err = keepAlive(context.Background(), time.Millisecond, time.Second, func(context.Context) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("keepAlive(error) = %v", err)
	}
}

func convertOverSocket(t *testing.T, ctx context.Context, connection *websocket.Conn, request convertRequest) []byte {
	t.Helper()
	if err := connection.Write(ctx, websocket.MessageText, marshal(t, request)); err != nil {
		t.Fatal(err)
	}
	status := readServerMessage(t, ctx, connection)
	if status.Type != "status" || status.ID != request.ID {
		t.Fatalf("status = %#v", status)
	}
	messageType, response, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageBinary || !bytes.HasPrefix(response, []byte("%PDF-")) {
		t.Fatalf("response type = %v, prefix = %q", messageType, response[:min(8, len(response))])
	}
	return response
}

func readServerMessage(t *testing.T, ctx context.Context, connection *websocket.Conn) serverMessage {
	t.Helper()
	messageType, data, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("message type = %v, want text", messageType)
	}
	var message serverMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func marshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func dialWebSocket(t *testing.T, serverURL string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws"
	connection, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func testLogoBase64(t *testing.T) string {
	t.Helper()
	logo := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			logo.Set(x, y, color.RGBA{R: 23, G: 107, B: 104, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, logo); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(data.Bytes())
}

type fakeRead struct {
	messageType websocket.MessageType
	data        []byte
	err         error
}

type fakeSocket struct {
	reads        []fakeRead
	writes       int
	writeErrorAt int
	closeCalls   int
}

func (connection *fakeSocket) Read(context.Context) (websocket.MessageType, []byte, error) {
	if len(connection.reads) == 0 {
		return 0, nil, errors.New("closed")
	}
	read := connection.reads[0]
	connection.reads = connection.reads[1:]
	return read.messageType, read.data, read.err
}

func (connection *fakeSocket) Write(context.Context, websocket.MessageType, []byte) error {
	connection.writes++
	if connection.writes == connection.writeErrorAt {
		return errors.New("write failed")
	}
	return nil
}

func (*fakeSocket) Ping(context.Context) error { return nil }
func (connection *fakeSocket) CloseNow() error {
	connection.closeCalls++
	return nil
}
func (*fakeSocket) SetReadLimit(int64) {}
