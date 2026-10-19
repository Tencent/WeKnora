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
