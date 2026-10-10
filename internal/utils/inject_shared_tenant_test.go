package utils

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func sharedTenantFixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
CREATE TABLE knowledge_bases (id TEXT, tenant_id INTEGER, name TEXT);
CREATE TABLE knowledges (id TEXT, tenant_id INTEGER, knowledge_base_id TEXT, title TEXT);
CREATE TABLE chunks (id TEXT, tenant_id INTEGER, knowledge_base_id TEXT, knowledge_id TEXT);
CREATE TABLE knowledge_tag_relations (knowledge_id TEXT, tag_id TEXT);
CREATE TABLE sessions (id TEXT, tenant_id INTEGER);
CREATE TABLE tenants (id INTEGER, name TEXT);
INSERT INTO knowledge_bases VALUES
 ('own-kb', 1, 'own'), ('shared-kb', 2, 'shared'),
 ('private-kb', 2, 'private'), ('shared-kb', 1, 'wrong-owner'),
 ('own-kb', 2, 'swapped-owner');
INSERT INTO knowledges VALUES
 ('own-doc', 1, 'own-kb', 'own'), ('shared-doc', 2, 'shared-kb', 'shared'),
 ('private-doc', 2, 'private-kb', 'private'),
 ('shared-doc', 1, 'shared-kb', 'wrong-owner'),
 ('own-doc', 2, 'own-kb', 'swapped-owner'),
 ('other-doc', 2, 'shared-kb', 'outside-document'),
 ('untagged-doc', 2, 'shared-kb', 'outside-tag');
INSERT INTO chunks VALUES
 ('own', 1, 'own-kb', 'own-doc'), ('shared', 2, 'shared-kb', 'shared-doc'),
 ('private', 2, 'private-kb', 'private-doc'),
 ('wrong-owner', 1, 'shared-kb', 'shared-doc'),
 ('swapped-owner', 2, 'own-kb', 'own-doc'),
 ('outside-document', 2, 'shared-kb', 'other-doc'),
 ('outside-tag', 2, 'shared-kb', 'untagged-doc');
INSERT INTO knowledge_tag_relations VALUES
 ('own-doc', 'own-tag'), ('shared-doc', 'shared-tag'), ('other-doc', 'shared-tag');
