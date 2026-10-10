package tools

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// HostApproval is the gate surface shell_exec needs on a Lite host workspace.
type HostApproval interface {
	RequestHostAndWait(ctx context.Context, req approval.HostPendingRequest) (approval.Decision, error)
}

var _ HostApproval = (*approval.Gate)(nil)

type hostCommandApprover struct {
	gate     HostApproval
	meta     *ToolExecContext
	tenantID uint64
}

func (a hostCommandApprover) ApproveCommand(
	ctx context.Context, req sandbox.CommandApprovalRequest,
) (sandbox.CommandApprovalDecision, error) {
	d, err := a.gate.RequestHostAndWait(ctx, approval.HostPendingRequest{
		TenantID:           a.tenantID,
		UserID:             a.meta.UserID,
		SessionID:          req.SessionID,
		AssistantMessageID: a.meta.AssistantMessageID,
		RequestID:          a.meta.RequestID,
		ToolCallID:         a.meta.ToolCallID,
		EventBus:           a.meta.EventBus,
		Host: event.HostApprovalPayload{
			Reason:          req.Reason,
			Command:         req.Command,
			Cwd:             req.Cwd,
			GrantPath:       req.GrantPath,
			GrantAccess:     req.GrantAccess,
			DenialSnippet:   req.DenialSnippet,
			FirstAttemptRan: req.FirstAttemptRan,
			AllowSession:    req.AllowSession,
			SessionRules:    req.SessionRules,
		},
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

// WithHostApproval lets host commands wait for the session owner. Without it
// the host sandbox refuses anything it would have asked about.
func (t *ShellExecTool) WithHostApproval(gate HostApproval) *ShellExecTool {
	if t != nil {
		t.hostApproval = gate
	}
	return t
}

// hostApprovalContext attaches the approval port for one host command. The
// result is rooted at ApprovalCtx so a user who takes minutes to answer does
// not use up the command's own timeout; the host sandbox times each attempt.
func (t *ShellExecTool) hostApprovalContext(ctx context.Context, command, stdin string) context.Context {
	review := command
	if stdin != "" && shellStdinIsProgram(command) {
		review = command + "\n" + stdin
	}
	port := sandbox.CommandApproval{Command: review}
	meta, ok := ToolExecFromContext(ctx)
	if !ok || t.hostApproval == nil || meta.EventBus == nil {
		return sandbox.WithCommandApproval(ctx, port)
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	port.Approver = hostCommandApprover{gate: t.hostApproval, meta: meta, tenantID: tenantID}
	base := ctx
	if meta.ApprovalCtx != nil {
		base = WithToolExecContext(meta.ApprovalCtx, meta)
	}
	return sandbox.WithCommandApproval(base, port)
}
