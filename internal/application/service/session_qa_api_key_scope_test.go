package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestResolveKnowledgeBasesFiltersImplicitAgentDefaultsForRestrictedAPIKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	svc := &sessionService{}

	kbIDs, knowledgeIDs, err := svc.resolveKnowledgeBases(ctx, &types.QARequest{
		Session: &types.Session{TenantID: 10000},
		CustomAgent: &types.CustomAgent{
			TenantID: 10000,
			Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{"kb-allowed", "kb-blocked"},
			},
		},
	})
	if err != nil {
		t.Fatalf("resolveKnowledgeBases returned error: %v", err)
	}
	if len(kbIDs) != 1 || kbIDs[0] != "kb-allowed" {
		t.Fatalf("kbIDs = %#v, want only kb-allowed", kbIDs)
	}
	if len(knowledgeIDs) != 0 {
		t.Fatalf("knowledgeIDs = %#v, want empty", knowledgeIDs)
	}
}

func TestResolveKnowledgeBasesRejectsExplicitOutOfScopeKBForRestrictedAPIKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	svc := &sessionService{}

	_, _, err := svc.resolveKnowledgeBases(ctx, &types.QARequest{
		Session:          &types.Session{TenantID: 10000},
		KnowledgeBaseIDs: []string{"kb-blocked"},
		CustomAgent: &types.CustomAgent{
			TenantID: 10000,
			Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{"kb-allowed", "kb-blocked"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected forbidden for explicit out-of-scope knowledge_base_ids")
	}
}

func TestResolveKnowledgeBasesRejectsFolderScopeOutsideRestrictedAPIKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	svc := &sessionService{}

	_, _, err := svc.resolveKnowledgeBases(ctx, &types.QARequest{
		Session:      &types.Session{TenantID: 10000},
		FolderScopes: []types.FolderScope{{KnowledgeBaseID: "kb-blocked", FolderID: "folder-1"}},
	})
	if err == nil {
		t.Fatal("expected forbidden for explicit out-of-scope folder")
	}
}

func TestResolveKnowledgeBasesAllowsHintedFileAndFolderForRestrictedAPIKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	svc := &sessionService{}

	kbIDs, knowledgeIDs, err := svc.resolveKnowledgeBases(ctx, &types.QARequest{
		Session:                      &types.Session{TenantID: 10000},
		KnowledgeIDs:                 []string{"doc-1"},
		KnowledgeBaseIDByKnowledgeID: map[string]string{"doc-1": "kb-allowed"},
		FolderScopes: []types.FolderScope{{
			KnowledgeBaseID: "kb-allowed",
			FolderID:        "folder-1",
		}},
	})
	if err != nil {
		t.Fatalf("resolveKnowledgeBases returned error: %v", err)
	}
	if len(kbIDs) != 0 {
		t.Fatalf("kbIDs = %#v, want empty", kbIDs)
	}
	if len(knowledgeIDs) != 1 || knowledgeIDs[0] != "doc-1" {
		t.Fatalf("knowledgeIDs = %#v, want doc-1", knowledgeIDs)
	}
}

func TestResolveKnowledgeBasesRejectsFolderScopedFileWithoutKBHintForRestrictedAPIKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	svc := &sessionService{}

	_, _, err := svc.resolveKnowledgeBases(ctx, &types.QARequest{
		Session:      &types.Session{TenantID: 10000},
		KnowledgeIDs: []string{"doc-1"},
		FolderScopes: []types.FolderScope{{
			KnowledgeBaseID: "kb-allowed",
			FolderID:        "folder-1",
		}},
	})
	if err == nil {
		t.Fatal("expected folder-scoped file without a KB hint to be rejected")
	}
}

func TestResolveKnowledgeBasesPreservesFileOnlyRestrictionForRestrictedAPIKey(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	svc := &sessionService{}

	_, _, err := svc.resolveKnowledgeBases(ctx, &types.QARequest{
		Session:                      &types.Session{TenantID: 10000},
		KnowledgeIDs:                 []string{"doc-1"},
		KnowledgeBaseIDByKnowledgeID: map[string]string{"doc-1": "kb-allowed"},
	})
	if err == nil {
		t.Fatal("expected file-only request to keep main's API key restriction")
	}
}

func TestResolveKnowledgeBasesPreservesMainTagOnlyAPIKeyBehavior(t *testing.T) {
	ctx := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	svc := &sessionService{}
	req := &types.QARequest{
		Session: &types.Session{TenantID: 10000},
		TagScopes: []types.TagScope{{
			KnowledgeBaseID: "kb-blocked",
			TagIDs:          []string{"tag-1"},
		}},
	}

	_, _, err := svc.resolveKnowledgeBases(ctx, req)
	if err != nil {
		t.Fatalf("tag-only request changed main API key behavior: %v", err)
	}
}

func TestResolveKnowledgeBasesRejectsSharedAgentFolderOutsideScope(t *testing.T) {
	svc := &sessionService{}
	req := &types.QARequest{
		Session: &types.Session{TenantID: 20000},
		CustomAgent: &types.CustomAgent{
			TenantID: 10000,
			Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{"kb-allowed"},
			},
		},
		FolderScopes: []types.FolderScope{
			{KnowledgeBaseID: "kb-allowed", FolderID: "folder-1"},
			{KnowledgeBaseID: "kb-blocked", FolderID: "folder-2"},
		},
	}

	_, _, err := svc.resolveKnowledgeBases(context.Background(), req)
	if err == nil {
		t.Fatal("expected shared agent folder scope outside the allow-list to be rejected")
	}
}

func TestResolveKnowledgeBasesPreservesSharedAgentFolderInsideScope(t *testing.T) {
	svc := &sessionService{}
	req := &types.QARequest{
		Session: &types.Session{TenantID: 20000},
		CustomAgent: &types.CustomAgent{
			TenantID: 10000,
			Config: types.CustomAgentConfig{
				KBSelectionMode: "selected",
				KnowledgeBases:  []string{"kb-allowed"},
			},
		},
		FolderScopes: []types.FolderScope{
			{KnowledgeBaseID: "kb-allowed", FolderID: "folder-1"},
		},
	}

	_, _, err := svc.resolveKnowledgeBases(context.Background(), req)
	if err != nil {
		t.Fatalf("resolveKnowledgeBases returned error: %v", err)
	}
	if len(req.FolderScopes) != 1 || req.FolderScopes[0].KnowledgeBaseID != "kb-allowed" {
		t.Fatalf("folder scopes = %#v, want kb-allowed", req.FolderScopes)
	}
}
