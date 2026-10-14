// Package approval implements human-in-the-loop gating for dangerous MCP tool calls (issue #1173).
//
// Gate owns the per-tool policy (enabled / requires approval) and adapts tool
// approvals and in-conversation MCP OAuth to the generic prompt broker in
// package hitl, which handles persistence, waiting and cross-replica resolve.
package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Checker answers per-tool MCP policy questions used during registration and execution.
type Checker interface {
	IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
}

// Decision is the outcome of a pending tool approval.
type Decision struct {
	owner    string // execution token of the waiter or resumed run
	answered bool
	// PendingID identifies the prompt the decision belongs to; callers that
	// execute an approved call report back with MarkExecuted(PendingID).
	PendingID       string
	Approved        bool
	ModifiedArgs    json.RawMessage // optional JSON object; when set and Approved, replaces original args
	Reason          string
	TimedOut        bool
	ContextCanceled bool
	// Suspended is set when no decision arrived during the in-memory wait
	// and the prompt now outlives the run; the call must not execute.
	Suspended *types.PromptSuspension
}

// PendingRequest carries everything needed to block and notify the UI.
type PendingRequest struct {
	TenantID           uint64
	UserID             string // owner of the session that initiated the call (used for Resolve authorization); empty disables user check
	SessionID          string
	AssistantMessageID string
	RequestID          string
	EventBus           *event.EventBus
	ServiceID          string
	ServiceName        string
	MCPToolName        string // name on MCP server
	RegisteredToolName string // registry name e.g. mcp_svc_tool
	Description        string
	Args               json.RawMessage
	ToolCallID         string
	// Snapshot, when set, is the run state to resume from: the wait is cut
	// to the hot-wait window and then suspends instead of timing out.
	Snapshot        []byte
	SnapshotVersion int
}

// MCPApproval is the surface used by MCPTool (mockable in tests).
type MCPApproval interface {
	NeedsApproval(ctx context.Context, tenantID uint64, serviceID, toolName string) bool
	IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	RequestAndWait(ctx context.Context, req PendingRequest) (Decision, error)
	// ResumeDecision is the decision carried by the answered prompt a resumed
	// run was suspended on.
	ResumeDecision(ctx context.Context, prompt *types.AgentPrompt, bus *event.EventBus) Decision
	// MarkExecuted records that an approved call has finished running, so an
	// approval is never mistaken for one still in flight.
	MarkExecuted(ctx context.Context, pendingID string)
}

// BuiltinApproval confirms built-in tool calls that change user data. It has
// no per-tool policy: every call it is asked about needs the user's answer.
type BuiltinApproval interface {
	RequestBuiltinApproval(ctx context.Context, req PendingRequest) (Decision, error)
	ResumeDecision(ctx context.Context, prompt *types.AgentPrompt, bus *event.EventBus) Decision
	MarkExecuted(ctx context.Context, pendingID string)
}

var _ BuiltinApproval = (*Gate)(nil)

// OAuthPendingRequest carries everything needed to prompt the user to authorize
// an OAuth-enabled MCP service mid-conversation and block until they do.
type OAuthPendingRequest struct {
	TenantID           uint64
	UserID             string // session owner; required for Resolve authorization
	SessionID          string
	AssistantMessageID string
	RequestID          string
	EventBus           *event.EventBus
	ServiceID          string
	ServiceName        string
	MCPToolName        string
	ToolCallID         string
	// WaitTimeout overrides the gate's default wait timeout when > 0. The wait
	// is always bounded (either by this value, the gate default, or ctx
	// cancellation) so the blocked goroutine never leaks.
	WaitTimeout time.Duration
}

// ResolveRequest is a user decision on a tool approval or OAuth prompt.
// SessionID, when set, must match the prompt. Kind selects how Decision is
// encoded and must match the prompt; empty means a tool approval.
type ResolveRequest struct {
	TenantID  uint64
	UserID    string
	SessionID string
	Kind      string
	PendingID string
	Decision  Decision
}

var _ MCPApproval = (*Gate)(nil)

