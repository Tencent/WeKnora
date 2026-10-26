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

// DefaultPageActionTimeout bounds how long a run waits for the browser. The
// browser answers by itself, so a page that does not answer quickly is gone.
const DefaultPageActionTimeout = 20 * time.Second

// PageActionRequest asks the user's browser to read or fill the open page.
type PageActionRequest struct {
	TenantID           uint64
	UserID             string
	SessionID          string
	AssistantMessageID string
	RequestID          string
	ToolCallID         string
	EventBus           *event.EventBus
	Action             types.PageActionPromptRequest
	// Timeout overrides DefaultPageActionTimeout when > 0.
	Timeout time.Duration
}

// PageActionResult is how the browser answered.
type PageActionResult struct {
	Answered bool
	Response types.PageActionPromptResponse
	Reason   string
	TimedOut bool
	Canceled bool
}

// PageActor is the surface the page tools use (mockable in tests).
type PageActor interface {
	RequestPageAction(ctx context.Context, req PageActionRequest) (PageActionResult, error)
}

var _ PageActor = (*Gate)(nil)

// RequestPageAction sends the action to the browser and waits for its answer.
// The prompt never suspends the run: the page is read or filled now or not
// at all.
func (g *Gate) RequestPageAction(ctx context.Context, req PageActionRequest) (PageActionResult, error) {
	if g == nil {
		return PageActionResult{}, fmt.Errorf("page action: nil gate")
	}
	if req.EventBus == nil {
		return PageActionResult{}, fmt.Errorf("page action: EventBus is nil")
	}
	request, err := json.Marshal(req.Action)
	if err != nil {
		return PageActionResult{}, fmt.Errorf("encode page action: %w", err)
	}
	wait := req.Timeout
	if wait <= 0 {
		wait = DefaultPageActionTimeout
	}
	out, err := g.broker.Await(ctx, hitl.Spec{
		Prompt: &types.AgentPrompt{
			TenantID:           req.TenantID,
			UserID:             req.UserID,
			SessionID:          req.SessionID,
			AssistantMessageID: req.AssistantMessageID,
			RequestID:          req.RequestID,
			Kind:               types.AgentPromptKindPageAction,
			ToolCallID:         req.ToolCallID,
			Request:            types.JSON(request),
		},
		Timeout:       wait,
		TimeoutReason: "the page did not respond",
		Emit: func(ctx context.Context, p *types.AgentPrompt) error {
			waitSec := max(int(wait/time.Second), 1)
			return req.EventBus.Emit(ctx, event.Event{
				ID:        p.ID + "-prompt-required",
				Type:      event.EventAgentPromptRequired,
				SessionID: req.SessionID,
				Data: event.AgentPromptRequiredData{
					PendingID:          p.ID,
					Kind:               types.AgentPromptKindPageAction,
					SessionID:          req.SessionID,
					AssistantMessageID: req.AssistantMessageID,
					Request:            request,
					TimeoutSeconds:     waitSec,
					HotWaitSeconds:     waitSec,
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
		return PageActionResult{}, fmt.Errorf("page action: %w", err)
	}
	res := PageActionResult{Reason: out.Reason, TimedOut: out.TimedOut, Canceled: out.Canceled}
	if out.Suspended || !out.Answered {
		if out.Suspended && res.Reason == "" {
			res.Reason = "the run was taken over"
		}
		return res, nil
	}
	if err := json.Unmarshal(out.Prompt.Response, &res.Response); err != nil {
		res.Reason = "unreadable page response"
		return res, nil
	}
	consumed, consumeErr := g.broker.Consume(ctx, out.Prompt.ID, types.AgentPromptDone, out.Prompt.OwnerInstance)
	if !consumed {
		res.Response = types.PageActionPromptResponse{}
		res.Reason = "the page response could not be confirmed"
		if consumeErr == nil {
			res.Reason = "the page response was already used"
		}
		return res, nil
	}
	res.Answered = true
	return res, nil
}
