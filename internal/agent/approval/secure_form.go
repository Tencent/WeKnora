package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

// SecureFormRequest raises a secure_form prompt.
type SecureFormRequest struct {
	TenantID           uint64
	UserID             string
	SessionID          string
	AssistantMessageID string
	RequestID          string
	ToolCallID         string
	EventBus           *event.EventBus
	Form               types.SecureFormPromptRequest
	// Snapshot, when set, lets the run suspend after the hot wait.
	Snapshot        []byte
	SnapshotVersion int
}

// SecureFormAnswer is how a secure_form prompt ended. Response holds a
// handle, never a value the user typed.
type SecureFormAnswer struct {
	owner     string
	answered  bool
	PromptID  string
	Answered  bool
	Response  types.SecureFormPromptResponse
	Reason    string
	TimedOut  bool
	Canceled  bool
	Suspended *types.PromptSuspension
}

// SecureFormAsker is the surface the request_secure_form tool uses.
type SecureFormAsker interface {
	RequestSecureForm(ctx context.Context, req SecureFormRequest) (SecureFormAnswer, error)
	ResumeSecureForm(ctx context.Context, prompt *types.AgentPrompt, bus *event.EventBus) SecureFormAnswer
}

var _ SecureFormAsker = (*Gate)(nil)

// RequestSecureForm shows the card and waits for the submission, the
// timeout, or the suspension of the run.
func (g *Gate) RequestSecureForm(ctx context.Context, req SecureFormRequest) (SecureFormAnswer, error) {
	if g == nil {
		return SecureFormAnswer{}, fmt.Errorf("secure form: nil gate")
	}
	if req.EventBus == nil {
		return SecureFormAnswer{}, fmt.Errorf("secure form: EventBus is nil")
	}
	request, err := json.Marshal(req.Form)
	if err != nil {
		return SecureFormAnswer{}, fmt.Errorf("encode secure form: %w", err)
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
			Kind:               types.AgentPromptKindSecureForm,
			ToolCallID:         req.ToolCallID,
			Request:            types.JSON(request),
		},
		Timeout:       wait,
		TimeoutReason: "no credentials in time",
		Emit: func(ctx context.Context, p *types.AgentPrompt) error {
			waitSec := promptWaitSeconds(p, wait, g.timeout)
			return req.EventBus.Emit(ctx, event.Event{
				ID:        p.ID + "-prompt-required",
				Type:      event.EventAgentPromptRequired,
				SessionID: req.SessionID,
				Data: event.AgentPromptRequiredData{
					PendingID:          p.ID,
					Kind:               types.AgentPromptKindSecureForm,
					SessionID:          req.SessionID,
					AssistantMessageID: req.AssistantMessageID,
					Request:            request,
					TimeoutSeconds:     waitSec,
					HotWaitSeconds:     waitSec,
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
		return SecureFormAnswer{}, fmt.Errorf("secure form: %w", err)
	}
	if out.Suspended {
		return SecureFormAnswer{
			PromptID: out.Prompt.ID,
			Suspended: &types.PromptSuspension{
				Detached:  out.Detached,
				PromptID:  out.Prompt.ID,
				Kind:      types.AgentPromptKindSecureForm,
				ExpiresAt: out.ExpiresAt,
			},
		}, nil
	}
	a := secureFormAnswer(out)
	g.consumeSecureForm(ctx, &a, req.EventBus, req.SessionID, req.AssistantMessageID, req.RequestID)
	return a, nil
}

// ResumeSecureForm implements SecureFormAsker.
func (g *Gate) ResumeSecureForm(ctx context.Context, prompt *types.AgentPrompt, bus *event.EventBus) SecureFormAnswer {
	a := secureFormAnswer(hitl.Outcome{Prompt: prompt, Answered: true})
	g.consumeSecureForm(ctx, &a, bus, prompt.SessionID, prompt.AssistantMessageID, prompt.RequestID)
	return a
}

// SecureFormPrompt reads a prompt row for the submission endpoint.
func (g *Gate) SecureFormPrompt(ctx context.Context, promptID string) (*types.AgentPrompt, error) {
	return g.broker.Store().Get(ctx, promptID)
}

// ResolveSecureForm records the server-built answer of a secure form. Only
// the submission endpoint calls it; the generic prompt endpoints refuse the
// kind because it is absent from AnswerableKinds.
func (g *Gate) ResolveSecureForm(ctx context.Context, req hitl.ResolveRequest) (hitl.Resolution, error) {
	if g == nil {
		return hitl.Resolution{}, fmt.Errorf("gate is nil")
	}
	req.Kind = types.AgentPromptKindSecureForm
	return g.broker.Resolve(ctx, req)
}

func (g *Gate) consumeSecureForm(
	ctx context.Context, a *SecureFormAnswer, bus *event.EventBus, sessionID, assistantMessageID, requestID string,
) {
	consumed, consumeErr := g.broker.Consume(ctx, a.PromptID, types.AgentPromptDone, a.owner)
	if !consumed && a.answered {
		a.Answered = false
		a.Response = types.SecureFormPromptResponse{}
		a.Reason = "answer is no longer owned or was already consumed"
		if consumeErr == nil {
			a.Suspended = &types.PromptSuspension{
				PromptID: a.PromptID, Kind: types.AgentPromptKindSecureForm, Detached: true,
			}
			return
		}
		a.Reason = "could not confirm the submission"
	}
	if bus == nil {
		return
	}
	var response json.RawMessage
	if a.Answered {
		response, _ = json.Marshal(a.Response)
	}
	_ = bus.Emit(context.WithoutCancel(ctx), event.Event{
		ID:        a.PromptID + "-prompt-resolved",
		Type:      event.EventAgentPromptResolved,
		SessionID: sessionID,
		Data: event.AgentPromptResolvedData{
			PendingID: a.PromptID,
			Kind:      types.AgentPromptKindSecureForm,
			Answered:  a.Answered,
			Response:  response,
			Reason:    a.Reason,
			TimedOut:  a.TimedOut,
			Canceled:  a.Canceled,
		},
		Metadata: map[string]interface{}{
			"assistant_message_id": assistantMessageID,
		},
		RequestID: requestID,
	})
}

func secureFormAnswer(out hitl.Outcome) SecureFormAnswer {
	a := SecureFormAnswer{
		owner: out.Prompt.OwnerInstance, answered: out.Answered, PromptID: out.Prompt.ID,
		Reason: out.Reason, TimedOut: out.TimedOut, Canceled: out.Canceled,
	}
	if !out.Answered {
		return a
	}
	if err := json.Unmarshal(out.Prompt.Response, &a.Response); err != nil {
		a.Reason = "unreadable answer"
		return a
	}
	a.Answered = !a.Response.Declined
	if a.Response.Declined {
		a.Reason = "the user declined to provide credentials"
	}
	return a
}
