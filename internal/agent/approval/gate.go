// Package approval implements human-in-the-loop gating for dangerous MCP tool calls (issue #1173).
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// pubsubChannel is the Redis channel used to fan-out Resolve calls across
// backend replicas (issue #1173 cross-instance support).
const pubsubChannel = "weknora:mcp_approval:resolve"

// resolveMessage is the JSON payload published when one instance receives a
// Resolve API call but the pending wait may live on another instance.
type resolveMessage struct {
	TenantID     uint64          `json:"tenant_id"`
	PendingID    string          `json:"pending_id"`
	Approved     bool            `json:"approved"`
	ModifiedArgs json.RawMessage `json:"modified_args,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	TimedOut     bool            `json:"timed_out,omitempty"`
	Canceled     bool            `json:"canceled,omitempty"`
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
	mu      sync.Mutex
	pending map[string]*waiter
	checker Checker
	timeout time.Duration
	rdb     *redis.Client // optional; nil disables cross-instance fan-out
}

type waiter struct {
	ch       chan Decision
	tenantID uint64
	once     sync.Once
}

func (w *waiter) deliver(d Decision) {
	if w == nil {
		return
	}
	w.once.Do(func() {
		select {
		case w.ch <- d:
		default:
		}
	})
}

var (
	// ErrPendingNotFound is returned when Resolve is called with an unknown id.
	ErrPendingNotFound = errors.New("tool approval pending not found")
	// ErrTenantMismatch is returned when Resolve tenant does not match the pending request.
	ErrTenantMismatch = errors.New("tenant mismatch for tool approval")
)

// NewGate builds a gate. checker may be nil (disables gating). cfg may be nil
// (defaults apply). rdb may be nil (single-instance mode).
func NewGate(cfg *config.Config, checker Checker, rdb *redis.Client) *Gate {
	timeout := 10 * time.Minute
	if cfg != nil && cfg.Agent != nil && cfg.Agent.ToolApprovalTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Agent.ToolApprovalTimeoutSeconds) * time.Second
	}
	g := &Gate{
		pending: make(map[string]*waiter),
		checker: checker,
		timeout: timeout,
		rdb:     rdb,
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
	for {
		sub := g.rdb.Subscribe(ctx, pubsubChannel)
		ch := sub.Channel()
		for msg := range ch {
			var m resolveMessage
			if err := json.Unmarshal([]byte(msg.Payload), &m); err != nil {
				logger.GetLogger(ctx).Warnf("mcp approval pubsub: bad payload: %v", err)
				continue
			}
			if err := g.deliverLocal(m.TenantID, m.PendingID, Decision{
				Approved:        m.Approved,
				ModifiedArgs:    m.ModifiedArgs,
				Reason:          m.Reason,
				TimedOut:        m.TimedOut,
				ContextCanceled: m.Canceled,
			}); err != nil && !errors.Is(err, ErrPendingNotFound) {
				logger.GetLogger(ctx).Warnf("mcp approval pubsub deliver: %v", err)
			}
		}
		_ = sub.Close()
		// Reconnect after brief backoff if Redis hiccups.
		time.Sleep(2 * time.Second)
	}
}

// NeedsApproval returns whether execution should pause for human confirmation.
func (g *Gate) NeedsApproval(ctx context.Context, tenantID uint64, serviceID, toolName string) bool {
	if g == nil || g.checker == nil || tenantID == 0 || serviceID == "" || toolName == "" {
		return false
	}
	ok, err := g.checker.IsRequired(ctx, tenantID, serviceID, toolName)
	if err != nil {
		logger.GetLogger(ctx).Warnf("mcp tool approval check failed (skip gate): %v", err)
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
		w.deliver(d)
		d = <-w.ch
		emitResolved(d)
		return d, nil
	case <-ctx.Done():
		d = Decision{Approved: false, Reason: "request canceled", ContextCanceled: true}
		w.deliver(d)
		d = <-w.ch
		emitResolved(d)
		return d, nil
	}
}

// Resolve completes a pending approval. tenantID must match the tenant that
// started the wait. If the waiter is not on this instance and Redis Pub/Sub
// is configured, the decision is fanned out to all replicas (best-effort).
func (g *Gate) Resolve(tenantID uint64, pendingID string, d Decision) error {
	if g == nil {
		return fmt.Errorf("gate is nil")
	}
	switch err := g.deliverLocal(tenantID, pendingID, d); {
	case err == nil:
		return nil
	case errors.Is(err, ErrTenantMismatch):
		return err
	case errors.Is(err, ErrPendingNotFound):
		if g.rdb == nil {
			return err
		}
		// Fan out to other replicas.
		payload, mErr := json.Marshal(resolveMessage{
			TenantID:     tenantID,
			PendingID:    pendingID,
			Approved:     d.Approved,
			ModifiedArgs: d.ModifiedArgs,
			Reason:       d.Reason,
			TimedOut:     d.TimedOut,
			Canceled:     d.ContextCanceled,
		})
		if mErr != nil {
			return fmt.Errorf("encode pubsub payload: %w", mErr)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if pErr := g.rdb.Publish(ctx, pubsubChannel, payload).Err(); pErr != nil {
			return fmt.Errorf("publish approval resolve: %w", pErr)
		}
		// Best-effort: the owning instance will deliver and emit Resolved.
		return nil
	default:
		return err
	}
}

// deliverLocal attempts to satisfy a waiter on this instance only.
func (g *Gate) deliverLocal(tenantID uint64, pendingID string, d Decision) error {
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
	g.mu.Unlock()

	w.deliver(d)
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
