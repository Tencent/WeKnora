package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ConfirmedTool asks the user before running a tool that changes data, the
// way run_operation does for catalog operations, so the assistant's access
// level covers its direct tools too. A call resumed with the user's answer
// is honored whatever the level is now: the user may have edited it.
type ConfirmedTool struct {
	inner  types.Tool
	risk   operations.Risk
	access operations.Access
	gate   approval.BuiltinApproval
}

// NewConfirmedTool wraps inner, whose calls carry risk.
func NewConfirmedTool(
	inner types.Tool, risk operations.Risk, access operations.Access, gate approval.BuiltinApproval,
) *ConfirmedTool {
	return &ConfirmedTool{inner: inner, risk: risk, access: access, gate: gate}
}

// Name returns the wrapped tool's name.
func (t *ConfirmedTool) Name() string { return t.inner.Name() }

// Description returns the wrapped tool's description.
func (t *ConfirmedTool) Description() string { return t.inner.Description() }

// Parameters returns the wrapped tool's argument schema.
func (t *ConfirmedTool) Parameters() json.RawMessage { return t.inner.Parameters() }

// Execute runs the inner tool once the access level allows it.
func (t *ConfirmedTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	meta, _ := ToolExecFromContext(ctx)
	resumed := meta != nil && meta.Answered != nil && meta.Answered.Kind == types.AgentPromptKindToolApproval
	if !resumed && !t.access.NeedsApprovalFor(t.risk) {
		return t.inner.Execute(ctx, args)
	}
	if t.gate == nil || meta == nil || meta.EventBus == nil || (!meta.Interactive && !resumed) {
		return operationFailure(fmt.Sprintf(
			"%s changes data and needs the user's confirmation, which cannot be shown here. "+
				"Tell the user what you would do and let them do it in WeKnora.", t.Name())), nil
	}

	waitCtx := ctx
	if meta.ApprovalCtx != nil {
		waitCtx = meta.ApprovalCtx
	}
	var decision approval.Decision
	if resumed {
		decision = t.gate.ResumeDecision(waitCtx, meta.Answered, meta.EventBus)
	} else {
		var snapshot []byte
		var snapshotVersion int
		if meta.Snapshot != nil {
			var err error
			if snapshot, snapshotVersion, err = meta.Snapshot(); err != nil {
				logger.Warnf(ctx, "[Tool][Confirmed] capture run state for approval: %v", err)
				snapshot = nil
			}
		}
		grant, grantScope := t.grant(ctx, args)
		var err error
		decision, err = t.gate.RequestBuiltinApproval(waitCtx, approval.PendingRequest{
			Snapshot:           snapshot,
			SnapshotVersion:    snapshotVersion,
			TenantID:           types.CallerFromContext(ctx).TenantID,
			UserID:             meta.UserID,
			SessionID:          meta.SessionID,
			AssistantMessageID: meta.AssistantMessageID,
			RequestID:          meta.RequestID,
			EventBus:           meta.EventBus,
			ServiceName:        operationApprovalService,
			MCPToolName:        t.Name(),
			RegisteredToolName: t.Name(),
			Description:        t.summary(ctx, args),
			Args:               args,
			ToolCallID:         meta.ToolCallID,
			Grant:              grant,
			GrantScope:         grantScope,
		})
		if err != nil {
			return operationFailure(fmt.Sprintf("could not ask the user to confirm %s: %v", t.Name(), err)), nil
		}
	}
	if decision.Suspended != nil {
		return &types.ToolResult{
			Success: false,
			Error:   "waiting for the user to confirm this change",
			Suspend: decision.Suspended,
		}, nil
	}
	if !decision.Approved {
		reason := decision.Reason
		if reason == "" {
			reason = "the user declined"
		}
		return operationFailure(fmt.Sprintf("%s was not run: %s", t.Name(), reason)), nil
	}
	defer t.gate.MarkExecuted(ctx, decision.PendingID)
	if len(decision.ModifiedArgs) > 0 {
		args = decision.ModifiedArgs
	}
	// Waiting for the user may have used up the per-tool budget; the call
	// itself gets a full window from the round context.
	if meta.ApprovalCtx != nil {
		timeout := meta.ExecTimeout
		if timeout <= 0 {
			timeout = defaultOperationTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(WithToolExecContext(meta.ApprovalCtx, meta), timeout)
		defer cancel()
	}
	return t.inner.Execute(ctx, args)
}

// approvalDescriber is a wrapped tool that writes its own confirmation card
// text; an empty description falls back to the summary below.
type approvalDescriber interface {
	ApprovalDescription(ctx context.Context, args json.RawMessage) string
}

func (t *ConfirmedTool) summary(ctx context.Context, args json.RawMessage) string {
	if d, ok := t.inner.(approvalDescriber); ok {
		if description := d.ApprovalDescription(ctx, args); description != "" {
			return description
		}
	}
	return confirmedToolSummary(ctx, t.Name(), args)
}

// approvalGranter is a wrapped tool whose approval may be extended to the
// rest of the session; see approval.PendingRequest.Grant.
type approvalGranter interface {
	ApprovalGrant(ctx context.Context, args json.RawMessage) (grant, scope string)
}

// grant offers a session-wide approval only for recoverable changes.
func (t *ConfirmedTool) grant(ctx context.Context, args json.RawMessage) (string, string) {
	if t.risk != operations.RiskWrite {
		return "", ""
	}
	if g, ok := t.inner.(approvalGranter); ok {
		return g.ApprovalGrant(ctx, args)
	}
	return "", ""
}

// wikiApprovalGrant scopes a Wiki tool's session approval to the knowledge
// bases the run may write to, so a turn that writes elsewhere asks again.
func wikiApprovalGrant(ctx context.Context, name string, kbIDs []string) (string, string) {
	if len(kbIDs) == 0 {
		return "", ""
	}
	sorted := slices.Sorted(slices.Values(kbIDs))
	return name + " knowledge_base:" + strings.Join(sorted, ","), confirmedToolSummary(ctx, name, nil)
}

// confirmedToolSummaries name each wrapped tool on the confirmation card, in
// Chinese and English.
var confirmedToolSummaries = map[string][2]string{
	ToolWikiWritePage:   {"写入 Wiki 页面", "Write a Wiki page"},
	ToolWikiReplaceText: {"修改 Wiki 页面内容", "Edit Wiki page text"},
	ToolWikiRenamePage:  {"重命名 Wiki 页面", "Rename a Wiki page"},
	ToolWikiDeletePage:  {"删除 Wiki 页面", "Delete a Wiki page"},
	ToolWikiFlagIssue:   {"登记 Wiki 问题", "Flag a Wiki issue"},
	ToolWikiUpdateIssue: {"更新 Wiki 问题状态", "Update a Wiki issue"},
}

func confirmedToolSummary(ctx context.Context, name string, args json.RawMessage) string {
	summary := name
	if pair, ok := confirmedToolSummaries[name]; ok {
		summary = pair[1]
		if strings.HasPrefix(types.LanguageFromContextOrDefault(ctx), "zh") {
			summary = pair[0]
		}
	}
	var fields struct {
		Slug    string `json:"slug"`
		NewSlug string `json:"new_slug"`
		Status  string `json:"status"`
	}
	if json.Unmarshal(args, &fields) != nil {
		return summary
	}
	if fields.Slug != "" {
		summary += ": " + fields.Slug
		if fields.NewSlug != "" {
			summary += " → " + fields.NewSlug
		}
	} else if fields.Status != "" {
		summary += ": " + fields.Status
	}
	return summary
}
