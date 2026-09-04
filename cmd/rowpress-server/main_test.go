package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestMainReportsRunServerExitCode(t *testing.T) {
	originalExit := exitProcess
	originalRun := runServerForProcess
	t.Cleanup(func() {
		exitProcess = originalExit
		runServerForProcess = originalRun
	})

	runServerForProcess = func(context.Context, string, io.Writer) int { return 7 }
	exitCode := -1
	exitProcess = func(code int) { exitCode = code }
	main()
	if exitCode != 7 {
		t.Fatalf("main() exit code = %d, want 7", exitCode)
	}
}

func TestRunServerReportsListenError(t *testing.T) {
	var stderr strings.Builder
	if code := runServer(context.Background(), "not-a-port", &stderr); code != 1 {
		t.Fatalf("runServer() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "listen") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestBasicAuthEnvironmentValidation(t *testing.T) {
	t.Setenv("BASIC_AUTH_USERNAME", "")
	t.Setenv("BASIC_AUTH_PASSWORD", "")
	config, err := basicAuthFromEnvironment()
	if err != nil || config != (basicAuthConfig{}) {
		t.Fatalf("disabled auth = %#v, %v", config, err)
	}

	t.Setenv("BASIC_AUTH_USERNAME", "demo")
	if _, err := basicAuthFromEnvironment(); err == nil {
		t.Fatal("one missing credential was accepted")
	}

	t.Setenv("BASIC_AUTH_PASSWORD", "secret")
	config, err = basicAuthFromEnvironment()
	if err != nil || config.username != "demo" || config.password != "secret" {
		t.Fatalf("enabled auth = %#v, %v", config, err)
	}

	t.Setenv("BASIC_AUTH_USERNAME", "invalid:name")
	if _, err := basicAuthFromEnvironment(); err == nil {
		t.Fatal("username containing a colon was accepted")
	}
}

func TestRunServerReportsInvalidAuthConfiguration(t *testing.T) {
	t.Setenv("BASIC_AUTH_USERNAME", "demo")
	t.Setenv("BASIC_AUTH_PASSWORD", "")
	var stderr strings.Builder
	if code := runServerWith(context.Background(), "1234", &stderr, nil, nil); code != 1 {
		t.Fatalf("runServerWith() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "configuration") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunServerWithUsesDefaultPortAndReturnsSuccess(t *testing.T) {
	var address string
	listen := func(_, value string) (net.Listener, error) {
		address = value
		return errorListener{}, nil
	}
	serveHTTP := func(context.Context, *http.Server, net.Listener) error { return nil }

	if code := runServerWith(context.Background(), "", &strings.Builder{}, listen, serveHTTP); code != 0 {
		t.Fatalf("runServerWith() = %d, want 0", code)
	}
	if address != "0.0.0.0:"+defaultPort {
		t.Fatalf("listen address = %q", address)
	}
}

func TestRunServerWithReportsServeError(t *testing.T) {
	listen := func(_, _ string) (net.Listener, error) { return errorListener{}, nil }
	serveHTTP := func(context.Context, *http.Server, net.Listener) error { return errors.New("serve failed") }
	var stderr strings.Builder

	if code := runServerWith(context.Background(), "1234", &stderr, listen, serveHTTP); code != 1 {
		t.Fatalf("runServerWith() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "serve failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestServeStopsCleanlyWithContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := serve(ctx, &http.Server{Handler: http.NewServeMux()}, listener); err != nil {
		t.Fatalf("serve() error = %v", err)
	}
}

func TestServeReturnsListenerError(t *testing.T) {
	err := serve(context.Background(), &http.Server{}, errorListener{})
	if err == nil || !strings.Contains(err.Error(), "accept failed") {
		t.Fatalf("serve() error = %v", err)
	}
}

type errorListener struct{}

func (errorListener) Accept() (net.Conn, error) { return nil, errors.New("accept failed") }
func (errorListener) Close() error              { return nil }
func (errorListener) Addr() net.Addr            { return &net.TCPAddr{} }
