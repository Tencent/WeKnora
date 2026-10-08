package types

import (
	"context"
	"testing"
)

func TestTaskInitiatorRoundTrip(t *testing.T) {
	requestCtx := context.WithValue(context.Background(), UserIDContextKey, "user-1")
	requestCtx = context.WithValue(requestCtx, TenantRoleContextKey, TenantRoleAdmin)

	initiator := TaskInitiatorFromContext(requestCtx)
	if initiator.UserID != "user-1" || initiator.Role != TenantRoleAdmin {
		t.Fatalf("TaskInitiatorFromContext() = %#v", initiator)
	}

	workerCtx := initiator.Apply(context.Background())
	if userID, ok := UserIDFromContext(workerCtx); !ok || userID != "user-1" {
		t.Fatalf("restored user = %q, %v", userID, ok)
	}
	if role := TenantRoleFromContext(workerCtx); role != TenantRoleAdmin {
		t.Fatalf("restored role = %q", role)
	}
}

func TestTaskInitiatorOmitsSyntheticAndLegacyActors(t *testing.T) {
	synthetic := context.WithValue(context.Background(), UserIDContextKey, "system-42")
	if got := TaskInitiatorFromContext(synthetic); got.UserID != "" {
		t.Fatalf("synthetic initiator = %#v", got)
	}

	legacyCtx := (TaskInitiator{}).Apply(context.Background())
	if userID, ok := UserIDFromContext(legacyCtx); ok || userID != "" {
		t.Fatalf("legacy user = %q, %v", userID, ok)
	}
}

func TestTaskInitiatorSnapshotsAPIKeyWithoutRestoringScope(t *testing.T) {
	requestCtx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KeyID: 9, Name: "alice-mcp",
	})
	requestCtx = context.WithValue(requestCtx, UserIDContextKey, "system-7")

	initiator := TaskInitiatorFromContext(requestCtx)
	if initiator.UserID != "" {
		t.Fatalf("synthetic user should stay empty, got %#v", initiator)
	}
	if initiator.APIKeyID != 9 || initiator.APIKeyName != "alice-mcp" {
		t.Fatalf("TaskInitiatorFromContext() = %#v", initiator)
	}

	workerCtx := initiator.Apply(context.Background())
	if _, ok := TenantAPIKeyScopeFromContext(workerCtx); ok {
		t.Fatal("Apply must not restore TenantAPIKeyScope")
	}
	got, ok := AuditAPIKeyFromContext(workerCtx)
	if !ok || got.ID != 9 || got.Name != "alice-mcp" {
		t.Fatalf("AuditAPIKeyFromContext() = %#v, %v", got, ok)
	}
	if userID, ok := UserIDFromContext(workerCtx); ok || userID != "" {
		t.Fatalf("worker user = %q, %v", userID, ok)
	}
}

func TestTaskInitiatorKeepsHumanActorAndAPIKey(t *testing.T) {
	requestCtx := WithTenantAPIKeyScope(context.Background(), TenantAPIKeyScope{
		KeyID: 3, Name: "bob-mcp",
	})
	requestCtx = context.WithValue(requestCtx, UserIDContextKey, "user-1")
	requestCtx = context.WithValue(requestCtx, TenantRoleContextKey, TenantRoleAdmin)

	initiator := TaskInitiatorFromContext(requestCtx)
	if initiator.UserID != "user-1" || initiator.APIKeyID != 3 || initiator.APIKeyName != "bob-mcp" {
		t.Fatalf("TaskInitiatorFromContext() = %#v", initiator)
	}

	workerCtx := initiator.Apply(context.Background())
	if userID, ok := UserIDFromContext(workerCtx); !ok || userID != "user-1" {
		t.Fatalf("restored user = %q, %v", userID, ok)
	}
	if _, ok := TenantAPIKeyScopeFromContext(workerCtx); ok {
		t.Fatal("Apply must not restore TenantAPIKeyScope")
	}
	got, ok := AuditAPIKeyFromContext(workerCtx)
	if !ok || got.Name != "bob-mcp" {
		t.Fatalf("AuditAPIKeyFromContext() = %#v, %v", got, ok)
	}
}
