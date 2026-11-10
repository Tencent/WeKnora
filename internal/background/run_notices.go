package background

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Delivery is background-work context for the model at one round boundary.
type Delivery struct {
	// Text is the model-facing body, one line per entry, without a wrapper.
	Text string
	// AckIDs are the completion notices Text delivers. They are acknowledged
	// once the assistant message carrying them is persisted.
	AckIDs []string
}

// RunNotices decides, for one agent run, what the model is told about
// background work and when. A run executes in one process, so its sets live in
// memory; the shared registry and inbox are only read here.
type RunNotices struct {
	session  interfaces.SessionRef
	registry interfaces.ActivityRegistry
	inbox    interfaces.NoticeInbox

	mu       sync.Mutex
	listed   bool
	seen     map[string]bool
	started  map[string]bool
	reminded map[string]bool
	newSeen  []string
}

// NewRunNotices returns a tracker for one run of session s.
func NewRunNotices(
	s interfaces.SessionRef, registry interfaces.ActivityRegistry, inbox interfaces.NoticeInbox,
) *RunNotices {
	return &RunNotices{
		session:  s,
		registry: registry,
		inbox:    inbox,
		seen:     make(map[string]bool),
		started:  make(map[string]bool),
		reminded: make(map[string]bool),
	}
}

// MarkStarted records work this run submitted and got back as still running.
// Only such work is reminded about before the final answer.
func (r *RunNotices) MarkStarted(id string) {
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started[id] = true
}

// MarkSeen records finished IDs a tool already showed the model, so their
// notices are not injected again and are acknowledged with the turn.
func (r *RunNotices) MarkSeen(ids ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		if id == "" || r.seen[id] {
			continue
		}
		r.seen[id] = true
		r.newSeen = append(r.newSeen, id)
	}
}

// TakeSeen returns the IDs MarkSeen recorded since the previous call.
func (r *RunNotices) TakeSeen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.newSeen
	r.newSeen = nil
	return out
}

// RoundStart returns what to tell the model before a round's LLM call: on the
// run's first boundary, the work still running; on every boundary, completion
// notices this run has not seen.
func (r *RunNotices) RoundStart(ctx context.Context) (Delivery, error) {
	r.mu.Lock()
	first := !r.listed
	r.mu.Unlock()

	var lines []string
	if first {
		live, err := r.registry.List(ctx, r.session)
		if err != nil {
			return Delivery{}, fmt.Errorf("list background work: %w", err)
		}
		if len(live) > 0 {
			lines = append(lines, "Background work still running in this session: "+describeActivities(live)+".")
		}
	}
	notices, err := r.inbox.Pending(ctx, r.session)
	if err != nil {
		return Delivery{}, fmt.Errorf("read background notices: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.listed = true
	return r.deliverLocked(notices, lines), nil
}

// BeforeFinal returns what to tell the model before it may finish: unseen
// completion notices, and one reminder for each item this run started that is
// still running and has not been reminded about.
func (r *RunNotices) BeforeFinal(ctx context.Context) (Delivery, error) {
	notices, err := r.inbox.Pending(ctx, r.session)
	if err != nil {
		return Delivery{}, fmt.Errorf("read background notices: %w", err)
	}
	live, err := r.registry.List(ctx, r.session)
	if err != nil {
		return Delivery{}, fmt.Errorf("list background work: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	var remind []interfaces.BackgroundActivity
	for _, item := range live {
		if r.started[item.ID] && !r.reminded[item.ID] {
			remind = append(remind, item)
		}
	}
	var lines []string
	if len(remind) > 0 {
		for _, item := range remind {
			r.reminded[item.ID] = true
		}
		lines = append(lines, "Background work you started in this turn is still running: "+
			describeActivities(remind)+
			". Finish your answer. Its result will arrive when it completes. "+
			"Call work_wait only if you cannot answer without that output.")
	}
	return r.deliverLocked(notices, lines), nil
}

// deliverLocked puts unseen notices first, then lines, and marks the notices
// seen. Caller holds mu.
func (r *RunNotices) deliverLocked(notices []interfaces.BackgroundNotice, lines []string) Delivery {
	var d Delivery
	text := make([]string, 0, len(notices)+len(lines))
	for _, n := range notices {
		if r.seen[n.ID] || strings.TrimSpace(n.Text) == "" {
			continue
		}
		r.seen[n.ID] = true
		text = append(text, n.Text)
		d.AckIDs = append(d.AckIDs, n.ID)
	}
	text = append(text, lines...)
	d.Text = strings.Join(text, "\n")
	return d
}

// describeActivities renders items as "title (id)" in start order.
func describeActivities(items []interfaces.BackgroundActivity) string {
	sorted := append([]interfaces.BackgroundActivity(nil), items...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].StartedAt.Equal(sorted[j].StartedAt) {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].StartedAt.Before(sorted[j].StartedAt)
	})
	parts := make([]string, 0, len(sorted))
	for _, item := range sorted {
		if item.Title == "" {
			parts = append(parts, item.ID)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", item.Title, item.ID))
	}
	return strings.Join(parts, ", ")
}
