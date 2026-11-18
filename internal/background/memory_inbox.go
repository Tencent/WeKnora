package background

import (
	"context"
	"sort"
	"sync"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MemoryNoticeInbox is the single-process NoticeInbox used by Lite.
type MemoryNoticeInbox struct {
	clock   Clock
	mu      sync.Mutex
	notices map[interfaces.SessionRef]map[string]interfaces.BackgroundNotice
}

var _ interfaces.NoticeInbox = (*MemoryNoticeInbox)(nil)

// NewMemoryNoticeInbox returns an empty inbox. A nil clock means SystemClock.
func NewMemoryNoticeInbox(clock Clock) *MemoryNoticeInbox {
	if clock == nil {
		clock = SystemClock{}
	}
	return &MemoryNoticeInbox{
		clock:   clock,
		notices: make(map[interfaces.SessionRef]map[string]interfaces.BackgroundNotice),
	}
}

// Push implements interfaces.NoticeInbox.
func (b *MemoryNoticeInbox) Push(
	_ context.Context, s interfaces.SessionRef, n interfaces.BackgroundNotice,
) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.notices[s]
	if _, ok := m[n.ID]; ok {
		return false, nil
	}
	if m == nil {
		m = make(map[string]interfaces.BackgroundNotice)
		b.notices[s] = m
	}
	m[n.ID] = n
	return true, nil
}

// Pending implements interfaces.NoticeInbox.
func (b *MemoryNoticeInbox) Pending(
	_ context.Context, s interfaces.SessionRef,
) ([]interfaces.BackgroundNotice, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cutoff := b.clock.Now().Add(-interfaces.NoticeRetention)
	m := b.notices[s]
	out := make([]interfaces.BackgroundNotice, 0, len(m))
	for id, n := range m {
		if n.FinishedAt.Before(cutoff) {
			delete(m, id)
			continue
		}
		out = append(out, n)
	}
	if len(m) == 0 {
		delete(b.notices, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FinishedAt.Equal(out[j].FinishedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].FinishedAt.Before(out[j].FinishedAt)
	})
	return out, nil
}

// Ack implements interfaces.NoticeInbox.
func (b *MemoryNoticeInbox) Ack(_ context.Context, s interfaces.SessionRef, ids []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.notices[s]
	for _, id := range ids {
		delete(m, id)
	}
	if len(m) == 0 {
		delete(b.notices, s)
	}
	return nil
}

// Clear implements interfaces.NoticeInbox.
func (b *MemoryNoticeInbox) Clear(_ context.Context, s interfaces.SessionRef) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.notices, s)
	return nil
}
