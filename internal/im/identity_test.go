package im

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/mcp/headertemplate"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestWithIMIdentity(t *testing.T) {
	const tenantID uint64 = 42
	msg := &IncomingMessage{Platform: PlatformFeishu, UserID: "open-id-1"}
	ctx := withIMIdentity(context.Background(), tenantID, "channel-1", msg)

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

	principal, ok := types.PrincipalFromContext(ctx)
	if !ok {
		t.Fatalf("Principal missing")
	}
	if principal.Type != types.PrincipalIMUser || principal.ID != "42:channel-1:feishu:open-id-1" {
		t.Fatalf("Principal = %#v, want im_user for the external IM user", principal)
	}

	if !types.IsMCPOAuthNonInteractive(ctx) {
		t.Fatal("IM context should mark MCP OAuth as non-interactive")
	}
	template, err := headertemplate.Parse("{{im.user_id}}/{{im.platform}}/{{principal.id}}/{{tenant.id}}")
	if err != nil {
		t.Fatal(err)
	}
	got, err := template.Resolve(types.MCPHeaderContextFromContext(ctx))
	if err != nil || got != "open-id-1/feishu/42:channel-1:feishu:open-id-1/42" {
		t.Fatalf("resolved MCP IM identity = %q, %v", got, err)
	}
}

func TestWithIMIdentityExposesWeComSenderIDToMCPHeaders(t *testing.T) {
	msg := &IncomingMessage{Platform: PlatformWeCom, UserID: "wecom-sender-42"}
	ctx := withIMIdentity(context.Background(), 42, "channel-wecom", msg)
	template, err := headertemplate.Parse("{{im.user_id}}/{{im.platform}}")
	if err != nil {
		t.Fatal(err)
	}
	got, err := template.Resolve(types.MCPHeaderContextFromContext(ctx))
	if err != nil || got != "wecom-sender-42/wecom" {
		t.Fatalf("resolved MCP WeCom identity = %q, %v", got, err)
	}
}