// Gate applies per-tool MCP policy and raises approval / OAuth prompts.
type Gate struct {
	checker   Checker
	timeout   time.Duration
	hotWait   time.Duration // in-memory wait before a suspendable approval suspends the run
	broker    *hitl.Broker
	failClose bool // when true, NeedsApproval errors block (require approval) instead of skip
}

// Errors returned by Resolve; they alias the prompt broker's errors.
var (
	ErrPendingNotFound = hitl.ErrPromptNotFound
	ErrTenantMismatch  = hitl.ErrTenantMismatch
	ErrAlreadyResolved = hitl.ErrAlreadyResolved
	ErrUserMismatch    = hitl.ErrUserMismatch
	ErrInvalidDecision = hitl.ErrInvalidResponse
)

// NewGate builds a gate. checker may be nil (disables gating). cfg may be nil
// (defaults apply). broker may be nil (process-local prompts, single replica).
func NewGate(cfg *config.Config, checker Checker, broker *hitl.Broker) *Gate {
	timeout := 10 * time.Minute
	hotWait := config.DefaultPromptHotWait
	if cfg != nil && cfg.Agent != nil {
		if cfg.Agent.ToolApprovalTimeoutSeconds > 0 {
			timeout = time.Duration(cfg.Agent.ToolApprovalTimeoutSeconds) * time.Second
		}
		hotWait = cfg.Agent.PromptHotWait()
	}
	if broker == nil {
		broker = hitl.NewBroker(nil, nil)
	}
	broker.RegisterKind(types.AgentPromptKindToolApproval, validateToolApprovalResponse)
	broker.RegisterKind(types.AgentPromptKindMCPOAuth, validateMCPOAuthResponse)
	broker.RegisterKind(types.AgentPromptKindAskUser, types.ValidateAskUserResponse)
	// Default fail-close: if the checker errors, require approval (safer for a
	// HITL feature). Set WEKNORA_AGENT_TOOL_APPROVAL_FAIL_OPEN=true to revert.
	failClose := !strings.EqualFold(strings.TrimSpace(os.Getenv("WEKNORA_AGENT_TOOL_APPROVAL_FAIL_OPEN")), "true")
	return &Gate{checker: checker, timeout: timeout, hotWait: hotWait, broker: broker, failClose: failClose}
}

// NeedsApproval returns whether execution should pause for human confirmation.
func (g *Gate) NeedsApproval(ctx context.Context, tenantID uint64, serviceID, toolName string) bool {
	if g == nil || g.checker == nil || tenantID == 0 || serviceID == "" || toolName == "" {
		return false
	}
	ok, err := g.checker.IsRequired(ctx, tenantID, serviceID, toolName)
	if err != nil {
		// Default fail-close: a transient DB error must NOT silently allow a
		// dangerous tool to run. Operators can opt into legacy behaviour via
		// WEKNORA_AGENT_TOOL_APPROVAL_FAIL_OPEN=true.
		if g.failClose {
			logger.GetLogger(ctx).Warnf("mcp tool approval check failed (fail-close: requiring approval): %v", err)
			return true
		}
		logger.GetLogger(ctx).Warnf("mcp tool approval check failed (fail-open: skip gate): %v", err)
		return false
	}
	return ok
}

// IsEnabled reports whether a per-tool MCP policy allows registration or
// execution. An absent gate/checker keeps tools enabled (policy store is
// optional). A missing tenant or tool identity is fail-closed: the caller
// cannot attribute a tenant-scoped disable flag, so the tool is not allowed.
func (g *Gate) IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	if g == nil || g.checker == nil {
		return true, nil
	}
	if tenantID == 0 || serviceID == "" || toolName == "" {
		return false, nil
	}
	return g.checker.IsEnabled(ctx, tenantID, serviceID, toolName)
}

// RequestAndWait emits a UI event, then blocks until Resolve, timeout, or ctx cancellation.
func (g *Gate) RequestAndWait(ctx context.Context, req PendingRequest) (Decision, error) {
	if g == nil {
		return Decision{Approved: true}, nil
	}
	if g.checker == nil {
		return Decision{Approved: true}, nil
	}
	return g.awaitToolApproval(ctx, req)
}

