package cmdutil

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	sdk "github.com/Tencent/WeKnora/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKMultipartUploadsRefreshAndReplay(t *testing.T) {
	for _, skill := range []bool{false, true} {
		name := "document"
		if skill {
			name = "skill"
		}
		t.Run(name, func(t *testing.T) {
			var calls, refreshes atomic.Int32
			bodies := make(chan []byte, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Contains(t, r.Header.Get("Content-Type"), "multipart/form-data; boundary=")
				if skill {
					assert.Contains(t, string(body), "archive bytes")
				} else {
					assert.Contains(t, string(body), "document bytes")
					assert.Contains(t, string(body), "regression")
				}
				bodies <- body
				call := calls.Add(1)
				if call == 1 {
					assert.Equal(t, "Bearer old-token", r.Header.Get("Authorization"))
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":"expired"}`))
					return
				}
				assert.Equal(t, "Bearer new-token", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				if skill {
					_, _ = w.Write([]byte(`{"success":true,"data":{"skill_id":"skill-test"}}`))
				} else {
					_, _ = w.Write([]byte(`{"success":true,"data":{"id":"doc-test"}}`))
				}
			}))
			defer server.Close()
			transport := NewAuthRetryTransport(http.DefaultTransport, "old-token",
				func(context.Context) (string, error) {
					refreshes.Add(1)
					return "new-token", nil
				},
			)
			c := sdk.NewClient(server.URL, sdk.WithBearerToken("old-token"), sdk.WithTransport(transport))
			if skill {
				id, err := c.UploadSandboxSkill(
					context.Background(), "sandbox-test", "skill.zip", []byte("archive bytes"),
				)
				require.NoError(t, err)
				assert.Equal(t, "skill-test", id)
			} else {
				path := filepath.Join(t.TempDir(), "report.txt")
				require.NoError(t, os.WriteFile(path, []byte("document bytes"), 0o600))
				knowledge, err := c.CreateKnowledgeFromFile(
					context.Background(), "kb-test", path, map[string]string{"source": "regression"},
					nil, "report.txt", "cli", nil,
				)
				require.NoError(t, err)
				assert.Equal(t, "doc-test", knowledge.ID)
			}
			assert.Equal(t, int32(2), calls.Load())
			assert.Equal(t, int32(1), refreshes.Load())
			assert.Equal(t, <-bodies, <-bodies, "replay must preserve the complete multipart payload")
		})
	}
}
