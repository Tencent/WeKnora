package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MCPManager manages MCP client connections
type MCPManager struct {
	clients              map[string]MCPClient // cacheKey -> client
	clientsMu            sync.RWMutex
	connecting           map[string]*pendingMCPConnection
	maxDynamicPerService int
	maxDynamicTotal      int
	oauthRepo            interfaces.MCPOAuthRepository
	ctx                  context.Context
	cancel               context.CancelFunc
}

// Connections to unrelated servers must not hold the manager lock during I/O.
// Waiting callers may cancel independently; the connection belongs to the manager.
type pendingMCPConnection struct {
	done      chan struct{}
	cancel    context.CancelFunc
	client    MCPClient
	err       error
	version   time.Time
	serviceID string
	dynamic   bool
}

type managedMCPClient struct {
	MCPClient
	cancel    context.CancelFunc
	version   time.Time
	serviceID string
	usageMu   sync.Mutex
	lastUsed  time.Time
	active    int
	dynamic   bool
	retired   bool
}

func (c *managedMCPClient) begin() error {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	if c.retired {
		return fmt.Errorf("MCP connection expired; reconnect required")
	}
	c.active++
	c.lastUsed = time.Now()
	return nil
}

func (c *managedMCPClient) end() {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	c.active--
	c.lastUsed = time.Now()
}

func (c *managedMCPClient) touch() {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	c.lastUsed = time.Now()
}

func (c *managedMCPClient) isAvailable() bool {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	return !c.retired
}

func (c *managedMCPClient) retireIdle(now time.Time) bool {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	if !c.dynamic || c.active != 0 || now.Sub(c.lastUsed) < 2*time.Minute {
		return false
	}
	c.retired = true
	return true
}

func (c *managedMCPClient) Connect(ctx context.Context) error {
	if err := c.begin(); err != nil {
		return err
	}
	defer c.end()
	return c.MCPClient.Connect(ctx)
}
func (c *managedMCPClient) Initialize(ctx context.Context) (*InitializeResult, error) {
	if err := c.begin(); err != nil {
		return nil, err
	}
	defer c.end()
	return c.MCPClient.Initialize(ctx)
}
func (c *managedMCPClient) ListTools(ctx context.Context) ([]*types.MCPTool, error) {
	if err := c.begin(); err != nil {
		return nil, err
	}
	defer c.end()
	return c.MCPClient.ListTools(ctx)
}
func (c *managedMCPClient) ListResources(ctx context.Context) ([]*types.MCPResource, error) {
	if err := c.begin(); err != nil {
		return nil, err
	}
	defer c.end()
	return c.MCPClient.ListResources(ctx)
}
func (c *managedMCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*CallToolResult, error) {
	if err := c.begin(); err != nil {
		return nil, err
	}
	defer c.end()
	return c.MCPClient.CallTool(ctx, name, args)
}
func (c *managedMCPClient) ReadResource(ctx context.Context, uri string) (*ReadResourceResult, error) {
	if err := c.begin(); err != nil {
		return nil, err
	}
	defer c.end()
	return c.MCPClient.ReadResource(ctx, uri)
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
		clients:              make(map[string]MCPClient),
		connecting:           make(map[string]*pendingMCPConnection),
		maxDynamicPerService: 512,
		maxDynamicTotal:      2048,
		oauthRepo:            oauthRepo,
		ctx:                  ctx,
		cancel:               cancel,
	}

	// Start cleanup goroutine
	go manager.cleanupIdleConnections()

	return manager
}

// cacheKey computes the connection-cache key for a service. OAuth services are
// keyed per principal (each identity connects with its own token); other
// static-header services share a connection per service ID. Dynamic headers
// add the effective request scope in GetOrCreateClient.
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

	config, err := PrepareClientConfig(ctx, service, m.oauthRepo)
	if err != nil {
		return nil, err
	}
	principal := config.Principal
	if service.AuthConfig.IsOAuth() {
		if !principal.Valid() {
			return nil, fmt.Errorf("principal context is required to connect to OAuth MCP service %s", service.Name)
		}
	}
	key := cacheKey(service, principal)
	if config.headerScope != "" {
		key = service.ID + "\x00" + config.headerScope
	}

	m.clientsMu.Lock()
	if err := m.ctx.Err(); err != nil {
		m.clientsMu.Unlock()
		return nil, err
	}
	if client, exists := m.clients[key]; exists && client.IsConnected() {
		managed, owned := client.(*managedMCPClient)
		if !owned || managed.version.Equal(service.UpdatedAt) && managed.isAvailable() {
			if owned {
				managed.touch()
			}
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
		if config.headerScope != "" {
			m.removeDisconnectedClientsLocked(time.Now())
			perService, total := m.dynamicCapacityLocked(service.ID)
			if perService >= m.maxDynamicPerService || total >= m.maxDynamicTotal {
				m.clientsMu.Unlock()
				logger.GetLogger(ctx).Warnf("MCP dynamic connection capacity reached for service %s: service=%d total=%d", service.ID, perService, total)
				return nil, fmt.Errorf("MCP dynamic connection capacity reached for service %s", service.Name)
			}
		}
		lifeCtx, cancel := context.WithCancel(m.ctx)
		pending = &pendingMCPConnection{done: make(chan struct{}), cancel: cancel, version: service.UpdatedAt, serviceID: service.ID, dynamic: config.headerScope != ""}
		m.connecting[key] = pending
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
		pending.client = &managedMCPClient{MCPClient: client, cancel: pending.cancel, version: pending.version, serviceID: config.Service.ID, dynamic: config.headerScope != "", lastUsed: time.Now()}
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
	ticker := time.NewTicker(30 * time.Second)
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
	m.removeDisconnectedClientsLocked(time.Now())
}

func (m *MCPManager) removeDisconnectedClientsLocked(now time.Time) {
	for serviceID, client := range m.clients {
		managed, owned := client.(*managedMCPClient)
		idle := owned && managed.retireIdle(now)
		if idle || !client.IsConnected() {
			_ = client.Disconnect()
			delete(m.clients, serviceID)
			logger.GetLogger(m.ctx).Infof("Removed disconnected MCP client: %s", serviceID)
		}
	}
}

// dynamicCapacityLocked counts live sessions and in-progress connections.
// Caller holds clientsMu so concurrent new keys cannot exceed the limits.
func (m *MCPManager) dynamicCapacityLocked(serviceID string) (perService, total int) {
	for _, client := range m.clients {
		if managed, ok := client.(*managedMCPClient); ok && managed.dynamic {
			total++
			if managed.serviceID == serviceID {
				perService++
			}
		}
	}
	for _, pending := range m.connecting {
		if pending.dynamic {
			total++
			if pending.serviceID == serviceID {
				perService++
			}
		}
	}
	return perService, total
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

	seen := make(map[string]bool)
	services := make([]string, 0, len(m.clients))
	for key, client := range m.clients {
		if client.IsConnected() {
			serviceID, _, _ := strings.Cut(key, "\x00")
			if !seen[serviceID] {
				seen[serviceID] = true
				services = append(services, serviceID)
			}
		}
	}
	sort.Strings(services)
	return services
}
