// Package collaboration owns durable agent work. Queue notifications are hints;
// the database is the authority for admission, ownership and cancellation.
package collaboration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrConflict indicates a stale revision or execution lease.
	ErrConflict = errors.New("collaboration changed; refresh its state")
	// ErrClosed indicates that the collaboration no longer accepts work.
	ErrClosed = errors.New("collaboration is closed")
	// ErrScope indicates that the caller cannot access the requested collaboration.
	ErrScope = errors.New("collaboration is outside the caller's scope")
	// ErrHistoryLimit indicates that an active aggregate cannot accept more history.
	ErrHistoryLimit = errors.New("collaboration history limit reached")
)

// maxAggregateBytes bounds one serialized collaboration aggregate.
var maxAggregateBytes = 32 << 20

// isFenceError reports a durable ownership or admission decision, as opposed
// to a transient store failure that may be retried.
func isFenceError(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrClosed) || errors.Is(err, ErrScope) ||
		errors.Is(err, ErrHistoryLimit)
}

// Record is an aggregate with an optimistic concurrency fence. One bounded
// document makes admission, event publication and the pending-work outbox atomic
// on both SQLite and PostgreSQL, without relying on process-local locks.
type Record struct {
	ID        string `gorm:"primaryKey;type:varchar(36)"`
	TenantID  uint64 `gorm:"not null"`
	UserID    string `gorm:"type:varchar(64);not null"`
	SessionID string `gorm:"type:varchar(36);not null"`
	Status    string `gorm:"type:varchar(32);not null;index"`
	Version   int64  `gorm:"not null"`
	Data      string `gorm:"type:text;not null"`
	UpdatedAt time.Time
}

// TableName returns the durable collaboration aggregate table.
func (Record) TableName() string { return "agent_collaborations" }

// Scope identifies a collaboration within its tenant, user and session.
type Scope struct {
	TenantID              uint64
	UserID, SessionID, ID string
}

// Group holds the parent state, children and ordered mailbox in one atomic aggregate.
type Group struct {
	// Revision is the database CAS version, never a caller-provided JSON field.
	Revision        int64 `json:"-"`
	InputBytes      int
	SuccessorID     string // durable admission for a subsequent human turn
	ParentMessageID string
	ParentOwner     string
	ParentLease     time.Time
	ParentWait      *ParentWait
	// ParentClosePending keeps interrupted/expired parents recoverable until their transcript and stream are closed.
	ParentClosePending bool
	RootQuery          string
	Scope              Scope
	Status             string
	Members            []*Member
	Events             []Event
	Requests           map[string]string
	NextSequence       int64
	Deadline           time.Time
	// RunBudget is the shared, cumulative allowance for the parent and every
	// child execution in this collaboration. Nil is uncapped.
	RunBudget *types.RunBudgetUsage `json:"run_budget,omitempty"`
	// RunBudgetTurnID fences a per-turn account from a previous human turn.
	RunBudgetTurnID string `json:"run_budget_turn_id,omitempty"`
	// RunBudgetStartedAt is the active clock origin; waiting for a human does
	// not extend it when the run is resumed.
	RunBudgetStartedAt time.Time `json:"run_budget_started_at,omitempty"`
	// BudgetReservations and BudgetActuals are keyed per model/tool operation.
	// Actual replaces a reservation once settled; unsettled keys stay reserved.
	BudgetReservations map[string]types.RunBudgetUsed `json:"budget_reservations,omitempty"`
	BudgetActuals      map[string]types.RunBudgetUsed `json:"budget_actuals,omitempty"`
	// BudgetExtensions records each owner decision that added capacity. The
	// key makes a retried resume idempotent instead of granting the allowance
	// twice.
	BudgetExtensions map[string]types.RunBudget `json:"budget_extensions,omitempty"`
}

// Member is a named child task with its execution history and incoming mailbox.
type Member struct {
	// Inherited instances remain addressable across parent turns but are not
	// current-turn UI tasks until explicitly continued.
	Inherited bool   `json:"inherited,omitempty"`
	ID        string `json:"member_id"`
	Name      string `json:"task_name"`
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	// ConfigJSON is stored separately from the model-visible projection.
	ConfigJSON string  `json:"config_snapshot"`
	Runs       []*Run  `json:"runs"`
	Mail       []Event `json:"mail,omitempty"`
}

