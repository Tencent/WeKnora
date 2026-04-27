package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// mcpServiceService implements MCPServiceService interface
type mcpServiceService struct {
	mcpServiceRepo interfaces.MCPServiceRepository
	mcpManager     *mcp.MCPManager
}

// NewMCPServiceService creates a new MCP service service
func NewMCPServiceService(
	mcpServiceRepo interfaces.MCPServiceRepository,
	mcpManager *mcp.MCPManager,
) interfaces.MCPServiceService {
	return &mcpServiceService{
		mcpServiceRepo: mcpServiceRepo,
		mcpManager:     mcpManager,
	}
}

// CreateMCPService creates a new MCP service
func (s *mcpServiceService) CreateMCPService(ctx context.Context, service *types.MCPService) error {
	// Stdio transport is disabled for security reasons
	if service.TransportType == types.MCPTransportStdio {
		return fmt.Errorf("stdio transport is disabled for security reasons; please use SSE or HTTP Streamable transport instead")
	}

	// Set default advanced config if not provided
	if service.AdvancedConfig == nil {
		service.AdvancedConfig = types.GetDefaultAdvancedConfig()
	}

	// Set timestamps
	service.CreatedAt = time.Now()
	service.UpdatedAt = time.Now()

	if err := s.mcpServiceRepo.Create(ctx, service); err != nil {
		logger.GetLogger(ctx).Errorf("Failed to create MCP service: %v", err)
		return fmt.Errorf("failed to create MCP service: %w", err)
	}

	return nil
}

// GetMCPServiceByID retrieves an MCP service by ID
func (s *mcpServiceService) GetMCPServiceByID(
	ctx context.Context,
	tenantID uint64,
	id string,
) (*types.MCPService, error) {
	service, err := s.mcpServiceRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		logger.GetLogger(ctx).Errorf("Failed to get MCP service: %v", err)
		return nil, fmt.Errorf("failed to get MCP service: %w", err)
	}

	if service == nil {
		return nil, fmt.Errorf("MCP service not found")
	}

	// Redact sensitive credentials before returning (mirrors ListMCPServices).
	// Previously GetMCPServiceByID returned plaintext credentials while List
	// redacted them, creating an inconsistency (see issue #988).
	service.MaskSensitiveData()

	return service, nil
}

// ListMCPServices lists all MCP services for a tenant
func (s *mcpServiceService) ListMCPServices(ctx context.Context, tenantID uint64) ([]*types.MCPService, error) {
	services, err := s.mcpServiceRepo.List(ctx, tenantID)
	if err != nil {
		logger.GetLogger(ctx).Errorf("Failed to list MCP services: %v", err)
		return nil, fmt.Errorf("failed to list MCP services: %w", err)
	}

	// Mask sensitive data for list view
	for i, service := range services {
		if service.IsBuiltin {
			services[i] = service.HideSensitiveInfo()
		} else {
			service.MaskSensitiveData()
		}
	}

	return services, nil
}

// ListMCPServicesByIDs retrieves multiple MCP services by IDs
func (s *mcpServiceService) ListMCPServicesByIDs(
	ctx context.Context,
	tenantID uint64,
	ids []string,
) ([]*types.MCPService, error) {
	if len(ids) == 0 {
		return []*types.MCPService{}, nil
	}

	services, err := s.mcpServiceRepo.ListByIDs(ctx, tenantID, ids)
	if err != nil {
		logger.GetLogger(ctx).Errorf("Failed to list MCP services by IDs: %v", err)
		return nil, fmt.Errorf("failed to list MCP services by IDs: %w", err)
	}

	return services, nil
}

