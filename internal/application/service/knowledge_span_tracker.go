// Package service: span tracker.
//
// SpanTracker is the pipeline-facing facade for recording per-attempt
// progress trees. It mirrors Langfuse's vocabulary (root / span /
// generation) so the UI's mental model matches what operators already use
// for LLM call observability.
//
// Lifecycle:
//
//	attempt := tracker.OpenAttempt(ctx, knowledgeID, langfuseTraceID)
//	  // creates the root span; every subsequent Begin* call uses this attempt
//
//	stage := tracker.BeginStage(ctx, knowledgeID, attempt, types.StageDocReader, input)
//	  // ...do work...
//	tracker.EndSpan(ctx, stage, output)         // success
//	tracker.FailSpan(ctx, stage, code, msg, err) // error
//	tracker.SkipSpan(ctx, stage, reason)        // intentionally not run
//
//	sub := tracker.BeginSubSpan(ctx, parentSpan, "multimodal.image[0]", types.SpanKindGeneration, input)
//	  // ...
//
// All operations are best-effort: a DB error is logged and swallowed so a
// tracker hiccup never breaks the parsing pipeline. Knowledge.parse_status
// remains the authoritative source of truth for completion.
package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Span is the in-memory handle the pipeline holds while a stage / subspan
// is executing. It carries enough context for End/Fail/Skip to write back
// without re-querying the DB. Returned (and required) from every Begin*.
type Span struct {
	KnowledgeID  string
	Attempt      int
	SpanID       string
	ParentSpanID string
	Name         string
	Kind         string
	StartedAt    time.Time
}

// SpanTracker is the only public surface — kept as an interface so tests
// can swap in a no-op without spinning up a database.
type SpanTracker interface {
	// OpenAttempt creates a new root span for (knowledgeID,
	// nextAttempt) and returns its number plus the root *Span. Call
	// at the start of a parse / reparse, before any other Begin*.
	OpenAttempt(ctx context.Context, knowledgeID, langfuseTraceID string) (root *Span, attempt int, err error)

	// LatestAttempt returns the highest attempt number recorded for
	// the knowledge, or 0 if it's never been parsed. Used by the API
	// layer to default to "show me the most recent run".
	LatestAttempt(ctx context.Context, knowledgeID string) int

	// BeginStage starts one of the canonical stages. Looks up the
	// root span for (kid, attempt) — caller passes attempt to make
	// the wiring explicit and let cross-process workers join an
	// existing attempt without new repo lookups.
	BeginStage(ctx context.Context, knowledgeID string, attempt int, stage string, input types.JSONMap) *Span

	// BeginSubSpan creates a child span under parent. parent may be a
	// stage span (for multimodal.image[i] / embedding.batch[i]) or
	// another subspan. kind is "subspan" or "generation" — generations
	// will be stitched to a Langfuse generation by trace_id.
	BeginSubSpan(ctx context.Context, parent *Span, name, kind string, input types.JSONMap) *Span

	// EndSpan marks span as done with optional output. Safe with nil.
	EndSpan(ctx context.Context, span *Span, output types.JSONMap)

	// FailSpan marks span as failed and cascade-cancels its
	// descendants. errorDetail (a Go error) is recorded verbatim in
	// error_detail (truncated to 8 KB) for admin views.
	FailSpan(ctx context.Context, span *Span, errorCode, errorMessage string, errorDetail error)

	// SkipSpan marks an intentionally not-run span (e.g. multimodal
	// on a text-only document). Distinct from cancelled — skipped is
	// "we chose not to" while cancelled is "an upstream broke".
	SkipSpan(ctx context.Context, span *Span, reason string)

	// LookupStage returns the stage's *Span for an in-flight attempt
	// — the cross-process bridge that lets an asynq worker (e.g.
	// image_multimodal) attach subspans to the parent stage span
	// created by the upstream pipeline.
	LookupStage(ctx context.Context, knowledgeID string, attempt int, stage string) *Span

	// FinalizeAttempt closes the root span for (knowledgeID, attempt)
	// with the given terminal status (done | failed). Idempotent:
	// re-closing an already-terminal root is a no-op so callers from
	// multiple paths (success orchestrator, dead-letter handler,
	// housekeeping) can fire without coordination. status defaults to
	// done; output/error are written verbatim.
	FinalizeAttempt(ctx context.Context, knowledgeID string, attempt int, status string,
		output types.JSONMap, errorCode, errorMessage string)

	// AbortAttempt cascade-cancels every still-running descendant of the
	// attempt's root span and then closes the root as cancelled. Used by
	// the user-initiated cancel path so the trace viewer doesn't leave
	// stranded subspans (multimodal images, postprocess subtasks)
	// looking like they're still in flight forever after the user
	// stopped the parse. Idempotent.
	AbortAttempt(ctx context.Context, knowledgeID string, attempt int, errorCode, errorMessage, reason string)
}

