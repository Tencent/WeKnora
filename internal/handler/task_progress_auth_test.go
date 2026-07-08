package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestRequireTaskProgressTenant_RejectsCrossTenant(t *testing.T) {
	taskID := utils.GenerateTaskID("faq_import", 999, "kb-victim")
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	err := requireTaskProgressTenant(ctx, taskID)
	if err == nil {
		t.Fatal("expected cross-tenant task to be rejected")
	}
}

func TestRequireTaskProgressTenant_AllowsOwnTenant(t *testing.T) {
	taskID := utils.GenerateTaskID("kb_clone", 1, "kb-source")
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	if err := requireTaskProgressTenant(ctx, taskID); err != nil {
		t.Fatalf("expected own-tenant task to pass, got %v", err)
	}
}

func TestRequireTaskProgressTenant_InvalidTaskID(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	err := requireTaskProgressTenant(ctx, "not-a-task")
	if err == nil {
		t.Fatal("expected invalid task ID to fail")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected error message")
	}
	_ = http.StatusBadRequest
}
