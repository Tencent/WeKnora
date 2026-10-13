// Package approval implements human-in-the-loop gating for dangerous MCP tool calls (issue #1173).
//
// Gate owns the per-tool policy (enabled / requires approval) and adapts tool
// approvals and in-conversation MCP OAuth to the generic prompt broker in
// package hitl, which handles persistence, waiting and cross-replica resolve.
package approval

import (
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
	// PendingID identifies the prompt the decision belongs to; callers that
	// execute an approved call report back with MarkExecuted(PendingID).
	PendingID       string
	Approved        bool
	ModifiedArgs    json.RawMessage // optional JSON object; when set and Approved, replaces original args
	Reason          string
	TimedOut        bool
	ContextCanceled bool
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
}

// MCPApproval is the surface used by MCPTool (mockable in tests).
type MCPApproval interface {
	NeedsApproval(ctx context.Context, tenantID uint64, serviceID, toolName string) bool
	IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	RequestAndWait(ctx context.Context, req PendingRequest) (Decision, error)
	// MarkExecuted records that an approved call has finished running, so an
	// approval is never mistaken for one still in flight.
	MarkExecuted(ctx context.Context, pendingID string)
}

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
	if cfg != nil && cfg.Agent != nil && cfg.Agent.ToolApprovalTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Agent.ToolApprovalTimeoutSeconds) * time.Second
	}
	if broker == nil {
		broker = hitl.NewBroker(nil, nil)
	}
	broker.RegisterKind(types.AgentPromptKindToolApproval, validateToolApprovalResponse)
	broker.RegisterKind(types.AgentPromptKindMCPOAuth, validateMCPOAuthResponse)
	// Default fail-close: if the checker errors, require approval (safer for a
	// HITL feature). Set WEKNORA_AGENT_TOOL_APPROVAL_FAIL_OPEN=true to revert.
	failClose := !strings.EqualFold(strings.TrimSpace(os.Getenv("WEKNORA_AGENT_TOOL_APPROVAL_FAIL_OPEN")), "true")
	return &Gate{checker: checker, timeout: timeout, broker: broker, failClose: failClose}
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
	timeoutSec := max(int(g.timeout/time.Second), 1)

	out, err := g.broker.Await(ctx, hitl.Spec{
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
		Timeout:       g.timeout,
		TimeoutReason: "approval timeout",
		Emit: func(ctx context.Context, p *types.AgentPrompt) error {
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

	d := toolApprovalDecision(out)
	// An approved call stays "executing" until MarkExecuted.
	consumeTo := types.AgentPromptDone
	if d.Approved {
		consumeTo = types.AgentPromptExecuting
	}
	g.broker.Consume(ctx, d.PendingID, consumeTo)
	_ = req.EventBus.Emit(context.WithoutCancel(ctx), event.Event{
		ID:        d.PendingID + "-approval-resolved",
		Type:      event.EventToolApprovalResolved,
		SessionID: req.SessionID,
		Data: event.ToolApprovalResolvedData{
			PendingID: d.PendingID,
			Approved:  d.Approved,
			Reason:    d.Reason,
			TimedOut:  d.TimedOut,
			Canceled:  d.ContextCanceled,
		},
		Metadata: map[string]interface{}{
			"assistant_message_id": req.AssistantMessageID,
		},
		RequestID: req.RequestID,
	})
	return d, nil
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
	g.broker.Consume(ctx, d.PendingID, types.AgentPromptDone)
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
func (g *Gate) Resolve(ctx context.Context, req ResolveRequest) error {
	if g == nil {
		return fmt.Errorf("gate is nil")
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
		return fmt.Errorf("%w: unsupported prompt kind %q", ErrInvalidDecision, kind)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return err
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

func toolApprovalDecision(out hitl.Outcome) Decision {
	d := Decision{
		PendingID:       out.Prompt.ID,
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
	var r types.ToolApprovalPromptResponse
	if err := json.Unmarshal(response, &r); err != nil {
		return err
	}
	trimmed := strings.TrimSpace(string(r.ModifiedArgs))
	if trimmed == "" {
		return nil
	}
	if !r.Approved {
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
