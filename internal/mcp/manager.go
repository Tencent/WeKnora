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
	retired MCPClient // closed by the connection worker before opening its replacement
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
		return principalCacheKey(service.ID, principal)
	}
	return service.ID
}

// principalCacheKey is the cache key of one principal's connection to an
// OAuth service.
func principalCacheKey(serviceID string, principal types.Principal) string {
	return serviceID + "\x00" + principal.StorageID()
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

	var retired MCPClient
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
		retired = client
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
		pending = &pendingMCPConnection{
			done: make(chan struct{}), cancel: cancel, version: service.UpdatedAt, retired: retired,
		}
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
	// Closing a session can issue a remote DELETE. Keep it off the manager
	// lock and the caller's goroutine, while preserving replacement order.
	if pending.retired != nil {
		_ = pending.retired.Disconnect()
	}
	var client MCPClient
	err := ctx.Err()
	if err == nil {
		client, err = NewMCPClient(config)
	}
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
	}
	if m.connecting[key] == pending {
		delete(m.connecting, key)
	}
	close(pending.done)
	m.clientsMu.Unlock()
	if err != nil && client != nil {
		_ = client.Disconnect()
	}
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
	for key, pending := range m.connecting {
		if key == serviceID || strings.HasPrefix(key, serviceID+"\x00") {
			pending.cancel()
			delete(m.connecting, key)
		}
	}

	retired := make(map[string]MCPClient)
	for key, client := range m.clients {
		// Match the plain service-ID key as well as per-principal OAuth keys
		// ("<serviceID>\x00<principal>").
		if key != serviceID && !strings.HasPrefix(key, serviceID+"\x00") {
			continue
		}
		retired[key] = client
		delete(m.clients, key)
	}
	m.clientsMu.Unlock()

	// Remove only the captured clients. A replacement may be installed while
	// a remote server is still acknowledging the old session's closure.
	for key, client := range retired {
		if err := client.Disconnect(); err != nil {
			logger.GetLogger(m.ctx).Errorf("Failed to disconnect MCP client %s: %v", key, err)
		}
		logger.GetLogger(m.ctx).Infof("MCP client closed: %s", key)
	}
	return nil
}

// CloseClientForPrincipal closes the connection one principal holds to an
// OAuth service and retires that principal's connection attempt if one is
// still in flight. Connections of other principals stay open, so the requests
// they have in flight are not canceled. A service without OAuth shares one
// connection that carries no per-user credential; it is left alone.
func (m *MCPManager) CloseClientForPrincipal(serviceID string, principal types.Principal) error {
	if serviceID == "" || !principal.Valid() {
		return nil
	}
	detached := make(map[string]MCPClient)
	m.clientsMu.Lock()
	m.detachLocked(principalCacheKey(serviceID, principal), detached)
	m.clientsMu.Unlock()
	m.disconnectDetached(serviceID, detached)
	return nil
}

// CloseClientsAfterAuthorization recycles the cached connections that a
// completed OAuth authorization of principal leaves outdated:
//
//   - The principal's own connection. The server may have tied its MCP
//     session, and the tools it advertised, to the account that authorized
//     it, and the new authorization may be for another account. An SSE event
//     stream also keeps the credentials it was opened with.
//   - Connections of the service in the same tenant that were built with an
//     OAuth client ID other than the registration stored now. The registration
//     is shared by every principal of the service and may have been replaced,
//     in this process or in another replica. A connection still holding the
//     old client ID can fail its next token refresh with invalid_client, and
//     handling that failure deletes the stored registration, which by then is
//     the replacement.
//
// Other connections stay open: each one reads its principal's token from the
// store on every request, and closing a connection cancels the requests it has
// in flight. Only connections cached by this process are examined, and
// connection attempts of other principals that are still in flight are not.
// If the stored registration cannot be read, every connection of the service
// is closed, unless ctx has ended, in which case only the principal's own
// connection is: a caller that went away must not cancel other principals'
// requests.
func (m *MCPManager) CloseClientsAfterAuthorization(
	ctx context.Context, tenantID uint64, serviceID string, principal types.Principal,
) error {
	if serviceID == "" {
		return nil
	}
	if m.oauthRepo == nil {
		// Without the repository no OAuth connection can be built, so there is
		// no registration to compare against.
		return m.CloseClientForPrincipal(serviceID, principal)
	}
	registration, err := m.oauthRepo.GetClient(ctx, tenantID, serviceID)
	if err != nil && ctx.Err() != nil {
		logger.GetLogger(ctx).Warnf(
			"Failed to read MCP OAuth client registration service=%s, closing only principal=%s: %v",
			serviceID, principal.StorageID(), err,
		)
		return m.CloseClientForPrincipal(serviceID, principal)
	}
	if err != nil {
		logger.GetLogger(ctx).Warnf(
			"Failed to read MCP OAuth client registration service=%s, closing all its connections: %v", serviceID, err,
		)
		return m.CloseClient(serviceID)
	}
	currentClientID := ""
	if registration != nil {
		currentClientID = registration.ClientID
	}

	detached := make(map[string]MCPClient)
	m.clientsMu.Lock()
	if principal.Valid() {
		m.detachLocked(principalCacheKey(serviceID, principal), detached)
	}
	for key, client := range m.clients {
		if !strings.HasPrefix(key, serviceID+"\x00") {
			continue
		}
		connTenantID, clientID, ok := oauthClientRegistrationOf(client)
		if ok && connTenantID == tenantID && clientID != currentClientID {
			m.detachLocked(key, detached)
		}
	}
	m.clientsMu.Unlock()
	m.disconnectDetached(serviceID, detached)
	return nil
}

