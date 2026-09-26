package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/types"
)

// ErrNoTenantConfig is returned for a plugin without a config.tenant schema.
var ErrNoTenantConfig = errors.New("this plugin has no workspace configuration")

// TenantConfig is a workspace's plugin configuration as the UI sees it.
type TenantConfig struct {
	Schema json.RawMessage `json:"schema"`
	// Values has secrets redacted.
	Values    map[string]any `json:"values"`
	UpdatedAt time.Time      `json:"updatedAt,omitzero"`
}

func (s *Service) tenantSchema(tenantID uint64, pluginID string) (*configschema.Schema, json.RawMessage, error) {
	m, ok := s.registry.Plugin(pluginID)
	if !ok || !s.registry.VisibleTo(pluginID, tenantID) {
		return nil, nil, ErrUnknownPlugin
	}
	raw := m.Config.TenantSchema
	if len(raw) == 0 {
		return nil, nil, ErrNoTenantConfig
	}
	schema, err := configschema.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("plugin %s tenant schema: %w", pluginID, err)
	}
	return schema, raw, nil
}

func storedValues(row *types.PluginTenantSetting) map[string]any {
	if row == nil || len(row.Config) == 0 {
		return map[string]any{}
	}
	var v map[string]any
	if json.Unmarshal(row.Config, &v) != nil || v == nil {
		return map[string]any{}
	}
	return v
}

// Config returns the tenant's configuration of a plugin, secrets redacted.
func (s *Service) Config(ctx context.Context, tenantID uint64, pluginID string) (*TenantConfig, error) {
	schema, raw, err := s.tenantSchema(tenantID, pluginID)
	if err != nil {
		return nil, err
	}
	row, err := s.repo.Get(ctx, tenantID, pluginID)
	if err != nil {
		return nil, err
	}
	out := &TenantConfig{Schema: raw, Values: configschema.Redact(schema, storedValues(row))}
	if row != nil {
		out.UpdatedAt = row.UpdatedAt
	}
	return out, nil
}

// SetConfig validates and stores the tenant's configuration of a plugin.
// Secrets sent back redacted keep their stored value. Validation failures
// come back as configschema.FieldErrors.
func (s *Service) SetConfig(
	ctx context.Context, tenantID uint64, pluginID string, values map[string]any, updatedBy string,
) (*TenantConfig, error) {
	schema, _, err := s.tenantSchema(tenantID, pluginID)
	if err != nil {
		return nil, err
	}
	row, err := s.repo.Get(ctx, tenantID, pluginID)
	if err != nil {
		return nil, err
	}
	sealed, err := configschema.Update(schema, storedValues(row), values)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(sealed)
	if err != nil {
		return nil, err
	}
	enabled := row != nil && row.Enabled
	if row == nil {
		m, _ := s.registry.Plugin(pluginID)
		enabled = enabledByDefault(m)
	}
	if err := s.repo.Upsert(ctx, &types.PluginTenantSetting{
		TenantID: tenantID, PluginID: pluginID, Enabled: enabled, Config: types.JSON(b),
		UpdatedBy: updatedBy, UpdatedAt: time.Now(),
	}, "config"); err != nil {
		return nil, err
	}
	return s.Config(ctx, tenantID, pluginID)
}

// OpenConfig returns the tenant's configuration with secrets decrypted, for
// code about to use it, and when it last changed.
func (s *Service) OpenConfig(ctx context.Context, tenantID uint64, pluginID string) (map[string]any, time.Time, error) {
	schema, _, err := s.tenantSchema(tenantID, pluginID)
	if errors.Is(err, ErrNoTenantConfig) {
		return map[string]any{}, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	row, err := s.repo.Get(ctx, tenantID, pluginID)
	if err != nil {
		return nil, time.Time{}, err
	}
	values, err := configschema.Open(schema, storedValues(row))
	if err != nil {
		return nil, time.Time{}, err
	}
	var updated time.Time
	if row != nil {
		updated = row.UpdatedAt
	}
	return values, updated, nil
}