type spanTracker struct {
	repo repository.KnowledgeSpanRepository
	// db is held purely for the heartbeat side-channel: every span
	// state transition pokes knowledge.updated_at so the housekeeping
	// sweep can tell "actively running long stage" from "abandoned".
	// nil-safe — when missing (test harness) the heartbeat is skipped.
	db *gorm.DB

	// startsMu guards the in-process duration cache. Cross-process
	// workers won't find their parent's start here — that's fine,
	// duration_ms falls back to (FinishedAt - row.StartedAt) computed
	// at write time when the cache misses.
	startsMu sync.Mutex
	starts   map[string]time.Time // span_id → started_at
}

// NewSpanTracker constructs the GORM-backed tracker. A nil repo collapses
// to a no-op so test harnesses don't need to spin up a database. The db
// is optional too: it's used only for the housekeeping heartbeat (see
// touchKnowledgeHeartbeat) and a nil db just disables that side-channel.
func NewSpanTracker(repo repository.KnowledgeSpanRepository, db *gorm.DB) SpanTracker {
	if repo == nil {
		return noopSpanTracker{}
	}
	return &spanTracker{
		repo:   repo,
		db:     db,
		starts: make(map[string]time.Time),
	}
}

// touchKnowledgeHeartbeat advances knowledge.updated_at to the current
// wall-clock so the housekeeping sweep treats this row as actively
// progressing. Called on every span Begin/End/Fail/Skip — the cost is
// one indexed UPDATE per transition (≤ a few dozen per knowledge), which
// is dwarfed by the work the stages themselves do.
//
// touchKnowledgeHeartbeat advances knowledge.updated_at to the current
// wall-clock so the housekeeping sweep treats this row as actively
// progressing. Called on root / stage span transitions only — subspan and
// generation transitions skip this side-channel because:
//
//   - The spans table itself is updated on every transition, and the
//     housekeeping sweep already reads MAX(spans.updated_at) per
//     knowledge, so subspan progress is observable without poking the
//     parent row.
//   - A multimodal stage with N images would produce 2*N+ extra UPDATEs
//     on the same hot row (Begin+End per image plus retries), which we
//     observed contributing to row-level contention under bursty
//     uploads.
//
// Best-effort. We deliberately do NOT bump status here: the parse_status
// column remains under the pipeline's control. Only the timestamp gets
// nudged, which is exactly what housekeeping reads as the fallback.
func (t *spanTracker) touchKnowledgeHeartbeat(ctx context.Context, knowledgeID, kind string) {
	if t.db == nil || knowledgeID == "" {
		return
	}
	// Subspan / generation transitions are observable through the spans
	// table directly; skip the parent-row UPDATE to avoid write
	// amplification on fan-out workloads.
	if kind != types.SpanKindRoot && kind != types.SpanKindStage {
		return
	}
	if err := t.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("id = ?", knowledgeID).
		Update("updated_at", time.Now()).Error; err != nil {
		// Don't log every failure — heartbeat is best-effort and
		// noisy logs would drown out real errors. Single line at
		// warn level is enough for ops to spot a chronic outage.
		logger.Warnf(ctx, "[SpanTracker] heartbeat update failed kid=%s: %v", knowledgeID, err)
	}
}

