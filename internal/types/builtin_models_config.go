package types

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Logging via stdlib `log` (with a [builtin-models] prefix) instead of the
// project's structured logger because `internal/logger` itself imports
// `internal/types`, creating an import cycle if we use it from here. The
// same constraint forces sibling files in this package (e.g. model.go's
// crypto error logs) to use stdlib log too. The prefix gives grep/Splunk
// users a stable handle even though the lines are unstructured.

// BuiltinModelManagedBy is the value written into `models.managed_by` for
// rows whose lifecycle is owned by config/builtin_models.yaml. Other rows
// (UI / API / hand-written SQL) keep the column at its default empty string
// and are never touched by the YAML loader.
const BuiltinModelManagedBy = "yaml"

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
// BUILTIN_MODELS_CONFIG) and reconciles the YAML-managed slice of `models`
// with the file contents.
//
// Lifecycle contract:
//   - YAML loader only ever writes rows tagged managed_by="yaml". Rows created
//     or explicitly overridden via UI/API/SQL (managed_by="") are detected by
//     id and preserved, and are never modified.
//   - Each YAML entry is UPSERTed by id, with deleted_at force-reset to NULL
//     (so a row that was soft-deleted previously is resurrected when it
//     reappears in the file).
//   - After all UPSERTs, any pre-existing yaml-managed row whose id is no
//     longer in the file is soft-deleted. Removing an entry from YAML is
//     therefore the supported way to retire a built-in model — no manual
//     SQL needed.
//   - If is_default=true is set on a YAML entry, the loader first clears
//     is_default on any other rows in the same (tenant_id, type) bucket,
//     mirroring the invariant enforced by the API path. Multiple entries
//     with is_default=true for the same bucket result in last-one-wins with
//     a warning.
//
// Failure handling:
//   - file not found / mount point is a directory / path unset: no-op
//   - YAML parse error: prints a warning and aborts the reconcile (the
//     drift sweep is NOT run, so a malformed file cannot accidentally wipe
//     YAML-managed rows)
//   - per-entry UPSERT error: prints a warning, the entry is dropped from
//     the "current YAML id set" so the sweep won't delete its existing
//     row either (treats the failure as "leave alone")
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
		log.Printf("[builtin-models] config not present at %s; skipping", path)
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[builtin-models] WARN: read config %s failed: %v; skipping", path, err)
		return nil
	}

	expanded := interpolateBuiltinModelEnv(string(raw))

	var file builtinModelsFile
	if err := yaml.Unmarshal([]byte(expanded), &file); err != nil {
		log.Printf("[builtin-models] WARN: parse config %s failed: %v; skipping reconcile", path, err)
		return nil
	}

	// yamlIDs collects every id we successfully upserted this run. Used by
	// the drift sweep below to determine which previously-yaml-managed rows
	// have disappeared and should be retired.
	yamlIDs := make([]string, 0, len(file.BuiltinModels))
	applied := 0

	for i := range file.BuiltinModels {
		e := &file.BuiltinModels[i]
		if err := validateBuiltinModelEntry(e, i); err != nil {
			log.Printf("[builtin-models] WARN: %v; skipping", err)
			continue
		}
		m := e.toModel()

		// A system-admin edit clears managed_by to claim the row as a runtime
		// override. Preserve that explicit override on subsequent starts rather
		// than reclaiming and overwriting it from YAML. Unscoped is required so
		// a manually managed soft-deleted collision is not resurrected either.
		var existing Model
		lookupErr := db.WithContext(ctx).Unscoped().
			Select("id", "managed_by").
			Where("id = ?", m.ID).
			First(&existing).Error
		if lookupErr == nil && existing.ManagedBy != BuiltinModelManagedBy {
			log.Printf("[builtin-models] preserving runtime override: id=%s", m.ID)
			continue
		}
		if lookupErr != nil && lookupErr != gorm.ErrRecordNotFound {
			log.Printf("[builtin-models] WARN: inspect existing model %s failed: %v; skipping", m.ID, lookupErr)
			continue
		}

		// Mirror the API path's "single default per (tenant_id, type)"
		// invariant: clear other defaults before promoting this one.
		// Excluded our own id so we don't churn against ourselves.
		if m.IsDefault {
			if err := db.WithContext(ctx).
				Model(&Model{}).
				Where("tenant_id = ? AND type = ? AND id <> ? AND is_default = ?",
					m.TenantID, m.Type, m.ID, true).
				Update("is_default", false).Error; err != nil {
				log.Printf("[builtin-models] WARN: clear existing default for tenant=%d type=%s failed: %v; continuing",
					m.TenantID, m.Type, err)
			}
		}

		res := db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"tenant_id", "name", "type", "source", "description",
				"parameters", "is_default", "status", "is_builtin",
				"managed_by", "deleted_at", "updated_at",
			}),
		}).Create(&m)
		if res.Error != nil {
			log.Printf("[builtin-models] WARN: upsert %s failed: %v; continuing", e.ID, res.Error)
			continue
		}
		applied++
		yamlIDs = append(yamlIDs, e.ID)
		log.Printf("[builtin-models] upserted: id=%s name=%s type=%s", e.ID, e.Name, e.Type)
	}

	// Drift sweep: retire YAML-managed rows that no longer appear in the
	// file. Manual rows (managed_by='') are untouched. Uses GORM soft delete
	// so the row remains recoverable.
	pruned, sweepErr := pruneOrphanYAMLManagedModels(ctx, db, yamlIDs)
	if sweepErr != nil {
		log.Printf("[builtin-models] WARN: drift sweep failed: %v; continuing", sweepErr)
	}

	log.Printf("[builtin-models] applied: %d upserted, %d pruned from %s", applied, pruned, path)
	return nil
}

