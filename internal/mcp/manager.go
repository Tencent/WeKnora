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
	clients   map[string]MCPClient // cacheKey -> client
	clientsMu sync.RWMutex
	oauthRepo interfaces.MCPOAuthRepository
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewMCPManager creates a new MCP manager. oauthRepo is used to wire per-user
// OAuth token stores for OAuth-enabled MCP services.
func NewMCPManager(oauthRepo interfaces.MCPOAuthRepository) *MCPManager {
	ctx, cancel := context.WithCancel(context.Background())

	manager := &MCPManager{
		clients:   make(map[string]MCPClient),
		oauthRepo: oauthRepo,
		ctx:       ctx,
		cancel:    cancel,
	}

	// Start cleanup goroutine
	go manager.cleanupIdleConnections()

	return manager
}

// cacheKey computes the connection-cache key for a service. OAuth services are
// keyed per user (each user connects with their own token); all other services
// share a single connection per service ID.
func cacheKey(service *types.MCPService, userID string) string {
	if service.AuthConfig.IsOAuth() {
		return service.ID + "\x00" + userID
	}
	return service.ID
}

// GetOrCreateClient gets an existing client or creates a new one
// Caches and reuses existing connections for SSE/HTTP Streamable
// Note: Stdio transport is disabled for security reasons
//
// For OAuth-enabled services the connection is keyed per user (derived from
// ctx) so each user connects with their own token.
func (m *MCPManager) GetOrCreateClient(ctx context.Context, service *types.MCPService) (MCPClient, error) {
	// Check if service is enabled
	if !service.Enabled {
		return nil, fmt.Errorf("MCP service %s is not enabled", service.Name)
	}

	// Stdio transport is disabled for security reasons
	if service.TransportType == types.MCPTransportStdio {
		return nil, fmt.Errorf("stdio transport is disabled for security reasons; please use SSE or HTTP Streamable transport instead")
	}

	var tenantID uint64
	var userID string
	if service.AuthConfig.IsOAuth() {
		tenantID, _ = types.TenantIDFromContext(ctx)
		userID, _ = types.UserIDFromContext(ctx)
		if userID == "" {
			return nil, fmt.Errorf("user context is required to connect to OAuth MCP service %s", service.Name)
		}
	}
	key := cacheKey(service, userID)

	// For SSE/HTTP Streamable, check if client already exists and reuse
	m.clientsMu.RLock()
	client, exists := m.clients[key]
	m.clientsMu.RUnlock()

	if exists && client.IsConnected() {
		return client, nil
	}

	// Create new client
	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	// Double check after acquiring write lock
	client, exists = m.clients[key]
	if exists && client.IsConnected() {
		return client, nil
	}

	// Create new client
	config := &ClientConfig{
		Service:   service,
		TenantID:  tenantID,
		UserID:    userID,
		OAuthRepo: m.oauthRepo,
	}

	client, err := NewMCPClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %w", err)
	}

	// For SSE connections, Connect() starts a persistent connection that needs a long-lived context
	// Use manager's context (m.ctx) which persists for the lifetime of the manager
	// The HTTP client's timeout will handle connection timeouts, not context cancellation
	if err := client.Connect(m.ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to MCP service: %w", err)
	}

	if err := m.initializeClient(service, client, "failed to initialize MCP client"); err != nil {
		return nil, err
	}

	// Store client (only for non-stdio transports)
	m.clients[key] = client

	logger.GetLogger(m.ctx).Infof("MCP client created and initialized for service: %s", service.Name)
	return client, nil
}

// initializeClient handles the shared initialization flow with timeout enforcement.
func (m *MCPManager) initializeClient(service *types.MCPService, client MCPClient, errPrefix string) error {
	initTimeout := 30 * time.Second
	if service.AdvancedConfig != nil && service.AdvancedConfig.Timeout > 0 {
		initTimeout = time.Duration(service.AdvancedConfig.Timeout) * time.Second
		if initTimeout > 60*time.Second {
			initTimeout = 60 * time.Second
		}
	}

	initCtx, initCancel := context.WithTimeout(m.ctx, initTimeout)
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
// OAuth services this spans every per-user connection (keys are prefixed with
// the service ID).
func (m *MCPManager) CloseClient(serviceID string) error {
	m.clientsMu.Lock()
	defer m.clientsMu.Unlock()

	for key, client := range m.clients {
		// Match the plain service-ID key as well as per-user OAuth keys
		// ("<serviceID>\x00<userID>").
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
