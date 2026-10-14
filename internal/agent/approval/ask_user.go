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

// AskRequest raises an ask_user prompt.
type AskRequest struct {
	TenantID           uint64
	UserID             string
	SessionID          string
	AssistantMessageID string
	RequestID          string
	ToolCallID         string
	EventBus           *event.EventBus
	Question           types.AskUserPromptRequest
	// Snapshot, when set, lets the run suspend after the hot wait.
	Snapshot        []byte
	SnapshotVersion int
}

// Answer is how an ask_user prompt ended.
type Answer struct {
	owner    string
	answered bool
	PromptID string
	Answered bool
	Response types.AskUserPromptResponse
	Reason   string
	TimedOut bool
	Canceled bool
	// Suspended is set when the run must end and wait for the answer.
	Suspended *types.PromptSuspension
}

// UserAsker is the surface the ask_user tool uses (mockable in tests).
type UserAsker interface {
	AskUser(ctx context.Context, req AskRequest) (Answer, error)
	// ResumeAnswer is the answer carried by the prompt a resumed run was
	// suspended on.
	ResumeAnswer(ctx context.Context, prompt *types.AgentPrompt, bus *event.EventBus) Answer
}

var _ UserAsker = (*Gate)(nil)

// AskUser shows the question and waits for the answer, the timeout, or the
// suspension of the run.
func (g *Gate) AskUser(ctx context.Context, req AskRequest) (Answer, error) {
	if g == nil {
		return Answer{}, fmt.Errorf("ask user: nil gate")
	}
	if req.EventBus == nil {
		return Answer{}, fmt.Errorf("ask user: EventBus is nil")
	}
	request, err := json.Marshal(req.Question)
	if err != nil {
		return Answer{}, fmt.Errorf("encode question: %w", err)
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
			Kind:               types.AgentPromptKindAskUser,
			ToolCallID:         req.ToolCallID,
			Request:            types.JSON(request),
		},
		Timeout:       wait,
		TimeoutReason: "no answer in time",
		Emit: func(ctx context.Context, p *types.AgentPrompt) error {
			waitSec := promptWaitSeconds(p, wait, g.timeout)
			return req.EventBus.Emit(ctx, event.Event{
				ID:        p.ID + "-prompt-required",
				Type:      event.EventAgentPromptRequired,
				SessionID: req.SessionID,
				Data: event.AgentPromptRequiredData{
					PendingID:          p.ID,
					Kind:               types.AgentPromptKindAskUser,
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
		return Answer{}, fmt.Errorf("ask user: %w", err)
	}
	if out.Suspended {
		return Answer{
			PromptID: out.Prompt.ID,
			Suspended: &types.PromptSuspension{
				Detached:  out.Detached,
				PromptID:  out.Prompt.ID,
				Kind:      types.AgentPromptKindAskUser,
				ExpiresAt: out.ExpiresAt,
			},
		}, nil
	}
	a := askAnswer(out)
	g.consumeAnswer(ctx, &a, req.EventBus, req.SessionID, req.AssistantMessageID, req.RequestID)
	return a, nil
}

// ResumeAnswer implements UserAsker.
func (g *Gate) ResumeAnswer(ctx context.Context, prompt *types.AgentPrompt, bus *event.EventBus) Answer {
	a := askAnswer(hitl.Outcome{Prompt: prompt, Answered: true})
	g.consumeAnswer(ctx, &a, bus, prompt.SessionID, prompt.AssistantMessageID, prompt.RequestID)
	return a
}

func (g *Gate) consumeAnswer(
	ctx context.Context, a *Answer, bus *event.EventBus, sessionID, assistantMessageID, requestID string,
) {
	consumed, consumeErr := g.broker.Consume(ctx, a.PromptID, types.AgentPromptDone, a.owner)
	if !consumed && a.answered {
		a.Answered = false
		a.Response = types.AskUserPromptResponse{}
		a.Reason = "answer is no longer owned or was already consumed"
		if consumeErr == nil {
			a.Suspended = &types.PromptSuspension{
				PromptID: a.PromptID, Kind: types.AgentPromptKindAskUser, Detached: true,
			}
			return
		}
		a.Reason = "could not confirm the user answer"
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
			Kind:      types.AgentPromptKindAskUser,
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

func askAnswer(out hitl.Outcome) Answer {
	a := Answer{
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
		a.Reason = "the user declined to answer"
	}
	return a
}
