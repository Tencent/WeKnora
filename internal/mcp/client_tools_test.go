package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type rawToolsTransport struct {
	transport.Interface
	send func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error)
}

func (t *rawToolsTransport) SendRequest(
	ctx context.Context,
	request transport.JSONRPCRequest,
) (*transport.JSONRPCResponse, error) {
	return t.send(ctx, request)
}

func TestRawToolsPaginationErrorsAndCancellation(t *testing.T) {
	for _, second := range []string{`{"tools":[],"nextCursor":"repeat"}`, `{"error":true}`, `not-json`} {
		t.Run(second, func(t *testing.T) {
			calls := 0
			seenIDs := map[string]bool{}
			tpt := &rawToolsTransport{
				send: func(_ context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
					calls++
					id, err := json.Marshal(request.ID)
					require.NoError(t, err)
					require.Contains(t, string(id), `"weknora-tools-`)
					require.False(t, seenIDs[string(id)])
					seenIDs[string(id)] = true
					require.Equal(t, "tools/list", request.Method)
					if calls == 1 {
						return &transport.JSONRPCResponse{Result: json.RawMessage(`{
  "tools": [
    {
      "name": "first",
      "inputSchema": {
        "type": "object"
      }
    }
  ],
  "nextCursor": "repeat"
}`)}, nil
					}
					params, _ := json.Marshal(request.Params)
					require.JSONEq(t, `{"cursor":"repeat"}`, string(params))
					if second == `{"error":true}` {
						return &transport.JSONRPCResponse{
							Error: &sdkmcp.JSONRPCErrorDetails{Code: -32603, Message: "page unavailable"},
						}, nil
					}
					return &transport.JSONRPCResponse{Result: json.RawMessage(second)}, nil
				},
			}
			c := &mcpGoClient{client: client.NewClient(tpt)}
			c.initialized.Store(true)
			tools, err := c.ListTools(context.Background())
			require.Error(t, err)
			require.Nil(t, tools, "never publish a partial directory")
			require.Equal(t, 2, calls)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = c.ListTools(ctx)
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, 2, calls)
		})
	}
}

func TestRawToolsBoundsHostileDirectorySize(t *testing.T) {
	// Distinct cursors defeat the repeat-cursor guard, so an unbounded reader
	// would keep growing the in-memory directory until the list timeout.
	t.Run("pages", func(t *testing.T) {
		calls := 0
		c := &mcpGoClient{client: client.NewClient(&rawToolsTransport{
			send: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
				calls++
				return &transport.JSONRPCResponse{Result: json.RawMessage(
					fmt.Sprintf(`{"tools":[{"name":"t%d"}],"nextCursor":"c%d"}`, calls, calls),
				)}, nil
			},
		})}
		c.initialized.Store(true)
		tools, err := c.ListTools(context.Background())
		require.ErrorContains(t, err, "exceeded")
		require.Nil(t, tools, "never publish a partial directory")
		require.LessOrEqual(t, calls, maxToolListPages)
	})
	t.Run("tools", func(t *testing.T) {
		page := make([]string, maxToolsPerService+1)
		for i := range page {
			page[i] = fmt.Sprintf(`{"name":"t%d"}`, i)
		}
		body := `{"tools":[` + strings.Join(page, ",") + `]}`
		c := &mcpGoClient{client: client.NewClient(&rawToolsTransport{
			send: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
				return &transport.JSONRPCResponse{Result: json.RawMessage(body)}, nil
			},
		})}
		c.initialized.Store(true)
		_, err := c.ListTools(context.Background())
		require.ErrorContains(t, err, "exceeded")
	})
	t.Run("schema", func(t *testing.T) {
		schema := `{"type":"object","description":"` + strings.Repeat("x", maxToolSchemaBytes) + `"}`
		c := &mcpGoClient{client: client.NewClient(&rawToolsTransport{
			send: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
				return &transport.JSONRPCResponse{Result: json.RawMessage(
					`{"tools":[{"name":"huge","inputSchema":` + schema + `}]}`,
				)}, nil
			},
		})}
		c.initialized.Store(true)
		_, err := c.ListTools(context.Background())
		require.ErrorContains(t, err, "exceeds")
	})
	t.Run("within limits", func(t *testing.T) {
		c := &mcpGoClient{client: client.NewClient(&rawToolsTransport{
			send: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
				return &transport.JSONRPCResponse{Result: json.RawMessage(
					`{"tools":[{"name":"ok","inputSchema":{"type":"object"}}]}`,
				)}, nil
			},
		})}
		c.initialized.Store(true)
		tools, err := c.ListTools(context.Background())
		require.NoError(t, err)
		require.Len(t, tools, 1)
	})
}

func TestRawToolsUsesOAuthLifecycleAndPreservesTransportErrors(t *testing.T) {
	runtime, _, refreshes, closeServer := newOAuthLifecycleFixture(
		t,
		http.StatusOK,
		map[string]any{
			"access_token":  "new-access",
			"refresh_token": "rotated-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
		},
	)
	defer closeServer()
	c := &mcpGoClient{
		oauth: runtime,
		client: client.NewClient(
			&rawToolsTransport{
				send: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
					require.EqualValues(t, 1, refreshes.Load(), "refresh must precede discovery")
					return &transport.JSONRPCResponse{Result: json.RawMessage(`{"tools":[]}`)}, nil
				},
			},
		),
	}
	c.initialized.Store(true)
	_, err := c.ListTools(context.Background())
	require.NoError(t, err)
	c.oauth = nil
	sentinel := errors.New("network failure")
	c.client = client.NewClient(
		&rawToolsTransport{send: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			return nil, sentinel
		}},
	)
	_, err = c.ListTools(context.Background())
	require.ErrorIs(t, err, sentinel)
	var transportErr *transport.Error
	require.ErrorAs(t, err, &transportErr)
}