INSERT INTO sessions VALUES ('own-session', 1), ('other-session', 2);
INSERT INTO tenants VALUES (1, 'own-tenant'), (2, 'other-tenant');`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func assertSharedTenantQuery(t *testing.T, db *sql.DB, query string, want []string, opts ...SQLValidationOption) {
	t.Helper()
	secured, validation, err := ValidateAndSecureSQL(query, opts...)
	if err != nil || !validation.Valid {
		t.Fatalf("secure query %q: validation=%+v, err=%v", query, validation, err)
	}
	rows, err := db.Query(secured)
	if err != nil {
		t.Fatalf("execute secured SQL %s: %v", secured, err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		got = append(got, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("results = %v, want %v; secured SQL: %s", got, want, secured)
	}
}

func TestStructuredScopeTenantPairing(t *testing.T) {
	db := sharedTenantFixture(t)
	scopes := []SearchScope{
		{TenantID: 1, KnowledgeBaseID: "own-kb", KnowledgeIDs: []string{"own-doc"}, TagIDs: []string{"own-tag"}},
		{
			TenantID: 2, KnowledgeBaseID: "shared-kb",
			KnowledgeIDs: []string{"shared-doc", "untagged-doc"}, TagIDs: []string{"shared-tag"},
		},
	}
	for _, query := range []string{
		"SELECT name FROM knowledge_bases ORDER BY name",
		"SELECT k.title FROM knowledges k ORDER BY k.title",
		"SELECT c.id FROM chunks c ORDER BY c.id",
		`SELECT "Shared Rows".id FROM chunks "Shared Rows" ORDER BY "Shared Rows".id`,
		"SELECT c.id FROM chunks c JOIN knowledges k ON k.id = c.knowledge_id ORDER BY c.id",
		"SELECT c.id FROM chunks c JOIN chunks d ON c.knowledge_id = d.knowledge_id ORDER BY c.id",
		"SELECT c.id FROM chunks c JOIN knowledge_bases kb ON kb.id = c.knowledge_base_id ORDER BY c.id",
		"SELECT c.id FROM chunks c WHERE c.id IN (SELECT c.id FROM chunks c) ORDER BY c.id",
	} {
		t.Run(query, func(t *testing.T) {
			assertSharedTenantQuery(t, db, query, []string{"own", "shared"},
				WithTenantIsolation(1), WithSearchScopes(scopes))
		})
	}
}

func TestStructuredScopeTenantTagOnly(t *testing.T) {
	db := sharedTenantFixture(t)
	assertSharedTenantQuery(t, db, "SELECT c.id FROM chunks c ORDER BY c.id", []string{"outside-document", "shared"},
		WithTenantIsolation(1), WithSearchScopes([]SearchScope{
			{TenantID: 2, KnowledgeBaseID: "shared-kb", TagIDs: []string{"shared-tag"}},
		}))
}

func TestStructuredScopeTenantZeroFallback(t *testing.T) {
	db := sharedTenantFixture(t)
	for _, query := range []string{
		"SELECT name FROM knowledge_bases ORDER BY name",
		"SELECT title FROM knowledges ORDER BY title",
		"SELECT id FROM chunks ORDER BY id",
	} {
		t.Run(query, func(t *testing.T) {
			assertSharedTenantQuery(t, db, query, []string{"own"},
				WithTenantIsolation(1), WithSearchScopes([]SearchScope{
					{KnowledgeBaseID: "own-kb"},
				}))
		})
	}
}

func TestStructuredScopeInvalidTenantScopesFailClosed(t *testing.T) {
	db := sharedTenantFixture(t)
	for _, table := range []string{"knowledge_bases", "knowledges", "chunks"} {
		t.Run(table, func(t *testing.T) {
			assertSharedTenantQuery(t, db, "SELECT id FROM "+table, nil,
				WithTenantIsolation(1), WithSearchScopes([]SearchScope{{TenantID: 2}, {}}))
		})
	}
	// Invalid alternatives must not turn an OR restriction into unrestricted access.
	assertSharedTenantQuery(t, db, "SELECT id FROM chunks ORDER BY id", []string{"own"},
		WithTenantIsolation(1), WithSearchScopes([]SearchScope{
			{TenantID: 2}, {TenantID: 1, KnowledgeBaseID: "own-kb"},
		}))
}

func TestStructuredScopePreservesOtherTenantTables(t *testing.T) {
	db := sharedTenantFixture(t)
	opts := []SQLValidationOption{
		WithTenantIsolation(1, "chunks", "sessions", "tenants"),
		WithSearchScopes([]SearchScope{
			{TenantID: 2, KnowledgeBaseID: "shared-kb", KnowledgeIDs: []string{"shared-doc"}},
		}),
	}
	assertSharedTenantQuery(t, db, "SELECT id FROM sessions ORDER BY id", []string{"own-session"}, opts...)
	assertSharedTenantQuery(t, db, "SELECT name FROM tenants ORDER BY name", []string{"own-tenant"}, opts...)
	assertSharedTenantQuery(t, db, "SELECT c.id || ':' || s.id FROM chunks c CROSS JOIN sessions s",
		[]string{"shared:own-session"}, opts...)
}

func TestStructuredScopePreservesLegacyTenantFiltering(t *testing.T) {
	db := sharedTenantFixture(t)
	assertSharedTenantQuery(t, db, "SELECT id FROM chunks ORDER BY id", []string{"own", "wrong-owner"},
		WithTenantIsolation(1), WithSearchScopeFilter([]string{"own-kb", "shared-kb"}, nil))
	assertSharedTenantQuery(t, db, "SELECT id FROM chunks ORDER BY id", []string{"own", "wrong-owner"},
		WithTenantIsolation(1), WithSearchScopes(nil))
	// A zero-tenant scope without tenant injection retains its original optional semantics.
	assertSharedTenantQuery(t, db, "SELECT id FROM chunks ORDER BY id", []string{"own", "swapped-owner"},
		WithSearchScopes([]SearchScope{{KnowledgeBaseID: "own-kb"}}))
	secured, _, err := ValidateAndSecureSQL("SELECT id FROM chunks",
		WithSearchScopes([]SearchScope{{KnowledgeBaseID: "own-kb"}}))
	if err != nil || strings.Contains(secured, "tenant_id") {
		t.Fatalf("optional scope unexpectedly injected tenant filter: %s; error=%v", secured, err)
	}
}

// The owner/KB predicate must remain bound to the outer table when a
// user-chosen alias also occurs inside an injected subquery.
func TestStructuredScopeTagAliasRetainsTenantBinding(t *testing.T) {
	db := sharedTenantFixture(t)
	assertSharedTenantQuery(t, db,
		"SELECT ktr.id FROM chunks ktr WHERE ktr.knowledge_id = 'shared-doc' ORDER BY ktr.id", []string{"shared"},
		WithTenantIsolation(1), WithSearchScopes([]SearchScope{
			{TenantID: 2, KnowledgeBaseID: "shared-kb", TagIDs: []string{"shared-tag"}},
		}))
}

func TestStructuredScopeCallerZeroRemainsRestricted(t *testing.T) {
	db := sharedTenantFixture(t)
	assertSharedTenantQuery(t, db, "SELECT id FROM chunks ORDER BY id", nil,
		WithTenantIsolation(0), WithSearchScopes([]SearchScope{{KnowledgeBaseID: "own-kb"}}))
	// Explicitly authorized ownership also constrains a scope used on its own.
	assertSharedTenantQuery(t, db, "SELECT id FROM chunks ORDER BY id", []string{"own"},
		WithSearchScopes([]SearchScope{{TenantID: 1, KnowledgeBaseID: "own-kb"}}))
}

func TestStructuredScopeStillRejectsCompoundQuery(t *testing.T) {
	_, validation, err := ValidateAndSecureSQL(
		"SELECT id FROM chunks UNION SELECT id FROM chunks ORDER BY id",
		WithTenantIsolation(1), WithSearchScopes([]SearchScope{{TenantID: 2, KnowledgeBaseID: "shared-kb"}}),
	)
	if err == nil || validation.Valid {
		t.Fatal("structured owner scopes must not permit previously rejected compound queries")
	}
}
