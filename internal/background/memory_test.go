package background

import (
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/background/backgroundtest"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestMemoryActivityRegistryContract(t *testing.T) {
	backgroundtest.RunActivityRegistryContract(t,
		func(*testing.T) (interfaces.ActivityRegistry, backgroundtest.Advance) {
			clock := newFakeClock()
			return NewMemoryActivityRegistry(clock), clock.Advance
		})
}

func TestMemoryNoticeInboxContract(t *testing.T) {
	backgroundtest.RunNoticeInboxContract(t,
		func(*testing.T) (interfaces.NoticeInbox, backgroundtest.Advance) {
			clock := newFakeClock()
			return NewMemoryNoticeInbox(clock), clock.Advance
		})
}
