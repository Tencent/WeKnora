// Package admission tracks operations in flight on a shared machine and on
// each session of it, so a rebuild, a reclaim, a rewind or a workspace delete
// can stop new work and wait for running work instead of cutting it off.
//
// Every user-mode operation holds a member in the machine scope and then in
// its session scope. A destructive path closes the scope it destroys, then
// requires ActiveExcept to report zero, and calls Barrier.Check before every
// destructive step. A lease that can no longer be confirmed reports Lost; the
// caller cancels the work it guards.
package admission

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

var (
	// ErrClosed reports that the scope is being rebuilt, reclaimed, or held
	// exclusively.
	ErrClosed = errors.New("sandbox: not admitting new operations")
	// ErrLeaseLost reports a lease or barrier whose ownership can no longer
	// be confirmed.
	ErrLeaseLost = errors.New("sandbox: operation lease lost")
)

// DefaultHoldTTL bounds a holder that died without releasing.
const DefaultHoldTTL = 2 * time.Minute

// Scope names one admission domain. Tag is the machine digest, so every
// scope of a machine shares one Redis hash tag.
type Scope struct {
	Tag  string
	Name string
}

// MachineScope covers every operation on a shared machine.
func MachineScope(digest string) Scope { return Scope{Tag: digest, Name: "machine"} }

// SessionScope covers one session's operations on its machine.
func SessionScope(digest, sessionID string) Scope {
	return Scope{Tag: digest, Name: "session:" + sessionID}
}

// ConfigCreateScope registers provider creates of one sandbox config, so a
// config deletion can stop new creates and wait for running ones before it
// takes inventory.
func ConfigCreateScope(configTenantID uint64, configID string) Scope {
	return Scope{Tag: "config:" + strconv.FormatUint(configTenantID, 10) + ":" + configID, Name: "create"}
}

// SessionMember names one operation of sessionID: kind is "turn", "shell",
// "file", "exec", "pty", "desktop", "fork" or "cleanup". token is unique per
// hold — including turns, which may nest — so releasing one hold never
// removes another.
func SessionMember(sessionID, kind, token string) string {
	return "s:" + sessionID + ":" + kind + ":" + token
}

// Signal is the loss side of a lease or a barrier.
type Signal interface {
	// Lost is closed once ownership can no longer be confirmed: the member
	// is gone, or the local deadline passed without a confirmation.
	Lost() <-chan struct{}
	// Check returns ErrLeaseLost once Lost is closed or the deadline passed.
	// Call it before every destructive step.
	Check() error
}

// Lease is one registered operation.
type Lease interface {
	Signal
	Member() string
	// Release removes the member. Idempotent.
	Release()
}

// Barrier is an exclusive close of a scope.
type Barrier interface {
	Signal
	// Release reopens the scope. Idempotent, and exclusive with Seal:
	// whichever runs first wins.
	Release()
	// Seal stops renewal and leaves the scope closed. It does not remove
	// the closed flag, so new creates of this scope stay refused. Idempotent,
	// and exclusive with Release.
	Seal()
}

// Bind returns a context cancelled with ErrLeaseLost as soon as any of
// signals is lost, and a function that ends the watch and cancels the
// context. Pass the context to every provider call the signals protect, so a
// lost lease also stops the call in flight.
func Bind(ctx context.Context, signals ...Signal) (context.Context, func()) {
	bound, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	for _, sig := range signals {
		if sig == nil {
			continue
		}
		if sig.Check() != nil {
			cancel(ErrLeaseLost)
			continue
		}
		lost := sig.Lost()
		go func() {
			select {
			case <-lost:
				cancel(ErrLeaseLost)
			case <-done:
			case <-bound.Done():
			}
		}()
	}
	var once sync.Once
	return bound, func() {
		once.Do(func() {
			close(done)
			cancel(nil)
		})
	}
}

// Registry is the admission contract. Implementations are safe for
// concurrent use.
type Registry interface {
	Hold(ctx context.Context, scope Scope, member string, ttl time.Duration) (Lease, error)
	ActiveExcept(ctx context.Context, scope Scope, except ...string) (int, error)
	Close(ctx context.Context, scope Scope, ttl time.Duration) (Barrier, error)
}

