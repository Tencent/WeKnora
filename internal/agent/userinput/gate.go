package userinput

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrPendingNotFound  = errors.New("user input pending request not found")
	ErrTenantMismatch   = errors.New("tenant mismatch for user input")
	ErrUserMismatch     = errors.New("user mismatch for user input")
	ErrAlreadyResolved  = errors.New("user input already resolved")
	ErrInvalidAnswer    = errors.New("invalid user input answer")
	ErrOwnerUnavailable = errors.New("user input owner unavailable")
)

// Gate coordinates a live ask_user call with its authenticated answer request.
type Gate struct {
	mu      sync.Mutex
	pending map[string]*waiter
	timeout time.Duration
	rdb     *redis.Client
}

type waiter struct {
	ch       chan Answer
	tenantID uint64
	userID   string
	question Question
	snapshot *PendingSnapshot
	once     sync.Once
	resolved atomic.Bool
}

func (w *waiter) deliver(answer Answer) bool {
	delivered := false
	w.once.Do(func() {
		w.ch <- answer
		w.resolved.Store(true)
		delivered = true
	})
	return delivered
}

func (w *waiter) finishWithoutAnswer() {
	w.once.Do(func() {
		w.resolved.Store(true)
	})
}

// NewGate creates a user-input gate using the existing human-interaction timeout.
func NewGate(cfg *config.Config, rdb *redis.Client) *Gate {
	timeout := 10 * time.Minute
	if cfg != nil && cfg.Agent != nil && cfg.Agent.ToolApprovalTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Agent.ToolApprovalTimeoutSeconds) * time.Second
	}
	return newGate(timeout, rdb)
}

func newGate(timeout time.Duration, rdb *redis.Client) *Gate {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	gate := &Gate{pending: make(map[string]*waiter), timeout: timeout, rdb: rdb}
	if rdb != nil {
		go gate.runSubscriber()
	}
	return gate
}

// RequestAndWait emits the structured question and blocks until one terminal result.
func (g *Gate) RequestAndWait(ctx context.Context, req PendingRequest) (Result, error) {
	if g == nil {
		return Result{}, fmt.Errorf("user input gate is nil")
	}
	if req.EventBus == nil {
		return Result{}, fmt.Errorf("user input EventBus is nil")
	}
	if err := ValidateQuestion(req.Question); err != nil {
		return Result{}, err
	}
	pendingID := uuid.NewString()
	w := &waiter{
		ch: make(chan Answer, 1), tenantID: req.TenantID,
		userID: req.UserID, question: req.Question,
	}
	w.snapshot = pendingSnapshot(pendingID, req, g.timeout)
	g.mu.Lock()
	g.pending[pendingID] = w
	g.mu.Unlock()
	defer g.remove(pendingID, w.snapshot)
	if err := g.storePending(ctx, w.snapshot); err != nil {
		return Result{}, err
	}

	if err := g.emitRequired(ctx, pendingID, req); err != nil {
		return Result{}, err
	}
	timer := time.NewTimer(g.timeout)
	defer timer.Stop()

	select {
	case answer := <-w.ch:
		result := buildResult(req.Question, answer)
		g.emitResolved(ctx, pendingID, req, result)
		return result, nil
	case <-timer.C:
		w.finishWithoutAnswer()
		result := terminalResult(req.Question, StatusTimedOut, "user input timeout")
		g.emitResolved(ctx, pendingID, req, result)
		return result, nil
	case <-ctx.Done():
		w.finishWithoutAnswer()
		result := terminalResult(req.Question, StatusCanceled, "request canceled")
		g.emitResolved(ctx, pendingID, req, result)
		return result, nil
	}
}

// Resolve validates and delivers an answer locally or to the owning replica.
func (g *Gate) Resolve(tenantID uint64, userID, pendingID string, answer Answer) error {
	if g == nil {
		return fmt.Errorf("user input gate is nil")
	}
	err := g.deliverLocal(tenantID, userID, pendingID, answer)
	if !errors.Is(err, ErrPendingNotFound) || g.rdb == nil {
		return err
	}
	return g.resolveCrossInstance(tenantID, userID, pendingID, answer)
}

func (g *Gate) deliverLocal(tenantID uint64, userID, pendingID string, answer Answer) error {
	g.mu.Lock()
	w := g.pending[pendingID]
	g.mu.Unlock()
	if w == nil {
		return ErrPendingNotFound
	}
	if w.tenantID != tenantID {
		return ErrTenantMismatch
	}
	if w.userID == "" || w.userID != userID {
		return ErrUserMismatch
	}
	if err := ValidateAnswer(w.question, answer); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAnswer, err)
	}
	if !w.deliver(answer) {
		return ErrAlreadyResolved
	}
	return nil
}

func (g *Gate) remove(pendingID string, snapshot *PendingSnapshot) {
	g.mu.Lock()
	delete(g.pending, pendingID)
	g.mu.Unlock()
	g.deleteStoredPending(snapshot)
}

func (g *Gate) GetPending(
	ctx context.Context,
	tenantID uint64,
	userID, sessionID string,
) (*PendingSnapshot, error) {
	if tenantID == 0 || userID == "" || sessionID == "" {
		return nil, ErrPendingNotFound
	}
	g.mu.Lock()
	for _, pending := range g.pending {
		if pending.tenantID == tenantID && pending.userID == userID && pending.snapshot.SessionID == sessionID {
			copy := *pending.snapshot
			g.mu.Unlock()
			return &copy, nil
		}
	}
	g.mu.Unlock()
	return g.loadStoredPending(ctx, tenantID, userID, sessionID)
}

func buildResult(question Question, answer Answer) Result {
	status := StatusAnswered
	if answer.Skipped {
		status = StatusSkipped
	}
	selected := make([]Option, 0, len(answer.SelectedOptionIDs))
	for _, id := range answer.SelectedOptionIDs {
		for _, option := range question.Options {
			if option.ID == id {
				selected = append(selected, option)
				break
			}
		}
	}
	return Result{Status: status, FieldKey: question.FieldKey, SchemaVersion: question.SchemaVersion,
		QuestionGroupID: question.GroupID, QuestionIndex: question.Index, QuestionTotal: question.Total,
		SelectedOptions: selected, OtherText: strings.TrimSpace(answer.OtherText), Value: answer.Value}
}

func terminalResult(question Question, status Status, reason string) Result {
	return Result{Status: status, FieldKey: question.FieldKey, SchemaVersion: question.SchemaVersion,
		QuestionGroupID: question.GroupID, QuestionIndex: question.Index, QuestionTotal: question.Total, Reason: reason}
}

func pendingSnapshot(pendingID string, req PendingRequest, timeout time.Duration) *PendingSnapshot {
	return &PendingSnapshot{
		PendingID: pendingID, TenantID: req.TenantID, UserID: req.UserID, SessionID: req.SessionID,
		AssistantMessageID: req.AssistantMessageID, RequestID: req.RequestID, ToolCallID: req.ToolCallID,
		Question: req.Question, Status: "pending", ExpiresAt: time.Now().UTC().Add(timeout),
	}
}
