package types

import (
	"context"
	"testing"
)

func TestAuthorizeTenantAPIKeyKnowledgeTargetsRejectsKnowledgeIDs(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	err := AuthorizeTenantAPIKeyKnowledgeTargets(ctx, []string{"kb-1"}, []string{"doc-1"})
	if err == nil {
		t.Fatal("expected forbidden when knowledge_ids supplied under KB-restricted key")
	}
}

func TestAuthorizeTenantAPIKeyKnowledgeTargetsAllowsUnspecifiedTargets(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	err := AuthorizeTenantAPIKeyKnowledgeTargets(ctx, nil, nil)
	if err != nil {
		t.Fatalf("expected unspecified targets to be allowed, got %v", err)
	}
}

func TestAuthorizeTenantAPIKeyOptionalTagIDsRejectsTags(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	err := AuthorizeTenantAPIKeyOptionalTagIDs(ctx, []string{"tag-1"})
	if err == nil {
		t.Fatal("expected forbidden when tag_ids supplied under KB-restricted key")
	}
}

func TestFilterKnowledgeBasesForTenantAPIKeyScopeIntersectsAgentDefaults(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1", "kb-2"},
	})
	got, err := FilterKnowledgeBasesForTenantAPIKeyScope(ctx, nil, []string{"kb-2", "kb-3"})
	if err != nil {
		t.Fatalf("FilterKnowledgeBasesForTenantAPIKeyScope returned error: %v", err)
	}
	if len(got) != 1 || got[0] != "kb-2" {
		t.Fatalf("filtered = %#v, want only kb-2", got)
	}
}

func TestFilterKnowledgeBasesForTenantAPIKeyScopeRejectsExplicitOutOfScope(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
	})
	_, err := FilterKnowledgeBasesForTenantAPIKeyScope(ctx, []string{"kb-1", "kb-2"}, []string{"kb-1", "kb-2"})
	if err == nil {
		t.Fatal("expected forbidden for explicit out-of-scope kb_ids")
	}
}

func TestScopeHasCapability(t *testing.T) {
	s := TenantAPIKeyScope{Capabilities: StringArray{"chat"}}
	if !s.HasCapability(APIKeyCapabilityChat) {
		t.Fatal("expected chat capability to be present")
	}
	if (TenantAPIKeyScope{}).HasCapability(APIKeyCapabilityChat) {
		t.Fatal("empty scope must not report chat capability")
	}
	// Unknown capability is never satisfied.
	if s.HasCapability(APIKeyCapability("bogus")) {
		t.Fatal("unknown capability must not be satisfied")
	}
}

func TestAllowsKnowledgeBaseCapabilityInheritsGlobalWhenMapEmpty(t *testing.T) {
	s := TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1", "kb-2"},
		Capabilities:     StringArray{"retrieve", "ingest", "manage_kbs"},
	}
	if !s.AllowsKnowledgeBaseCapability("kb-1", APIKeyCapabilityIngest) {
		t.Fatal("empty permissions map should inherit ingest for every allow-listed KB")
	}
	if !s.AllowsKnowledgeBaseCapability("kb-2", APIKeyCapabilityManageKnowledgeBases) {
		t.Fatal("empty permissions map should inherit manage_kbs for every allow-listed KB")
	}
}

func TestAllowsKnowledgeBaseCapabilitySubtractsIngestOnOneKB(t *testing.T) {
	s := TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1", "kb-2"},
		Capabilities:     StringArray{"retrieve", "ingest"},
		KnowledgeBasePermissions: KnowledgeBasePermissionMap{
			"kb-1": StringArray{"retrieve"},
		},
	}
	if s.AllowsKnowledgeBaseCapability("kb-1", APIKeyCapabilityIngest) {
		t.Fatal("kb-1 should not allow ingest after the overlay removed it")
	}
	if !s.AllowsKnowledgeBaseCapability("kb-1", APIKeyCapabilityRetrieve) {
		t.Fatal("kb-1 should still allow retrieve")
	}
	if !s.AllowsKnowledgeBaseCapability("kb-2", APIKeyCapabilityIngest) {
		t.Fatal("kb-2 with no overlay entry should inherit ingest")
	}
}

func TestAllowsKnowledgeBaseCapabilityGlobalCeiling(t *testing.T) {
	s := TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
		Capabilities:     StringArray{"retrieve"},
		KnowledgeBasePermissions: KnowledgeBasePermissionMap{
			"kb-1": StringArray{"retrieve", "ingest"},
		},
	}
	if s.AllowsKnowledgeBaseCapability("kb-1", APIKeyCapabilityIngest) {
		t.Fatal("ingest overlay must not exceed the global capability ceiling")
	}
}

func TestAllowsKnowledgeBaseCapabilityChatCountsAsReadCeiling(t *testing.T) {
	s := TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
		Capabilities:     StringArray{"chat", "ingest"},
	}
	if !s.AllowsKnowledgeBaseCapability("kb-1", APIKeyCapabilityRetrieve) {
		t.Fatal("chat should satisfy the per-KB read ceiling")
	}
}

