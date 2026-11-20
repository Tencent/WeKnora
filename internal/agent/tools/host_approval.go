package tools

import (
	"context"
	"errors"
	"regexp"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/hostreview"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// HostApproval is the gate surface a host tool needs to ask the session owner.
type HostApproval interface {
	RequestHostAndWait(ctx context.Context, req approval.HostPendingRequest) (approval.Decision, error)
}

var _ HostApproval = (*approval.Gate)(nil)

// HostReviewer answers a host approval in auto mode.
type HostReviewer interface {
	Review(ctx context.Context, turn hostreview.Turn, req sandbox.CommandApprovalRequest) hostreview.Verdict
}

var _ HostReviewer = (*hostreview.Reviewer)(nil)

// HostApprovals is what a host tool needs to ask about one local action: the
// gate that shows the user a card (ask), and the reviewer that answers for
// the user (auto). The sandbox picks one by the session's mode.
type HostApprovals struct {
	Gate     HostApproval
	Reviewer HostReviewer
}

// hostPreviewLimit caps the write preview a card shows.
const hostPreviewLimit = 64 << 10

// attach puts the approval port for one tool call on ctx. The result is
// rooted at ApprovalCtx so a wait does not use up the tool's own timeout; the
// sandbox times each command itself.
func (h HostApprovals) attach(ctx context.Context, port sandbox.CommandApproval) context.Context {
	meta, ok := ToolExecFromContext(ctx)
	if !ok || meta.EventBus == nil {
		return sandbox.WithCommandApproval(ctx, port)
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	if h.Gate != nil {
		port.Approver = hostUserApprover{gate: h.Gate, meta: meta, tenantID: tenantID}
	}
	if h.Reviewer != nil {
		port.Reviewer = hostModelApprover{reviewer: h.Reviewer, meta: meta}
	}
	base := ctx
	if meta.ApprovalCtx != nil {
		base = WithToolExecContext(WithOutputBudget(meta.ApprovalCtx, OutputBudget(ctx)), meta)
	}
	return sandbox.WithCommandApproval(base, port)
}

type hostUserApprover struct {
	gate     HostApproval
	meta     *ToolExecContext
	tenantID uint64
}

func requestIDOf(ctx context.Context, meta *ToolExecContext) string {
	if meta != nil && meta.RequestID != "" {
		return meta.RequestID
	}
	id, _ := types.RequestIDFromContext(ctx)
	return id
}

func (a hostUserApprover) ApproveCommand(
	ctx context.Context, req sandbox.CommandApprovalRequest,
) (sandbox.CommandApprovalDecision, error) {
	d, err := a.gate.RequestHostAndWait(ctx, approval.HostPendingRequest{
		TenantID:           a.tenantID,
		UserID:             a.meta.UserID,
		SessionID:          req.SessionID,
		AssistantMessageID: a.meta.AssistantMessageID,
		RequestID:          requestIDOf(ctx, a.meta),
		ToolCallID:         a.meta.ToolCallID,
		EventBus:           a.meta.EventBus,
		Tool:               req.Tool,
		Host:               hostPayload(req, "user"),
	})
	if err != nil {
		return sandbox.CommandApprovalDecision{}, err
	}
	switch {
	case d.TimedOut:
		return sandbox.CommandApprovalDecision{}, errors.New("the approval request timed out")
	case d.ContextCanceled:
		return sandbox.CommandApprovalDecision{}, errors.New("the approval request was canceled")
	}
	return sandbox.CommandApprovalDecision{
		Approved: d.Approved,
		Session:  d.Scope == "session" && req.AllowSession,
		Access:   d.Access,
		Reason:   d.Reason,
	}, nil
}

// hostModelApprover asks the review model and records the question and the
// answer as the same pair of events a card uses, marked reviewer=model, so
// the timeline shows what was decided and why.
type hostModelApprover struct {
	reviewer HostReviewer
	meta     *ToolExecContext
}

func (a hostModelApprover) ApproveCommand(
	ctx context.Context, req sandbox.CommandApprovalRequest,
) (sandbox.CommandApprovalDecision, error) {
	pendingID := uuid.New().String()
	host := hostPayload(req, "model")
	requestID := requestIDOf(ctx, a.meta)
	a.emit(ctx, event.EventToolApprovalRequired, pendingID+"-approval-required", event.ToolApprovalRequiredData{
		PendingID: pendingID, SessionID: req.SessionID, AssistantMessageID: a.meta.AssistantMessageID,
		RegisteredToolName: req.Tool, ToolCallID: a.meta.ToolCallID, RequestID: requestID,
		Kind: approval.HostApprovalKindCommand, Host: &host,
	})
	v := a.reviewer.Review(ctx, hostreview.Turn{SessionID: req.SessionID, RequestID: requestID}, req)
	a.emit(context.WithoutCancel(ctx), event.EventToolApprovalResolved, pendingID+"-approval-resolved",
		event.ToolApprovalResolvedData{
			PendingID: pendingID, Approved: v.Approved, Reason: v.Reason, RiskLevel: v.RiskLevel,
		})
	return sandbox.CommandApprovalDecision{Approved: v.Approved, Reason: v.Reason, RiskLevel: v.RiskLevel}, nil
}

// emit records the review; a failed emit only loses the timeline row.
func (a hostModelApprover) emit(ctx context.Context, typ event.EventType, id string, data interface{}) {
	_ = a.meta.EventBus.Emit(ctx, event.Event{
		ID: id, Type: typ, SessionID: a.meta.SessionID, Data: data, RequestID: requestIDOf(ctx, a.meta),
		Metadata: map[string]interface{}{"assistant_message_id": a.meta.AssistantMessageID},
	})
}

// hostPayload renders the approval card. The preview is cut by bytes, so it
// backs off to a rune boundary: writes and diffs are frequently Chinese, and
// a partial rune would reach the card as U+FFFD through the event JSON.
func hostPayload(req sandbox.CommandApprovalRequest, reviewer string) event.HostApprovalPayload {
	preview := req.WriteContent
	if req.EditDiff != "" {
		preview = req.EditDiff
	}
	preview = common.TruncateUTF8Bytes(preview, hostPreviewLimit)
	return event.HostApprovalPayload{
		Reason:          req.Reason,
		Reviewer:        reviewer,
		Tool:            req.Tool,
		Command:         reviewText(req),
		Cwd:             req.Cwd,
		GrantPath:       req.GrantPath,
		GrantAccess:     req.GrantAccess,
		DenialSnippet:   req.DenialSnippet,
		FirstAttemptRan: req.FirstAttemptRan,
		AllowSession:    req.AllowSession && reviewer == "user",
		SessionRules:    req.SessionRules,
		FilePath:        req.FilePath,
		WriteMode:       req.WriteMode,
		WriteBytes:      writeBytes(req),
		WritePreview:    preview,
		Env:             maskedEnv(req.Env),
		SkillName:       req.SkillName,
		Stdin:           stdinData(req),
		Scripts:         hostScripts(req.Scripts),
	}
}

// secretEnvName matches names whose value a card must not print. The name
// still shows, so an unexpected variable is visible.
var secretEnvName = regexp.MustCompile(`(?i)token|secret|passw|credential|api_?key|private_?key|auth|cookie|_key$`)

func maskedEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for name, value := range env {
		if secretEnvName.MatchString(name) {
			value = "***"
		}
		out[name] = value
	}
	return out
}

// stdinData is stdin the command reads as data. A program on stdin is already
// part of the command text the card shows. Like the write preview, the bound
// is a byte budget, so back off to a rune boundary before cutting.
func stdinData(req sandbox.CommandApprovalRequest) string {
	if req.StdinIsProgram {
		return ""
	}
	return common.TruncateUTF8Bytes(req.Stdin, hostPreviewLimit)
}

func hostScripts(scripts []sandbox.ScriptFact) []event.HostScript {
	out := make([]event.HostScript, 0, len(scripts))
	for _, script := range scripts {
		out = append(out, event.HostScript{Path: script.Path, Content: script.Content})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// writeBytes is the size the card shows. Overwrite and append carry that
// size as WriteContent. An edit carries a diff and the resulting file size
// separately, so an empty content must not display as zero bytes.
func writeBytes(req sandbox.CommandApprovalRequest) int {
	if req.WriteContent != "" {
		return len(req.WriteContent)
	}
	if req.WriteBytes > 0 {
		return req.WriteBytes
	}
	return 0
}

func reviewText(req sandbox.CommandApprovalRequest) string {
	if req.Stdin == "" || !req.StdinIsProgram {
		return req.Command
	}
	return req.Command + "\n" + req.Stdin
}
