package tools

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDatabaseQueryInjectsEnabledChunkFilter(t *testing.T) {
	tool := NewDatabaseQueryTool(nil, types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-1",
	}})

	securedSQL, err := tool.validateAndSecureSQL(
		"SELECT c.id, c.content FROM chunks c WHERE c.chunk_type = 'faq'",
		1,
	)
	if err != nil {
		t.Fatalf("validateAndSecureSQL() error = %v", err)
	}
	if !strings.Contains(securedSQL, "c.is_enabled = true") {
		t.Fatalf("Agent SQL must exclude disabled chunks:\n%s", securedSQL)
	}
}

// Execute the secured SQL against synthetic rows so the test checks access,
// rather than just the spelling of an injected predicate.
func TestDatabaseQuerySharedTenantScope(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
 CREATE TABLE chunks (id TEXT, tenant_id INTEGER, knowledge_base_id TEXT,
 knowledge_id TEXT, is_enabled BOOLEAN, deleted_at TEXT);
 INSERT INTO chunks VALUES
 ('shared', 2, 'shared-kb', 'shared-doc', true, NULL),
 ('own', 1, 'own-kb', 'own-doc', true, NULL),
 ('unshared', 2, 'private-kb', 'private-doc', true, NULL),
 ('wrong-owner', 3, 'shared-kb', 'shared-doc', true, NULL),
 ('swapped-owner', 2, 'own-kb', 'own-doc', true, NULL),
 ('disabled', 2, 'shared-kb', 'shared-doc', false, NULL),
 ('deleted', 2, 'shared-kb', 'shared-doc', true, '2026-01-01');`)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewDatabaseQueryTool(nil, types.SearchTargets{
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "shared-kb", TenantID: 2},
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "own-kb", TenantID: 1},
	})
	secured, err := tool.validateAndSecureSQL("SELECT c.id FROM chunks c ORDER BY c.id", 1)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(secured)
	if err != nil {
		t.Fatalf("query %s: %v", secured, err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "own,shared" {
		t.Fatalf("authorized own and shared rows = %v, want [own shared]; SQL: %s", got, secured)
	}
}

func TestDatabaseQueryRejectsEmptyScope(t *testing.T) {
	for name, targets := range map[string]types.SearchTargets{
		"nil":                  nil,
		"empty":                {},
		"nil target":           {nil},
		"empty KB":             {{Type: types.SearchTargetTypeKnowledgeBase, TenantID: 2}},
		"empty document scope": {{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "shared-kb", TenantID: 2}},
	} {
		t.Run(name, func(t *testing.T) {
			tool := NewDatabaseQueryTool(nil, targets)
			secured, err := tool.validateAndSecureSQL("SELECT id FROM chunks", 1)
			if err == nil || secured != "" {
				t.Fatalf("empty scope produced executable SQL %q, error %v", secured, err)
			}
		})
	}
}
