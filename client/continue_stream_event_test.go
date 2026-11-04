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
)

func TestContinueStreamDefaultMessageEvent(t *testing.T) {
	for _, fields := range []string{"", "event:\n", "event: \n", "event\n", "event: progress\nevent\n"} {
		t.Run(fmt.Sprintf("fields=%q", fields), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				// Named events stay filtered, and their type must not leak to the next frame.
				_, _ = fmt.Fprint(w, "event: progress\ndata: ignored\n\n")
				_, _ = fmt.Fprint(w, fields+"data: {\"response_type\":\"answer\",\"content\":\"hello\"}\n\n")
				_, _ = fmt.Fprint(w, "data: {\"response_type\":\"answer\",\"content\":\"world\"}\n\n")
			}))
			defer srv.Close()

			var contents []string
			c := NewClient(srv.URL)
			err := c.ContinueStream(context.Background(), "sess", "msg", func(e *StreamResponse) error {
				contents = append(contents, e.Content)
				return nil
			})
			if err != nil {
				t.Fatalf("ContinueStream: %v", err)
			}
			if got := strings.Join(contents, ","); got != "hello,world" {
				t.Errorf("contents = %q, want hello,world", got)
			}
		})
	}
}

func TestContinueStreamDefaultMessageErrors(t *testing.T) {
	callbackErr := errors.New("stop callback")
	for _, tc := range []struct {
		name        string
		payload     string
		callbackErr error
	}{
		{"terminal error", `{"response_type":"error","content":"model failed","done":true}`, nil},
		{"callback error", `{"response_type":"answer","content":"hello"}`, callbackErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: %s\n\n", tc.payload)
				w.(http.Flusher).Flush()
				// Returning from the callback/error must close the body even before EOF.
				<-r.Context().Done()
			}))
			defer srv.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			count := 0
			err := NewClient(srv.URL).ContinueStream(ctx, "sess", "msg", func(*StreamResponse) error {
				count++
				return tc.callbackErr
			})
			if tc.callbackErr != nil {
				if !errors.Is(err, tc.callbackErr) {
					t.Errorf("error = %v, want callback error", err)
				}
			} else {
				var streamErr *SSEStreamError
				if !errors.As(err, &streamErr) || streamErr.Content != "model failed" {
					t.Errorf("error = %v, want terminal SSE error", err)
				}
			}
			if count != 1 {
				t.Errorf("callbacks = %d, want 1", count)
			}
		})
	}
}
