package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	defaultPort     = "10000"
	shutdownTimeout = 10 * time.Second
)

var (
	exitProcess         = os.Exit
	runServerForProcess = runServer
)

type listenFunc func(network, address string) (net.Listener, error)
type serveFunc func(context.Context, *http.Server, net.Listener) error

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	exitProcess(runServerForProcess(ctx, os.Getenv("PORT"), os.Stderr))
}

func runServer(ctx context.Context, port string, stderr io.Writer) int {
	return runServerWith(ctx, port, stderr, net.Listen, serve)
}

func runServerWith(
	ctx context.Context,
	port string,
	stderr io.Writer,
	listen listenFunc,
	serveHTTP serveFunc,
) int {
	auth, err := basicAuthFromEnvironment()
	if err != nil {
		fmt.Fprintf(stderr, "rowpress-server: configuration: %v\n", err)
		return 1
	}
	if port == "" {
		port = defaultPort
	}

	listener, err := listen("tcp", "0.0.0.0:"+port)
	if err != nil {
		fmt.Fprintf(stderr, "rowpress-server: listen: %v\n", err)
		return 1
	}

	server := &http.Server{
		Handler:           newHandler(ctx, auth),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := serveHTTP(ctx, server, listener); err != nil {
		fmt.Fprintf(stderr, "rowpress-server: serve: %v\n", err)
		return 1
	}
	return 0
}

func basicAuthFromEnvironment() (basicAuthConfig, error) {
	username := os.Getenv("BASIC_AUTH_USERNAME")
	password := os.Getenv("BASIC_AUTH_PASSWORD")
	if username == "" && password == "" {
		return basicAuthConfig{}, nil
	}
	if username == "" || password == "" {
		return basicAuthConfig{}, errors.New("BASIC_AUTH_USERNAME and BASIC_AUTH_PASSWORD must both be set")
	}
	if strings.Contains(username, ":") {
		return basicAuthConfig{}, errors.New("BASIC_AUTH_USERNAME cannot contain a colon")
	}
	return basicAuthConfig{username: username, password: password}, nil
}

func serve(ctx context.Context, server *http.Server, listener net.Listener) error {
	shutdownFinished := make(chan struct{})
	stopShutdown := make(chan struct{})
	go func() {
		defer close(shutdownFinished)
		select {
		case <-ctx.Done():
			shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			_ = server.Shutdown(shutdownContext)
		case <-stopShutdown:
		}
	}()

	err := server.Serve(listener)
	close(stopShutdown)
	<-shutdownFinished
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
