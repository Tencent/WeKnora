package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestScopeClause_KnowledgeAndFullKBOredTogether(t *testing.T) {
	sql, args := scopeClause(
		[]string{"kb-1"},
		[]string{"doc-9"},
		nil,
		map[string]uint64{"kb-1": 7},
	)

	if !strings.Contains(sql, "chunks.knowledge_id IN ?") {
		t.Fatalf("expected knowledge-id predicate, got %q", sql)
	}
	if !strings.Contains(sql, "chunks.knowledge_base_id = ? AND chunks.tenant_id = ?") {
		t.Fatalf("expected kb-tenant predicate, got %q", sql)
	}
	if !strings.Contains(sql, " OR ") {
		t.Fatalf("expected the two scopes to be OR'd, got %q", sql)
	}
	// args: knowledgeIDs slice + kbID + tenantID
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}
}

func TestScopeClause_TagScopeUsesExistsInsteadOfExpandedKnowledgeIDs(t *testing.T) {
	sql, args := scopeClause(
		nil,
		nil,
		[]*types.SearchTarget{{
			KnowledgeBaseID: "kb-1",
			TenantID:        7,
			TagIDs:          []string{"tag-a", "tag-b"},
		}},
		map[string]uint64{"kb-1": 7},
	)

	if strings.Contains(sql, "chunks.knowledge_id IN ?") {
		t.Fatalf("tag scope must not expand to knowledge-id IN predicate, got %q", sql)
	}
	if !strings.Contains(sql, "EXISTS") || !strings.Contains(sql, "knowledge_tag_relations") {
		t.Fatalf("expected tag relation EXISTS predicate, got %q", sql)
	}
	if !strings.Contains(sql, "ktr.tag_id IN ?") {
		t.Fatalf("expected tag-id predicate, got %q", sql)
	}
	if len(args) != 3 {
		t.Fatalf("expected kbID, tenantID, tagIDs args, got %d: %v", len(args), args)
	}
}

func TestScopeClause_SkipsKBWithoutTenant(t *testing.T) {
	sql, args := scopeClause(
		[]string{"kb-1", "kb-2"},
		nil,
		nil,
		map[string]uint64{"kb-1": 7}, // kb-2 has no tenant mapping
	)
	if strings.Contains(sql, "kb-2") {
		t.Fatalf("kb-2 has no tenant and must be skipped, got %q", sql)
	}
	if len(args) != 2 {
		t.Fatalf("expected only kb-1 (2 args), got %d: %v", len(args), args)
	}
}

func TestScopeClause_EmptyWhenNoUsableScope(t *testing.T) {
	sql, args := scopeClause([]string{"kb-1"}, nil, nil, map[string]uint64{})
	if sql != "" || args != nil {
		t.Fatalf("expected empty scope, got sql=%q args=%v", sql, args)
	}
}