// RequestBuiltinApproval asks the user to approve a built-in tool call.
// Unlike RequestAndWait it has no MCP policy to consult and never approves
// on its own: without a broker or an event bus to reach the user, the call
// is refused.
func (g *Gate) RequestBuiltinApproval(ctx context.Context, req PendingRequest) (Decision, error) {
	if g == nil || g.broker == nil {
		return Decision{}, fmt.Errorf("tool approval is not available")
	}
	return g.awaitToolApproval(ctx, req)
}

func (g *Gate) awaitToolApproval(ctx context.Context, req PendingRequest) (Decision, error) {
	if req.EventBus == nil {
		return Decision{}, fmt.Errorf("tool approval: EventBus is nil")
	}

	request, err := json.Marshal(types.ToolApprovalPromptRequest{
		ServiceID:          req.ServiceID,
		ServiceName:        req.ServiceName,
		MCPToolName:        req.MCPToolName,
		RegisteredToolName: req.RegisteredToolName,
		Description:        req.Description,
		Args:               req.Args,
	})
	if err != nil {
		return Decision{}, fmt.Errorf("encode tool approval: %w", err)
	}
	var argsObj interface{}
	if len(req.Args) > 0 {
		_ = json.Unmarshal(req.Args, &argsObj)
	}
	wait := g.timeout
	var suspend *hitl.SuspendSpec
	if req.Snapshot != nil {
		wait = g.hotWait
		suspend = &hitl.SuspendSpec{Snapshot: req.Snapshot, SnapshotVersion: req.SnapshotVersion}
	}
	out, err := g.broker.Await(ctx, hitl.Spec{
		Suspend:         suspend,
		FallbackTimeout: g.timeout,
		Prompt: &types.AgentPrompt{
			TenantID:           req.TenantID,
			UserID:             req.UserID,
			SessionID:          req.SessionID,
			AssistantMessageID: req.AssistantMessageID,
			RequestID:          req.RequestID,
			Kind:               types.AgentPromptKindToolApproval,
			ToolCallID:         req.ToolCallID,
			Request:            types.JSON(request),
		},
		Timeout:       wait,
		TimeoutReason: "approval timeout",
		Emit: func(ctx context.Context, p *types.AgentPrompt) error {
			timeoutSec := promptWaitSeconds(p, wait, g.timeout)
			return req.EventBus.Emit(ctx, event.Event{
				ID:        p.ID + "-approval-required",
				Type:      event.EventToolApprovalRequired,
				SessionID: req.SessionID,
				Data: event.ToolApprovalRequiredData{
					PendingID:          p.ID,
					TenantID:           req.TenantID,
					SessionID:          req.SessionID,
					AssistantMessageID: req.AssistantMessageID,
					ServiceID:          req.ServiceID,
					ServiceName:        req.ServiceName,
					MCPToolName:        req.MCPToolName,
					RegisteredToolName: req.RegisteredToolName,
					Description:        req.Description,
					Args:               argsObj,
					ArgsJSON:           string(req.Args),
					TimeoutSeconds:     timeoutSec,
					HotWaitSeconds:     timeoutSec,
					Suspendable:        p.Suspendable,
					RequestedAtUnix:    time.Now().Unix(),
					ToolCallID:         req.ToolCallID,
					RequestID:          req.RequestID,
				},
				Metadata: map[string]interface{}{
					"assistant_message_id": req.AssistantMessageID,
					"pending_id":           p.ID,
				},
				RequestID: req.RequestID,
			})
		},
	})
	if err != nil {
		return Decision{}, fmt.Errorf("tool approval: %w", err)
	}
	if out.Suspended {
		// Still open: the run ends here and the decision resumes it later.
		return Decision{
			PendingID: out.Prompt.ID,
			Suspended: &types.PromptSuspension{
				Detached:  out.Detached,
				PromptID:  out.Prompt.ID,
				Kind:      types.AgentPromptKindToolApproval,
				ExpiresAt: out.ExpiresAt,
			},
		}, nil
	}

	d := toolApprovalDecision(out)
	g.consumeDecision(ctx, &d, req.EventBus, req.SessionID, req.AssistantMessageID, req.RequestID)
	return d, nil
}