// pruneOrphanYAMLManagedModels soft-deletes rows where managed_by='yaml'
// and id is NOT in keepIDs. Used to retire entries that have been removed
// from builtin_models.yaml. Returns the number of rows affected.
//
// Safety:
//   - Only rows tagged managed_by='yaml' are eligible — manual rows are
//     never visible to this query.
//   - Already-soft-deleted rows are skipped (GORM default scope), so this
//     is idempotent across restarts.
//   - When keepIDs is empty the sweep retires ALL yaml-managed rows. That
//     matches the natural reading of "the YAML file declares zero entries
//     ⇒ no yaml-managed models should exist". The caller has already
//     short-circuited on parse failure, so we only reach this branch when
//     the operator deliberately reduced the file to an empty list.
func pruneOrphanYAMLManagedModels(ctx context.Context, db *gorm.DB, keepIDs []string) (int64, error) {
	q := db.WithContext(ctx).
		Where("managed_by = ?", BuiltinModelManagedBy)
	if len(keepIDs) > 0 {
		q = q.Where("id NOT IN ?", keepIDs)
	}
	res := q.Delete(&Model{})
	return res.RowsAffected, res.Error
}

// validBuiltinModelTypes is the set of model types the loader accepts.
// Mirrors the ModelType* constants. Anything else is rejected with a
// warning rather than persisted, because provider factories match types
// case-sensitively and a misspelled YAML entry would produce a row that
// looks present in the table but is unusable downstream.
var validBuiltinModelTypes = map[ModelType]struct{}{
	ModelTypeKnowledgeQA: {},
	ModelTypeEmbedding:   {},
	ModelTypeRerank:      {},
	ModelTypeVLLM:        {},
	ModelTypeASR:         {},
}

// validBuiltinModelStatuses is the set of statuses the loader accepts.
// Empty is allowed and is normalized to ModelStatusActive in toModel().
var validBuiltinModelStatuses = map[ModelStatus]struct{}{
	ModelStatusActive:         {},
	ModelStatusDownloading:    {},
	ModelStatusDownloadFailed: {},
}

// validateBuiltinModelEntry returns nil if the entry is loadable, or an
// error describing what's wrong. Catches the failure modes that would
// either crash the INSERT or silently produce an unusable row:
//
//   - empty id (cannot UPSERT)
//   - id longer than the DB column (PG/SQLite cap at varchar(64),
//     see ModelIDMaxLen) which would fail at INSERT time
//   - empty or misspelled type (provider factories match exact strings)
//   - explicit non-empty status outside the known set
//
// Source is intentionally NOT validated against a fixed list because the
// provider matrix in internal/models/* keeps growing and a too-strict
// check here would force changes in two places per new provider.
func validateBuiltinModelEntry(e *BuiltinModelEntry, index int) error {
	if e.ID == "" {
		return errBuiltinModel("entry %d has empty id", index)
	}
	if len(e.ID) > ModelIDMaxLen {
		return errBuiltinModel("entry %d id %q exceeds %d-char DB limit (got %d)",
			index, e.ID, ModelIDMaxLen, len(e.ID))
	}
	if e.Type == "" {
		return errBuiltinModel("entry %d (%s) missing required field 'type'", index, e.ID)
	}
	if _, ok := validBuiltinModelTypes[e.Type]; !ok {
		return errBuiltinModel(
			"entry %d (%s) has unknown type %q (expected one of: KnowledgeQA, Embedding, Rerank, VLLM, ASR)",
			index, e.ID, e.Type)
	}
	if e.Status != "" {
		if _, ok := validBuiltinModelStatuses[e.Status]; !ok {
			return errBuiltinModel(
				"entry %d (%s) has unknown status %q (expected: active, downloading, download_failed, or empty)",
				index, e.ID, e.Status)
		}
	}
	return nil
}

// errBuiltinModel formats a validation error with a stable prefix so log
// output stays consistent with the rest of this package's warnings.
func errBuiltinModel(format string, args ...interface{}) error {
	return fmt.Errorf(format, args...)
}

// toModel converts a YAML entry to a runtime Model with sensible defaults.
// tenant_id defaults to DefaultBuiltinModelTenantID (10000, matching the
// seed value of tenants_id_seq); source defaults to "remote"; status
// defaults to "active". IsBuiltin and ManagedBy are always forced
// regardless of YAML input.
func (e *BuiltinModelEntry) toModel() Model {
	tenantID := e.TenantID
	if tenantID == 0 {
		tenantID = DefaultBuiltinModelTenantID
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
		ManagedBy:   BuiltinModelManagedBy,
		Status:      status,
	}
}
