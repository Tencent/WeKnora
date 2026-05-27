package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// span tracker tests use a real GORM-backed repo against an in-memory
// SQLite DB. We do this instead of a stub repo because the cascade /
// LookupStage logic interacts non-trivially with the persistence layer
// (UPSERT, MAX(attempt), parent IN ...) — a stub would let regressions
// in those queries slip through.
//
// We DDL-define the spans table inline (same content as the repo test's
// spansTestDDL — kept duplicated rather than exported because a service
// test crossing into the repository test file's identifiers couples the
// two too tightly).
const spanTrackerTestDDL = `
CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    knowledge_id    VARCHAR(64) NOT NULL,
    attempt         INTEGER     NOT NULL DEFAULT 1,
    span_id         VARCHAR(64) NOT NULL,
    parent_span_id  VARCHAR(64),
    name            VARCHAR(64) NOT NULL,
    kind            VARCHAR(16) NOT NULL,
    status          VARCHAR(16) NOT NULL,
    input           TEXT,
    output          TEXT,
    metadata        TEXT,
    error_code      VARCHAR(64),
    error_message   TEXT,
    error_detail    TEXT,
    started_at      DATETIME,
    finished_at     DATETIME,
    duration_ms     BIGINT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (knowledge_id, attempt, span_id)
);
`

func setupSpanTrackerTest(t *testing.T) (SpanTracker, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(spanTrackerTestDDL).Error)
	// Pass nil for the heartbeat db: these tests don't exercise
	// heartbeat side-effects (those are covered in the housekeeping
	// suite). Keeping it nil also avoids needing the knowledges
	// table just to validate span behaviour.
	repo := repository.NewKnowledgeSpanRepository(db)
	return NewSpanTracker(repo, nil), db
}

// TestSpanTracker_OpenAttempt_AllocatesFreshNumbers covers the contract
// that drives reparse history: each OpenAttempt must hand out a strictly
// increasing attempt number per knowledge, and previous attempts'
// rows must remain queryable (via a separate ?attempt=N navigation).
func TestSpanTracker_OpenAttempt_AllocatesFreshNumbers(t *testing.T) {
	tracker, db := setupSpanTrackerTest(t)
	ctx := context.Background()

	root1, n1, err := tracker.OpenAttempt(ctx, "kid", "trace-1")
	require.NoError(t, err)
	require.NotNil(t, root1)
	assert.Equal(t, 1, n1)

	root2, n2, err := tracker.OpenAttempt(ctx, "kid", "trace-2")
	require.NoError(t, err)
	require.NotNil(t, root2)
	assert.Equal(t, 2, n2, "second OpenAttempt must allocate attempt 2")
	assert.NotEqual(t, root1.SpanID, root2.SpanID, "each attempt has its own root span ID")

	// Both roots must persist — a reparse must NOT erase the previous
	// attempt's history.
	var count int64
	require.NoError(t, db.Table("knowledge_processing_spans").
		Where("knowledge_id = ? AND kind = 'root'", "kid").
		Count(&count).Error)
	assert.Equal(t, int64(2), count, "previous attempt's root must remain after reparse")
}

