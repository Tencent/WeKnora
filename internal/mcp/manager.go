package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MCPManager manages MCP client connections
type MCPManager struct {
	clients    map[string]MCPClient // cacheKey -> client
	clientsMu  sync.RWMutex
	connecting map[string]*pendingMCPConnection
	oauthRepo  interfaces.MCPOAuthRepository
	ctx        context.Context
	cancel     context.CancelFunc
}

// Connections to unrelated servers must not hold the manager lock during I/O.
// Waiting callers may cancel independently; the connection belongs to the manager.
type pendingMCPConnection struct {
	done    chan struct{}
	cancel  context.CancelFunc
	client  MCPClient
	err     error
	version time.Time
}

type managedMCPClient struct {
	MCPClient
	cancel  context.CancelFunc
	version time.Time
}

func (c *managedMCPClient) Disconnect() error {
	c.cancel()
	return c.MCPClient.Disconnect()
}

func (c *managedMCPClient) ServerInstructions() string {
	if provider, ok := c.MCPClient.(interface{ ServerInstructions() string }); ok {
		return provider.ServerInstructions()
	}
	return ""
}

// NewMCPManager creates a new MCP manager. oauthRepo is used to wire per-user
// OAuth token stores for OAuth-enabled MCP services.
func NewMCPManager(oauthRepo interfaces.MCPOAuthRepository) *MCPManager {
	ctx, cancel := context.WithCancel(context.Background())

	manager := &MCPManager{
		clients:    make(map[string]MCPClient),
		connecting: make(map[string]*pendingMCPConnection),
		oauthRepo:  oauthRepo,
		ctx:        ctx,
		cancel:     cancel,
	}

	// Start cleanup goroutine
	go manager.cleanupIdleConnections()

	return manager
}

// cacheKey computes the connection-cache key for a service. OAuth services are
// keyed per principal (each identity connects with its own token); all other
// services share a single connection per service ID.
func cacheKey(service *types.MCPService, principal types.Principal) string {
	if service.AuthConfig.IsOAuth() {
		return service.ID + "\x00" + principal.Normalize().StorageID()
	}
	return service.ID
}

// GetOrCreateClient gets an existing client or creates a new one
// Caches and reuses existing connections for SSE/HTTP Streamable
// Note: Stdio transport is disabled for security reasons
//
// For OAuth-enabled services the connection is keyed per principal (derived from
// ctx) so each identity connects with its own token.
func (m *MCPManager) GetOrCreateClient(ctx context.Context, service *types.MCPService) (MCPClient, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Check if service is enabled
	if !service.Enabled {
		return nil, fmt.Errorf("MCP service %s is not enabled", service.Name)
	}

	// Stdio transport is disabled for security reasons
	if service.TransportType == types.MCPTransportStdio {
		return nil, fmt.Errorf("stdio transport is disabled for security reasons; please use SSE or HTTP Streamable transport instead")
	}

	var tenantID uint64
	var principal types.Principal
	if service.AuthConfig.IsOAuth() {
		tenantID, _ = types.TenantIDFromContext(ctx)
		principal, _ = types.PrincipalFromContext(ctx)
		principal = types.MCPOAuthPrincipalFromContext(ctx)
		if !principal.Valid() {
			return nil, fmt.Errorf("principal context is required to connect to OAuth MCP service %s", service.Name)
		}
	}
	key := cacheKey(service, principal)

	m.clientsMu.Lock()
	if err := m.ctx.Err(); err != nil {
		m.clientsMu.Unlock()
		return nil, err
	}
	if client, exists := m.clients[key]; exists && client.IsConnected() {
		managed, owned := client.(*managedMCPClient)
		if !owned || managed.version.Equal(service.UpdatedAt) {
			m.clientsMu.Unlock()
			return client, nil
		}
		_ = client.Disconnect()
		delete(m.clients, key)
	}
	pending := m.connecting[key]
	if pending != nil && !pending.version.Equal(service.UpdatedAt) {
		pending.cancel()
		delete(m.connecting, key)
		pending = nil
	}
	if pending == nil {
		lifeCtx, cancel := context.WithCancel(m.ctx)
		pending = &pendingMCPConnection{done: make(chan struct{}), cancel: cancel, version: service.UpdatedAt}
		m.connecting[key] = pending
		config := &ClientConfig{Service: service, TenantID: tenantID, Principal: principal, OAuthRepo: m.oauthRepo}
		go m.connectClient(lifeCtx, key, config, pending)
	}
	m.clientsMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-pending.done:
		return pending.client, pending.err
	}
}

