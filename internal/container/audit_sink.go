package container

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/application/repository/retriever/opensearch"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// auditSinkAdapter bridges the OpenSearch driver's AuditSink (which the driver
// owns so it imports no service package) to the service-layer AuditLogService.
// This keeps the dependency one-way: the driver depends only on its own
// AuditSink abstraction; the container implements it.
type auditSinkAdapter struct {
	svc interfaces.AuditLogService
}

// newAuditSinkAdapter returns an opensearch.AuditSink backed by svc. A nil svc
// yields a sink whose emits are no-ops.
func newAuditSinkAdapter(svc interfaces.AuditLogService) opensearch.AuditSink {
	return auditSinkAdapter{svc: svc}
}

func (a auditSinkAdapter) EmitIndexCreated(ctx context.Context, alias string, dim int) {
	a.emit(ctx, types.AuditActionOpenSearchIndexCreated, alias,
		map[string]any{"alias": alias, "dim": dim})
}

func (a auditSinkAdapter) EmitReindexExecuted(ctx context.Context, srcAlias, dstAlias string, docs int64) {
	a.emit(ctx, types.AuditActionOpenSearchReindexExecuted, dstAlias,
		map[string]any{"src_alias": srcAlias, "dst_alias": dstAlias, "docs": docs})
}

// emit writes one audit entry. It skips (with a warning) when the context
// carries no tenant — driver events can fire from background task contexts
// (e.g. lazy index creation under an async copy task), and writing tenant_id=0
// would collide with the system-scope sentinel and corrupt the audit trail.
func (a auditSinkAdapter) emit(ctx context.Context, action types.AuditAction, target string, detail map[string]any) {
	if a.svc == nil {
		return
	}
	tid, ok := types.TenantIDFromContext(ctx)
	if !ok {
		logger.GetLogger(ctx).Warnf("[audit] %s: no tenant in context, skipping audit (target=%s)", action, target)
		return
	}
	// Details is a typed JSON blob — only bounded, non-secret fields. Never
	// include cluster reason strings or connection secrets.
	b, err := json.Marshal(detail)
	if err != nil {
		logger.GetLogger(ctx).Warnf("[audit] %s: marshal details failed: %v", action, err)
		b = []byte("{}")
	}
	if err := a.svc.Log(ctx, &types.AuditLog{
		TenantID:   tid,
		Action:     action,
		TargetType: "opensearch_index",
		TargetID:   target,
		Details:    types.JSON(b),
	}); err != nil {
		logger.GetLogger(ctx).Warnf("[audit] %s emit failed: %v", action, err)
	}
}
