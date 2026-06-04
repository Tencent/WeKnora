package im

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestWithIMIdentity(t *testing.T) {
	const tenantID uint64 = 42
	ctx := withIMIdentity(context.Background(), tenantID)

	gotTenant, ok := types.TenantIDFromContext(ctx)
	if !ok || gotTenant != tenantID {
		t.Fatalf("TenantID = %d (ok=%v), want %d", gotTenant, ok, tenantID)
	}

	userID, ok := types.UserIDFromContext(ctx)
	if !ok || userID == "" {
		t.Fatalf("UserID = %q (ok=%v), want non-empty synthetic user", userID, ok)
	}
	if want := "system-42"; userID != want {
		t.Fatalf("UserID = %q, want %q", userID, want)
	}

	// The synthetic shape must be recognised so RBAC code does not record it
	// as a resource creator.
	if !types.IsSyntheticUserID(userID) {
		t.Fatalf("IsSyntheticUserID(%q) = false, want true", userID)
	}

	// Non-empty UserID is the gate the shared-KB resolution relies on; without
	// it Organization-shared KBs are silently skipped on the IM path.
	if role := types.TenantRoleFromContext(ctx); role != types.TenantRoleViewer {
		t.Fatalf("TenantRole = %v, want %v", role, types.TenantRoleViewer)
	}
}