// TestSpanTracker_FailSpan_CascadesDownstream verifies that failing a
// stage flips its dependent stages to "cancelled" so the UI shows a
// clear blast radius instead of orphan spinners. This is the central
// guarantee of the DAG model — without it, a Chunking failure leaves
// Embedding/Multimodal/PostProcess as pending forever.
func TestSpanTracker_FailSpan_CascadesDownstream(t *testing.T) {
	tracker, db := setupSpanTrackerTest(t)
	ctx := context.Background()

	_, attempt, err := tracker.OpenAttempt(ctx, "kid", "")
	require.NoError(t, err)
	require.Equal(t, 1, attempt)

	// Begin every stage so the cascade has something to cancel.
	docreader := tracker.BeginStage(ctx, "kid", attempt, types.StageDocReader, nil)
	tracker.EndSpan(ctx, docreader, nil)
	chunking := tracker.BeginStage(ctx, "kid", attempt, types.StageChunking, nil)
	embedding := tracker.BeginStage(ctx, "kid", attempt, types.StageEmbedding, nil)
	multimodal := tracker.BeginStage(ctx, "kid", attempt, types.StageMultimodal, nil)
	postprocess := tracker.BeginStage(ctx, "kid", attempt, types.StagePostProcess, nil)

	// Fail Chunking. Embedding/Multimodal/PostProcess must cascade.
	tracker.FailSpan(ctx, chunking, "CHUNKING_FAILED", "synthetic", errors.New("boom"))

	statusBy := map[string]string{}
	type row struct {
		Name, Status string
	}
	var rows []row
	require.NoError(t, db.Table("knowledge_processing_spans").
		Select("name, status").
		Where("knowledge_id = ? AND attempt = ?", "kid", attempt).
		Find(&rows).Error)
	for _, r := range rows {
		statusBy[r.Name] = r.Status
	}

	assert.Equal(t, types.SpanStatusDone, statusBy[types.StageDocReader], "upstream stage stays done")
	assert.Equal(t, types.SpanStatusFailed, statusBy[types.StageChunking], "the failed stage itself stays failed")
	assert.Equal(t, types.SpanStatusCancelled, statusBy[types.StageEmbedding], "direct dependent must cascade")
	assert.Equal(t, types.SpanStatusCancelled, statusBy[types.StageMultimodal], "sibling dependent must cascade")
	assert.Equal(t, types.SpanStatusCancelled, statusBy[types.StagePostProcess], "transitive dependent must cascade")

	// Quiet the unused-variable check: embedding / multimodal /
	// postprocess pointers were used to seed the table; their state
	// is now in statusBy. Linter still wants them "consumed".
	_ = embedding
	_ = multimodal
	_ = postprocess
}

// TestSpanTracker_LookupStage_FindsAcrossProcesses simulates the
// cross-process bridge an asynq worker uses: the upstream pipeline
// creates the multimodal stage span, then a separate worker process
// must locate it by (kid, attempt, name) to attach its image subspan.
func TestSpanTracker_LookupStage_FindsAcrossProcesses(t *testing.T) {
	tracker, _ := setupSpanTrackerTest(t)
	ctx := context.Background()

	_, attempt, err := tracker.OpenAttempt(ctx, "kid", "")
	require.NoError(t, err)
	mm := tracker.BeginStage(ctx, "kid", attempt, types.StageMultimodal, nil)
	require.NotNil(t, mm)

	// Pretend we're a different process — the in-memory `starts`
	// cache is the same map here, but the cross-process semantics
	// don't depend on it; LookupStage hits the DB.
	found := tracker.LookupStage(ctx, "kid", attempt, types.StageMultimodal)
	require.NotNil(t, found)
	assert.Equal(t, mm.SpanID, found.SpanID, "LookupStage must return the same span row")

	// A different stage must not be confused with multimodal.
	other := tracker.LookupStage(ctx, "kid", attempt, types.StageEmbedding)
	assert.Nil(t, other, "LookupStage(missing) must return nil")
}

// TestSpanTracker_BeginSubSpan_HangsUnderParent confirms multimodal /
// embedding fan-out subspans reference the parent stage's span_id —
// the structural invariant the buildSpanTree handler walks.
func TestSpanTracker_BeginSubSpan_HangsUnderParent(t *testing.T) {
	tracker, db := setupSpanTrackerTest(t)
	ctx := context.Background()

	_, attempt, err := tracker.OpenAttempt(ctx, "kid", "")
	require.NoError(t, err)
	parent := tracker.BeginStage(ctx, "kid", attempt, types.StageMultimodal, nil)
	require.NotNil(t, parent)

	sub := tracker.BeginSubSpan(ctx, parent, "multimodal.image[0]", types.SpanKindGeneration, types.JSONMap{
		"image_url": "x",
	})
	require.NotNil(t, sub)

	type row struct {
		Name, Kind, ParentSpanID string
	}
	var rows []row
	require.NoError(t, db.Table("knowledge_processing_spans").
		Select("name, kind, parent_span_id").
		Where("knowledge_id = ? AND name = ?", "kid", "multimodal.image[0]").
		Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, types.SpanKindGeneration, rows[0].Kind)
	assert.Equal(t, parent.SpanID, rows[0].ParentSpanID, "subspan must reference parent stage's span_id")
}

