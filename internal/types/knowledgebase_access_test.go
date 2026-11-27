package types

import (
	"context"
	"testing"
)

func TestCallerKnowledgeBasePermissionAPIKeyPerKBOverlay(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		Capabilities:     StringArray{"retrieve", "ingest", "manage_kbs"},
		KnowledgeBaseIDs: StringArray{"kb-read", "kb-write", "kb-manage"},
		KnowledgeBasePermissions: KnowledgeBasePermissionMap{
			"kb-read":   StringArray{"retrieve"},
			"kb-write":  StringArray{"retrieve", "ingest"},
			"kb-manage": StringArray{"retrieve", "ingest", "manage_kbs"},
		},
	})
	got := CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-read", TenantID: 1}, 1)
	if got != (KnowledgeBaseAccess{Read: true, Write: false, Manage: false}) {
		t.Fatalf("kb-read: %+v", got)
	}
	got = CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-write", TenantID: 1}, 1)
	if got != (KnowledgeBaseAccess{Read: true, Write: true, Manage: false}) {
		t.Fatalf("kb-write: %+v", got)
	}
	got = CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-manage", TenantID: 1}, 1)
	if got != (KnowledgeBaseAccess{Read: true, Write: true, Manage: true}) {
		t.Fatalf("kb-manage: %+v", got)
	}
}

func TestCallerKnowledgeBasePermissionAPIKeyInheritsGlobalCaps(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		Capabilities:     StringArray{"retrieve", "ingest"},
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	got := CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-1", TenantID: 1}, 1)
	if got != (KnowledgeBaseAccess{Read: true, Write: true, Manage: false}) {
		t.Fatalf("inherit global: %+v", got)
	}
}

func TestCallerKnowledgeBasePermissionAPIKeyFullAccess(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{FullAccess: true})
	got := CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-1", TenantID: 1}, 1)
	if got != (KnowledgeBaseAccess{Read: true, Write: true, Manage: true}) {
		t.Fatalf("full access: %+v", got)
	}
}

func TestCallerKnowledgeBasePermissionAPIKeyChatCountsAsRead(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		Capabilities:     StringArray{"chat"},
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	got := CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-1", TenantID: 1}, 1)
	if got != (KnowledgeBaseAccess{Read: true}) {
		t.Fatalf("chat-only: %+v", got)
	}
}

func TestCallerKnowledgeBasePermissionAPIKeyWinsOverOwnerRole(t *testing.T) {
	ctx := context.WithValue(context.Background(), TenantRoleContextKey, TenantRoleOwner)
	ctx = WithTenantAPIKeyScope(ctx, TenantAPIKeyScope{
		Capabilities:     StringArray{"retrieve"},
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	got := CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-1", TenantID: 1}, 1)
	if got != (KnowledgeBaseAccess{Read: true}) {
		t.Fatalf("api key must cap owner role: %+v", got)
	}
}

func TestCallerKnowledgeBasePermissionJWTAdmin(t *testing.T) {
	ctx := context.WithValue(context.Background(), TenantRoleContextKey, TenantRoleAdmin)
	got := CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-1", TenantID: 1}, 1)
	if got != (KnowledgeBaseAccess{Read: true, Write: true, Manage: true}) {
		t.Fatalf("admin: %+v", got)
	}
}

func TestCallerKnowledgeBasePermissionJWTContributorOwnVsOther(t *testing.T) {
	ctx := context.WithValue(context.Background(), TenantRoleContextKey, TenantRoleContributor)
	ctx = context.WithValue(ctx, UserIDContextKey, "user-1")
	own := CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-own", TenantID: 1, CreatorID: "user-1"}, 1)
	if own != (KnowledgeBaseAccess{Read: true, Write: true, Manage: true}) {
		t.Fatalf("own kb: %+v", own)
	}
	other := CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-other", TenantID: 1, CreatorID: "user-2"}, 1)
	if other != (KnowledgeBaseAccess{Read: true}) {
		t.Fatalf("other kb: %+v", other)
	}
}

func TestCallerKnowledgeBasePermissionCrossTenantIsReadOnly(t *testing.T) {
	ctx := context.WithValue(context.Background(), TenantRoleContextKey, TenantRoleOwner)
	got := CallerKnowledgeBasePermission(ctx, &KnowledgeBase{ID: "kb-shared", TenantID: 2}, 1)
	if got != (KnowledgeBaseAccess{Read: true}) {
		t.Fatalf("cross-tenant: %+v", got)
	}
}
