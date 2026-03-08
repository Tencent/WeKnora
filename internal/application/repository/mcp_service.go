package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// mcpServiceRepository implements the MCPServiceRepository interface
type mcpServiceRepository struct {
	db *gorm.DB
}

// NewMCPServiceRepository creates a new MCP service repository
func NewMCPServiceRepository(db *gorm.DB) interfaces.MCPServiceRepository {
	return &mcpServiceRepository{db: db}
}

// Create creates a new MCP service
func (r *mcpServiceRepository) Create(ctx context.Context, service *types.MCPService) error {
	return r.db.WithContext(ctx).Create(service).Error
}

// GetByID retrieves an MCP service by ID and tenant ID
// Builtin MCP services are visible to all tenants
func (r *mcpServiceRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.MCPService, error) {
	var service types.MCPService
	err := r.db.WithContext(ctx).
		Where("id = ?", id).
		Where("tenant_id = ? OR is_builtin = true", tenantID).
		First(&service).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return &service, nil
}

// List retrieves all MCP services for a tenant
// Includes builtin MCP services visible to all tenants
func (r *mcpServiceRepository) List(ctx context.Context, tenantID uint64) ([]*types.MCPService, error) {
	var services []*types.MCPService
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? OR is_builtin = true", tenantID).
		Order("created_at DESC").
		Find(&services).Error
	if err != nil {
		return nil, err
	}

	return services, nil
}

// ListEnabled retrieves all enabled MCP services for a tenant
// Includes enabled builtin MCP services visible to all tenants
func (r *mcpServiceRepository) ListEnabled(ctx context.Context, tenantID uint64) ([]*types.MCPService, error) {
	var services []*types.MCPService
	err := r.db.WithContext(ctx).
		Where("(tenant_id = ? OR is_builtin = true) AND enabled = ?", tenantID, true).
		Order("created_at DESC").
		Find(&services).Error
	if err != nil {
		return nil, err
	}

	return services, nil
}

// ListByIDs retrieves MCP services by multiple IDs for a tenant
// Includes builtin MCP services visible to all tenants
func (r *mcpServiceRepository) ListByIDs(
	ctx context.Context,
	tenantID uint64,
	ids []string,
) ([]*types.MCPService, error) {
	if len(ids) == 0 {
		return []*types.MCPService{}, nil
	}

	var services []*types.MCPService
	err := r.db.WithContext(ctx).
		Where("(tenant_id = ? OR is_builtin = true) AND id IN ?", tenantID, ids).
		Find(&services).Error
	if err != nil {
		return nil, err
	}

	return services, nil
}

// Update updates an MCP service
func (r *mcpServiceRepository) Update(ctx context.Context, service *types.MCPService) error {
	// Build update map with only non-zero fields (except enabled which should always be updated if set)
	updateMap := make(map[string]interface{})
	updateMap["updated_at"] = service.UpdatedAt

	// Always include enabled field if it's being updated (service layer ensures it's set correctly)
	updateMap["enabled"] = service.Enabled

	if service.Name != "" {
		updateMap["name"] = service.Name
	}
	// Description can be empty, so we check if it's different from existing
	// For now, we'll always update it if provided
	updateMap["description"] = service.Description

	if service.TransportType != "" {
		updateMap["transport_type"] = service.TransportType
	}
	if service.URL != nil {
		updateMap["url"] = *service.URL
	}
	if service.StdioConfig != nil {
		updateMap["stdio_config"] = service.StdioConfig
	}
	if service.EnvVars != nil {
		updateMap["env_vars"] = service.EnvVars
	}
	if service.Headers != nil {
		updateMap["headers"] = service.Headers
	}
	if service.AuthConfig != nil {
		updateMap["auth_config"] = service.AuthConfig
	}
	if service.AdvancedConfig != nil {
		updateMap["advanced_config"] = service.AdvancedConfig
	}

	return r.db.WithContext(ctx).
		Model(&types.MCPService{}).
		Where("id = ? AND tenant_id = ?", service.ID, service.TenantID).
		Updates(updateMap).Error
}

// Delete deletes an MCP service (soft delete)
func (r *mcpServiceRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Delete(&types.MCPService{}).Error
}
