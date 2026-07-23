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
