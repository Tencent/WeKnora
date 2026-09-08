// Package usage hosts the process-wide Model Usage recorder consumed by the
// chat / embedding / rerank wrappers. It is the single place that turns a
// completed logical model invocation into a model_usage row, so business
// services never talk to the repository directly and never duplicate the
// context-derived attribution (tenant / evaluation run / purpose).
package usage

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
)

// persistenceTimeout bounds a single model_usage INSERT. Persistence is
// best-effort: it must not block forever once the (detached) context has no
// model-call deadline, so it gets its own short write budget, consistent with
// the project's other write-side timeouts (memory/embedWriteTimeout).
const persistenceTimeout = 10 * time.Second

// Repository is the minimal persistence contract the recorder needs. It is
// declared here (rather than importing types/interfaces) to avoid an import
// cycle: types/interfaces imports the model packages that in turn import this
// package. The concrete ModelUsageRepository satisfies it structurally.
type Repository interface {
	Create(ctx context.Context, usage *types.ModelUsage) error
}

// CostProcessor derives and persists cost for a completed usage row.
type CostProcessor interface {
	Process(ctx context.Context, usage *types.ModelUsage) error
}

// Recorder persists completed logical model invocations. A single process-wide
// instance is installed by the container and consumed by the wrappers.
type Recorder struct {
	repo          Repository
	costProcessor CostProcessor
}

// NewRecorder creates a process-wide model usage recorder.
func NewRecorder(repo Repository, costProcessors ...CostProcessor) *Recorder {
	r := &Recorder{repo: repo}
	if len(costProcessors) > 0 {
		r.costProcessor = costProcessors[0]
	}
	return r
}

// ResolveProvider returns the resolved provider name for a model config: the
// configured provider when set, otherwise the provider detected from the base
// URL. This is what model_usage.resolved_provider stores, never the raw (and
// possibly empty) config.Provider string.
func ResolveProvider(configProvider, baseURL string) string {
	if name := provider.ProviderName(configProvider); name != "" {
		return string(name)
	}
	return string(provider.DetectProvider(baseURL))
}

// IntPtr returns a pointer to v, used for the nullable token counters.
func IntPtr(v int) *int { return &v }

// StatusFromError maps a terminal error to the normalized usage status. Only
// context.Canceled and context.DeadlineExceeded are classified as cancelled /
// timeout; every other error (including provider HTTP 504s without a Go
// context deadline) is plain error.
func StatusFromError(err error) types.UsageStatus {
	switch {
	case err == nil:
		return types.UsageStatusSuccess
	case errors.Is(err, context.Canceled):
		return types.UsageStatusCancelled
	case errors.Is(err, context.DeadlineExceeded):
		return types.UsageStatusTimeout
	default:
		return types.UsageStatusError
	}
}

var defaultRecorder atomic.Pointer[Recorder]

// SetRecorder installs the process-wide recorder. A nil recorder disables
// recording (used by tests and by processes that never wire the container).
func SetRecorder(r *Recorder) { defaultRecorder.Store(r) }

// Record fills tenant_id, evaluation_run_id and purpose from ctx and persists
// u. It never returns an error: model usage observability must not break a
// model call. A persistence failure is logged with a message that contains no
// prompt, query, response body, key or raw provider response.
func Record(ctx context.Context, u *types.ModelUsage) {
	r := defaultRecorder.Load()
	if r == nil || r.repo == nil {
		return
	}
	r.record(ctx, u)
}

func (r *Recorder) record(ctx context.Context, u *types.ModelUsage) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		// No tenant to attribute the row to; nothing safe to persist.
		return
	}
	u.TenantID = tenantID

	if evalRunID, ok := types.EvaluationRunIDFromContext(ctx); ok {
		id := evalRunID
		u.EvaluationRunID = &id
	}

	// A purpose carried on the context (e.g. "knowledge_qa", "agent_round")
	// wins over any wrapper-derived default, which the wrapper has already set
	// on u.Purpose before calling Record.
	if purpose, _ := types.LLMCallMetadataFromContext(ctx); purpose != "" {
		u.Purpose = purpose
	}

	// Attribution has now been captured from the original model context. The
	// persistence itself must NOT inherit the model call's cancellation or
	// deadline — a terminal cancelled/timeout row would otherwise fail its
	// INSERT with "context canceled". Detach, then bound with an independent
	// short write timeout so a detached insert can never block forever.
	persistCtx := context.WithoutCancel(ctx)
	persistCtx, cancel := context.WithTimeout(persistCtx, persistenceTimeout)
	defer cancel()

	if err := r.repo.Create(persistCtx, u); err != nil {
		logger.Warnf(ctx, "[ModelUsage] failed to persist model usage: %v", err)
		return
	}
	// Cost is a best-effort derived fact and is attempted only after the usage
	// fact exists. A pricing lookup/calculation/write failure cannot roll back
	// or otherwise affect the already-persisted model_usage row.
	if r.costProcessor != nil {
		if err := r.costProcessor.Process(persistCtx, u); err != nil {
			logger.Warnf(ctx, "[ModelUsageCost] failed to derive or persist cost for usage %s: %v", u.ID, err)
		}
	}
}