func newSpanID() string {
	// Stripping the dashes saves 4 bytes per row — JSON parsers don't
	// care, and operators paste these into queries / Langfuse where a
	// hex-only ID is friendlier.
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func (t *spanTracker) recordStart(spanID string, at time.Time) {
	t.startsMu.Lock()
	t.starts[spanID] = at
	t.startsMu.Unlock()
}

func (t *spanTracker) takeStart(spanID string) (time.Time, bool) {
	t.startsMu.Lock()
	defer t.startsMu.Unlock()
	v, ok := t.starts[spanID]
	if ok {
		delete(t.starts, spanID)
	}
	return v, ok
}

func (t *spanTracker) OpenAttempt(ctx context.Context, knowledgeID, langfuseTraceID string) (*Span, int, error) {
	attempt, err := t.repo.NextAttempt(ctx, knowledgeID)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now()
	rootID := newSpanID()
	meta := types.JSONMap{}
	if langfuseTraceID != "" {
		// The frontend renders a "open in Langfuse" link from this.
		meta["langfuse_trace_id"] = langfuseTraceID
	}
	row := &types.KnowledgeProcessingSpan{
		KnowledgeID: knowledgeID,
		Attempt:     attempt,
		SpanID:      rootID,
		Name:        "knowledge_processing",
		Kind:        types.SpanKindRoot,
		Status:      types.SpanStatusRunning,
		Metadata:    meta,
		StartedAt:   &now,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[SpanTracker] OpenAttempt failed kid=%s: %v", knowledgeID, err)
		return nil, attempt, err
	}
	t.recordStart(rootID, now)
	t.touchKnowledgeHeartbeat(ctx, knowledgeID, types.SpanKindRoot)
	return &Span{
		KnowledgeID: knowledgeID,
		Attempt:     attempt,
		SpanID:      rootID,
		Name:        "knowledge_processing",
		Kind:        types.SpanKindRoot,
		StartedAt:   now,
	}, attempt, nil
}

func (t *spanTracker) LatestAttempt(ctx context.Context, knowledgeID string) int {
	n, err := t.repo.LatestAttempt(ctx, knowledgeID)
	if err != nil {
		logger.Warnf(ctx, "[SpanTracker] LatestAttempt failed kid=%s: %v", knowledgeID, err)
		return 0
	}
	return n
}

func (t *spanTracker) BeginStage(ctx context.Context, knowledgeID string, attempt int, stage string, input types.JSONMap) *Span {
	if knowledgeID == "" || stage == "" {
		return nil
	}
	// Find root span — we need its span_id as parent for stage rows. We
	// also reuse this scan to detect an existing row for the same stage
	// name in this attempt: re-entry (asynq retry, double-call from
	// adjacent code paths) MUST NOT create a second row, otherwise the
	// timeline shows two segments for the same stage and LookupStage
	// becomes ambiguous.
	rows, err := t.repo.ListByAttempt(ctx, knowledgeID, attempt)
	if err != nil {
		logger.Warnf(ctx, "[SpanTracker] BeginStage list failed kid=%s attempt=%d: %v",
			knowledgeID, attempt, err)
		return nil
	}
	var (
		rootID    string
		existing  *types.KnowledgeProcessingSpan
	)
	for i := range rows {
		r := rows[i]
		if r.Kind == types.SpanKindRoot && rootID == "" {
			rootID = r.SpanID
		}
		if r.Kind == types.SpanKindStage && r.Name == stage {
			cp := r
			existing = &cp
		}
	}
	if rootID == "" {
		// Pipeline started before tracker was wired (legacy data,
		// or the OpenAttempt repo write failed). Synthesize a
		// rootless stage so we still record SOMETHING.
		logger.Warnf(ctx, "[SpanTracker] BeginStage: no root for kid=%s attempt=%d, recording rootless",
			knowledgeID, attempt)
	}
	now := time.Now()
	// Re-entry path: keep the original span_id so any subspan that
	// already references it stays attached. Reset state to running and
	// refresh started_at; clear terminal-only fields so the row reads
	// cleanly as "running again". Output/error fields go through
	// Upsert's DoUpdates list, which only writes the columns we set —
	// any nil JSONMap / empty string explicitly clears the column.
	if existing != nil {
		row := &types.KnowledgeProcessingSpan{
			KnowledgeID:  existing.KnowledgeID,
			Attempt:      existing.Attempt,
			SpanID:       existing.SpanID,
			ParentSpanID: existing.ParentSpanID,
			Name:         existing.Name,
			Kind:         existing.Kind,
			Status:       types.SpanStatusRunning,
			Input:        input,
			Output:       nil,
			StartedAt:    &now,
			FinishedAt:   nil,
			DurationMs:   0,
		}
		if err := t.repo.Upsert(ctx, row); err != nil {
			logger.Warnf(ctx, "[SpanTracker] BeginStage re-enter failed kid=%s stage=%s: %v",
				knowledgeID, stage, err)
			return nil
		}
		t.recordStart(existing.SpanID, now)
		t.touchKnowledgeHeartbeat(ctx, knowledgeID, types.SpanKindStage)
		return &Span{
			KnowledgeID:  existing.KnowledgeID,
			Attempt:      existing.Attempt,
			SpanID:       existing.SpanID,
			ParentSpanID: existing.ParentSpanID,
			Name:         existing.Name,
			Kind:         existing.Kind,
			StartedAt:    now,
		}
	}
	id := newSpanID()
	row := &types.KnowledgeProcessingSpan{
		KnowledgeID:  knowledgeID,
		Attempt:      attempt,
		SpanID:       id,
		ParentSpanID: rootID,
		Name:         stage,
		Kind:         types.SpanKindStage,
		Status:       types.SpanStatusRunning,
		Input:        input,
		StartedAt:    &now,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[SpanTracker] BeginStage failed kid=%s stage=%s: %v",
			knowledgeID, stage, err)
		return nil
	}
	t.recordStart(id, now)
	t.touchKnowledgeHeartbeat(ctx, knowledgeID, types.SpanKindStage)
	return &Span{
		KnowledgeID:  knowledgeID,
		Attempt:      attempt,
		SpanID:       id,
		ParentSpanID: rootID,
		Name:         stage,
		Kind:         types.SpanKindStage,
		StartedAt:    now,
	}
}

