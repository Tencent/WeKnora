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
	d, err := b.inner.ApproveCommand(ctx, toCommandRequest(req))
	if err != nil {
		return localsandbox.ApprovalDecision{}, err
	}
	decision := localsandbox.ApprovalDecision{
		Approved: d.Approved, Session: d.Session, Reason: d.Reason, RiskLevel: d.RiskLevel,
	}
	if d.Access != "" && req.Proposed != nil {
		narrowed := *req.Proposed
		narrowed.Access = localsandbox.Access(d.Access)
		decision.Grant = &narrowed
	}
	return decision, nil
}

func toCommandRequest(req localsandbox.ApprovalRequest) sandbox.CommandApprovalRequest {
	out := sandbox.CommandApprovalRequest{
		SessionID:       req.SessionID,
		Tool:            req.Spec.Tool,
		Command:         req.Spec.Command,
		Stdin:           req.Spec.Stdin,
		StdinIsProgram:  req.Spec.StdinIsProgram,
		Env:             req.Spec.Env,
		SkillName:       req.Spec.SkillName,
		Cwd:             req.Cwd,
		WorkspaceRoot:   req.WorkspaceRoot,
		Reason:          string(req.Reason),
		Hidden:          req.Hidden,
		DenialSnippet:   req.DenialSnippet,
		FirstAttemptRan: req.FirstAttemptRan,
		AllowSession:    req.AllowSession,
		SessionRules:    req.SessionRules,
		WriteMode:       req.WriteMode,
		WriteContent:    req.WriteContent,
		WriteBytes:      req.WriteBytes,
		EditDiff:        req.EditDiff,
		TargetsComplete: req.TargetsComplete,

		ScriptsIncomplete: req.ScriptsIncomplete,
	}
	for _, script := range req.Scripts {
		out.Scripts = append(out.Scripts, sandbox.ScriptFact{Path: script.Path, Content: script.Content})
	}
	if req.Proposed != nil {
		out.GrantPath = req.Proposed.Path
		out.GrantAccess = string(req.Proposed.Access)
		out.GrantKind = "directory"
		if req.Proposed.File {
			out.GrantKind = "file"
			out.FilePath = req.Proposed.Path
		}
	}
	for _, t := range req.Targets {
		out.Targets = append(out.Targets, sandbox.TargetFact{
			Path: t.Path, Type: t.Type, Size: t.Size, Entries: t.Entries,
		})
	}
	return out
}

// runApprovals reads the approval port for one call. Without a port the
// request carries no approver, so the sandbox refuses anything it would ask.
func runApprovals(ctx context.Context) (user, reviewer localsandbox.Approver, spec localsandbox.ReviewSpec) {
	port, ok := sandbox.CommandApprovalFrom(ctx)
	if !ok {
		return nil, nil, localsandbox.ReviewSpec{}
	}
	spec = localsandbox.ReviewSpec{
		Tool: port.Tool, Command: port.Command, Stdin: port.Stdin, StdinIsProgram: port.StdinIsProgram,
		Env: port.Env, SkillName: port.SkillName,
	}
	if port.Approver != nil {
		user = approverBridge{inner: port.Approver}
	}
	if port.Reviewer != nil {
		reviewer = approverBridge{inner: port.Reviewer}
	}
	return user, reviewer, spec
}
