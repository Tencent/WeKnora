package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	auditScopeKnowledgeBase    = "knowledge_base"
	kbActivitySampleTitleLimit = 5
)

type kbActivityTaskMetadata struct {
	TaskID  string
	Trigger string
}

type kbActivityTaskContextKey struct{}
type kbActivitySuppressedContextKey struct{}

// withKBActivityTask annotates a worker context with stable correlation fields
// that recordKBActivity will add to each event produced by that task.
func withKBActivityTask(ctx context.Context, taskID, trigger string) context.Context {
	if taskID == "" && trigger == "" {
		return ctx
	}
	return context.WithValue(ctx, kbActivityTaskContextKey{}, kbActivityTaskMetadata{
		TaskID: taskID, Trigger: trigger,
	})
}

func kbActivityTrigger(ctx context.Context) string {
	if task, ok := ctx.Value(kbActivityTaskContextKey{}).(kbActivityTaskMetadata); ok && task.Trigger != "" {
		return task.Trigger
	}
	if userID, ok := types.UserIDFromContext(ctx); ok && !types.IsSyntheticUserID(userID) {
		return "user"
	}
	return "system"
}

// kbActivityAppendSampleTitles adds a bounded, human-readable preview of batch
// mutations into activity details. The first sample is also mirrored as title so
// list views can show what was affected without opening the drawer.
func kbActivityAppendSampleTitles(details map[string]any, titles ...string) {
	if details == nil {
		return
	}
	samples := make([]string, 0, kbActivitySampleTitleLimit)
	seen := make(map[string]struct{}, kbActivitySampleTitleLimit)
	appendSample := func(title string) {
		title = strings.TrimSpace(title)
		if title == "" {
			return
		}
		if _, ok := seen[title]; ok {
			return
		}
		if len(samples) >= kbActivitySampleTitleLimit {
			return
		}
		seen[title] = struct{}{}
		samples = append(samples, title)
	}
	if existing, ok := details["title"].(string); ok {
		appendSample(existing)
	}
	for _, title := range titles {
		appendSample(title)
	}
	if len(samples) == 0 {
		return
	}
	details["title"] = samples[0]
	if len(samples) > 1 {
		details["titles"] = samples
	}
}

// withKBActivitySuppressed is used for high-volume child mutations inside a
// composite task. The task's bounded summary event remains visible, while one
// sync run cannot flood the audit table with thousands of per-item rows.
func withKBActivitySuppressed(ctx context.Context) context.Context {
	return context.WithValue(ctx, kbActivitySuppressedContextKey{}, true)
}

// recordKBActivity appends one bounded, non-secret activity summary to the
// existing audit stream. It is deliberately best-effort, matching the audit
// service's failure semantics: a temporary audit outage must not roll back a
// completed business mutation.
func recordKBActivity(
	ctx context.Context,
	audit interfaces.AuditLogService,
	tenantID uint64,
	kbID string,
	action types.AuditAction,
	targetType string,
	targetID string,
	outcome types.AuditOutcome,
	details map[string]any,
) {
	if suppressed, _ := ctx.Value(kbActivitySuppressedContextKey{}).(bool); suppressed {
		return
	}
	if audit == nil || kbID == "" || action == "" {
		return
	}
	if tenantID == 0 {
		tenantID, _ = types.TenantIDFromContext(ctx)
	}
	if tenantID == 0 {
		return
	}
	if outcome == "" {
		outcome = types.AuditOutcomeSuccess
	}
	activityDetails := make(map[string]any, len(details)+2)
	for key, value := range details {
		activityDetails[key] = value
	}
	if task, ok := ctx.Value(kbActivityTaskContextKey{}).(kbActivityTaskMetadata); ok {
		if task.TaskID != "" {
			if _, exists := activityDetails["task_id"]; !exists {
				activityDetails["task_id"] = task.TaskID
			}
		}
		if task.Trigger != "" {
			if _, exists := activityDetails["trigger"]; !exists {
				activityDetails["trigger"] = task.Trigger
			}
		}
		if _, exists := activityDetails["processing_status"]; !exists {
			switch outcome {
			case types.AuditOutcomeAccepted:
				activityDetails["processing_status"] = "pending"
			case types.AuditOutcomeSuccess:
				activityDetails["processing_status"] = "completed"
			case types.AuditOutcomePartial:
				activityDetails["processing_status"] = "partial"
			case types.AuditOutcomeFailed, types.AuditOutcomeDenied:
				activityDetails["processing_status"] = "failed"
			case types.AuditOutcomeCanceled:
				activityDetails["processing_status"] = "canceled"
			}
		}
	}
	var detailJSON types.JSON
	if len(activityDetails) > 0 {
		if b, err := json.Marshal(activityDetails); err == nil {
			detailJSON = types.JSON(b)
		}
	}
	actorID := auditActor(ctx)
	actorRole := ""
	if actorID != "" {
		actorRole = auditActorRole(ctx)
	}
	_ = audit.Log(ctx, &types.AuditLog{
		TenantID:    tenantID,
		ActorUserID: actorID,
		ActorRole:   actorRole,
		Action:      action,
		ScopeType:   auditScopeKnowledgeBase,
		ScopeID:     kbID,
		TargetType:  targetType,
		TargetID:    targetID,
		Outcome:     outcome,
		Details:     detailJSON,
	})
}
