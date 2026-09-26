package mcp

import (
	"sync"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/Tencent/WeKnora/internal/types"
)

// TransportFactory builds the transport of a service whose transport type
// another package provides, such as MCP servers plugins serve themselves.
type TransportFactory func(svc *types.MCPService) (transport.Interface, error)

var (
	transportsMu sync.RWMutex
	transports   = map[types.MCPTransportType]TransportFactory{}
)

// RegisterTransport makes NewMCPClient build services of a transport type
// with f. Registering again replaces the factory.
func RegisterTransport(kind types.MCPTransportType, f TransportFactory) {
	transportsMu.Lock()
	defer transportsMu.Unlock()
	transports[kind] = f
}

func registeredTransport(kind types.MCPTransportType) (TransportFactory, bool) {
	transportsMu.RLock()
	defer transportsMu.RUnlock()
	f, ok := transports[kind]
	return f, ok
}

// target names what a client talks to, for logs.
func target(svc *types.MCPService) string {
	if svc.URL != nil && *svc.URL != "" {
		return "URL:" + *svc.URL
	}
	if svc.PluginID != "" {
		return "plugin:" + svc.PluginID + "/" + svc.PluginServer
	}
	return "transport:" + string(svc.TransportType)
}
