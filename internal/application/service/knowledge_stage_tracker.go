// Package service: stage tracker.
//
// StageTracker is the thin facade every pipeline call site uses to record
// progress. Its three operations (Begin, Done, Fail) cover the lifecycle
// of every stage; everything else (ordering, finalize semantics) is the
// caller's responsibility — the tracker just persists what it's told.
//
// All operations are best-effort: a DB error is logged and swallowed so
// the parsing pipeline itself never breaks because tracker bookkeeping
// failed. The Knowledge.parse_status column remains the authoritative
// source of truth for whether a document is done.
package service

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// StageTracker exposes the trio of write operations the pipeline needs.
// Kept as an interface so unit tests can swap in a no-op implementation
// without spinning up a database.
type StageTracker interface {
	Begin(ctx context.Context, knowledgeID, stage string)
	Done(ctx context.Context, knowledgeID, stage string)
	Fail(ctx context.Context, knowledgeID, stage, errorCode, errorMessage string, errorDetail error)
	Skip(ctx context.Context, knowledgeID, stage string)
}

type stageTracker struct {
	repo repository.KnowledgeStageRepository
	// in-memory map of (knowledge_id|stage) → start time so we can
	// compute duration_ms when Done/Fail fires without an extra DB
	// read. Best-effort: a missed Begin (e.g. process restart between
	// Begin and Done) just yields duration_ms=0, which the UI renders
	// as "—" instead of a wrong number.
	starts map[string]time.Time
}

// NewStageTracker constructs a tracker backed by the given repo.
func NewStageTracker(repo repository.KnowledgeStageRepository) StageTracker {
	if repo == nil {
		return noopStageTracker{}
	}
	return &stageTracker{
		repo:   repo,
		starts: make(map[string]time.Time),
	}
}

func startKey(kid, stage string) string {
	return kid + "|" + stage
}

func (t *stageTracker) Begin(ctx context.Context, knowledgeID, stage string) {
	if knowledgeID == "" || stage == "" {
		return
	}
	now := time.Now()
	t.starts[startKey(knowledgeID, stage)] = now
	row := &types.KnowledgeProcessingStage{
		KnowledgeID: knowledgeID,
		Stage:       stage,
		Status:      types.ProcessingStageRunning,
		StartedAt:   &now,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[StageTracker] Begin failed kid=%s stage=%s: %v", knowledgeID, stage, err)
	}
}

func (t *stageTracker) Done(ctx context.Context, knowledgeID, stage string) {
	if knowledgeID == "" || stage == "" {
		return
	}
	now := time.Now()
	var dur int64
	if start, ok := t.starts[startKey(knowledgeID, stage)]; ok {
		dur = now.Sub(start).Milliseconds()
		delete(t.starts, startKey(knowledgeID, stage))
	}
	row := &types.KnowledgeProcessingStage{
		KnowledgeID: knowledgeID,
		Stage:       stage,
		Status:      types.ProcessingStageDone,
		FinishedAt:  &now,
		DurationMs:  dur,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[StageTracker] Done failed kid=%s stage=%s: %v", knowledgeID, stage, err)
	}
}

func (t *stageTracker) Fail(ctx context.Context, knowledgeID, stage, errorCode, errorMessage string, errorDetail error) {
	if knowledgeID == "" || stage == "" {
		return
	}
	now := time.Now()
	var dur int64
	if start, ok := t.starts[startKey(knowledgeID, stage)]; ok {
		dur = now.Sub(start).Milliseconds()
		delete(t.starts, startKey(knowledgeID, stage))
	}
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
	row := &types.KnowledgeProcessingStage{
		KnowledgeID:  knowledgeID,
		Stage:        stage,
		Status:       types.ProcessingStageFailed,
		FinishedAt:   &now,
		DurationMs:   dur,
		ErrorCode:    strings.TrimSpace(errorCode),
		ErrorMessage: errorMessage,
		ErrorDetail:  detail,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[StageTracker] Fail failed kid=%s stage=%s: %v", knowledgeID, stage, err)
	}
}

func (t *stageTracker) Skip(ctx context.Context, knowledgeID, stage string) {
	if knowledgeID == "" || stage == "" {
		return
	}
	now := time.Now()
	row := &types.KnowledgeProcessingStage{
		KnowledgeID: knowledgeID,
		Stage:       stage,
		Status:      types.ProcessingStageSkipped,
		FinishedAt:  &now,
	}
	if err := t.repo.Upsert(ctx, row); err != nil {
		logger.Warnf(ctx, "[StageTracker] Skip failed kid=%s stage=%s: %v", knowledgeID, stage, err)
	}
}

// noopStageTracker is returned when the repo is nil — keeps call sites
// trivial (no `if t != nil` guards) at the cost of one struct allocation.
type noopStageTracker struct{}

func (noopStageTracker) Begin(_ context.Context, _, _ string)               {}
func (noopStageTracker) Done(_ context.Context, _, _ string)                {}
func (noopStageTracker) Fail(_ context.Context, _, _, _, _ string, _ error) {}
func (noopStageTracker) Skip(_ context.Context, _, _ string)                {}