func TestAllowsKnowledgeBaseCapabilityManageWithoutIngest(t *testing.T) {
	s := TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
		Capabilities:     StringArray{"retrieve", "ingest", "manage_kbs"},
		KnowledgeBasePermissions: KnowledgeBasePermissionMap{
			"kb-1": StringArray{"retrieve", "manage_kbs"},
		},
	}
	if s.AllowsKnowledgeBaseCapability("kb-1", APIKeyCapabilityIngest) {
		t.Fatal("manage-only overlay must not grant ingest")
	}
	if !s.AllowsKnowledgeBaseCapability("kb-1", APIKeyCapabilityManageKnowledgeBases) {
		t.Fatal("manage overlay should grant manage_kbs")
	}
}

func TestAllowsKnowledgeBaseCapabilityFullAccessAndUnrestricted(t *testing.T) {
	full := TenantAPIKeyScope{FullAccess: true}
	if !full.AllowsKnowledgeBaseCapability("kb-any", APIKeyCapabilityIngest) {
		t.Fatal("full-access keys skip the per-KB overlay")
	}
	unrestricted := TenantAPIKeyScope{Capabilities: StringArray{"ingest"}}
	if !unrestricted.AllowsKnowledgeBaseCapability("kb-any", APIKeyCapabilityIngest) {
		t.Fatal("unrestricted keys follow global capabilities only")
	}
}

func TestAuthorizeTenantAPIKeyKnowledgeBaseCapabilityRejectsWrite(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1"},
		Capabilities:     StringArray{"retrieve", "ingest"},
		KnowledgeBasePermissions: KnowledgeBasePermissionMap{
			"kb-1": StringArray{"retrieve"},
		},
	})
	if err := AuthorizeTenantAPIKeyKnowledgeBaseCapability(ctx, "kb-1", APIKeyCapabilityIngest); err == nil {
		t.Fatal("expected forbidden ingest on a read-only KB overlay")
	}
	if err := AuthorizeTenantAPIKeyKnowledgeBaseCapability(ctx, "kb-1", APIKeyCapabilityRetrieve); err != nil {
		t.Fatalf("retrieve should pass, got %v", err)
	}
}

func TestFilterKnowledgeBasesForTenantAPIKeyScopeDropsUnreadKBs(t *testing.T) {
	ctx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KnowledgeBaseIDs: StringArray{"kb-1", "kb-2"},
		Capabilities:     StringArray{"retrieve", "ingest"},
		KnowledgeBasePermissions: KnowledgeBasePermissionMap{
			"kb-2": StringArray{"ingest"},
		},
	})
	got, err := FilterKnowledgeBasesForTenantAPIKeyScope(ctx, nil, []string{"kb-1", "kb-2"})
	if err != nil {
		t.Fatalf("FilterKnowledgeBasesForTenantAPIKeyScope returned error: %v", err)
	}
	if len(got) != 1 || got[0] != "kb-1" {
		t.Fatalf("filtered = %#v, want only kb-1 (kb-2 has no retrieve)", got)
	}
}

func TestKnowledgeBasePermissionsExceedCeiling(t *testing.T) {
	if KnowledgeBasePermissionsExceedCeiling(
		KnowledgeBasePermissionMap{"kb-1": StringArray{"ingest"}},
		StringArray{"retrieve"},
	) != true {
		t.Fatal("ingest grant without global ingest should exceed the ceiling")
	}
	if KnowledgeBasePermissionsExceedCeiling(
		KnowledgeBasePermissionMap{"kb-1": StringArray{"retrieve"}},
		StringArray{"retrieve", "chat"},
	) {
		t.Fatal("retrieve under a retrieve/chat key should not exceed the ceiling")
	}
}

func TestNormalizeKnowledgeBasePermissionsDropsEmptyGrantRows(t *testing.T) {
	ids, perms := NormalizeKnowledgeBasePermissions(
		StringArray{"kb-1", "kb-2"},
		KnowledgeBasePermissionMap{"kb-1": StringArray{}},
		StringArray{"retrieve", "ingest"},
		false,
	)
	if len(ids) != 1 || ids[0] != "kb-2" {
		t.Fatalf("ids = %#v, want [kb-2]", ids)
	}
	if len(perms) != 0 {
		t.Fatalf("perms = %#v, want empty inherit map", perms)
	}
}

func TestNormalizeAPIKeyCapabilities(t *testing.T) {
	got := NormalizeAPIKeyCapabilities(StringArray{
		" Retrieve ",
		"chat",
		"read_agents",
		"manage_kbs",
		"message_history",
		"manage_mcp_services",
		"manage_members",
		"manage_spaces",
		"bogus",
		"",
	})
	want := []string{"retrieve", "chat", "read_agents", "manage_kbs", "message_history", "manage_mcp_services", "manage_members", "manage_spaces"}
	if len(got) != len(want) {
		t.Fatalf("normalized = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalized = %#v, want %#v", got, want)
		}
	}
}
