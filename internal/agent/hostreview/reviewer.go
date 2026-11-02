// Package hostreview answers Lite host approvals in auto mode with a separate
// model call. It never runs tools and never widens what the sandbox proposed.
package hostreview

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

const defaultTimeout = 60 * time.Second

// UserMessages is the conversation the reviewer may read.
type UserMessages interface {
	GetRecentUserMessages(ctx context.Context, sessionID string, limit int) ([]*types.Message, error)
}

// Turn identifies the agent turn whose action is reviewed.
type Turn struct {
	SessionID string
	RequestID string
}

// Verdict is the review's answer.
type Verdict struct {
	Approved  bool
	Reason    string
	RiskLevel string
}

// Reviewer reviews one action per call.
type Reviewer struct {
	model    chat.Chat
	messages UserMessages
	timeout  time.Duration
}

// New returns a reviewer that uses model, normally the session agent's own
// chat model, in a separate call.
func New(model chat.Chat, messages UserMessages) *Reviewer {
	return &Reviewer{model: model, messages: messages, timeout: defaultTimeout}
}

// Review fails closed: any error, timeout or malformed reply is a refusal.
func (r *Reviewer) Review(ctx context.Context, turn Turn, req sandbox.CommandApprovalRequest) Verdict {
	started := time.Now()
	v := r.review(ctx, turn, req)
	logger.Infof(ctx, "[HostReview] session=%s tool=%s reason=%s approved=%t risk=%s elapsed=%s why=%q",
		turn.SessionID, req.Tool, reviewReason(req), v.Approved, v.RiskLevel, time.Since(started), v.Reason)
	return v
}

func (r *Reviewer) review(ctx context.Context, turn Turn, req sandbox.CommandApprovalRequest) Verdict {
	if r == nil || r.model == nil {
		return Verdict{Reason: "auto review is unavailable; ask the user to switch to ask mode"}
	}
	if !req.TargetsComplete {
		return Verdict{Reason: "too many targets to review automatically; ask the user to switch to ask mode"}
	}
	if req.ScriptsIncomplete {
		return Verdict{Reason: "this command runs a script that cannot be shown for review; " +
			"ask the user to switch to ask mode"}
	}
	if size, err := actionSize(req); err != nil || size > maxActionBytes {
		return Verdict{Reason: "the action is too long to review automatically; ask the user to switch to ask mode"}
	}
	if turn.RequestID == "" {
		turn.RequestID, _ = types.RequestIDFromContext(ctx)
	}
	var msgs []*types.Message
	if r.messages != nil {
		var err error
		if msgs, err = r.messages.GetRecentUserMessages(ctx, turn.SessionID, transcriptFetch); err != nil {
			return Verdict{Reason: fmt.Sprintf("auto review could not read the conversation: %v", err)}
		}
	}
	current, earlier := splitTranscript(msgs, turn.RequestID)
	// Only this turn can authorize. An empty current list would leave the
	// model guessing from earlier messages or the untrusted action.
	if turn.RequestID == "" || len(current) == 0 {
		return Verdict{Reason: "auto review could not identify this turn's user messages; " +
			"ask the user to switch to ask mode"}
	}
	if currentTurnTooLong(current) {
		return Verdict{Reason: "this turn's messages are too long to review automatically; " +
			"ask the user to switch to ask mode"}
	}
	prompt, err := buildUserPrompt(current, earlier, req)
	if err != nil {
		return Verdict{Reason: fmt.Sprintf("auto review could not build its input: %v", err)}
	}

	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	resp, err := r.model.Chat(callCtx, []chat.Message{
		{Role: "system", Content: systemPolicy},
		{Role: "user", Content: prompt},
	}, &chat.ChatOptions{
		Temperature:         0,
		MaxCompletionTokens: 4096,
		// Off, not low: a graded level still turns thinking on, and a small
		// completion budget is then spent before the JSON answer is written.
		ReasoningEffort: api.ReasoningOff,
		Isolated:        true,
		PromptCacheKey:  "host-review:" + turn.SessionID,
	})
	switch {
	case errors.Is(callCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		return Verdict{Reason: "auto review timed out"}
	case ctx.Err() != nil:
		return Verdict{Reason: "the turn was stopped"}
	case err != nil:
		return Verdict{Reason: fmt.Sprintf("auto review failed: %v", err)}
	case resp == nil:
		return Verdict{Reason: "auto review returned nothing"}
	case len(resp.ToolCalls) > 0:
		return Verdict{Reason: "auto review tried to call a tool, which is not allowed"}
	}
	return parseAssessment(resp.Content).verdict()
}
