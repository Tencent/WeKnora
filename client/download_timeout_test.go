package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSingleDownloadUsesStreamingTimeoutPolicy(t *testing.T) {
	for _, policy := range []string{"default", "option", "context"} {
		t.Run(policy, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "8")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				select {
				case <-time.After(150 * time.Millisecond):
					_, _ = w.Write([]byte("complete"))
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			c := NewClient(server.URL)
			// Scale the ordinary 30s timeout down to keep the slow-body test fast.
			// This leaves the streaming policy at its actual default (no timeout).
			c.httpClient.Timeout = 30 * time.Millisecond
			ctx := t.Context()
			if policy == "option" {
				WithTimeout(30 * time.Millisecond)(c)
			}
			if policy == "context" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer cancel()
			}
			dest := filepath.Join(t.TempDir(), "download.bin")
			err := c.DownloadKnowledgeFile(ctx, "doc-test", dest)
			if policy != "default" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("error = %v, want deadline exceeded", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("slow file body hit ordinary request timeout: %v", err)
			}
			body, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != "complete" {
				t.Fatalf("downloaded body = %q", body)
			}
		})
	}
}
