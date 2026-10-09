package adapter

import (
	"context"

	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// approverBridge adapts the agent-side approval port to the sandbox's own
// Approver. It is the only place that knows both vocabularies.
type approverBridge struct{ inner sandbox.CommandApprover }

func (b approverBridge) Approve(
	ctx context.Context, req localsandbox.ApprovalRequest,
) (localsandbox.ApprovalDecision, error) {
	out := sandbox.CommandApprovalRequest{
		SessionID:       req.SessionID,
		Command:         req.Command,
		Cwd:             req.Cwd,
		Reason:          string(req.Reason),
		DenialSnippet:   req.DenialSnippet,
		FirstAttemptRan: req.FirstAttemptRan,
		AllowSession:    req.AllowSession,
		SessionRules:    req.SessionRules,
	}
	if req.Proposed != nil {
		out.GrantPath = req.Proposed.Path
		out.GrantAccess = string(req.Proposed.Access)
	}
	d, err := b.inner.ApproveCommand(ctx, out)
	if err != nil {
		return localsandbox.ApprovalDecision{}, err
	}
	decision := localsandbox.ApprovalDecision{Approved: d.Approved, Session: d.Session, Reason: d.Reason}
	if d.Access != "" && req.Proposed != nil {
		decision.Grant = &localsandbox.Grant{Path: req.Proposed.Path, Access: localsandbox.Access(d.Access)}
	}
	return decision, nil
}

// runApproval reads the approval port for one command. Without a port the
// request carries no approver, so the sandbox refuses anything it would ask.
func runApproval(ctx context.Context) (localsandbox.Approver, string) {
	approval, ok := sandbox.CommandApprovalFrom(ctx)
	if !ok {
		return nil, ""
	}
	if approval.Approver == nil {
		return nil, approval.Command
	}
	return approverBridge{inner: approval.Approver}, approval.Command
}
