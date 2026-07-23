package database

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Schema parity test for the MySQL squash baseline.
//
// The squash in migrations/mysql/000000_init.up.sql is a hand-translated
// distillation of 72 PostgreSQL migrations into one MySQL file. It is
// mechanical but human-error-prone — a missed table or column surfaces
// as a late runtime error on a user's MySQL instance. These tests parse
// the file (no live MySQL needed) and assert structural invariants:
//
//   - every required metadata table is present
//   - the embeddings table is NOT present (MySQL mode delegates vectors
//     to an external retrieval engine)
//   - every CREATE TABLE carries the utf8mb4 / InnoDB table options
//   - tenants.AUTO_INCREMENT starts at 10000 (matches PG sequence)
//   - the down migration drops every table the up migration creates
//
// This is a parsing test, not a behaviour test. Column-level type
// parity is validated separately by running gorm AutoMigrate against
// the model structs and diffing — out of scope for this file.

// pathToMySqlBaseline resolves the squash file path relative to the
// test working directory. The test runs from the package dir
// (internal/database/), so the repo root is three levels up.
func pathToMySqlBaseline(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../migrations/mysql/000000_init.up.sql")
	if err != nil {
		t.Fatalf("resolve mysql baseline path: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("mysql baseline file not found at %s (has the squash been generated?): %v", abs, err)
	}
	return abs
}

// requiredMetadataTables is the canonical list of tables that must
// exist in the MySQL baseline. This mirrors the final-state table set
// after applying all 72 PostgreSQL migrations (accounting for RENAMEs
// and the deliberate exclusion of the deprecated
// organization_members_pre_plan3 table, which a fresh MySQL deployment
// does not need). If a new metadata table is added in a future PG
// migration, it must be added here too (and to the squash).
var requiredMetadataTables = []string{
	"agent_shares", "audit_logs", "auth_tokens", "chunks", "custom_agents",
	"data_sources", "embed_channels", "im_channel_sessions", "im_channels",
	"kb_shares", "knowledge_bases", "knowledge_processing_spans",
	"knowledge_tag_relations", "knowledge_tags", "knowledges",
	"mcp_oauth_clients", "mcp_oauth_tokens", "mcp_services", "mcp_tool_approvals",
	"message_suggestion_events", "message_suggestion_sets", "messages",
	"models", "organization_join_requests",
	"organization_tenant_members", "organizations", "resource_access_grants",
	"resource_bindings", "resources", "sessions", "storage_backends",
	"sync_logs", "system_settings", "task_dead_letters", "task_pending_ops",
	"temporary_documents", "tenant_api_keys", "tenant_disabled_shared_agents",
	"tenant_invitations", "tenant_members", "tenants", "user_kb_pins",
	"user_resource_favorites", "users", "vector_stores", "web_search_providers",
	"wiki_folders", "wiki_log_entries", "wiki_page_issues", "wiki_pages",
}

// extractCreateTableNames returns the lowercased table names from every
// `CREATE TABLE [IF NOT EXISTS] <name> (` in the file.
func extractCreateTableNames(t *testing.T, content string) []string {
	t.Helper()
	// Match CREATE TABLE [IF NOT EXISTS] <name> (possibly with backticks)
	re := regexp.MustCompile(`(?im)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?[\` + "`" + `]?([a-zA-Z0-9_]+)[\` + "`" + `]?\s*\(`)
	matches := re.FindAllStringSubmatch(content, -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, strings.ToLower(m[1]))
	}
	return names
}

func TestMySQLBaseline_AllRequiredTablesPresent(t *testing.T) {
	content, err := os.ReadFile(pathToMySqlBaseline(t))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	present := extractCreateTableNames(t, string(content))
	have := make(map[string]bool, len(present))
	for _, n := range present {
		have[n] = true
	}

	var missing []string
	for _, want := range requiredMetadataTables {
		if !have[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		t.Errorf("baseline is missing %d required table(s): %v", len(missing), missing)
	}
}

func TestMySQLBaseline_EmbeddingsTableNotPresent(t *testing.T) {
	content, err := os.ReadFile(pathToMySqlBaseline(t))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	// The embeddings table is PostgreSQL-only (pgvector halfvec + HNSW +
	// ParadeDB BM25). MySQL mode must NOT create it - vectors live in an
	// external retrieval engine.
	if strings.Contains(strings.ToLower(string(content)), "create table") &&
		regexp.MustCompile(`(?im)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?[\`+"`"+`]?embeddings[\`+"`"+`]?\s*\(`).MatchString(string(content)) {
		t.Fatal("baseline must NOT create an embeddings table - MySQL mode delegates vectors to RETRIEVE_DRIVER")
	}
}

// jsonArrayColumns lists every JSON column whose Go-side type is a slice
// (StringArray / JSON / WikiLogPageRefs / SuggestionItems). The MySQL
// baseline must default these to an empty array, NOT an empty object:
// under STRICT_TRANS_TABLES a `{}` default makes GORM's scan into a Go
// slice fail with "json: cannot unmarshal object into Go value of type
// <sliceType>". This was a real bug caught in external review.
var jsonArrayColumns = []struct {
	table  string
	column string
}{
	{"tenant_api_keys", "knowledge_base_ids"},
	{"tenant_api_keys", "capabilities"},
	{"embed_channels", "allowed_origins"},
	{"wiki_log_entries", "pages_affected"},
	{"message_suggestion_sets", "questions"},
	{"temporary_documents", "chunks"},
	{"temporary_documents", "image_refs"},
}

// TestMySQLBaseline_JSONSliceColumnsDefaultToArray parses the baseline
// and asserts that every JSON column whose Go type is a slice uses
// DEFAULT (JSON_ARRAY()) - not JSON_OBJECT(). A parsing test so it runs
// without a live MySQL; the behavioural round-trip is covered by
// TestJSONArrayDefaultsRoundTrip under the integration_db tag.
func TestMySQLBaseline_JSONSliceColumnsDefaultToArray(t *testing.T) {
	content, err := os.ReadFile(pathToMySqlBaseline(t))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	for _, c := range jsonArrayColumns {
		// Match the column declaration line within any CREATE TABLE.
		// The DEFAULT expression is `(JSON_ARRAY())` or `(JSON_OBJECT())`
		// - parens around a fixed identifier with an empty arg list. We
		// match that shape explicitly rather than fighting nested parens
		// with a generic capture group.
		lineRe := regexp.MustCompile(
			`(?im)^\s*` + regexp.QuoteMeta(c.column) + `\s+JSON\s+NOT\s+NULL\s+DEFAULT\s*\((JSON_ARRAY\(\)|JSON_OBJECT\(\))\)\s*,`,
		)
		m := lineRe.FindStringSubmatch(string(content))
		if m == nil {
			t.Errorf("%s.%s: no matching JSON DEFAULT clause found", c.table, c.column)
			continue
		}
		got := strings.TrimSpace(m[1])
		if !strings.EqualFold(got, "JSON_ARRAY()") {
			t.Errorf("%s.%s: JSON column default must be JSON_ARRAY() (slice Go type); got %s",
				c.table, c.column, got)
		}
	}
}

func TestMySQLBaseline_EveryTableUsesUtf8mb4InnoDB(t *testing.T) {
	content, err := os.ReadFile(pathToMySqlBaseline(t))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}

	// Split into CREATE TABLE blocks. Each must end with the table options
	// line. A block starts at "CREATE TABLE" and ends at the matching ";" —
	// but MySQL DDL can contain ";" inside string defaults, so we split
	// carefully: find each CREATE TABLE and scan forward to the ENGINE= clause.
	blocks := regexp.MustCompile(`(?is)CREATE\s+TABLE.*?ENGINE\s*=\s*InnoDB[^;]*;`).FindAllString(string(content), -1)
	if len(blocks) == 0 {
		t.Fatal("no CREATE TABLE ... ENGINE=InnoDB blocks found — every table must end with ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 ...")
	}

	for i, block := range blocks {
		lower := strings.ToLower(block)
		// The regexp above already guarantees ENGINE=InnoDB is present.
		if !strings.Contains(lower, "charset=utf8mb4") {
			t.Errorf("table block %d missing charset=utf8mb4; got tail: %s",
				i, lastN(block, 120))
		}
		if !strings.Contains(lower, "collate=utf8mb4_0900_ai_ci") {
			t.Errorf("table block %d missing collate=utf8mb4_0900_ai_ci; got tail: %s",
				i, lastN(block, 120))
		}
	}
}

// partialUniqueTables lists every table that had a PostgreSQL partial unique
// index (WHERE deleted_at IS NULL, WHERE status = 'pending', etc.) and must
// have a MySQL equivalent via a VIRTUAL generated column + UNIQUE KEY.
// Without these, MySQL allows duplicate active records that PG would prevent.
var partialUniqueTables = []struct {
	table         string
	generatedCol  string // name of the generated column
	uniqueKeyName string // name of the unique key
}{
	{"tenant_members", "live_marker", "idx_tenant_members_user_tenant_live"},
	{"organizations", "live_invite_code", "idx_organizations_live_invite_code"},
	{"kb_shares", "live_marker", "idx_kb_shares_live"},
	{"organization_join_requests", "pending_marker", "uq_org_join_requests_pending_live"},
	{"agent_shares", "live_marker", "idx_agent_shares_live"},
	{"tenant_invitations", "pending_invitee", "idx_tenant_invitations_live_pending"},
	{"tenant_invitations", "live_token", "idx_tenant_invitations_live_token"},
	{"im_channel_sessions", "live_marker", "idx_im_channel_sessions_live_channel"},
	{"im_channels", "live_bot_identity", "idx_im_channels_live_bot_identity"},
	{"embed_channels", "live_publish_token", "idx_embed_channels_live_publish_token"},
	{"vector_stores", "live_marker", "idx_vector_stores_name_tenant_live"},
	{"wiki_folders", "live_marker", "idx_wiki_folders_parent_name_live"},
	{"wiki_pages", "live_marker", "idx_wiki_pages_kb_slug_live"},
	{"storage_backends", "live_marker", "idx_storage_backends_name_live"},
	{"storage_backends", "live_legacy_marker", "idx_storage_backends_legacy_live"},
	{"resources", "live_marker", "idx_resources_location_live"},
}

func TestMySQLBaseline_PartialUniqueIndexesHaveGeneratedColumns(t *testing.T) {
	content, err := os.ReadFile(pathToMySqlBaseline(t))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	for _, c := range partialUniqueTables {
		block := extractCreateTableBlock(string(content), c.table)
		if block == "" {
			t.Errorf("table %s: CREATE TABLE block not found", c.table)
			continue
		}
		if !strings.Contains(block, c.generatedCol+" ") {
			t.Errorf("%s: generated column %q not found in table block", c.table, c.generatedCol)
		}
		if !strings.Contains(block, "GENERATED ALWAYS AS") {
			t.Errorf("%s: no GENERATED ALWAYS AS clause found", c.table)
		}
		if !strings.Contains(block, c.uniqueKeyName) {
			t.Errorf("%s: unique key %q not found", c.table, c.uniqueKeyName)
		}
	}
}

// TestMySQLBaseline_TenantIdIsUnsigned verifies that all tenant_id columns
// use BIGINT UNSIGNED (matching the Go layer's uint64 type).
func TestMySQLBaseline_TenantIdIsUnsigned(t *testing.T) {
	content, err := os.ReadFile(pathToMySqlBaseline(t))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	// Any tenant_id declared as plain INTEGER or signed BIGINT is a bug.
	signedPatterns := []string{
		"tenant_id INTEGER",
		"tenant_id BIGINT NOT NULL DEFAULT",
		"tenant_id BIGINT,",
		"tenant_id BIGINT NOT NULL,",
	}
	for _, pat := range signedPatterns {
		if strings.Contains(string(content), pat) {
			t.Errorf("found signed tenant_id column: %q — all tenant_id columns must be BIGINT UNSIGNED", pat)
		}
	}
}

// TestMySQLBaseline_UsesDateTime6 verifies that no bare TIMESTAMP column
// type remains (all should be DATETIME(6) for microsecond precision).
func TestMySQLBaseline_UsesDateTime6(t *testing.T) {
	content, err := os.ReadFile(pathToMySqlBaseline(t))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	// Find column declarations using TIMESTAMP as type (not inside CURRENT_TIMESTAMP).
	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "CURRENT_TIMESTAMP") {
			continue
		}
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		// Match "    colname TIMESTAMP" but not "TIMESTAMP WITH TIME ZONE" or "CURRENT_TIMESTAMP"
		if regexp.MustCompile(`^\s+\w+\s+TIMESTAMP(\s|$|,)`).MatchString(line) {
			t.Errorf("line %d: bare TIMESTAMP column type found, should be DATETIME(6): %s", i+1, trimmed)
		}
	}
}

func TestMySQLBaseline_TenantsAutoIncrement10000(t *testing.T) {
	content, err := os.ReadFile(pathToMySqlBaseline(t))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	// Find the tenants CREATE TABLE block specifically and check AUTO_INCREMENT=10000.
	tenantsBlock := regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?[\` + "`" + `]?tenants[\` + "`" + `]?\s*\(.*?ENGINE[^;]*;`).
		FindString(string(content))
	if tenantsBlock == "" {
		t.Fatal("no tenants CREATE TABLE block found")
	}
	if !regexp.MustCompile(`(?i)AUTO_INCREMENT\s*=\s*10000`).MatchString(tenantsBlock) {
		t.Errorf("tenants table must set AUTO_INCREMENT=10000 to match PG tenants_id_seq RESTART WITH 10000; got: %s", lastN(tenantsBlock, 150))
	}
}

func TestMySQLBaseline_DownDropsAllUpTables(t *testing.T) {
	upPath := pathToMySqlBaseline(t)
	downPath := filepath.Dir(upPath) + "/000000_init.down.sql"
	if _, err := os.Stat(downPath); err != nil {
		t.Fatalf("down migration file not found at %s: %v", downPath, err)
	}
	upContent, err := os.ReadFile(upPath)
	if err != nil {
		t.Fatalf("read up: %v", err)
	}
	downContent, err := os.ReadFile(downPath)
	if err != nil {
		t.Fatalf("read down: %v", err)
	}

	upTables := extractCreateTableNames(t, string(upContent))
	dropMatches := regexp.MustCompile(`(?im)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?[\`+"`"+`]?([a-zA-Z0-9_]+)`).
		FindAllStringSubmatch(string(downContent), -1)
	dropped := make(map[string]bool, len(dropMatches))
	for _, m := range dropMatches {
		dropped[strings.ToLower(m[1])] = true
	}

	var notDropped []string
	for _, name := range upTables {
		if !dropped[name] {
			notDropped = append(notDropped, name)
		}
	}
	if len(notDropped) > 0 {
		t.Errorf("down migration does not DROP %d table(s) created by up: %v", len(notDropped), notDropped)
	}
}

// lastN returns the last n characters of s, for compact error messages.
func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// extractColumnsFromCreateTable parses a single CREATE TABLE block and
// returns the lowercased column names declared in it. Used by the
// column-parity tests below to guard against typos and dropped columns
// in the hand-translated MySQL squash.
//
// The matcher is deliberately conservative: it picks up lines that
// start with whitespace + an identifier + a space + a known SQL type
// keyword. It skips lines that are obviously constraints
// (CONSTRAINT/PRIMARY/UNIQUE/INDEX/KEY/FOREIGN) and the closing ")" of
// the table options. This is not a full SQL parser - it is a cheap
// guard that catches the dominant failure modes (typo'd column name,
// missing column, wrong table) without false-positiving on index lines.
func extractColumnsFromCreateTable(block string) []string {
	typeLine := regexp.MustCompile(`(?im)^\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+` +
		`(?:BIGINT|INT|INTEGER|SMALLINT|TINYINT|VARCHAR|CHAR|TEXT|MEDIUMTEXT|LONGTEXT|JSON|TIMESTAMP|DATETIME|DATE|TIME|BLOB|BOOLEAN|FLOAT|DOUBLE|DECIMAL|NUMERIC)`)
	skip := regexp.MustCompile(`(?im)^\s*(CONSTRAINT|PRIMARY|UNIQUE|INDEX|KEY|FOREIGN|CHECK)\b`)
	var cols []string
	for _, line := range strings.Split(block, "\n") {
		if skip.MatchString(line) {
			continue
		}
		m := typeLine.FindStringSubmatch(line)
		if m != nil {
			cols = append(cols, strings.ToLower(m[1]))
		}
	}
	return cols
}

// extractCreateTableBlock returns the full CREATE TABLE ... ENGINE...; block
// for the given table name from the baseline file content, or "" if not
// found.
func extractCreateTableBlock(content, tableName string) string {
	re := regexp.MustCompile(
		`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?[\` + "`" + `]?` +
			regexp.QuoteMeta(tableName) + `[\` + "`" + `]?\s*\(.*?ENGINE\s*=\s*InnoDB[^;]*;`,
	)
	return re.FindString(content)
}

// requiredColumnsByTable is a hand-curated subset of columns that the
// MySQL baseline MUST declare for the named tables. It is not a full
// column list - it focuses on the columns the refactored repository
// code actually reads/writes under MySQL, so a typo or omission here
// surfaces as a runtime error in a code path the column-parity guard is
// meant to protect.
//
// Add to this map when you add a MySQL code path that reads/writes a
// new column; the test exists to make that addition deliberate.
var requiredColumnsByTable = map[string][]string{
	"tenant_api_keys":         {"id", "tenant_id", "name", "key_hash", "api_key", "scope_type", "full_access", "knowledge_base_ids", "capabilities"},
	"temporary_documents":     {"id", "tenant_id", "session_id", "chunks", "image_refs", "metadata", "status"},
	"embed_channels":          {"id", "tenant_id", "agent_id", "allowed_origins"},
	"wiki_log_entries":        {"id", "tenant_id", "knowledge_base_id", "pages_affected"},
	"message_suggestion_sets": {"id", "tenant_id", "session_id", "questions"},
	"knowledges":              {"id", "tenant_id", "knowledge_base_id", "metadata"},
	"wiki_pages":              {"id", "tenant_id", "knowledge_base_id", "slug", "title", "source_refs", "in_links"},
	"chunks":                  {"id", "tenant_id", "knowledge_base_id", "content", "is_enabled", "flags", "status", "tag_id"},
	"knowledge_bases":         {"id", "tenant_id", "name", "storage_provider_config"},
	"storage_backends":        {"id", "tenant_id", "provider"},
}

// TestMySQLBaseline_RequiredColumnsPresent asserts that every column
// listed in requiredColumnsByTable is actually declared in the MySQL
// baseline's CREATE TABLE for that table. This catches:
//
//   - typo'd column names (e.g. "knolwedge_base_id")
//   - dropped columns (a future PG migration that renames a column but
//     the MySQL squash is not updated)
//   - wrong table (a column accidentally declared on the wrong table)
//
// It does NOT check column types, defaults, nullability, or indexes -
// those are covered by the JSON-defaults tests above and the
// integration_db round-trip tests in this package. The goal here is to
// cheaply catch the dominant failure mode (column name drift) without
// maintaining a full type map.
func TestMySQLBaseline_RequiredColumnsPresent(t *testing.T) {
	content, err := os.ReadFile(pathToMySqlBaseline(t))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}

	var failures []string
	for table, requiredCols := range requiredColumnsByTable {
		block := extractCreateTableBlock(string(content), table)
		if block == "" {
			failures = append(failures, fmt.Sprintf("table %s: CREATE TABLE block not found", table))
			continue
		}
		actual := extractColumnsFromCreateTable(block)
		have := make(map[string]bool, len(actual))
		for _, c := range actual {
			have[c] = true
		}
		for _, want := range requiredCols {
			if !have[want] {
				failures = append(failures, fmt.Sprintf("table %s: required column %q not declared (have %v)", table, want, actual))
			}
		}
	}

	if len(failures) > 0 {
		t.Errorf("column-parity check failed (%d issue(s)):\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
}
