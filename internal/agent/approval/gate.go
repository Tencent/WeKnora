// Package approval implements human-in-the-loop gating for dangerous MCP tool calls (issue #1173).
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// pubsubChannelBase is the Redis channel prefix used to fan-out Resolve calls
// across backend replicas (issue #1173 cross-instance support). The actual
// channel is suffixed with WEKNORA_REDIS_NAMESPACE (when set) so multiple
// deployments sharing the same Redis don't cross-talk.
const pubsubChannelBase = "weknora:mcp_approval:resolve"

// instanceID is a process-unique id used to ignore self-published pubsub
// messages (avoids "pending not found" log noise on the publisher side).
var instanceID = uuid.New().String()

// resolveMessage is the JSON payload published when one instance receives a
// Resolve API call but the pending wait may live on another instance.
type resolveMessage struct {
	TenantID     uint64          `json:"tenant_id"`
	UserID       string          `json:"user_id,omitempty"`
	PendingID    string          `json:"pending_id"`
	Approved     bool            `json:"approved"`
	ModifiedArgs json.RawMessage `json:"modified_args,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	TimedOut     bool            `json:"timed_out,omitempty"`
	Canceled     bool            `json:"canceled,omitempty"`
	// ReplyChannel, when non-empty, asks the owning instance to publish a
	// resolveAck so the caller can know if delivery actually happened.
	ReplyChannel string `json:"reply_channel,omitempty"`
	OriginID     string `json:"origin_id,omitempty"`
}

// resolveAck is published by the owning instance back to the caller's
// per-pending reply channel so HTTP semantics stay accurate cross-instance.
type resolveAck struct {
	PendingID string `json:"pending_id"`
	Status    string `json:"status"` // ok | not_found | tenant_mismatch | user_mismatch | already_resolved
	OriginID  string `json:"origin_id,omitempty"`
}

// pubsubChannel returns the namespaced pubsub channel name.
func pubsubChannel() string {
	if ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); ns != "" {
		return pubsubChannelBase + ":" + ns
	}
	return pubsubChannelBase
}

// Checker answers whether a concrete MCP tool requires human approval before execution.
type Checker interface {
	IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
}

// Decision is the outcome of a pending tool approval.
type Decision struct {
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
	RequestAndWait(ctx context.Context, req PendingRequest) (Decision, error)
}

var _ MCPApproval = (*Gate)(nil)

// Gate coordinates wait/resolve for MCP tool approvals.
//
// Pending waiters live in-memory on the instance that started RequestAndWait.
// When a redis client is supplied, Resolve calls hitting any replica are
// published over Redis Pub/Sub so the owning instance can deliver the decision
// (issue #1173 cross-instance support). Without redis, the gate degrades to
// single-process behavior (deployments must use sticky sessions).
type Gate struct {
	mu        sync.Mutex
	pending   map[string]*waiter
	checker   Checker
	timeout   time.Duration
	rdb       *redis.Client // optional; nil disables cross-instance fan-out
	failClose bool          // when true, NeedsApproval errors block (require approval) instead of skip
}

type waiter struct {
	ch       chan Decision
	tenantID uint64
	userID   string // empty means "skip user check"
	once     sync.Once
	resolved bool // set true once a decision was delivered (by user, timer, or ctx)
}

// deliver returns true when this call won the race and actually delivered the
// decision; false when a previous deliver already happened. Callers can use
// the return value to map to ErrAlreadyResolved for HTTP feedback.
func (w *waiter) deliver(d Decision) bool {
	if w == nil {
		return false
	}
	delivered := false
	w.once.Do(func() {
		select {
		case w.ch <- d:
			w.resolved = true
			delivered = true
		default:
		}
	})
	return delivered
}

var (
	// ErrPendingNotFound is returned when Resolve is called with an unknown id.
	ErrPendingNotFound = errors.New("tool approval pending not found")
	// ErrTenantMismatch is returned when Resolve tenant does not match the pending request.
	ErrTenantMismatch = errors.New("tenant mismatch for tool approval")
	// ErrAlreadyResolved is returned when Resolve loses the race against a
	// timeout / cancellation: the waiter is still in the map but its decision
	// channel was already consumed by the timer/ctx branch.
	ErrAlreadyResolved = errors.New("tool approval already resolved")
	// ErrUserMismatch is returned when Resolve is invoked by a user that does
	// not own the originating session.
	ErrUserMismatch = errors.New("user mismatch for tool approval")
)

// NewGate builds a gate. checker may be nil (disables gating). cfg may be nil
// (defaults apply). rdb may be nil (single-instance mode).
func NewGate(cfg *config.Config, checker Checker, rdb *redis.Client) *Gate {
	timeout := 10 * time.Minute
	if cfg != nil && cfg.Agent != nil && cfg.Agent.ToolApprovalTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Agent.ToolApprovalTimeoutSeconds) * time.Second
	}
	// Default fail-close: if the checker errors, require approval (safer for a
	// HITL feature). Set WEKNORA_AGENT_TOOL_APPROVAL_FAIL_OPEN=true to revert.
	failClose := !strings.EqualFold(strings.TrimSpace(os.Getenv("WEKNORA_AGENT_TOOL_APPROVAL_FAIL_OPEN")), "true")
	g := &Gate{
		pending:   make(map[string]*waiter),
		checker:   checker,
		timeout:   timeout,
		rdb:       rdb,
		failClose: failClose,
	}
	if rdb != nil {
		go g.runSubscriber()
	}
	return g
}

// runSubscriber listens for cross-instance Resolve fan-outs and delivers
// decisions to local waiters. Runs for the lifetime of the process.
func (g *Gate) runSubscriber() {
	ctx := context.Background()
	channel := pubsubChannel()
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		sub := g.rdb.Subscribe(ctx, channel)
		ch := sub.Channel()
		// Reset backoff once we have an active subscription.
		backoff = time.Second
		for msg := range ch {
			var m resolveMessage
			if err := json.Unmarshal([]byte(msg.Payload), &m); err != nil {
				logger.GetLogger(ctx).Warnf("mcp approval pubsub: bad payload: %v", err)
				continue
			}
			// Skip messages we published ourselves; they would always miss
			// locally and produce noisy ErrPendingNotFound.
			if m.OriginID == instanceID {
				continue
			}
			err := g.deliverLocal(m.TenantID, m.UserID, m.PendingID, Decision{
				Approved:        m.Approved,
				ModifiedArgs:    m.ModifiedArgs,
				Reason:          m.Reason,
				TimedOut:        m.TimedOut,
				ContextCanceled: m.Canceled,
			})
			// Reply to the originating instance so it can return accurate HTTP
			// status codes. Only the owning instance (or one that detects a
			// real conflict) replies; other replicas stay silent on NotFound.
			if m.ReplyChannel != "" {
				status := ""
				switch {
				case err == nil:
					status = "ok"
				case errors.Is(err, ErrTenantMismatch):
					status = "tenant_mismatch"
				case errors.Is(err, ErrUserMismatch):
					status = "user_mismatch"
				case errors.Is(err, ErrAlreadyResolved):
					status = "already_resolved"
				}
				if status != "" {
					ackPayload, _ := json.Marshal(resolveAck{
						PendingID: m.PendingID,
						Status:    status,
						OriginID:  instanceID,
					})
					pubCtx, pubCancel := context.WithTimeout(ctx, 2*time.Second)
					if pErr := g.rdb.Publish(pubCtx, m.ReplyChannel, ackPayload).Err(); pErr != nil {
						logger.GetLogger(ctx).Warnf("mcp approval pubsub reply: %v", pErr)
					}
					pubCancel()
				}
			}
			switch {
			case err == nil, errors.Is(err, ErrPendingNotFound):
				// Either delivered or this isn't the owning replica — quiet.
			default:
				logger.GetLogger(ctx).Warnf("mcp approval pubsub deliver: %v", err)
			}
		}
		_ = sub.Close()
		// Reconnect with capped exponential backoff if Redis hiccups.
		time.Sleep(backoff)
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
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

	pendingID := uuid.New().String()
	w := &waiter{
		ch:       make(chan Decision, 1),
		tenantID: req.TenantID,
		userID:   req.UserID,
	}

	g.mu.Lock()
	g.pending[pendingID] = w
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		delete(g.pending, pendingID)
		g.mu.Unlock()
	}()

	var argsObj interface{}
	if len(req.Args) > 0 {
		_ = json.Unmarshal(req.Args, &argsObj)
	}

	timeoutSec := int(g.timeout / time.Second)
	if timeoutSec < 1 {
		timeoutSec = 1
	}

	evtData := event.ToolApprovalRequiredData{
		PendingID:          pendingID,
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
	}

	if err := req.EventBus.Emit(ctx, event.Event{
		ID:        pendingID + "-approval-required",
		Type:      event.EventToolApprovalRequired,
		SessionID: req.SessionID,
		Data:      evtData,
		Metadata: map[string]interface{}{
			"assistant_message_id": req.AssistantMessageID,
			"pending_id":           pendingID,
		},
		RequestID: req.RequestID,
	}); err != nil {
		return Decision{}, fmt.Errorf("emit tool approval required: %w", err)
	}

	timer := time.NewTimer(g.timeout)
	defer timer.Stop()

	emitResolved := func(d Decision) {
		if req.EventBus == nil {
			return
		}
		_ = req.EventBus.Emit(context.WithoutCancel(ctx), event.Event{
			ID:        pendingID + "-approval-resolved",
			Type:      event.EventToolApprovalResolved,
			SessionID: req.SessionID,
			Data: event.ToolApprovalResolvedData{
				PendingID: pendingID,
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
	}

	var d Decision
	select {
	case d = <-w.ch:
		emitResolved(d)
		return d, nil
	case <-timer.C:
		d = Decision{Approved: false, Reason: "approval timeout", TimedOut: true}
		_ = w.deliver(d)
		d = <-w.ch
		emitResolved(d)
		return d, nil
	case <-ctx.Done():
		d = Decision{Approved: false, Reason: "request canceled", ContextCanceled: true}
		_ = w.deliver(d)
		d = <-w.ch
		emitResolved(d)
		return d, nil
	}
}

// Resolve completes a pending approval. tenantID must match the tenant that
// started the wait; userID, when non-zero, must match the session owner.
//
// If the waiter is not on this instance and Redis Pub/Sub is configured, the
// decision is fanned out to all replicas and ack'd via a reply key in Redis
// so the caller can distinguish "delivered elsewhere" from "no instance had
// the pending" (issue #1173 follow-up).
func (g *Gate) Resolve(tenantID uint64, userID, pendingID string, d Decision) error {
	if g == nil {
		return fmt.Errorf("gate is nil")
	}
	switch err := g.deliverLocal(tenantID, userID, pendingID, d); {
	case err == nil:
		return nil
	case errors.Is(err, ErrTenantMismatch),
		errors.Is(err, ErrUserMismatch),
		errors.Is(err, ErrAlreadyResolved):
		return err
	case errors.Is(err, ErrPendingNotFound):
		if g.rdb == nil {
			return err
		}
		return g.resolveCrossInstance(tenantID, userID, pendingID, d)
	default:
		return err
	}
}

// resolveCrossInstance publishes a resolve request and waits briefly for an
// ack from the instance that owns the pending. The ack is sent over a
// per-pending Redis pub/sub reply channel and contains the same error codes
// the local path would return, so HTTP responses stay accurate even when the
// session lives on a different replica.
func (g *Gate) resolveCrossInstance(tenantID uint64, userID, pendingID string, d Decision) error {
	replyChannel := pubsubChannel() + ":reply:" + pendingID
	sub := g.rdb.Subscribe(context.Background(), replyChannel)
	defer func() { _ = sub.Close() }()
	// Wait for the subscription to be active before publishing so we don't
	// miss the ack.
	subCtx, subCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer subCancel()
	if _, err := sub.Receive(subCtx); err != nil {
		return fmt.Errorf("subscribe approval reply: %w", err)
	}

	payload, mErr := json.Marshal(resolveMessage{
		TenantID:     tenantID,
		UserID:       userID,
		PendingID:    pendingID,
		Approved:     d.Approved,
		ModifiedArgs: d.ModifiedArgs,
		Reason:       d.Reason,
		TimedOut:     d.TimedOut,
		Canceled:     d.ContextCanceled,
		ReplyChannel: replyChannel,
		OriginID:     instanceID,
	})
	if mErr != nil {
		return fmt.Errorf("encode pubsub payload: %w", mErr)
	}
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	if pErr := g.rdb.Publish(pubCtx, pubsubChannel(), payload).Err(); pErr != nil {
		return fmt.Errorf("publish approval resolve: %w", pErr)
	}

	// Wait for the owning instance to ack. Window is short (3s) — UX wins
	// from getting an accurate 404/409 over slowly returning 200.
	ackCtx, ackCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer ackCancel()
	for {
		msg, err := sub.ReceiveMessage(ackCtx)
		if err != nil {
			// Timeout or subscription error: nobody confirmed, treat as not found.
			return ErrPendingNotFound
		}
		var ack resolveAck
		if jErr := json.Unmarshal([]byte(msg.Payload), &ack); jErr != nil {
			continue
		}
		switch ack.Status {
		case "ok":
			return nil
		case "tenant_mismatch":
			return ErrTenantMismatch
		case "user_mismatch":
			return ErrUserMismatch
		case "already_resolved":
			return ErrAlreadyResolved
		case "not_found":
			return ErrPendingNotFound
		default:
			return fmt.Errorf("approval reply: unexpected status %q", ack.Status)
		}
	}
}

// deliverLocal attempts to satisfy a waiter on this instance only.
func (g *Gate) deliverLocal(tenantID uint64, userID, pendingID string, d Decision) error {
	g.mu.Lock()
	w, ok := g.pending[pendingID]
	if !ok {
		g.mu.Unlock()
		return ErrPendingNotFound
	}
	if w.tenantID != tenantID {
		g.mu.Unlock()
		return ErrTenantMismatch
	}
	if w.userID != "" && userID != "" && w.userID != userID {
		g.mu.Unlock()
		return ErrUserMismatch
	}
	already := w.resolved
	g.mu.Unlock()

	if already {
		return ErrAlreadyResolved
	}
	if !w.deliver(d) {
		return ErrAlreadyResolved
	}
	return nil
}

// Adapter makes MCPToolApprovalService satisfy Checker without importing the service package here.
type Adapter struct {
	Svc interface {
		IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error)
	}
}

// IsRequired implements Checker.
func (a *Adapter) IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	if a == nil || a.Svc == nil {
		return false, nil
	}
	return a.Svc.IsRequired(ctx, tenantID, serviceID, toolName)
}
