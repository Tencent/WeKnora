package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

func stream(t *testing.T, handler http.HandlerFunc) (json string, err error) {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	data, err := New(srv.URL, nil, nil).Stream(context.Background(), "/v1/x", pluginapi.Envelope{}, nil,
		func(pluginapi.Event) error { return nil })
	return string(data), err
}

// An event too long to read fails for good: every try would fail on it.
func TestStreamEventTooLongIsNotRetryable(t *testing.T) {
	_, err := stream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"type":"item","data":"%s"}`+"\n", strings.Repeat("x", MaxEventBytes))
	})
	var pe *pluginapi.Error
	if !errors.As(err, &pe) || pe.Retryable {
		t.Fatalf("err = %v", err)
	}
}

// A stream that goes quiet is given up, as retryable; events keep it alive.
func TestStreamIdleTimeout(t *testing.T) {
	old := StreamIdleTimeout
	StreamIdleTimeout = 300 * time.Millisecond
	t.Cleanup(func() { StreamIdleTimeout = old })
	data, err := stream(t, func(w http.ResponseWriter, _ *http.Request) {
		for range 4 {
			_, _ = fmt.Fprintln(w, `{"type":"progress"}`)
			w.(http.Flusher).Flush()
			time.Sleep(150 * time.Millisecond)
		}
		_, _ = fmt.Fprintln(w, `{"type":"end","data":"done"}`)
	})
	if err != nil || data != `"done"` {
		t.Fatalf("a stream sending progress: %q, %v", data, err)
	}
	start := time.Now()
	_, err = stream(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `{"type":"progress"}`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	var pe *pluginapi.Error
	if !errors.As(err, &pe) || !pe.Retryable || !strings.Contains(pe.Message, "stalled") {
		t.Fatalf("a stalled stream: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("gave up after %s", took)
	}
}

// Answers without a protocol body are classed by status.
func TestErrorsWithoutAProtocolBody(t *testing.T) {
	for status, want := range map[int]struct {
		code      pluginapi.ErrorCode
		retryable bool
	}{
		http.StatusTooManyRequests:     {pluginapi.CodeRateLimited, true},
		http.StatusGatewayTimeout:      {pluginapi.CodeUnavailable, true},
		http.StatusBadGateway:          {pluginapi.CodeUnavailable, true},
		http.StatusInternalServerError: {pluginapi.CodeInternal, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "proxy page", status)
		}))
		err := New(srv.URL, nil, nil).Call(context.Background(), "/v1/x", pluginapi.Envelope{}, nil, nil)
		srv.Close()
		var pe *pluginapi.Error
		if !errors.As(err, &pe) || pe.Code != want.code || pe.Retryable != want.retryable {
			t.Errorf("HTTP %d: %v", status, err)
		}
	}
}
