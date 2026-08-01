// Package modelusage provides process-wide recording of model invocation
// events (tokens, latency, outcome) for the model usage dashboard.
//
// Model wrappers call Record, which is a no-op until the container installs
// an AsyncRecorder via SetRecorder. Events carry only metadata — never
// prompts, documents, image/audio bytes, or model outputs.
package modelusage

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Recorder accepts a single model usage event.
type Recorder interface {
	Record(ctx context.Context, event types.ModelUsageEvent)
}

type noopRecorder struct{}

func (noopRecorder) Record(context.Context, types.ModelUsageEvent) {}

var (
	currentMu sync.RWMutex
	current   Recorder = noopRecorder{}
)

// SetRecorder installs the process-wide recorder; nil restores the no-op.
func SetRecorder(rec Recorder) {
	currentMu.Lock()
	defer currentMu.Unlock()
	if rec == nil {
		current = noopRecorder{}
		return
	}
	current = rec
}

// Record forwards an event to the installed recorder.
func Record(ctx context.Context, event types.ModelUsageEvent) {
	currentMu.RLock()
	rec := current
	currentMu.RUnlock()
	rec.Record(ctx, event)
}

// EventStore is the persistence sink used by AsyncRecorder.
type EventStore interface {
	Create(ctx context.Context, event *types.ModelUsageEvent) error
}

// AsyncRecorder buffers events on a bounded channel and persists them from a
// single background goroutine, so model calls never block on the database.
// When the queue is full the event is dropped (metering must never break the
// call path).
type AsyncRecorder struct {
	repo EventStore
	ch   chan types.ModelUsageEvent
	done chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

// NewAsyncRecorder creates and starts an AsyncRecorder.
func NewAsyncRecorder(repo EventStore) *AsyncRecorder {
	rec := &AsyncRecorder{
		repo: repo,
		ch:   make(chan types.ModelUsageEvent, 2048),
		done: make(chan struct{}),
	}
	rec.wg.Add(1)
	go rec.run()
	return rec
}

// Record enqueues the event after stamping tenant/user/request identity from
// ctx. Events without a tenant (e.g. background jobs without session context)
// are skipped.
func (r *AsyncRecorder) Record(ctx context.Context, event types.ModelUsageEvent) {
	if r == nil || r.repo == nil {
		return
	}
	tenantID, ok := types.SessionTenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return
	}
	event.TenantID = tenantID
	if userID, ok := types.UserIDFromContext(ctx); ok {
		event.UserID = userID
	}
	if requestID, ok := types.RequestIDFromContext(ctx); ok {
		event.RequestID = requestID
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.TotalTokens == 0 {
		event.TotalTokens = event.PromptTokens + event.CompletionTokens
	}
	if event.UsageSource == "" {
		event.UsageSource = types.ModelUsageSourceMissing
	}

	select {
	case r.ch <- event:
	default:
		logger.Warnf(ctx, "model usage recorder queue full; dropping event model_id=%s kind=%s",
			event.ModelID, event.RequestKind)
	}
}

// Shutdown drains the queue and stops the worker, honoring ctx deadline.
func (r *AsyncRecorder) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		close(r.ch)
		go func() {
			r.wg.Wait()
			close(r.done)
		}()
	})
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *AsyncRecorder) run() {
	defer r.wg.Done()
	for event := range r.ch {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := r.repo.Create(ctx, &event); err != nil {
			logger.Warnf(ctx, "failed to persist model usage event: %v", err)
		}
		cancel()
	}
}

// ErrorType returns a short, stable label for err (concrete type name without
// pointer marker), capped to fit the error_type column.
func ErrorType(err error) string {
	if err == nil {
		return ""
	}
	name := reflect.TypeOf(err).String()
	name = strings.TrimPrefix(name, "*")
	if name == "" {
		name = "error"
	}
	if len(name) > 128 {
		return name[:128]
	}
	return name
}