func (t *spanTracker) BeginSubSpan(ctx context.Context, parent *Span, name, kind string, input types.JSONMap) *Span {
	if parent == nil || name == "" {
		return nil
	}
	if kind != types.SpanKindGeneration && kind != types.SpanKindSubSpan {
		kind = types.SpanKindSubSpan
	}
	now := time.Now()
	id := newSpanID()
	row := &types.KnowledgeProcessingSpan{
		KnowledgeID:  parent.KnowledgeID,
		Attempt:      parent.Attempt,
		SpanID:       id,
		ParentSpanID: parent.SpanID,
		Name:         name,
		Kind:         kind,
		Status:       types.SpanStatusRunning,
		Input:        input,
		StartedAt:    &now,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[SpanTracker] BeginSubSpan failed parent=%s name=%s: %v",
			parent.SpanID, name, err)
		return nil
	}
	t.recordStart(id, now)
	t.touchKnowledgeHeartbeat(ctx, parent.KnowledgeID, kind)
	return &Span{
		KnowledgeID:  parent.KnowledgeID,
		Attempt:      parent.Attempt,
		SpanID:       id,
		ParentSpanID: parent.SpanID,
		Name:         name,
		Kind:         kind,
		StartedAt:    now,
	}
}

func (t *spanTracker) EndSpan(ctx context.Context, span *Span, output types.JSONMap) {
	if span == nil {
		return
	}
	now := time.Now()
	dur := durationSince(t, span, now)
	row := &types.KnowledgeProcessingSpan{
		KnowledgeID:  span.KnowledgeID,
		Attempt:      span.Attempt,
		SpanID:       span.SpanID,
		ParentSpanID: span.ParentSpanID,
		Name:         span.Name,
		Kind:         span.Kind,
		Status:       types.SpanStatusDone,
		Output:       output,
		StartedAt:    &span.StartedAt,
		FinishedAt:   &now,
		DurationMs:   dur,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[SpanTracker] EndSpan failed span=%s: %v", span.SpanID, err)
	}
	t.touchKnowledgeHeartbeat(ctx, span.KnowledgeID, span.Kind)
}