// TestSpanTracker_BeginStage_ReentryIsIdempotent guarantees that a second
// BeginStage call for the same (kid, attempt, stage) reuses the existing
// span row instead of inserting a duplicate. Without this, an asynq retry
// or any code path that begins a stage twice would produce two timeline
// segments for the same stage, and LookupStage would resolve to whichever
// row sorts first — both regressions the original implementation had.
func TestSpanTracker_BeginStage_ReentryIsIdempotent(t *testing.T) {
	tracker, db := setupSpanTrackerTest(t)
	ctx := context.Background()

	_, attempt, err := tracker.OpenAttempt(ctx, "kid", "")
	require.NoError(t, err)

	first := tracker.BeginStage(ctx, "kid", attempt, types.StageDocReader, types.JSONMap{"pages": 1})
	require.NotNil(t, first)
	// Simulate an intermediate End so the row is in a terminal state when
	// the re-entry happens (mirrors retry-after-failure ordering).
	tracker.FailSpan(ctx, first, "TEST", "first failure", errors.New("boom"))

	second := tracker.BeginStage(ctx, "kid", attempt, types.StageDocReader, types.JSONMap{"pages": 2})
	require.NotNil(t, second)
	assert.Equal(t, first.SpanID, second.SpanID,
		"re-entry must reuse the existing stage span_id")

	type row struct {
		SpanID, Status string
	}
	var rows []row
	require.NoError(t, db.Table("knowledge_processing_spans").
		Select("span_id, status").
		Where("knowledge_id = ? AND attempt = ? AND name = ?", "kid", attempt, types.StageDocReader).
		Find(&rows).Error)
	require.Len(t, rows, 1, "exactly one row per (knowledge, attempt, stage)")
	assert.Equal(t, types.SpanStatusRunning, rows[0].Status,
		"row must transition back to running after re-entry")
}

// TestSpanTracker_FailSpan_CascadesDependentSubspans verifies that when a
// chunking failure flips Embedding to "cancelled" (sibling cascade),
// embedding's already-running subspan (e.g. embedding.batch[0]) is ALSO
// cancelled. Without this, the UI rendered a cancelled stage with an
// orphan running batch hanging underneath.
func TestSpanTracker_FailSpan_CascadesDependentSubspans(t *testing.T) {
	tracker, db := setupSpanTrackerTest(t)
	ctx := context.Background()

	_, attempt, err := tracker.OpenAttempt(ctx, "kid", "")
	require.NoError(t, err)

	chunking := tracker.BeginStage(ctx, "kid", attempt, types.StageChunking, nil)
	embedding := tracker.BeginStage(ctx, "kid", attempt, types.StageEmbedding, nil)
	require.NotNil(t, embedding)
	// Subspan attached to the dependent (sibling) stage that's about to
	// be cascade-cancelled.
	batch := tracker.BeginSubSpan(ctx, embedding, "embedding.batch[0]", types.SpanKindGeneration, nil)
	require.NotNil(t, batch)

	tracker.FailSpan(ctx, chunking, "CHUNKING_FAILED", "synthetic", errors.New("boom"))

	type row struct {
		Name, Status string
	}
	var rows []row
	require.NoError(t, db.Table("knowledge_processing_spans").
		Select("name, status").
		Where("knowledge_id = ?", "kid").
		Find(&rows).Error)
	statusBy := map[string]string{}
	for _, r := range rows {
		statusBy[r.Name] = r.Status
	}
	assert.Equal(t, types.SpanStatusCancelled, statusBy[types.StageEmbedding],
		"dependent stage cascades to cancelled")
	assert.Equal(t, types.SpanStatusCancelled, statusBy["embedding.batch[0]"],
		"subspan under the cascaded stage must also be cancelled")
}
