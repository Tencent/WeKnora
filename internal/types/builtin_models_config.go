package types

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BuiltinModelEntry mirrors one entry in builtin_models.yaml.
// Each entry becomes a row in the models table with is_builtin=true.
type BuiltinModelEntry struct {
	ID          string          `yaml:"id"`
	TenantID    uint64          `yaml:"tenant_id"`
	Name        string          `yaml:"name"`
	Type        ModelType       `yaml:"type"`
	Source      ModelSource     `yaml:"source"`
	Description string          `yaml:"description"`
	IsDefault   bool            `yaml:"is_default"`
	Status      ModelStatus     `yaml:"status"`
	Parameters  ModelParameters `yaml:"parameters"`
}

type builtinModelsFile struct {
	BuiltinModels []BuiltinModelEntry `yaml:"builtin_models"`
}

// builtinModelEnvPattern matches ${NAME} placeholders. Mirrors the pattern in
// internal/config/config.go so YAML interpolation behaves identically to the
// main config.yaml flow.
var builtinModelEnvPattern = regexp.MustCompile(`\${([^}]+)}`)

// interpolateBuiltinModelEnv substitutes ${NAME} occurrences with the
// corresponding os.Getenv value. Unset vars are left as the literal ${NAME}
// so misconfiguration surfaces visibly in downstream provider calls instead
// of failing silently with an empty token.
func interpolateBuiltinModelEnv(s string) string {
	return builtinModelEnvPattern.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		if v := os.Getenv(name); v != "" {
			return v
		}
		return m
	})
}

// LoadBuiltinModelsConfig reads builtin_models.yaml (or the path pointed to by
// BUILTIN_MODELS_CONFIG) and UPSERTs each entry into the models table.
//
// Behaviour:
//   - file not found / mount point is a directory / path unset: no-op
//   - YAML parse error: prints a warning, returns nil (never aborts startup)
//   - per-entry UPSERT error: prints a warning, continues with the next entry
//   - never deletes entries that disappeared from YAML: operators may have
//     seeded extras manually via SQL; removing them here would be surprising
func LoadBuiltinModelsConfig(ctx context.Context, db *gorm.DB, configDir string) error {
	path := os.Getenv("BUILTIN_MODELS_CONFIG")
	if path == "" {
		path = filepath.Join(configDir, "builtin_models.yaml")
	}

	// Treat "missing", "is a directory", and other non-regular-file cases the
	// same way. Docker bind-mounting a non-existent source file silently
	// substitutes a directory; we don't want that to spam WARN logs.
	info, statErr := os.Stat(path)
	if statErr != nil || !info.Mode().IsRegular() {
		fmt.Printf("Built-in models config not present at %s; skipping.\n", path)
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("Warning: read built-in models config %s failed: %v; skipping.\n", path, err)
		return nil
	}

	expanded := interpolateBuiltinModelEnv(string(raw))

	var file builtinModelsFile
	if err := yaml.Unmarshal([]byte(expanded), &file); err != nil {
		fmt.Printf("Warning: parse built-in models config %s failed: %v; skipping.\n", path, err)
		return nil
	}

	if len(file.BuiltinModels) == 0 {
		fmt.Printf("Built-in models config %s contains no entries.\n", path)
		return nil
	}

	applied := 0
	for i := range file.BuiltinModels {
		e := &file.BuiltinModels[i]
		if e.ID == "" {
			fmt.Printf("Warning: built-in model entry %d has empty id; skipping.\n", i)
			continue
		}
		m := e.toModel()
		res := db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"tenant_id", "name", "type", "source", "description",
				"parameters", "is_default", "status", "is_builtin", "updated_at",
			}),
		}).Create(&m)
		if res.Error != nil {
			fmt.Printf("Warning: upsert built-in model %s failed: %v; continuing.\n", e.ID, res.Error)
			continue
		}
		applied++
		fmt.Printf("Built-in model upserted: id=%s name=%s type=%s\n", e.ID, e.Name, e.Type)
	}
	fmt.Printf("Built-in models config applied: %d entries from %s.\n", applied, path)
	return nil
}

// toModel converts a YAML entry to a runtime Model with sensible defaults.
// tenant_id defaults to 10000 (matches the seed value of tenants_id_seq);
// source defaults to "remote"; status defaults to "active". IsBuiltin is
// always forced to true regardless of YAML input.
func (e *BuiltinModelEntry) toModel() Model {
	tenantID := e.TenantID
	if tenantID == 0 {
		tenantID = 10000
	}
	source := e.Source
	if source == "" {
		source = ModelSourceRemote
	}
	status := e.Status
	if status == "" {
		status = ModelStatusActive
	}
	return Model{
		ID:          e.ID,
		TenantID:    tenantID,
		Name:        e.Name,
		Type:        e.Type,
		Source:      source,
		Description: e.Description,
		Parameters:  e.Parameters,
		IsDefault:   e.IsDefault,
		IsBuiltin:   true,
		Status:      status,
	}
}