func (t *spanTracker) FailSpan(ctx context.Context, span *Span, errorCode, errorMessage string, errorDetail error) {
	if span == nil {
		return
	}
	now := time.Now()
	dur := durationSince(t, span, now)
	detail := ""
	if errorDetail != nil {
		detail = errorDetail.Error()
		if len(detail) > 8192 {
			detail = detail[:8192]
		}
	}
	if len(errorMessage) > 1024 {
		errorMessage = errorMessage[:1024]
	}
	row := &types.KnowledgeProcessingSpan{
		KnowledgeID:  span.KnowledgeID,
		Attempt:      span.Attempt,
		SpanID:       span.SpanID,
		ParentSpanID: span.ParentSpanID,
		Name:         span.Name,
		Kind:         span.Kind,
		Status:       types.SpanStatusFailed,
		ErrorCode:    strings.TrimSpace(errorCode),
		ErrorMessage: errorMessage,
		ErrorDetail:  detail,
		StartedAt:    &span.StartedAt,
		FinishedAt:   &now,
		DurationMs:   dur,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[SpanTracker] FailSpan failed span=%s: %v", span.SpanID, err)
	}
	// Cascade: anything downstream of this span gets cancelled. The
	// reason string is what the UI surfaces under each cancelled
	// child's tooltip — keep it short and human.
	reason := "upstream " + span.Name + " failed"
	if errorCode != "" {
		reason = reason + " (" + errorCode + ")"
	}
	if _, err := t.repo.CancelDescendants(ctx, span.KnowledgeID, span.Attempt, span.SpanID, reason); err != nil {
		logger.Warnf(ctx, "[SpanTracker] cancel descendants failed span=%s: %v", span.SpanID, err)
	}
	// For STAGE failures, also cascade to dependent stages declared
	// in StageDependencies (those are siblings, not descendants).
	if span.Kind == types.SpanKindStage {
		t.cascadeDependentStages(ctx, span, reason)
		// Any failure in a MAIN pipeline stage means the attempt is
		// done — the parse cannot succeed past this point. Close the
		// root span as failed so the UI doesn't show "进行中" forever.
		// Optional downstream stages (summary/question/wiki/graph) do
		// not poison the attempt: they can fail without invalidating
		// the parsed document.
		if isMainPipelineStage(span.Name) {
			t.FinalizeAttempt(ctx, span.KnowledgeID, span.Attempt,
				types.SpanStatusFailed, nil, errorCode, errorMessage)
		}
	}
	t.touchKnowledgeHeartbeat(ctx, span.KnowledgeID, span.Kind)
}

func (t *spanTracker) SkipSpan(ctx context.Context, span *Span, reason string) {
	if span == nil {
		return
	}
	now := time.Now()
	row := &types.KnowledgeProcessingSpan{
		KnowledgeID:  span.KnowledgeID,
		Attempt:      span.Attempt,
		SpanID:       span.SpanID,
		ParentSpanID: span.ParentSpanID,
		Name:         span.Name,
		Kind:         span.Kind,
		Status:       types.SpanStatusSkipped,
		ErrorMessage: reason,
		StartedAt:    &span.StartedAt,
		FinishedAt:   &now,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[SpanTracker] SkipSpan failed span=%s: %v", span.SpanID, err)
	}
	t.touchKnowledgeHeartbeat(ctx, span.KnowledgeID, span.Kind)
}

func (t *spanTracker) LookupStage(ctx context.Context, knowledgeID string, attempt int, stage string) *Span {
	rows, err := t.repo.ListByAttempt(ctx, knowledgeID, attempt)
	if err != nil {
		logger.Warnf(ctx, "[SpanTracker] LookupStage list failed kid=%s attempt=%d: %v",
			knowledgeID, attempt, err)
		return nil
	}
	for i := range rows {
		r := rows[i]
		if r.Kind != types.SpanKindStage || r.Name != stage {
			continue
		}
		started := time.Time{}
		if r.StartedAt != nil {
			started = *r.StartedAt
		}
		return &Span{
			KnowledgeID:  r.KnowledgeID,
			Attempt:      r.Attempt,
			SpanID:       r.SpanID,
			ParentSpanID: r.ParentSpanID,
			Name:         r.Name,
			Kind:         r.Kind,
			StartedAt:    started,
		}
	}
	return nil
}

// cascadeDependentStages flips downstream STAGE rows to "cancelled" using
// types.StageDependencies. Without this, a Chunking failure leaves
// Embedding / Multimodal as "pending" forever, even though they cannot
// possibly run. After flipping a dependent stage we ALSO cascade-cancel
// any subspan/generation that already attached to it (e.g. an embedding
// batch that started before the chunking failure was observed) — without
// this second walk those subspans would remain in pending/running and
// surface as orphan spinners under a cancelled parent.
func (t *spanTracker) cascadeDependentStages(ctx context.Context, failedStage *Span, reason string) {
	rows, err := t.repo.ListByAttempt(ctx, failedStage.KnowledgeID, failedStage.Attempt)
	if err != nil {
		return
	}
	dependents := stagesDependingOn(failedStage.Name)
	if len(dependents) == 0 {
		return
	}
	now := time.Now()
	for _, row := range rows {
		if row.Kind != types.SpanKindStage {
			continue
		}
		if row.Status != types.SpanStatusPending && row.Status != types.SpanStatusRunning {
			continue
		}
		if !contains(dependents, row.Name) {
			continue
		}
		updated := row // copy
		updated.Status = types.SpanStatusCancelled
		updated.ErrorCode = "UPSTREAM_FAILED"
		updated.ErrorMessage = reason
		updated.FinishedAt = &now
		if err := t.repo.Upsert(ctx, &updated); err != nil {
			logger.Warnf(ctx, "[SpanTracker] cascade dependent stage %s: %v", row.Name, err)
			continue
		}
		// Recurse into the cascaded stage's own subtree so any
		// in-flight subspan/generation is cancelled too. The
		// repo-level walk is iterative and cheap (small fan-out).
		if _, err := t.repo.CancelDescendants(ctx, row.KnowledgeID, row.Attempt, row.SpanID, reason); err != nil {
			logger.Warnf(ctx, "[SpanTracker] cascade descendants of dependent %s: %v", row.Name, err)
		}
	}
}