func (m *MCPManager) connectClient(
	ctx context.Context, key string, config *ClientConfig, pending *pendingMCPConnection,
) {
	client, err := NewMCPClient(config)
	if err == nil {
		// SSE needs the connection lifetime, not the requesting turn's deadline.
		err = client.Connect(ctx)
	}
	if err == nil {
		err = m.initializeClient(ctx, config.Service, client, "failed to initialize MCP client")
	}
	m.clientsMu.Lock()
	// CloseClient/CloseAll may retire this attempt while it is connecting.
	if m.connecting[key] != pending || ctx.Err() != nil {
		if err == nil {
			err = context.Canceled
		}
	}
	if err == nil {
		pending.client = &managedMCPClient{MCPClient: client, cancel: pending.cancel, version: pending.version}
		m.clients[key] = pending.client
	} else {
		pending.err = err
		pending.cancel()
		if client != nil {
			_ = client.Disconnect()
		}
	}
	if m.connecting[key] == pending {
		delete(m.connecting, key)
	}
	close(pending.done)
	m.clientsMu.Unlock()
}

// initializeClient handles the shared initialization flow with timeout enforcement.
func (m *MCPManager) initializeClient(
	ctx context.Context, service *types.MCPService, client MCPClient, errPrefix string,
) error {
	initTimeout := 30 * time.Second
	if service.AdvancedConfig != nil && service.AdvancedConfig.Timeout > 0 {
		initTimeout = time.Duration(service.AdvancedConfig.Timeout) * time.Second
		if initTimeout > 60*time.Second {
			initTimeout = 60 * time.Second
		}
	}

	initCtx, initCancel := context.WithTimeout(ctx, initTimeout)
	defer initCancel()

	if _, err := client.Initialize(initCtx); err != nil {
		client.Disconnect()
		if errPrefix == "" {
			errPrefix = "failed to initialize MCP client"
		}
		return fmt.Errorf("%s: %w", errPrefix, err)
	}

	return nil
}

// GetClient gets an existing client
func (m *MCPManager) GetClient(serviceID string) (MCPClient, bool) {
	m.clientsMu.RLock()
	defer m.clientsMu.RUnlock()

	client, exists := m.clients[serviceID]
	return client, exists
}

// CloseClient closes and removes all cached connections for a service. For
// OAuth services this spans every per-principal connection (keys are prefixed with
// the service ID).
func (m *MCPManager) CloseClient(serviceID string) error {
	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()
	for key, pending := range m.connecting {
		if key == serviceID || strings.HasPrefix(key, serviceID+"\x00") {
			pending.cancel()
			delete(m.connecting, key)
		}
	}

	for key, client := range m.clients {
		// Match the plain service-ID key as well as per-principal OAuth keys
		// ("<serviceID>\x00<principal>").
		if key != serviceID && !strings.HasPrefix(key, serviceID+"\x00") {
			continue
		}
		if err := client.Disconnect(); err != nil {
			logger.GetLogger(m.ctx).Errorf("Failed to disconnect MCP client %s: %v", key, err)
		}
		delete(m.clients, key)
		logger.GetLogger(m.ctx).Infof("MCP client closed: %s", key)
	}
	return nil
}

// CloseAll closes all clients
func (m *MCPManager) CloseAll() {
	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()
	for key, pending := range m.connecting {
		pending.cancel()
		delete(m.connecting, key)
	}

	for serviceID, client := range m.clients {
		if err := client.Disconnect(); err != nil {
			logger.GetLogger(m.ctx).Errorf("Failed to disconnect MCP client %s: %v", serviceID, err)
		}
	}

	m.clients = make(map[string]MCPClient)
	logger.GetLogger(m.ctx).Info("All MCP clients closed")
}

// Shutdown gracefully shuts down the manager
func (m *MCPManager) Shutdown() {
	m.cancel()
	m.CloseAll()
}

// cleanupIdleConnections periodically cleans up disconnected clients
func (m *MCPManager) cleanupIdleConnections() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.removeDisconnectedClients()
		}
	}
}

// removeDisconnectedClients removes clients that are no longer connected
func (m *MCPManager) removeDisconnectedClients() {
	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	for serviceID, client := range m.clients {
		if !client.IsConnected() {
			delete(m.clients, serviceID)
			logger.GetLogger(m.ctx).Infof("Removed disconnected MCP client: %s", serviceID)
		}
	}
}

// GetActiveClients returns the number of active clients
func (m *MCPManager) GetActiveClients() int {
	m.clientsMu.RLock()
	defer m.clientsMu.RUnlock()

	count := 0
	for _, client := range m.clients {
		if client.IsConnected() {
			count++
		}
	}
	return count
}

// ListActiveServices returns IDs of services with active connections
func (m *MCPManager) ListActiveServices() []string {
	m.clientsMu.RLock()
	defer m.clientsMu.RUnlock()

	services := make([]string, 0, len(m.clients))
	for serviceID, client := range m.clients {
		if client.IsConnected() {
			services = append(services, serviceID)
		}
	}
	return services
}
