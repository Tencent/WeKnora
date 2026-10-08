package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type resourceListTransport struct {
	transport.Interface
	send func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error)
}

func (t *resourceListTransport) SendRequest(
	ctx context.Context, request transport.JSONRPCRequest,
) (*transport.JSONRPCResponse, error) {
	if request.Method == "initialize" {
		return &transport.JSONRPCResponse{Result: json.RawMessage(`{
			"protocolVersion":"2025-06-18", "capabilities":{"resources":{}},
			"serverInfo":{"name":"resources-test","version":"1"}
		}`)}, nil
	}
	return t.send(ctx, request)
}

func (*resourceListTransport) SendNotification(context.Context, sdkmcp.JSONRPCNotification) error {
	return nil
}

func resourceListClient(t *testing.T, tpt *resourceListTransport) *mcpGoClient {
	t.Helper()
	c := &mcpGoClient{client: client.NewClient(tpt)}
	_, err := c.client.Initialize(t.Context(), sdkmcp.InitializeRequest{})
	require.NoError(t, err)
	c.initialized.Store(true)
	return c
}

func TestResourceListStopsRepeatedCursors(t *testing.T) {
	for _, cursors := range [][]string{{"same", "same"}, {"first", "second", "first"}} {
		t.Run(fmt.Sprint(cursors), func(t *testing.T) {
			calls := 0
			c := resourceListClient(t, &resourceListTransport{
				send: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
					calls++
					// Stop an unfixed client deterministically instead of hanging the test.
					if calls > len(cursors) {
						return nil, fmt.Errorf("test guard: client requested another repeated page")
					}
					return &transport.JSONRPCResponse{Result: json.RawMessage(fmt.Sprintf(
						`{"resources":[{"uri":"file:///a","name":"a"}],"nextCursor":%q}`, cursors[calls-1],
					))}, nil
				},
			})
			resources, err := c.ListResources(t.Context())
			require.ErrorContains(t, err, "repeated cursor")
			require.Nil(t, resources, "a looping directory must not be published partially")
			require.Equal(t, len(cursors), calls)
		})
	}
}

func TestResourceListBoundsDistinctCursorPages(t *testing.T) {
	calls := 0
	c := resourceListClient(t, &resourceListTransport{
		send: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			calls++
			if calls > 100 {
				return nil, fmt.Errorf("test guard: client requested more than 100 pages")
			}
			return &transport.JSONRPCResponse{Result: json.RawMessage(fmt.Sprintf(
				`{"resources":[],"nextCursor":"page-%d"}`, calls,
			))}, nil
		},
	})
	resources, err := c.ListResources(t.Context())
	require.ErrorContains(t, err, "exceeded")
	require.Nil(t, resources)
	require.Equal(t, 100, calls)
}

func TestResourceListPreservesPaginationAndErrors(t *testing.T) {
	for _, failSecond := range []bool{false, true} {
		t.Run(fmt.Sprint(failSecond), func(t *testing.T) {
			calls := 0
			c := resourceListClient(t, &resourceListTransport{
				send: func(_ context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
					calls++
					require.Equal(t, "resources/list", request.Method)
					if calls == 1 {
						// An empty page with a cursor must still be followed.
						return &transport.JSONRPCResponse{Result: json.RawMessage(
							`{"resources":[],"nextCursor":"opaque +/= cursor"}`,
						)}, nil
					}
					params, err := json.Marshal(request.Params)
					require.NoError(t, err)
					require.JSONEq(t, `{"cursor":"opaque +/= cursor"}`, string(params))
					if failSecond {
						return &transport.JSONRPCResponse{Error: &sdkmcp.JSONRPCErrorDetails{
							Code: -32603, Message: "resource page unavailable",
						}}, nil
					}
					return &transport.JSONRPCResponse{Result: json.RawMessage(`{"resources":[{
						"uri":"file:///last","name":"last","description":"final page","mimeType":"text/plain"
					}]}`)}, nil
				},
			})
			resources, err := c.ListResources(t.Context())
			if failSecond {
				require.ErrorContains(t, err, "resource page unavailable")
				require.Nil(t, resources)
			} else {
				require.NoError(t, err)
				require.Len(t, resources, 1)
				require.Equal(t, "file:///last", resources[0].URI)
				require.Equal(t, "last", resources[0].Name)
				require.Equal(t, "final page", resources[0].Description)
				require.Equal(t, "text/plain", resources[0].MimeType)
			}
			require.Equal(t, 2, calls)
		})
	}
}

func TestResourceListAcceptsTerminalPageAtLimit(t *testing.T) {
	calls := 0
	c := resourceListClient(t, &resourceListTransport{
		send: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			calls++
			cursor := fmt.Sprintf("page-%d", calls)
			if calls == 100 {
				cursor = ""
			}
			return &transport.JSONRPCResponse{Result: json.RawMessage(fmt.Sprintf(
				`{"resources":[{"uri":"file:///%d","name":"entry"}],"nextCursor":%q}`, calls, cursor,
			))}, nil
		},
	})
	resources, err := c.ListResources(t.Context())
	require.NoError(t, err)
	require.Len(t, resources, 100)
	require.Equal(t, "file:///1", resources[0].URI)
	require.Equal(t, "file:///100", resources[99].URI)
	require.Equal(t, 100, calls)
}

func TestResourceListHonorsCancellationBetweenPages(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	c := resourceListClient(t, &resourceListTransport{
		send: func(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
			calls++
			cancel()
			return &transport.JSONRPCResponse{Result: json.RawMessage(
				`{"resources":[{"uri":"file:///a","name":"a"}],"nextCursor":"next"}`,
			)}, nil
		},
	})
	resources, err := c.ListResources(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, resources)
	require.Equal(t, 1, calls)
}
