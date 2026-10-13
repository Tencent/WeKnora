package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Exercise the production HTTP client through Wiki's request coalescing layer.
// A model that never sends headers or stops in the middle of a response must
// not prevent cancellation or a subsequent independent request from completing.
func TestWikiHTTPModelCancellationAndRecovery(t *testing.T) {
	withSSRFWhitelist(t, "127.0.0.1")
	for _, partialBody := range []bool{false, true} {
		name := "before_headers"
		if partialBody {
			name = "during_body"
		}
		t.Run(name, func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			shutdown := make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if calls.Add(1) == 1 {
					if partialBody {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"choices":[`)
						w.(http.Flusher).Flush()
					}
					close(started)
					select {
					case <-r.Context().Done():
					case <-shutdown:
					}
					close(stopped)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"recovered"},"finish_reason":"stop"}]}`)
			}))
			t.Cleanup(func() {
				close(shutdown)
				server.Close()
			})
			model := openaicompletions.New(openaicompletions.Config{
				Endpoint: api.Endpoint{
					BaseURL: server.URL, Model: "fixture", ModelID: "fixture", Client: server.Client(),
				},
				Settings: api.DefaultOpenAICompletions(),
			})
			svc := &wikiIngestService{}
			parent := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			ctx, cancel := context.WithCancel(parent)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := svc.generateWithTemplate(ctx, model, "stalled request", nil)
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("model request did not reach the HTTP server")
			}
			cancel()
			select {
			case err := <-result:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(3 * time.Second):
				t.Fatal("Wiki model call did not exit on cancellation")
			}
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("the underlying HTTP request remained active after cancellation")
			}
			recoveryCtx, recoveryCancel := context.WithTimeout(parent, 3*time.Second)
			defer recoveryCancel()
			content, err := svc.generateWithTemplate(recoveryCtx, model, "independent recovery request", nil)
			require.NoError(t, err)
			require.Equal(t, "recovered", content)
			require.EqualValues(t, 2, calls.Load())
		})
	}
}