// oauthClientRegistrationOf reports the tenant and OAuth client ID a cached
// connection was built with; ok is false for a connection without OAuth.
func oauthClientRegistrationOf(client MCPClient) (tenantID uint64, clientID string, ok bool) {
	if managed, owned := client.(*managedMCPClient); owned {
		client = managed.MCPClient
	}
	registered, ok := client.(interface {
		oauthClientRegistration() (uint64, string, bool)
	})
	if !ok {
		return 0, "", false
	}
	return registered.oauthClientRegistration()
}

// detachLocked removes key's connection and in-flight connection attempt from
// the manager, cancelling the attempt and adding the connection to detached.
// The caller holds clientsMu and disconnects the detached connections once it
// has released the lock, since closing a transport may wait on the server.
func (m *MCPManager) detachLocked(key string, detached map[string]MCPClient) {
	if pending, ok := m.connecting[key]; ok {
		pending.cancel()
		delete(m.connecting, key)
	}
	if client, ok := m.clients[key]; ok {
		detached[key] = client
		delete(m.clients, key)
	}
}

// disconnectDetached disconnects connections of serviceID that detachLocked
// removed from the manager.
func (m *MCPManager) disconnectDetached(serviceID string, detached map[string]MCPClient) {
	for key, client := range detached {
		principal := strings.TrimPrefix(key, serviceID+"\x00")
		if err := client.Disconnect(); err != nil {
			logger.GetLogger(m.ctx).Errorf(
				"Failed to disconnect MCP client service=%s principal=%s: %v", serviceID, principal, err,
			)
		}
		logger.GetLogger(m.ctx).Infof("MCP client closed: service=%s principal=%s", serviceID, principal)
	}
}

// InvalidateToolSchemas drops the header-schema caches of every cached
// connection for a service after listedBy completed a newer directory read.
// Connections stay open; their next modern CallTool reloads the directory.
// listedBy's own cache is current and is kept.
func (m *MCPManager) InvalidateToolSchemas(serviceID string, listedBy MCPClient) {
	m.clientsMu.RLock()
	defer m.clientsMu.RUnlock()
	for key, client := range m.clients {
		if key != serviceID && !strings.HasPrefix(key, serviceID+"\x00") {
			continue
		}
		if client == listedBy {
			continue
		}
		if managed, ok := client.(*managedMCPClient); ok {
			if managed.MCPClient == listedBy {
				continue
			}
			client = managed.MCPClient
		}
		if cached, ok := client.(interface{ invalidateToolSchemas() }); ok {
			cached.invalidateToolSchemas()
		}
	}
}

// closeAllTimeout bounds CloseAll. Disconnecting a remote transport can mean
// a request to the server, and one that stopped answering must not hold up
// process exit.
var closeAllTimeout = 5 * time.Second

// CloseAll closes all clients. The clients are detached under the lock and
// disconnected concurrently outside it, so one slow server neither serializes
// the rest nor blocks callers waiting on the lock; whatever has not finished
// within closeAllTimeout is abandoned.
func (m *MCPManager) CloseAll() {
	m.clientsMu.Lock()
	for key, pending := range m.connecting {
		pending.cancel()
		delete(m.connecting, key)
	}
	clients := m.clients
	m.clients = make(map[string]MCPClient)
	m.clientsMu.Unlock()

	var wg sync.WaitGroup
	for key, client := range clients {
		wg.Add(1)
		go func(key string, client MCPClient) {
			defer wg.Done()
			if err := client.Disconnect(); err != nil {
				logger.GetLogger(m.ctx).Errorf("Failed to disconnect MCP client %s: %v", key, err)
			}
		}(key, client)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.GetLogger(m.ctx).Info("All MCP clients closed")
	case <-time.After(closeAllTimeout):
		logger.GetLogger(m.ctx).Warnf("MCP clients still disconnecting after %s; abandoning them", closeAllTimeout)
	}
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