// UpdateMCPService updates an MCP service
func (s *mcpServiceService) UpdateMCPService(ctx context.Context, service *types.MCPService) error {
	// Check if service exists
	existing, err := s.mcpServiceRepo.GetByID(ctx, service.TenantID, service.ID)
	if err != nil {
		return fmt.Errorf("failed to get MCP service: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("MCP service not found")
	}

	// Builtin MCP services cannot be updated
	if existing.IsBuiltin {
		return fmt.Errorf("builtin MCP services cannot be updated")
	}

	// Determine the final transport type after merge
	finalTransportType := existing.TransportType
	if service.TransportType != "" {
		finalTransportType = service.TransportType
	}

	// Stdio transport is disabled for security reasons
	if finalTransportType == types.MCPTransportStdio {
		return fmt.Errorf("stdio transport is disabled for security reasons; please use SSE or HTTP Streamable transport instead")
	}

	// Store old enabled state BEFORE any updates
	oldEnabled := existing.Enabled

	// Merge updates: only update fields that are provided (non-zero or explicitly set)
	// This ensures that false values for enabled field are properly updated
	// Handler ensures that service.Enabled is only set if "enabled" key exists in the request
	// So we can safely update enabled field if service.Name is empty (indicating partial update)
	// or if we're updating other fields (indicating full update)
	// For enabled field, we'll update it if this is a partial update (only enabled) or if it's explicitly set
	if service.Name == "" {
		// Partial update - only update enabled field
		existing.Enabled = service.Enabled
	} else {
		// Full update - update all fields including enabled
		existing.Name = service.Name
		if service.Description != existing.Description {
			existing.Description = service.Description
		}
		existing.Enabled = service.Enabled
		if service.TransportType != "" {
			existing.TransportType = service.TransportType
		}
		if service.URL != nil {
			existing.URL = service.URL
		}
		if service.StdioConfig != nil {
			existing.StdioConfig = service.StdioConfig
		}
		if service.EnvVars != nil {
			existing.EnvVars = service.EnvVars
		}
		if service.Headers != nil {
			existing.Headers = service.Headers
		}
		if service.AuthConfig != nil {
			// Preserve existing credentials when the incoming value is the redacted
			// placeholder or empty — this prevents the UI from silently overwriting
			// real secrets with masked display values (see issue #988).
			if existing.AuthConfig == nil {
				existing.AuthConfig = service.AuthConfig
			} else {
				existing.AuthConfig.APIKey = types.PreserveIfRedacted(service.AuthConfig.APIKey, existing.AuthConfig.APIKey)
				existing.AuthConfig.Token = types.PreserveIfRedacted(service.AuthConfig.Token, existing.AuthConfig.Token)
				// Copy non-secret fields unconditionally.
				existing.AuthConfig.AuthType = service.AuthConfig.AuthType
			}
		}
		if service.AdvancedConfig != nil {
			existing.AdvancedConfig = service.AdvancedConfig
		}
	}

	// Update timestamp
	existing.UpdatedAt = time.Now()

	if err := s.mcpServiceRepo.Update(ctx, existing); err != nil {
		logger.GetLogger(ctx).Errorf("Failed to update MCP service: %v", err)
		return fmt.Errorf("failed to update MCP service: %w", err)
	}

	// Check if critical configuration changed (URL/StdioConfig, transport type, or auth config)
	configChanged := false
	if service.URL != nil && existing.URL != nil && *service.URL != *existing.URL {
		configChanged = true
	} else if (service.URL != nil) != (existing.URL != nil) {
		configChanged = true
	}
	if service.StdioConfig != nil && existing.StdioConfig != nil {
		if service.StdioConfig.Command != existing.StdioConfig.Command ||
			!equalStringSlices(service.StdioConfig.Args, existing.StdioConfig.Args) {
			configChanged = true
		}
	} else if (service.StdioConfig != nil) != (existing.StdioConfig != nil) {
		configChanged = true
	}
	if service.TransportType != "" && service.TransportType != existing.TransportType {
		configChanged = true
	}
	if service.AuthConfig != nil {
		configChanged = true
	}
	name := secutils.SanitizeForLog(existing.Name)
	// Close existing client connection if:
	// 1. Service is now disabled (need to close connection)
	// 2. Critical configuration changed (need to reconnect with new config)
	if !existing.Enabled {
		s.mcpManager.CloseClient(service.ID)
		logger.GetLogger(ctx).Infof("MCP service disabled, connection closed: %s (ID: %s)", name, service.ID)
	} else if configChanged {
		s.mcpManager.CloseClient(service.ID)
		logger.GetLogger(ctx).Infof("MCP service config changed, connection closed: %s (ID: %s)", name, service.ID)
	} else if oldEnabled != existing.Enabled && existing.Enabled {
		// Service was just enabled (was disabled, now enabled)
		// Close any existing connection to ensure clean state
		s.mcpManager.CloseClient(service.ID)
		logger.GetLogger(ctx).Infof("MCP service enabled, existing connection closed: %s (ID: %s)", name, service.ID)
	}

	logger.GetLogger(ctx).Infof("MCP service updated: %s (ID: %s), enabled: %v", name, service.ID, existing.Enabled)
	return nil
}

// DeleteMCPService deletes an MCP service
func (s *mcpServiceService) DeleteMCPService(ctx context.Context, tenantID uint64, id string) error {
	// Check if service exists
	existing, err := s.mcpServiceRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return fmt.Errorf("failed to get MCP service: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("MCP service not found")
	}

	// Builtin MCP services cannot be deleted
	if existing.IsBuiltin {
		return fmt.Errorf("builtin MCP services cannot be deleted")
	}

	// Close client connection
	s.mcpManager.CloseClient(id)

	if err := s.mcpServiceRepo.Delete(ctx, tenantID, id); err != nil {
		logger.GetLogger(ctx).Errorf("Failed to delete MCP service: %v", err)
		return fmt.Errorf("failed to delete MCP service: %w", err)
	}

	logger.GetLogger(ctx).Infof("MCP service deleted: %s (ID: %s)", secutils.SanitizeForLog(existing.Name), id)
	return nil
}

// TestMCPService tests the connection to an MCP service and returns available tools/resources
func (s *mcpServiceService) TestMCPService(
	ctx context.Context,
	tenantID uint64,
	id string,
) (*types.MCPTestResult, error) {
	// Get service
	service, err := s.mcpServiceRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP service: %w", err)
	}
	if service == nil {
		return nil, fmt.Errorf("MCP service not found")
	}

	// Create temporary client for testing
	config := &mcp.ClientConfig{
		Service: service,
	}

	client, err := mcp.NewMCPClient(config)
	if err != nil {
		return &types.MCPTestResult{
			Success: false,
			Message: fmt.Sprintf("Failed to create client: %v", err),
		}, nil
	}

	// Connect
	testCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := client.Connect(testCtx); err != nil {
		return &types.MCPTestResult{
			Success: false,
			Message: fmt.Sprintf("Connection failed: %v", err),
		}, nil
	}
	defer client.Disconnect()

	// Initialize
	initResult, err := client.Initialize(testCtx)
	if err != nil {
		return &types.MCPTestResult{
			Success: false,
			Message: fmt.Sprintf("Initialization failed: %v", err),
		}, nil
	}

	// List tools
	tools, err := client.ListTools(testCtx)
	if err != nil {
		logger.GetLogger(ctx).Warnf("Failed to list tools: %v", err)
		tools = []*types.MCPTool{}
	}

	// List resources
	resources, err := client.ListResources(testCtx)
	if err != nil {
		logger.GetLogger(ctx).Warnf("Failed to list resources: %v", err)
		resources = []*types.MCPResource{}
	}

	return &types.MCPTestResult{
		Success: true,
		Message: fmt.Sprintf(
			"Connected successfully to %s v%s",
			initResult.ServerInfo.Name,
			initResult.ServerInfo.Version,
		),
		Tools:     tools,
		Resources: resources,
	}, nil
}

// GetMCPServiceTools retrieves the list of tools from an MCP service
func (s *mcpServiceService) GetMCPServiceTools(
	ctx context.Context,
	tenantID uint64,
	id string,
) ([]*types.MCPTool, error) {
	// Get service
	service, err := s.mcpServiceRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP service: %w", err)
	}
	if service == nil {
		return nil, fmt.Errorf("MCP service not found")
	}

	// Get or create client
	client, err := s.mcpManager.GetOrCreateClient(service)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP client: %w", err)
	}

	// List tools
	tools, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	return tools, nil
}

// GetMCPServiceResources retrieves the list of resources from an MCP service
func (s *mcpServiceService) GetMCPServiceResources(
	ctx context.Context,
	tenantID uint64,
	id string,
) ([]*types.MCPResource, error) {
	// Get service
	service, err := s.mcpServiceRepo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP service: %w", err)
	}
	if service == nil {
		return nil, fmt.Errorf("MCP service not found")
	}

	// Get or create client
	client, err := s.mcpManager.GetOrCreateClient(service)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCP client: %w", err)
	}

	// List resources
	resources, err := client.ListResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	return resources, nil
}

// equalStringSlices compares two string slices for equality
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