// stagesDependingOn returns the transitive closure of stages that have
// `stage` as an upstream dependency (direct or indirect). Computed by
// reverse-walking StageDependencies; the result is bounded to 5 since
// AllStages has five members, so a naive O(N²) walk is fine.
func stagesDependingOn(stage string) []string {
	var out []string
	seen := map[string]bool{}
	frontier := []string{stage}
	for len(frontier) > 0 {
		var next []string
		for _, candidate := range types.AllStages {
			if seen[candidate] {
				continue
			}
			deps := types.StageDependencies[candidate]
			for _, d := range deps {
				if contains(frontier, d) {
					seen[candidate] = true
					out = append(out, candidate)
					next = append(next, candidate)
					break
				}
			}
		}
		frontier = next
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// isMainPipelineStage reports whether stage is one of the 5 mandatory
// pipeline stages (docreader / chunking / embedding / multimodal /
// postprocess). A failure in any of these terminally invalidates the
// attempt and must close the root as failed. Optional downstream stages
// added later (summary, question, wiki, graph) do NOT match — those
// can fail individually without poisoning the parse result.
func isMainPipelineStage(name string) bool {
	for _, s := range types.AllStages {
		if s == name {
			return true
		}
	}
	return false
}

// durationSince computes elapsed ms preferring the in-process cache;
// falls back to the *Span's StartedAt for cross-process callers.
func durationSince(t *spanTracker, span *Span, now time.Time) int64 {
	if start, ok := t.takeStart(span.SpanID); ok {
		return now.Sub(start).Milliseconds()
	}
	if !span.StartedAt.IsZero() {
		return now.Sub(span.StartedAt).Milliseconds()
	}
	return 0
}

// FinalizeAttempt closes the root span for (knowledgeID, attempt). The
// pipeline calls this from two places: the success orchestrator
// (PostProcess) when the parse reaches Completed, and FailSpan when a
// MAIN stage fails terminally. Without this, the root row created by
// OpenAttempt would stay in "running" forever even after parse_status
// flips to completed/failed — operators would see a perpetually
// "running" trace despite a terminal knowledge state.
//
// Idempotent: re-closing a root that's already done/failed is a no-op
// so callers from different paths (success vs. cascade-fail vs.
// dead-letter) don't have to coordinate. We deliberately avoid the
// recordStart cache here (cross-process callers won't have it); we
// recompute duration from the persisted row's started_at.
func (t *spanTracker) FinalizeAttempt(ctx context.Context, knowledgeID string, attempt int, status string,
	output types.JSONMap, errorCode, errorMessage string,
) {
	if knowledgeID == "" || attempt <= 0 {
		return
	}
	if status == "" {
		status = types.SpanStatusDone
	}
	rows, err := t.repo.ListByAttempt(ctx, knowledgeID, attempt)
	if err != nil {
		logger.Warnf(ctx, "[SpanTracker] FinalizeAttempt list failed kid=%s attempt=%d: %v",
			knowledgeID, attempt, err)
		return
	}
	var root *types.KnowledgeProcessingSpan
	for i := range rows {
		if rows[i].Kind == types.SpanKindRoot {
			cp := rows[i]
			root = &cp
			break
		}
	}
	if root == nil {
		// No root means nothing to close — likely an attempt that
		// predates the tracker or whose OpenAttempt write failed.
		return
	}
	if root.Status == types.SpanStatusDone || root.Status == types.SpanStatusFailed ||
		root.Status == types.SpanStatusCancelled || root.Status == types.SpanStatusSkipped {
		return
	}
	now := time.Now()
	var started time.Time
	if root.StartedAt != nil {
		started = *root.StartedAt
	}
	dur := int64(0)
	if !started.IsZero() {
		dur = now.Sub(started).Milliseconds()
	}
	if len(errorMessage) > 1024 {
		errorMessage = errorMessage[:1024]
	}
	row := &types.KnowledgeProcessingSpan{
		KnowledgeID:  root.KnowledgeID,
		Attempt:      root.Attempt,
		SpanID:       root.SpanID,
		ParentSpanID: root.ParentSpanID,
		Name:         root.Name,
		Kind:         root.Kind,
		Status:       status,
		Input:        root.Input,
		Output:       output,
		Metadata:     root.Metadata,
		ErrorCode:    strings.TrimSpace(errorCode),
		ErrorMessage: errorMessage,
		StartedAt:    root.StartedAt,
		FinishedAt:   &now,
		DurationMs:   dur,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[SpanTracker] FinalizeAttempt upsert failed kid=%s attempt=%d: %v",
			knowledgeID, attempt, err)
		return
	}
	t.touchKnowledgeHeartbeat(ctx, knowledgeID, types.SpanKindRoot)
}

// AbortAttempt is the user-cancel counterpart to FinalizeAttempt. It
// flips every still-running / still-pending span for this attempt to
// cancelled — regardless of tree position — and then closes the root.
//
// Why a flat sweep instead of CancelDescendants' BFS: fan-out stages
// (e.g. 多模态识别) call EndSpan on the stage as soon as they finish
// DISPATCHING their async per-image work, so by the time the user
// hits cancel the stage row is already status=done but its image[*]
// children are still status=running. A BFS that stops at terminal
// parents would orphan those leaves. The flat sweep doesn't care
// about the tree shape — anything not-yet-terminal gets flipped.
func (t *spanTracker) AbortAttempt(ctx context.Context, knowledgeID string, attempt int,
	errorCode, errorMessage, reason string,
) {
	if knowledgeID == "" || attempt <= 0 {
		return
	}
	if reason == "" {
		reason = "user cancelled"
	}
	if errorCode == "" {
		errorCode = "USER_CANCELLED"
	}
	if n, err := t.repo.CancelAllOpenSpans(ctx, knowledgeID, attempt, errorCode, reason); err != nil {
		logger.Warnf(ctx, "[SpanTracker] AbortAttempt sweep failed kid=%s attempt=%d: %v",
			knowledgeID, attempt, err)
		// Fall through to FinalizeAttempt anyway — closing the root
		// is more important than perfectly closing every child.
	} else if n > 0 {
		logger.Infof(ctx,
			"[SpanTracker] AbortAttempt swept %d open span(s) for kid=%s attempt=%d",
			n, knowledgeID, attempt)
	}
	t.FinalizeAttempt(ctx, knowledgeID, attempt,
		types.SpanStatusCancelled, nil, errorCode, errorMessage)
}

// noopSpanTracker collapses every method to a no-op for tests/lite.
type noopSpanTracker struct{}

func (noopSpanTracker) OpenAttempt(_ context.Context, _, _ string) (*Span, int, error) {
	return nil, 0, nil
}
func (noopSpanTracker) LatestAttempt(_ context.Context, _ string) int { return 0 }
func (noopSpanTracker) BeginStage(_ context.Context, _ string, _ int, _ string, _ types.JSONMap) *Span {
	return nil
}
func (noopSpanTracker) BeginSubSpan(_ context.Context, _ *Span, _, _ string, _ types.JSONMap) *Span {
	return nil
}
func (noopSpanTracker) EndSpan(_ context.Context, _ *Span, _ types.JSONMap)            {}
func (noopSpanTracker) FailSpan(_ context.Context, _ *Span, _, _ string, _ error)      {}
func (noopSpanTracker) SkipSpan(_ context.Context, _ *Span, _ string)                  {}
func (noopSpanTracker) LookupStage(_ context.Context, _ string, _ int, _ string) *Span { return nil }
func (noopSpanTracker) FinalizeAttempt(_ context.Context, _ string, _ int, _ string, _ types.JSONMap, _, _ string) {
}
func (noopSpanTracker) AbortAttempt(_ context.Context, _ string, _ int, _, _, _ string) {}