// promptWaitSeconds is how long the UI should count down for p: the hot wait
// while it can suspend, the full timeout once it fell back to a plain wait.
func promptWaitSeconds(p *types.AgentPrompt, wait, fallback time.Duration) int {
	if !p.Suspendable {
		wait = fallback
	}
	return max(int(wait/time.Second), 1)
}

// ResumeDecision turns the answered prompt a resumed run was suspended on
// into the decision for its blocked call, as if RequestAndWait had just
// returned it.
func (g *Gate) ResumeDecision(ctx context.Context, prompt *types.AgentPrompt, bus *event.EventBus) Decision {
	d := toolApprovalDecision(hitl.Outcome{Prompt: prompt, Answered: true})
	g.consumeDecision(ctx, &d, bus, prompt.SessionID, prompt.AssistantMessageID, prompt.RequestID)
	return d
}

func (g *Gate) consumeDecision(
	ctx context.Context, d *Decision, bus *event.EventBus, sessionID, assistantMessageID, requestID string,
) {
	// An approved call stays "executing" until MarkExecuted.
	consumeTo := types.AgentPromptDone
	if d.Approved {
		consumeTo = types.AgentPromptExecuting
	}
	consumed, consumeErr := g.broker.Consume(ctx, d.PendingID, consumeTo, d.owner)
	if !consumed && d.answered {
		d.Approved = false
		d.ModifiedArgs = nil
		d.Reason = "approval is no longer owned or was already consumed"
		if consumeErr == nil {
			d.Suspended = &types.PromptSuspension{
				PromptID: d.PendingID, Kind: types.AgentPromptKindToolApproval, Detached: true,
			}
			return
		}
		d.Reason = "could not confirm tool approval; tool was not executed"
	}
	if bus == nil {
		return
	}
	_ = bus.Emit(context.WithoutCancel(ctx), event.Event{
		ID:        d.PendingID + "-approval-resolved",
		Type:      event.EventToolApprovalResolved,
		SessionID: sessionID,
		Data: event.ToolApprovalResolvedData{
			PendingID: d.PendingID,
			Approved:  d.Approved,
			Reason:    d.Reason,
			TimedOut:  d.TimedOut,
			Canceled:  d.ContextCanceled,
		},
		Metadata: map[string]interface{}{
			"assistant_message_id": assistantMessageID,
		},
		RequestID: requestID,
	})
}

