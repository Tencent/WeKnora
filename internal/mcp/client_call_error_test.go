package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestCallToolOAuthRefreshFailureDoesNotClaimUnknownExecution(t *testing.T) {
	for _, expired := range []bool{true, false} {
		name := "after_resource_401"
		if expired {
			name = "before_call"
		}
		t.Run(name, func(t *testing.T) {
			utils.SetSSRFWhitelistFromRaw("127.0.0.1")
			t.Cleanup(utils.ResetSSRFWhitelistForTest)
			runtime, repo, _, closeIssuer := newOAuthLifecycleFixture(t, http.StatusServiceUnavailable,
				map[string]any{"error": "temporarily_unavailable"})
			t.Cleanup(closeIssuer)
			row := repo.tokens[fakeOAuthKey(7, runtime.principal, "svc-1")]
			row.ExpiresAt = time.Now().Add(time.Hour)
			if expired {
				row.ExpiresAt = time.Now().Add(-time.Minute)
			}
			server := sdkserver.NewMCPServer("orders", "1", sdkserver.WithToolCapabilities(false))
			transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithDisableStreaming(true))
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(body))
				var request struct {
					Method string `json:"method"`
				}
				_ = json.Unmarshal(body, &request)
				if request.Method == "tools/call" {
					calls.Add(1)
					http.Error(w, "authorization required", http.StatusUnauthorized)
					return
				}
				transport.ServeHTTP(w, r)
			}))
			t.Cleanup(upstream.Close)
			client, err := NewMCPClient(&ClientConfig{Service: &types.MCPService{
				ID: "orders", Enabled: true, URL: &upstream.URL, TransportType: types.MCPTransportHTTPStreamable,
			}})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Disconnect()) })
			require.NoError(t, client.Connect(context.Background()))
			_, err = client.Initialize(context.Background())
			require.NoError(t, err)
			client.(*mcpGoClient).oauth = runtime
			_, err = client.CallTool(context.Background(), "write", map[string]any{})
			require.Error(t, err)
			require.False(t, errors.Is(err, ErrToolCallOutcomeUnknown),
				"token refresh failures are not evidence of an attempted write")
			if expired {
				require.Zero(t, calls.Load())
			} else {
				require.EqualValues(t, 1, calls.Load(), "401 rejects the call before execution")
			}
		})
	}
}