// Progress is bounded operational telemetry, not model reasoning or tool payloads.
type Progress struct {
	Phase      string    `json:"phase"`
	ToolName   string    `json:"tool_name,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	Iteration  int       `json:"iteration,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Run tracks one child execution, its lease and eventual result.
type Run struct {
	// Inherited identifies a baseline run copied from an earlier parent turn.
	Inherited       bool         `json:"inherited,omitempty"`
	Prompt          *PromptState `json:"prompt,omitempty"`
	Progress        *Progress    `json:"progress,omitempty"`
	ID              string       `json:"run_id"`
	State           string       `json:"status"`
	Query           string       `json:"query"`
	Owner           string       `json:"-"`
	Lease           time.Time    `json:"lease_until"`
	Attempt         int          `json:"attempt"`
	CancelRequested bool         `json:"cancel_requested"`
	Result          Result       `json:"result"`
	// DeliveredSequence fences automatic followups even when the executor cannot return a result.
	DeliveredSequence int64  `json:"delivered_sequence,omitempty"`
	PromptID          string `json:"prompt_id,omitempty"`
	MessageID         string `json:"message_id,omitempty"`
	UserMessageID     string `json:"user_message_id,omitempty"`
	// RunBudgetTurnID pins this execution to its parent turn's shared account.
	RunBudgetTurnID string `json:"run_budget_turn_id,omitempty"`
}

// MarshalJSON preserves private ownership for persistence; model projections omit it.
func (r Run) MarshalJSON() ([]byte, error) {
	type alias Run
	return json.Marshal(struct {
		alias
		Owner string `json:"owner"`
	}{alias(r), r.Owner})
}

// UnmarshalJSON restores private ownership alongside the persisted execution state.
func (r *Run) UnmarshalJSON(b []byte) error {
	type alias Run
	v := struct {
		*alias
		Owner string `json:"owner"`
	}{alias: (*alias)(r)}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	r.Owner = v.Owner
	return nil
}

// Result records a child’s answer, evidence and execution outcome.
type Result struct {
	// Artifacts are runtime-produced metadata, never parsed from model prose.
	Artifacts        types.MessageArtifacts `json:"artifacts,omitempty"`
	Truncated        bool                   `json:"truncated,omitempty"`
	Evidence         string                 `json:"evidence,omitempty"`
	Answer           string                 `json:"answer,omitempty"`
	Error            string                 `json:"error,omitempty"`
	MessageID        string                 `json:"message_id,omitempty"`
	ConsumedSequence int64                  `json:"consumed_sequence,omitempty"`
	Suspended        bool                   `json:"suspended,omitempty"`
}

// Event is an ordered, run-correlated mailbox message.
type Event struct {
	Sequence int64  `json:"sequence"`
	From     string `json:"from"`
	RunID    string `json:"run_id,omitempty"`
	Kind     string `json:"kind"`
	Content  string `json:"content"`
}

// Store persists collaboration aggregates using optimistic concurrency.
type Store struct {
	db *gorm.DB
	// OnStatus runs after a successful commit, never inside a retried mutation.
	// Consumers must tolerate duplicate and out-of-order snapshots by revision.
	OnStatus func(context.Context, *Group)
}

// NewStore wraps a database connection for collaboration persistence.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// ErrRunBudgetExhausted means a child cannot start without exceeding the
// parent-owned shared contract. It is distinct from a storage failure.
var ErrRunBudgetExhausted = errors.New("collaboration run budget exhausted")

// chargedBudget returns actual usage once an operation settles, otherwise its
// reservation. Keeping the maps separate makes retries idempotent and releases
// worst-case request headroom when the provider reports the real amount.
func chargedBudget(g *Group, key string) types.RunBudgetUsed {
	// Once an operation settles, actual usage is authoritative. A worst-case
	// request reservation must be released even when the response used less.
	if actual, ok := g.BudgetActuals[key]; ok {
		return actual
	}
	return g.BudgetReservations[key]
}

func chargedBudgetTotal(g *Group) types.RunBudgetUsed {
	var total types.RunBudgetUsed
	keys := map[string]struct{}{}
	for k := range g.BudgetReservations {
		keys[k] = struct{}{}
	}
	for k := range g.BudgetActuals {
		keys[k] = struct{}{}
	}
	for k := range keys {
		used := chargedBudget(g, k)
		total.TotalTokens += used.TotalTokens
		total.WallTimeSec += used.WallTimeSec
		total.WriteActions += used.WriteActions
	}
	return total
}

// SetCollaborationRunBudget initializes the one cumulative account shared by
// the parent and its children. Repeating it for the same turn is idempotent;
// a later human turn starts a fresh account instead of inheriting an exhausted
// predecessor.
func (s *Store) SetCollaborationRunBudget(
	ctx context.Context, scope Scope, turnID string, usage *types.RunBudgetUsage,
) error {
	if turnID == "" || usage == nil {
		return ErrScope
	}
	return s.Change(ctx, scope, func(g *Group) error {
		if g.RunBudgetTurnID == turnID && g.RunBudget != nil {
			// Re-attaching the same turn must not erase reservations, actuals,
			// or an owner-granted extension.
			return nil
		}
		g.RunBudgetTurnID = turnID
		g.RunBudget = &types.RunBudgetUsage{Limit: usage.Limit}
		g.RunBudgetStartedAt = time.Now()
		g.BudgetReservations = map[string]types.RunBudgetUsed{}
		g.BudgetActuals = map[string]types.RunBudgetUsed{}
		g.BudgetExtensions = map[string]types.RunBudget{}
		return nil
	})
}

// RunBudgetAccount is the durable identity and charged usage of one
// collaboration turn. Children use the same account as their parent.
type RunBudgetAccount struct {
	TurnID string
	Usage  types.RunBudgetUsage
}

// SharedRunBudget returns the account for a turn. Nil means the collaboration
// has no explicit run contract or the account belongs to another turn.
func (s *Store) SharedRunBudget(ctx context.Context, scope Scope, turnID string) (*types.RunBudgetUsage, error) {
	account, err := s.RunBudgetAccount(ctx, scope, turnID)
	if err != nil || account == nil {
		return nil, err
	}
	return &account.Usage, nil
}

// RunBudgetAccount returns the account identity together with current usage.
func (s *Store) RunBudgetAccount(ctx context.Context, scope Scope, turnID string) (*RunBudgetAccount, error) {
	g, err := s.Read(ctx, scope)
	if err != nil {
		return nil, err
	}
	if g.RunBudget == nil || (turnID != "" && g.RunBudgetTurnID != "" && g.RunBudgetTurnID != turnID) {
		return nil, nil
	}
	usage := *g.RunBudget
	usage.Used = chargedBudgetTotal(g)
	usage.Used.WallTimeSec = max(usage.Used.WallTimeSec, 0)
	if !g.RunBudgetStartedAt.IsZero() {
		accountElapsed := max(int(time.Since(g.RunBudgetStartedAt)/time.Second), 0)
		if usage.Used.WallTimeSec == 0 {
			usage.Used.WallTimeSec = accountElapsed
		} else {
			usage.Used.WallTimeSec = min(usage.Used.WallTimeSec, accountElapsed)
		}
	}
	return &RunBudgetAccount{TurnID: g.RunBudgetTurnID, Usage: usage}, nil
}

// ExcludeWallTime removes interactive wait time from the account's active
// clock. It is additive and idempotent only because the caller passes the
// measured interval once per wait.
func (s *Store) ExcludeWallTime(ctx context.Context, scope Scope, turnID string, wait time.Duration) error {
	if turnID == "" || wait <= 0 {
		return nil
	}
	return s.Change(ctx, scope, func(g *Group) error {
		if g.RunBudgetTurnID != turnID || g.RunBudgetStartedAt.IsZero() {
			return nil
		}
		g.RunBudgetStartedAt = g.RunBudgetStartedAt.Add(wait)
		return nil
	})
}

// ExtendBudget adds capacity to the existing turn account and clears the
// exhausted condition without resetting any reservations or actuals.
func (s *Store) ExtendBudget(
	ctx context.Context, scope Scope, turnID, extensionKey string, extension types.RunBudget,
) error {
	if turnID == "" || extensionKey == "" || !extension.Active() {
		return ErrScope
	}
	return s.Change(ctx, scope, func(g *Group) error {
		if g.RunBudgetTurnID != turnID || g.RunBudget == nil {
			return ErrRunBudgetExhausted
		}
		if g.BudgetExtensions == nil {
			g.BudgetExtensions = map[string]types.RunBudget{}
		}
		if _, applied := g.BudgetExtensions[extensionKey]; applied {
			return nil
		}
		g.BudgetExtensions[extensionKey] = extension
		if extension.MaxTotalTokens > 0 {
			if g.RunBudget.Limit.MaxTotalTokens > 0 {
				g.RunBudget.Limit.MaxTotalTokens += extension.MaxTotalTokens
			} else {
				g.RunBudget.Limit.MaxTotalTokens = chargedBudgetTotal(g).TotalTokens + extension.MaxTotalTokens
			}
		}
		if extension.MaxWallTimeSec > 0 {
			if g.RunBudget.Limit.MaxWallTimeSec > 0 {
				g.RunBudget.Limit.MaxWallTimeSec += extension.MaxWallTimeSec
			} else {
				g.RunBudget.Limit.MaxWallTimeSec = int(time.Since(g.RunBudgetStartedAt)/time.Second) + extension.MaxWallTimeSec
			}
		}
		if extension.MaxWriteActions > 0 {
			if g.RunBudget.Limit.MaxWriteActions > 0 {
				g.RunBudget.Limit.MaxWriteActions += extension.MaxWriteActions
			} else {
				g.RunBudget.Limit.MaxWriteActions = chargedBudgetTotal(g).WriteActions + extension.MaxWriteActions
			}
		}
		return nil
	})
}

// StoreBudgetSink adapts this Store to an engine-facing shared account. The
// store is already scoped to one collaboration.
type StoreBudgetSink struct {
	Store *Store
	Scope Scope
}

// ExcludeWallTime keeps approval/budget waits outside the shared active-time
// allowance. ownerKey is accepted for the engine's optional-clock interface.
func (s StoreBudgetSink) ExcludeWallTime(
	ctx context.Context, turnID, ownerKey string, wait time.Duration,
) error {
	_ = ownerKey
	if s.Store == nil {
		return nil
	}
	return s.Store.ExcludeWallTime(ctx, s.Scope, turnID, wait)
}

func (s StoreBudgetSink) ReserveBudget(
	ctx context.Context, turnID, key string, reserved types.RunBudgetUsed,
) error {
	if s.Store == nil {
		return nil
	}
	return s.Store.ReserveBudget(ctx, s.Scope, turnID, key, reserved)
}

func (s StoreBudgetSink) SettleBudget(
	ctx context.Context, turnID, key string, actual types.RunBudgetUsed,
) error {
	if s.Store == nil {
		return nil
	}
	return s.Store.SettleBudget(ctx, s.Scope, turnID, key, actual)
}

// ExtendBudget adds only a continuation's extra allowance; the existing
// reservation ledger remains intact.
func (s StoreBudgetSink) ExtendBudget(
	ctx context.Context, turnID, extensionKey string, extension types.RunBudget,
) error {
	if s.Store == nil {
		return nil
	}
	return s.Store.ExtendBudget(ctx, s.Scope, turnID, extensionKey, extension)
}

// UsageBudget returns the account with active wall time included.
func (s StoreBudgetSink) UsageBudget(ctx context.Context, turnID string) (types.RunBudgetUsage, error) {
	if s.Store == nil {
		return types.RunBudgetUsage{}, nil
	}
	account, err := s.Store.RunBudgetAccount(ctx, s.Scope, turnID)
	if err != nil || account == nil {
		return types.RunBudgetUsage{}, err
	}
	return account.Usage, nil
}

// ReserveBudget reserves one idempotent operation. The key is typically
// "<child>/<round>/model" or "<child>/<round>/tool/<call-id>". Reservations are
// checked and written under the aggregate CAS, so parallel children cannot
// jointly overspend.
func (s *Store) ReserveBudget(ctx context.Context, scope Scope, turnID, key string, reserved types.RunBudgetUsed) error {
	if key == "" || turnID == "" {
		return ErrScope
	}
	return s.Change(ctx, scope, func(g *Group) error {
		if g.Status != "active" {
			return ErrClosed
		}
		if g.RunBudgetTurnID != turnID || g.RunBudget == nil || !g.RunBudget.Limit.Active() {
			return nil
		}
		if g.BudgetReservations == nil {
			g.BudgetReservations = map[string]types.RunBudgetUsed{}
		}
		if old, ok := g.BudgetReservations[key]; ok {
			reserved.TotalTokens = max(reserved.TotalTokens, old.TotalTokens)
			reserved.WallTimeSec = max(reserved.WallTimeSec, old.WallTimeSec)
			reserved.WriteActions = max(reserved.WriteActions, old.WriteActions)
		}
		previous := g.BudgetReservations[key]
		g.BudgetReservations[key] = reserved
		after := chargedBudgetTotal(g)
		if g.RunBudget.Limit.MaxTotalTokens > 0 && after.TotalTokens > g.RunBudget.Limit.MaxTotalTokens {
			g.BudgetReservations[key] = previous
			return ErrRunBudgetExhausted
		}
		if g.RunBudget.Limit.MaxWallTimeSec > 0 && int(time.Since(g.RunBudgetStartedAt)/time.Second)+after.WallTimeSec > g.RunBudget.Limit.MaxWallTimeSec {
			g.BudgetReservations[key] = previous
			return ErrRunBudgetExhausted
		}
		// A child at the exact write limit may still run a no-cost read batch;
		// the next mutating reservation is rejected here.
		if g.RunBudget.Limit.MaxWriteActions > 0 && after.WriteActions > g.RunBudget.Limit.MaxWriteActions {
			g.BudgetReservations[key] = previous
			return ErrRunBudgetExhausted
		}
		return nil
	})
}

// SettleBudget records actual usage for an already-reserved key. Repeating the
// same settlement is idempotent, and actual can replace the worst-case request
// reservation (downward as well as upward).
func (s *Store) SettleBudget(ctx context.Context, scope Scope, turnID, key string, actual types.RunBudgetUsed) error {
	if key == "" || turnID == "" {
		return ErrScope
	}
	return s.Change(ctx, scope, func(g *Group) error {
		if g.RunBudgetTurnID != turnID || g.RunBudget == nil {
			return nil
		}
		if g.BudgetActuals == nil {
			g.BudgetActuals = map[string]types.RunBudgetUsed{}
		}
		previous := g.BudgetActuals[key]
		g.BudgetActuals[key] = types.RunBudgetUsed{
			TotalTokens:  max(previous.TotalTokens, actual.TotalTokens),
			WallTimeSec:  max(previous.WallTimeSec, actual.WallTimeSec),
			WriteActions: max(previous.WriteActions, actual.WriteActions),
		}
		return nil
	})
}

// Ensure creates a scoped group if absent and verifies access to an existing group.
func (s *Store) Ensure(ctx context.Context, scope Scope) error {
	return s.ensure(ctx, scope, nil)
}

func (s *Store) ensure(ctx context.Context, scope Scope, members []*Member) error {
	if scope.ID == "" || scope.SessionID == "" || scope.UserID == "" || scope.TenantID == 0 {
		return ErrScope
	}
	g := Group{
		Scope: scope, Status: "active", Members: members,
		Requests: map[string]string{}, Deadline: time.Now().Add(collaborationHorizon),
	}
	b, err := json.Marshal(g)
	if err != nil {
		return err
	}
	r := Record{
		ID:        scope.ID,
		TenantID:  scope.TenantID,
		UserID:    scope.UserID,
		SessionID: scope.SessionID,
		Status:    g.Status,
		Version:   1,
		Data:      string(b),
	}
	if err = s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&r).Error; err != nil {
		return err
	}
	_, err = s.Read(ctx, scope)
	return err
}

