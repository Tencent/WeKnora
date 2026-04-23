package langfuse

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
)

// Manager is the public façade of the langfuse package. A singleton is
// installed via Init(); callers should treat a nil *Manager as "disabled"
// and still invoke methods — every public method tolerates a nil receiver.
type Manager struct {
	cfg    Config
	client *client

	queue    chan ingestionEvent
	done     chan struct{}
	workerWG sync.WaitGroup
	closed   atomic.Bool

	// rng is used for sampling decisions. Guarded by rngMu because
	// math/rand.Source isn't goroutine-safe.
	rngMu sync.Mutex
	rng   *rand.Rand
}

var (
	globalMu sync.RWMutex
	global   *Manager
)

// Init builds a Manager from cfg and installs it as the package-wide
// singleton. When cfg.Enabled is false this returns a disabled manager that
// behaves as a no-op for every public method.
func Init(cfg Config) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	m := &Manager{cfg: cfg}
	if cfg.Enabled {
		m.client = newClient(cfg)
		m.queue = make(chan ingestionEvent, cfg.QueueSize)
		m.done = make(chan struct{})
		m.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
		m.workerWG.Add(1)
		go m.runWorker()
	}

	globalMu.Lock()
	global = m
	globalMu.Unlock()

	if cfg.Enabled {
		logger.Infof(context.Background(),
			"[Langfuse] enabled host=%s flush_at=%d flush_interval=%s sample_rate=%.2f",
			cfg.Host, cfg.FlushAt, cfg.FlushInterval, cfg.SampleRate,
		)
	}
	return m, nil
}

// GetManager returns the installed singleton, or nil if Init has not been
// called. Callers must tolerate a nil return.
func GetManager() *Manager {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

// Enabled reports whether the manager would actually emit events.
func (m *Manager) Enabled() bool {
	return m != nil && m.cfg.Enabled && !m.closed.Load()
}

// Shutdown drains pending events, signals the worker to stop and waits for it.
// Safe to call multiple times.
func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil || !m.cfg.Enabled {
		return nil
	}
	if !m.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(m.done)

	doneCh := make(chan struct{})
	go func() {
		m.workerWG.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// enqueue drops silently when either disabled, full or closed. Langfuse is
// observability, not business logic — back-pressure or failures must never
// block the request path.
func (m *Manager) enqueue(ev ingestionEvent) {
	if !m.Enabled() {
		return
	}
	select {
	case m.queue <- ev:
	default:
		if m.cfg.Debug {
			logger.Warnf(context.Background(), "[Langfuse] queue full, dropping event type=%s", ev.Type)
		}
	}
}

// sample decides whether to emit based on SampleRate. Sampling is applied once
// per trace; observations attached to an already-sampled trace are always kept
// (Langfuse itself would drop orphaned observations anyway).
func (m *Manager) sample() bool {
	if !m.Enabled() {
		return false
	}
	if m.cfg.SampleRate >= 1.0 {
		return true
	}
	m.rngMu.Lock()
	defer m.rngMu.Unlock()
	return m.rng.Float64() < m.cfg.SampleRate
}

// runWorker batches queued events and flushes them either when the batch
// reaches FlushAt, when FlushInterval elapses, or when the manager shuts down.
func (m *Manager) runWorker() {
	defer m.workerWG.Done()
	ticker := time.NewTicker(m.cfg.FlushInterval)
	defer ticker.Stop()

	buf := make([]ingestionEvent, 0, m.cfg.FlushAt)
	flush := func(reason string) {
		if len(buf) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), m.cfg.RequestTimeout)
		defer cancel()
		if err := m.client.ingest(ctx, buf); err != nil && m.cfg.Debug {
			logger.Warnf(ctx, "[Langfuse] flush (%s) failed: %v", reason, err)
		}
		buf = buf[:0]
	}

	for {
		select {
		case ev := <-m.queue:
			buf = append(buf, ev)
			if len(buf) >= m.cfg.FlushAt {
				flush("batch-full")
			}
		case <-ticker.C:
			flush("interval")
		case <-m.done:
			// Drain whatever remains in the queue before exiting.
			for {
				select {
				case ev := <-m.queue:
					buf = append(buf, ev)
					if len(buf) >= m.cfg.FlushAt {
						flush("batch-full-on-shutdown")
					}
				default:
					flush("shutdown")
					return
				}
			}
		}
	}
}

// newID returns a Langfuse-compatible UUIDv4.
func newID() string {
	return uuid.New().String()
}
