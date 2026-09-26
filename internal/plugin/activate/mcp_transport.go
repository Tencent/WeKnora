package activate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// SetInvoker lets the activator reach MCP servers plugins serve themselves
// (mcpServers without a url): the MCP client talks to them through plugin
// calls, which carry the workspace's context and configuration.
func (a *MCPServers) SetInvoker(iv *Invoker) {
	a.mu.Lock()
	a.iv = iv
	a.mu.Unlock()
	mcp.RegisterTransport(types.MCPTransportPlugin, a.newTransport)
}

func (a *MCPServers) newTransport(svc *types.MCPService) (transport.Interface, error) {
	a.mu.RLock()
	iv := a.iv
	var found *mcpServer
	for _, s := range a.servers[svc.PluginID] {
		if s.contrib.ID == svc.PluginServer {
			s := s
			found = &s
		}
	}
	a.mu.RUnlock()
	if iv == nil || found == nil {
		return nil, fmt.Errorf("plugin MCP server %s/%s is not loaded", svc.PluginID, svc.PluginServer)
	}
	return &pluginTransport{iv: iv, m: found.manifest, server: found.contrib.ID, tenantID: svc.TenantID}, nil
}

// pluginTransport carries MCP messages as plugin calls. Plugin servers are
// stateless: there is no session to start or close, and notifications
// (initialized, cancelled) need no delivery.
type pluginTransport struct {
	iv       *Invoker
	m        *manifest.Manifest
	server   string
	tenantID uint64
}

func (t *pluginTransport) Start(context.Context) error { return nil }

func (t *pluginTransport) SendRequest(
	ctx context.Context, req transport.JSONRPCRequest,
) (*transport.JSONRPCResponse, error) {
	id, err := json.Marshal(req.ID)
	if err != nil {
		return nil, err
	}
	in := pluginapi.MCPRequest{JSONRPC: "2.0", ID: id, Method: req.Method}
	if req.Params != nil {
		if in.Params, err = json.Marshal(req.Params); err != nil {
			return nil, err
		}
	}
	// The service belongs to one workspace; calls run in its context even
	// when the MCP manager connects without one.
	ctx = context.WithValue(ctx, types.TenantIDContextKey, t.tenantID)
	var out pluginapi.MCPResponse
	if err := t.iv.Call(ctx, t.m, pluginapi.MCPPath(t.server), nil, in, &out); err != nil {
		return nil, err
	}
	resp := &transport.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: out.Result}
	if out.Error != nil {
		resp.Error = &mcpgo.JSONRPCErrorDetails{Code: out.Error.Code, Message: out.Error.Message}
	}
	return resp, nil
}

func (t *pluginTransport) SendNotification(context.Context, mcpgo.JSONRPCNotification) error {
	return nil
}

func (t *pluginTransport) SetNotificationHandler(func(mcpgo.JSONRPCNotification)) {}

func (t *pluginTransport) Close() error { return nil }

//nolint:revive // mcp-go's transport.Interface names it so.
func (t *pluginTransport) GetSessionId() string { return "" }
