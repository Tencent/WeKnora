package cmdutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/secrets"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
	sdk "github.com/Tencent/WeKnora/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRejectedAutomaticRefreshPreservesHTTPClassification(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			testutil.XDGTempDir(t)
			store, err := secrets.NewFileStore()
			require.NoError(t, err)
			require.NoError(t, store.Set("test", "refresh", "rejected-refresh"))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/auth/refresh" {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"refresh rejected"}`))
					return
				}
				assert.Equal(t, "/api/v1/knowledge/doc-test", r.URL.Path)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer server.Close()
			transport := NewAuthRetryTransport(http.DefaultTransport, "expired-access",
				func(ctx context.Context) (string, error) {
					return RefreshAndPersist(ctx, store, sdk.NewClient(server.URL), "test")
				})
			client := sdk.NewClient(server.URL, sdk.WithBearerToken("expired-access"), sdk.WithTransport(transport))
			_, err = client.GetKnowledge(context.Background(), "doc-test")
			require.Error(t, err)
			wrapped := WrapHTTP(err, "fetch document")
			assert.Equal(t, ClassifyHTTPStatus(status), wrapped.Code)
			assert.True(t, IsAuthError(wrapped))
			assert.Equal(t, 3, ExitCode(wrapped))
		})
	}
}
