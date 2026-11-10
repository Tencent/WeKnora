package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func startTestServer(t *testing.T) (*http.Server, net.Listener, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()
	waitUntilAccepting(t, ln.Addr().String())
	return srv, ln, errCh
}

func waitUntilAccepting(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s did not accept", addr)
}

func TestShutdownReturnsErrServerClosed(t *testing.T) {
	srv, _, errCh := startTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve err = %v, want ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestManualListenerCloseIsNotErrServerClosed(t *testing.T) {
	_, ln, errCh := startTestServer(t)
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-errCh:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve err = %v, want the raw accept error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
}
