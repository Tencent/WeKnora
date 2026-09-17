package database

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// schemaRequirement is a column/table the running binary needs. When
// schema_migrations claims a high version but these are missing (restored
// dump + forced version, or silent migrate skip), the app serves 500s on
// session create / agent-chat / sandbox APIs. Checking here fails closed.
type schemaRequirement struct {
	table          string
	column         string // empty => require the table itself
	sinceMigration int
	symptomHint    string
}

// criticalSchemaRequirements covers objects that caused production 500s when
// schema_migrations was ahead of the real schema (TreeRAG offline upgrade).
var criticalSchemaRequirements = []schemaRequirement{
	{
		table: "wiki_pages", column: "parent_slug", sinceMigration: 61,
		symptomHint: "GET /wiki/index hierarchy fields",
	},
	{
		table: "wiki_pages", column: "wiki_path", sinceMigration: 61,
		symptomHint: "GET /wiki/index directory sort",
	},
	{
		table: "wiki_pages", column: "last_edit_source", sinceMigration: 75,
		symptomHint: "wiki page load / revisions",
	},
	{
		table: "wiki_folders", sinceMigration: 61,
		symptomHint: "wiki folder browser",
	},
	{
		table: "knowledges", column: "custom_metadata", sinceMigration: 78,
		symptomHint: "knowledge upload / custom metadata",
	},
	{
		table: "chunks", column: "source_content", sinceMigration: 78,
		symptomHint: "editable chunks",
	},
	{
		table: "knowledges", column: "folder_path", sinceMigration: 79,
		symptomHint: "knowledge folder listing",
	},
	{
		table: "messages", column: "artifacts", sinceMigration: 81,
		symptomHint: "agent-chat message insert",
	},
	{
		table: "tenant_sandbox_configs", sinceMigration: 82,
		symptomHint: "GET /api/v1/sandbox-configs",
	},
	{
		table: "sessions", column: "sandbox_config_id", sinceMigration: 83,
		symptomHint: "POST /sessions and embed session create",
	},
	{
		table: "messages", column: "usage", sinceMigration: 85,
		symptomHint: "message token usage persistence",
	},
	{
		table: "org_units", sinceMigration: 96,
		symptomHint: "org-unit tree APIs",
	},
	{
		table: "knowledge_bases", column: "org_unit_id", sinceMigration: 96,
		symptomHint: "KB org-unit scoping",
	},
	{
		table: "knowledge_bases", column: "share_with_descendants", sinceMigration: 98,
		symptomHint: "KB share-to-descendants",
	},
	{
		table: "guest_link_channels", sinceMigration: 103,
		symptomHint: "guest / web publish links",
	},
	{
		table: "guest_link_channels", column: "session_secret", sinceMigration: 105,
		symptomHint: "embed session HMAC handles",
	},
	{
		table: "agent_publish_api_keys", sinceMigration: 108,
		symptomHint: "agent publish API keys",
	},
}

// SchemaDrift describes one missing table or column.
type SchemaDrift struct {
	Table          string
	Column         string
	SinceMigration int
	SymptomHint    string
}

func (d SchemaDrift) String() string {
	if d.Column == "" {
		return fmt.Sprintf(
			"missing table %q (migration >= %d; affects %s)",
			d.Table, d.SinceMigration, d.SymptomHint,
		)
	}
	return fmt.Sprintf(
		"missing column %s.%s (migration >= %d; affects %s)",
		d.Table, d.Column, d.SinceMigration, d.SymptomHint,
	)
}

// DetectSchemaDrift returns critical schema objects absent from db.
func DetectSchemaDrift(db *gorm.DB) []SchemaDrift {
	if db == nil {
		return nil
	}
	migrator := db.Migrator()
	var drifts []SchemaDrift
	for _, req := range criticalSchemaRequirements {
		missing := false
		if req.column == "" {
			missing = !migrator.HasTable(req.table)
		} else if !migrator.HasTable(req.table) {
			missing = true
		} else {
			missing = !migrator.HasColumn(req.table, req.column)
		}
		if !missing {
			continue
		}
		drifts = append(drifts, SchemaDrift{
			Table:          req.table,
			Column:         req.column,
			SinceMigration: req.sinceMigration,
			SymptomHint:    req.symptomHint,
		})
	}
	return drifts
}

// LowestRepairVersion returns the schema_migrations version to force so
// golang-migrate re-applies from the first missing migration. Returns -1 when
// there is no drift.
func LowestRepairVersion(drifts []SchemaDrift) int {
	if len(drifts) == 0 {
		return -1
	}
	lowest := drifts[0].SinceMigration
	for _, drift := range drifts[1:] {
		if drift.SinceMigration < lowest {
			lowest = drift.SinceMigration
		}
	}
	return lowest - 1
}

// FormatSchemaDriftError builds an operator-facing error for startup failure.
func FormatSchemaDriftError(drifts []SchemaDrift) error {
	if len(drifts) == 0 {
		return nil
	}
	parts := make([]string, 0, len(drifts))
	for _, drift := range drifts {
		parts = append(parts, drift.String())
	}
	repairTo := LowestRepairVersion(drifts)
	return fmt.Errorf(
		"schema drift detected (schema_migrations ahead of real tables): %s. "+
			"Repair: UPDATE schema_migrations SET version=%d, dirty=false; "+
			"then restart the app so AUTO_MIGRATE re-applies pending SQL. "+
			"Or set SCHEMA_DRIFT_FATAL=false to boot anyway (not recommended)",
		strings.Join(parts, "; "),
		repairTo,
	)
}