// RequestOAuthAndWait emits an "MCP OAuth required" UI event, then blocks
// until the user authorizes (delivered via Resolve), the wait times out, or
// the request ctx is canceled. A returned Decision.Approved==true means the
// user completed authorization and the tool call should be retried.
//
// Unlike RequestAndWait this does NOT consult the approval checker — it is
// driven reactively by an authorization-required error from the MCP transport.
func (g *Gate) RequestOAuthAndWait(ctx context.Context, req OAuthPendingRequest) (Decision, error) {
	if g == nil {
		return Decision{}, fmt.Errorf("oauth gate: nil gate")
	}
	if req.EventBus == nil {
		return Decision{}, fmt.Errorf("oauth gate: EventBus is nil")
	}

	request, err := json.Marshal(types.MCPOAuthPromptRequest{
		ServiceID:   req.ServiceID,
		ServiceName: req.ServiceName,
		MCPToolName: req.MCPToolName,
	})
	if err != nil {
		return Decision{}, fmt.Errorf("encode oauth prompt: %w", err)
	}
	waitTimeout := g.timeout
	if req.WaitTimeout > 0 {
		waitTimeout = req.WaitTimeout
	}
	timeoutSec := max(int(waitTimeout/time.Second), 1)

	out, err := g.broker.Await(ctx, hitl.Spec{
		Prompt: &types.AgentPrompt{
			TenantID:           req.TenantID,
			UserID:             req.UserID,
			SessionID:          req.SessionID,
			AssistantMessageID: req.AssistantMessageID,
			RequestID:          req.RequestID,
			Kind:               types.AgentPromptKindMCPOAuth,
			ToolCallID:         req.ToolCallID,
			Request:            types.JSON(request),
		},
		Timeout:       waitTimeout,
		TimeoutReason: "authorization timeout",
		Emit: func(ctx context.Context, p *types.AgentPrompt) error {
			return req.EventBus.Emit(ctx, event.Event{
				ID:        p.ID + "-mcp-oauth-required",
				Type:      event.EventMCPOAuthRequired,
				SessionID: req.SessionID,
				Data: event.MCPOAuthRequiredData{
					PendingID:          p.ID,
					TenantID:           req.TenantID,
					SessionID:          req.SessionID,
					AssistantMessageID: req.AssistantMessageID,
					ServiceID:          req.ServiceID,
					ServiceName:        req.ServiceName,
					MCPToolName:        req.MCPToolName,
					TimeoutSeconds:     timeoutSec,
					RequestedAtUnix:    time.Now().Unix(),
					ToolCallID:         req.ToolCallID,
					RequestID:          req.RequestID,
				},
				Metadata: map[string]interface{}{
					"assistant_message_id": req.AssistantMessageID,
					"pending_id":           p.ID,
				},
				RequestID: req.RequestID,
			})
		},
	})
	if err != nil {
		return Decision{}, fmt.Errorf("oauth gate: %w", err)
	}

	d := oauthDecision(out)
	consumed, consumeErr := g.broker.Consume(ctx, d.PendingID, types.AgentPromptDone, d.owner)
	if !consumed && d.answered {
		d.Approved = false
		d.Reason = "authorization is no longer owned or was already consumed"
		if consumeErr != nil {
			d.Reason = "could not confirm authorization; tool was not retried"
		}
	}
	_ = req.EventBus.Emit(context.WithoutCancel(ctx), event.Event{
		ID:        d.PendingID + "-mcp-oauth-resolved",
		Type:      event.EventMCPOAuthResolved,
		SessionID: req.SessionID,
		Data: event.MCPOAuthResolvedData{
			PendingID:  d.PendingID,
			ServiceID:  req.ServiceID,
			Authorized: d.Approved,
			Reason:     d.Reason,
			TimedOut:   d.TimedOut,
			Canceled:   d.ContextCanceled,
		},
		Metadata: map[string]interface{}{
			"assistant_message_id": req.AssistantMessageID,
		},
		RequestID: req.RequestID,
	})
	return d, nil
}

// MarkExecuted records that an approved call has finished running.
func (g *Gate) MarkExecuted(ctx context.Context, pendingID string) {
	if g == nil {
		return
	}
	g.broker.MarkExecuted(ctx, pendingID)
}

// Resolve records a user decision on a pending prompt and wakes its waiter.
// A suspended prompt answered this way is not resumed; use ResolveForResume.
func (g *Gate) Resolve(ctx context.Context, req ResolveRequest) error {
	_, err := g.ResolveForResume(ctx, req)
	return err
}

// ResolveForResume records a user decision. When the prompt was suspended,
// the returned resolution asks the caller to resume its run.
func (g *Gate) ResolveForResume(ctx context.Context, req ResolveRequest) (hitl.Resolution, error) {
	if g == nil {
		return hitl.Resolution{}, fmt.Errorf("gate is nil")
	}
	kind := req.Kind
	if kind == "" {
		kind = types.AgentPromptKindToolApproval
	}
	var response any
	switch kind {
	case types.AgentPromptKindToolApproval:
		r := types.ToolApprovalPromptResponse{Approved: req.Decision.Approved, Reason: req.Decision.Reason}
		if r.Approved {
			r.ModifiedArgs = req.Decision.ModifiedArgs
		}
		response = r
	case types.AgentPromptKindMCPOAuth:
		response = types.MCPOAuthPromptResponse{Authorized: req.Decision.Approved, Reason: req.Decision.Reason}
	default:
		return hitl.Resolution{}, fmt.Errorf("%w: unsupported prompt kind %q", ErrInvalidDecision, kind)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return hitl.Resolution{}, err
	}
	return g.broker.Resolve(ctx, hitl.ResolveRequest{
		TenantID:  req.TenantID,
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Kind:      kind,
		PromptID:  req.PendingID,
		Response:  payload,
	})
}