func (s *Store) read(ctx context.Context, scope Scope) (Record, *Group, error) {
	var row Record
	err := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND user_id = ? AND session_id = ?",
			scope.ID,
			scope.TenantID,
			scope.UserID,
			scope.SessionID).
		First(&row).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, nil, ErrScope
	}
	if err != nil {
		return row, nil, err
	}
	var g Group
	if err = json.Unmarshal([]byte(row.Data), &g); err != nil {
		return row, nil, fmt.Errorf("decode collaboration: %w", err)
	}
	g.Revision = row.Version
	return row, &g, nil
}

func (s *Store) Read(ctx context.Context, scope Scope) (*Group, error) {
	_, g, e := s.read(ctx, scope)
	return g, e
}

// Change callbacks can be retried: no IO or non-idempotent external actions in fn.
//
// An active aggregate that would exceed maxAggregateBytes is closed instead of
// rejecting every later mutation, including the cancellation that would end it.
// The caller's mutation is discarded and ErrHistoryLimit is returned.
func (s *Store) Change(ctx context.Context, scope Scope, fn func(*Group) error) error {
	forceClose := false
	for attempt := 0; attempt < 60; attempt++ {
		row, g, err := s.read(ctx, scope)
		if err == nil {
			before := g.StatusView()
			parentMessageID := g.ParentMessageID
			if forceClose {
				closeForHistoryLimit(g)
			} else {
				if owner, ok := ctx.Value(parentOwnerKey{}).(string); ok &&
					(g.ParentOwner != owner || time.Now().After(g.ParentLease)) {
					return ErrConflict
				}
				if err = fn(g); err != nil {
					return err
				}
			}
			var b []byte
			b, err = json.Marshal(g)
			if err == nil && len(b) > maxAggregateBytes {
				if g.Status == "active" && !forceClose {
					forceClose = true
					continue
				}
				compactClosed(g)
				if b, err = json.Marshal(g); err == nil && len(b) > maxAggregateBytes {
					return ErrHistoryLimit
				}
			}
			if err == nil {
				if string(b) == row.Data && g.Status == row.Status {
					if forceClose {
						return ErrHistoryLimit
					}
					return nil
				}
				q := s.db.WithContext(ctx).
					Model(&Record{}).
					Where("id = ? AND version = ?", row.ID, row.Version).
					Updates(map[string]any{
						"data":       string(b),
						"status":     g.Status,
						"version":    row.Version + 1,
						"updated_at": time.Now().UTC(),
					})
				err = q.Error
				if err == nil && q.RowsAffected == 1 {
					g.Revision = row.Version + 1
					after := g.StatusView()
					after.Revision = before.Revision
					if s.OnStatus != nil &&
						(parentMessageID != g.ParentMessageID || !reflect.DeepEqual(before, after)) {
						s.OnStatus(context.WithoutCancel(ctx), g)
					}
					if forceClose {
						return ErrHistoryLimit
					}
					return nil
				}
			}
		}
		if err != nil && !strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "busy") {
			return err
		}
		t := time.NewTimer(time.Duration(min(attempt+1, 20)) * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return ErrConflict
}

// Pending lists groups that require scheduling, recovery or cancellation.
func (s *Store) Pending(ctx context.Context) ([]Scope, error) {
	var rows []Record
	if err := s.db.WithContext(ctx).Select("id, tenant_id, user_id, session_id").Where("status IN ?",
		[]string{
			"active",
			"cancelling",
		}).Order("updated_at").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Scope, 0, len(rows))
	for _, r := range rows {
		out = append(out, Scope{r.TenantID, r.UserID, r.SessionID, r.ID})
	}
	return out, nil
}

func current(m *Member) *Run {
	if len(m.Runs) == 0 {
		return nil
	}
	return m.Runs[len(m.Runs)-1]
}

func active(r *Run) bool {
	return r != nil && (r.State == "queued" || r.State == "running" || r.State == "waiting")
}

func member(g *Group, target string) (*Member, error) {
	for _, m := range g.Members {
		if target == m.ID || target == m.Name || target == "/root/"+m.Name {
			return m, nil
		}
	}
	return nil, errors.New("unknown child agent in this collaboration")
}

func publish(g *Group, m *Member, r *Run) {
	g.NextSequence++
	payload := struct {
		Result    Result `json:"result"`
		SessionID string `json:"session_id"`
	}{r.Result, m.SessionID}
	data, _ := json.Marshal(payload)
	g.Events = append(g.Events, Event{g.NextSequence, m.Name, r.ID, r.State, string(data)})
}

func reserveInput(g *Group, n int) error {
	if g.InputBytes+n > 512<<10 {
		return errors.New("collaboration input budget exhausted")
	}
	g.InputBytes += n
	return nil
}
