package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type captureKBActivityAudit struct {
	entry *types.AuditLog
}

func (c *captureKBActivityAudit) Log(_ context.Context, entry *types.AuditLog) error {
	c.entry = entry
	return nil
}

func (*captureKBActivityAudit) LogDenied(context.Context, *gin.Context, uint64, string, string, types.TenantRole) error {
	return nil
}

func (*captureKBActivityAudit) List(context.Context, uint64, *interfaces.AuditLogQuery) ([]*types.AuditLog, error) {
	return nil, nil
}

func (*captureKBActivityAudit) Purge(context.Context, int) (int64, error) { return 0, nil }

func TestRecordKBActivityCarriesInitiatorAndTaskMetadata(t *testing.T) {
	ctx := types.TaskInitiator{UserID: "user-1", Role: types.TenantRoleAdmin}.Apply(context.Background())
	ctx = withKBActivityTask(ctx, "task-1", "user")
	audit := &captureKBActivityAudit{}

	recordKBActivity(ctx, audit, 7, "kb-1", types.AuditActionKnowledgeMoveCompleted,
		"knowledge_move", "task-1", types.AuditOutcomeSuccess, map[string]any{"count": 2})

	if audit.entry == nil {
		t.Fatal("expected an activity entry")
	}
	if audit.entry.ActorUserID != "user-1" || audit.entry.ActorRole != "admin" {
		t.Fatalf("actor = %q/%q", audit.entry.ActorUserID, audit.entry.ActorRole)
	}
	var details map[string]any
	if err := json.Unmarshal(audit.entry.Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if details["task_id"] != "task-1" || details["trigger"] != "user" || details["count"] != float64(2) {
		t.Fatalf("details = %#v", details)
	}
}

func TestRecordKBActivityCanSuppressCompositeTaskChildren(t *testing.T) {
	audit := &captureKBActivityAudit{}
	ctx := withKBActivitySuppressed(context.Background())
	recordKBActivity(ctx, audit, 7, "kb-1", types.AuditActionKnowledgeCreated,
		"knowledge", "knowledge-1", types.AuditOutcomeAccepted, nil)
	if audit.entry != nil {
		t.Fatalf("suppressed child activity was recorded: %#v", audit.entry)
	}
}
