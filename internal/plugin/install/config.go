package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ErrNoSystemConfig is returned for a plugin without a config.system schema.
var ErrNoSystemConfig = errors.New("this plugin has no platform configuration")

// SystemConfig is a plugin's platform configuration as the UI sees it.
type SystemConfig struct {
	Schema json.RawMessage `json:"schema"`
	// Values has secrets redacted.
	Values map[string]any `json:"values"`
}

func (s *Service) systemSchema(ctx context.Context, id string) (*types.InstalledPlugin, *configschema.Schema,
	json.RawMessage, error,
) {
	view, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	if view.Manifest == nil || len(view.Manifest.Config.SystemSchema) == 0 {
		return nil, nil, nil, ErrNoSystemConfig
	}
	raw := view.Manifest.Config.SystemSchema
	schema, err := configschema.Parse(raw)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("plugin %s system schema: %w", id, err)
	}
	row := view.InstalledPlugin
	return &row, schema, raw, nil
}

func systemValues(row *types.InstalledPlugin) map[string]any {
	var v map[string]any
	if len(row.SystemConfig) == 0 || json.Unmarshal(row.SystemConfig, &v) != nil || v == nil {
		return map[string]any{}
	}
	return v
}

// GetSystemConfig returns a plugin's platform configuration, secrets
// redacted.
func (s *Service) GetSystemConfig(ctx context.Context, id string) (*SystemConfig, error) {
	row, schema, raw, err := s.systemSchema(ctx, id)
	if err != nil {
		return nil, err
	}
	return &SystemConfig{Schema: raw, Values: configschema.Redact(schema, systemValues(row))}, nil
}

// SetSystemConfig validates and stores a plugin's platform configuration.
// Validation failures come back as configschema.FieldErrors.
func (s *Service) SetSystemConfig(ctx context.Context, id string, values map[string]any) (*SystemConfig, error) {
	row, schema, _, err := s.systemSchema(ctx, id)
	if err != nil {
		return nil, err
	}
	sealed, err := configschema.Update(schema, systemValues(row), values)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(sealed)
	if err != nil {
		return nil, err
	}
	row.SystemConfig = types.JSON(b)
	if err := s.repo.UpdatePlugin(ctx, row, "system_config"); err != nil {
		return nil, err
	}
	return s.GetSystemConfig(ctx, id)
}

// OpenSystemConfig returns the platform configuration of a loaded plugin with
// secrets decrypted, for code about to use it, and when the row last
// changed. m is the manifest the node runs, which carries the schema.
func OpenSystemConfig(
	ctx context.Context, repo interfaces.PluginRepository, m *manifest.Manifest,
) (map[string]any, time.Time, error) {
	if len(m.Config.SystemSchema) == 0 {
		return map[string]any{}, time.Time{}, nil
	}
	schema, err := configschema.Parse(m.Config.SystemSchema)
	if err != nil {
		return nil, time.Time{}, err
	}
	row, err := repo.GetPlugin(ctx, m.ID)
	if err != nil {
		return nil, time.Time{}, err
	}
	if row == nil {
		return nil, time.Time{}, ErrNotInstalled
	}
	values, err := configschema.Open(schema, systemValues(row))
	if err != nil {
		return nil, time.Time{}, err
	}
	return values, row.UpdatedAt, nil
}