// AnswerableKinds are the prompt kinds a user may answer with a raw
// response. MCP OAuth is absent: only the OAuth callback may report an
// authorization, never the user's own claim.
var AnswerableKinds = map[string]bool{
	types.AgentPromptKindAskUser:      true,
	types.AgentPromptKindToolApproval: true,
}

// ResolveResponse records a user's answer given as the kind's response
// JSON. Authorization and validation are the same as ResolveForResume.
func (g *Gate) ResolveResponse(ctx context.Context, req hitl.ResolveRequest) (hitl.Resolution, error) {
	if g == nil {
		return hitl.Resolution{}, fmt.Errorf("gate is nil")
	}
	if !AnswerableKinds[req.Kind] {
		return hitl.Resolution{}, fmt.Errorf("%w: prompt kind %q cannot be answered here", ErrInvalidDecision, req.Kind)
	}
	return g.broker.Resolve(ctx, req)
}

func toolApprovalDecision(out hitl.Outcome) Decision {
	d := Decision{
		PendingID:       out.Prompt.ID,
		owner:           out.Prompt.OwnerInstance,
		answered:        out.Answered,
		Reason:          out.Reason,
		TimedOut:        out.TimedOut,
		ContextCanceled: out.Canceled,
	}
	if !out.Answered {
		return d
	}
	var r types.ToolApprovalPromptResponse
	if err := json.Unmarshal(out.Prompt.Response, &r); err != nil {
		d.Reason = "unreadable approval response"
		return d
	}
	d.Approved, d.Reason = r.Approved, r.Reason
	if r.Approved && len(r.ModifiedArgs) > 0 {
		d.ModifiedArgs = r.ModifiedArgs
	}
	return d
}

func oauthDecision(out hitl.Outcome) Decision {
	d := Decision{
		PendingID:       out.Prompt.ID,
		owner:           out.Prompt.OwnerInstance,
		answered:        out.Answered,
		Reason:          out.Reason,
		TimedOut:        out.TimedOut,
		ContextCanceled: out.Canceled,
	}
	if !out.Answered {
		return d
	}
	var r types.MCPOAuthPromptResponse
	if err := json.Unmarshal(out.Prompt.Response, &r); err != nil {
		d.Reason = "unreadable authorization response"
		return d
	}
	d.Approved, d.Reason = r.Authorized, r.Reason
	return d
}

func validateToolApprovalResponse(_, response json.RawMessage) error {
	// Strict decoding with a required "approved": a misspelled or missing
	// field must not read as a rejection.
	var r struct {
		Approved     *bool           `json:"approved"`
		ModifiedArgs json.RawMessage `json:"modified_args,omitempty"`
		Reason       string          `json:"reason,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(response))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return err
	}
	if r.Approved == nil {
		return errors.New(`"approved" is required`)
	}
	trimmed := strings.TrimSpace(string(r.ModifiedArgs))
	if trimmed == "" {
		return nil
	}
	if !*r.Approved {
		return errors.New("modified_args is only allowed on approval")
	}
	var probe map[string]any
	if err := json.Unmarshal(r.ModifiedArgs, &probe); err != nil || probe == nil {
		return errors.New("modified_args must be a non-null JSON object")
	}
	return nil
}

func validateMCPOAuthResponse(_, response json.RawMessage) error {
	var r types.MCPOAuthPromptResponse
	return json.Unmarshal(response, &r)
}

// Adapter makes MCPToolApprovalService satisfy Checker without importing the service package here.
type Adapter struct {
	Svc interface {
		IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
		IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	}
}

var _ Checker = (*Adapter)(nil)

// IsRequired implements Checker.
func (a *Adapter) IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	if a == nil || a.Svc == nil {
		return false, nil
	}
	return a.Svc.IsRequired(ctx, tenantID, serviceID, toolName)
}

// IsEnabled implements Checker.
func (a *Adapter) IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	if a == nil || a.Svc == nil {
		return true, nil
	}
	return a.Svc.IsEnabled(ctx, tenantID, serviceID, toolName)
}