// signal is the shared Lost/deadline/Release bookkeeping of leases and
// barriers. A zero deadline (the memory registry) never expires on its own.
type signal struct {
	lost       chan struct{}
	lostOnce   sync.Once
	finishOnce sync.Once
	release    func()
	// seal stops renewal without reopening. Nil on a memory barrier: the
	// scope stays in the barrier map.
	seal func()

	mu       sync.Mutex
	deadline time.Time
	timer    *time.Timer
	// stopped is set by Release. extend and the timer callback both refuse
	// to arm another timer once it is set, so a confirmation that arrives
	// as Release runs cannot resurrect the deadline.
	stopped bool
}

func newSignal() *signal { return &signal{lost: make(chan struct{})} }

func (s *signal) markLost() {
	s.mu.Lock()
	s.closeLostLocked()
	s.mu.Unlock()
}

func (s *signal) closeLostLocked() {
	s.lostOnce.Do(func() { close(s.lost) })
}

func (s *signal) Lost() <-chan struct{} { return s.lost }

// extend moves the local deadline to sentAt+ttl-margin. sentAt is when the
// confirming request was sent: Redis may have applied it any time after, so
// counting from the reply would overstate how long the lease is still ours.
// A lost or released signal is never extended.
func (s *signal) extend(sentAt time.Time, ttl time.Duration) {
	d := sentAt.Add(ttl - renewMargin(ttl))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.lostClosed() {
		return
	}
	if !s.deadline.IsZero() && !time.Now().Before(s.deadline) {
		s.closeLostLocked()
		return
	}
	if !d.After(s.deadline) {
		return
	}
	s.deadline = d
	s.armLocked(time.Until(d))
}

// armLocked schedules markLost at the deadline. A callback that wakes early,
// or one already in flight when the deadline moves, waits again instead of
// closing Lost. Go's AfterFunc Reset can run an already-started callback
// alongside the rescheduled one; the callback re-reads the deadline under
// s.mu so a stale wake cannot drop a lease that was just confirmed.
func (s *signal) armLocked(delay time.Duration) {
	if s.timer != nil {
		s.timer.Stop()
	}
	if delay <= 0 {
		s.closeLostLocked()
		return
	}
	s.timer = time.AfterFunc(delay, s.onDeadline)
}

func (s *signal) onDeadline() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.lostClosed() || s.deadline.IsZero() {
		return
	}
	if delay := time.Until(s.deadline); delay > 0 {
		// This wake is from an earlier deadline. Drop it and any timer it
		// would race with, and wait out the deadline we hold now.
		if s.timer != nil {
			s.timer.Stop()
		}
		s.timer = time.AfterFunc(delay, s.onDeadline)
		return
	}
	s.closeLostLocked()
}

func (s *signal) lostClosed() bool {
	select {
	case <-s.lost:
		return true
	default:
		return false
	}
}

func (s *signal) currentDeadline() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadline
}

func (s *signal) Release() { s.finish(true) }

// Seal stops the local deadline timer the same way Release does, so it
// cannot mark the barrier lost, and runs the non-reopening callback.
func (b *barrier) Seal() { b.finish(false) }

func (s *signal) finish(reopen bool) {
	s.finishOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		if s.timer != nil {
			s.timer.Stop()
			s.timer = nil
		}
		s.mu.Unlock()
		if reopen {
			if s.release != nil {
				s.release()
			}
			return
		}
		if s.seal != nil {
			s.seal()
		}
	})
}

func (s *signal) Check() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lostClosed() {
		return ErrLeaseLost
	}
	// Release and Seal both stop the deadline. A stopped signal is not a
	// loss, even if the old deadline has since passed.
	if s.stopped {
		return nil
	}
	if d := s.deadline; !d.IsZero() && !time.Now().Before(d) {
		s.closeLostLocked()
		return ErrLeaseLost
	}
	return nil
}

type lease struct {
	*signal
	member string
}

func (l *lease) Member() string { return l.member }

type barrier struct{ *signal }

// renewMargin is how long before expiry an unconfirmed lease is declared
// lost: the holder must stop before another replica can see it as gone.
func renewMargin(ttl time.Duration) time.Duration { return ttl / 6 }
