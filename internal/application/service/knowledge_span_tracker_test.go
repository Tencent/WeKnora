package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

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
    name            VARCHAR(255) NOT NULL,
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

func TestFitSpanName(t *testing.T) {
	short := "postprocess.wiki.extract"
	if got := fitSpanName(short); got != short {
		t.Fatalf("short name should pass through, got %q", got)
	}

	// Regression: names in the 65–223 char window failed under VARCHAR(64)
	// but must pass through unchanged at VARCHAR(255).
	mid := "postprocess.wiki.page[concept/" + strings.Repeat("a", 100) + "]"
	if got := fitSpanName(mid); got != mid {
		t.Fatalf("mid-length wiki page name should pass through, got %q", got)
	}

	// Use a synthetic overlong wiki span name (> varchar(255)).
	long := "postprocess.wiki.page[concept/" + strings.Repeat("a", 280) + "]"
	got := fitSpanName(long)
	if utf8.RuneCountInString(got) > maxSpanNameLen {
		t.Fatalf("fitted name runes=%d, want <= %d: %q", utf8.RuneCountInString(got), maxSpanNameLen, got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("fitted name must be valid UTF-8: %q", got)
	}
	if got == long {
		t.Fatalf("expected truncation, got unchanged %q", got)
	}
	if fitSpanName(long) != got {
		t.Fatal("fitSpanName must be deterministic")
	}

	other := "postprocess.wiki.page[concept/" + strings.Repeat("b", 280) + "]"
	if fitSpanName(other) == got {
		t.Fatalf("different long names must not collapse to the same fitted name")
	}

	// CJK slugs must truncate on rune boundaries, not byte boundaries.
	cjkLong := "postprocess.wiki.page[" + strings.Repeat("中", 260) + "]"
	cjkGot := fitSpanName(cjkLong)
	if utf8.RuneCountInString(cjkGot) > maxSpanNameLen {
		t.Fatalf("CJK fitted name runes=%d, want <= %d", utf8.RuneCountInString(cjkGot), maxSpanNameLen)
	}
	if !utf8.ValidString(cjkGot) {
		t.Fatalf("CJK fitted name must be valid UTF-8: %q", cjkGot)
	}
}

// TestSpanTracker_BeginSubSpan_LongWikiPageName verifies wiki ingest's
// postprocess.wiki.page[<slug>] subspans persist even when the slug pushes
// the name past varchar(255).
func TestSpanTracker_BeginSubSpan_LongWikiPageName(t *testing.T) {
	tracker, db := setupSpanTrackerTest(t)
	ctx := context.Background()

	_, attempt, err := tracker.OpenAttempt(ctx, "kid", "")
	require.NoError(t, err)
	parent := tracker.BeginStage(ctx, "kid", attempt, types.StagePostProcess, nil)
	require.NotNil(t, parent)

	rawName := "postprocess.wiki.page[concept/" + strings.Repeat("x", 280) + "]"
	sub := tracker.BeginSubSpan(ctx, parent, rawName, types.SpanKindSubSpan, types.JSONMap{
		"slug": "concept/" + strings.Repeat("x", 280),
	})
	require.NotNil(t, sub)
	require.LessOrEqual(t, utf8.RuneCountInString(sub.Name), maxSpanNameLen)

	var count int64
	require.NoError(t, db.Table("knowledge_processing_spans").
		Where("knowledge_id = ? AND name = ?", "kid", sub.Name).
		Count(&count).Error)
	require.Equal(t, int64(1), count)
}

// TestSpanTracker_LookupSpanByName_FitsLongName verifies cross-process
// callers can look up a wiki page subspan using the raw overlong name.
func TestSpanTracker_LookupSpanByName_FitsLongName(t *testing.T) {
	tracker, _ := setupSpanTrackerTest(t)
	ctx := context.Background()

	_, attempt, err := tracker.OpenAttempt(ctx, "kid", "")
	require.NoError(t, err)
	parent := tracker.BeginStage(ctx, "kid", attempt, types.StagePostProcess, nil)
	require.NotNil(t, parent)

	rawName := "postprocess.wiki.page[concept/" + strings.Repeat("y", 280) + "]"
	created := tracker.BeginSubSpan(ctx, parent, rawName, types.SpanKindSubSpan, nil)
	require.NotNil(t, created)

	found := tracker.LookupSpanByName(ctx, "kid", attempt, rawName)
	require.NotNil(t, found, "LookupSpanByName must normalize the raw name")
	assert.Equal(t, created.SpanID, found.SpanID)
	assert.Equal(t, created.Name, found.Name)
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

// TestPostprocessSubspan_AttachesUnderPostProcessStage covers the contract
// that the async post-pipeline tasks (summary, question, graph) rely on:
// after the parsing pipeline closes the postprocess stage span, an
// out-of-band worker can still LookupStage + BeginSubSpan to record its
// real processing time as a child of postprocess. Without this guarantee
// the trace viewer's postprocess row stays at the ~10ms enqueue duration
// even when summary generation takes 20 s.
func TestPostprocessSubspan_AttachesUnderPostProcessStage(t *testing.T) {
	tracker, db := setupSpanTrackerTest(t)
	ctx := context.Background()
	repo := repository.NewKnowledgeSpanRepository(db)

	// Set up the parent attempt with a closed postprocess stage — the
	// async worker must still find it via LookupStage.
	_, attempt, err := tracker.OpenAttempt(ctx, "kid", "lf-trace")
	require.NoError(t, err)

	post := tracker.BeginStage(ctx, "kid", attempt, types.StagePostProcess, types.JSONMap{
		"chunks_total": 20,
	})
	require.NotNil(t, post)
	tracker.EndSpan(ctx, post, types.JSONMap{"enqueued_summary": true})

	// Simulate ProcessSummaryGeneration entering: lookup parent +
	// BeginSubSpan (the same call shape as beginPostprocessSubspan).
	parent := tracker.LookupStage(ctx, "kid", attempt, types.StagePostProcess)
	require.NotNil(t, parent, "lookup must succeed even after EndSpan closed the parent")
	assert.Equal(t, types.StagePostProcess, parent.Name)
	assert.Equal(t, types.SpanKindStage, parent.Kind)

	sumSpan := tracker.BeginSubSpan(ctx, parent, "postprocess.summary", types.SpanKindSubSpan,
		types.JSONMap{"language": "zh-CN"})
	require.NotNil(t, sumSpan)
	assert.Equal(t, parent.SpanID, sumSpan.ParentSpanID,
		"subspan must hang off the postprocess stage's span_id")
	assert.Equal(t, types.SpanKindSubSpan, sumSpan.Kind)

	tracker.EndSpan(ctx, sumSpan, types.JSONMap{
		"text_chunks":   20,
		"summary_chars": 142,
	})

	// Verify the row landed under the right parent with the right name.
	rows, err := repo.ListByAttempt(ctx, "kid", attempt)
	require.NoError(t, err)
	var found *types.KnowledgeProcessingSpan
	for i := range rows {
		if rows[i].Name == "postprocess.summary" {
			cp := rows[i]
			found = &cp
			break
		}
	}
	require.NotNil(t, found, "summary subspan row must persist")
	assert.Equal(t, parent.SpanID, found.ParentSpanID,
		"persisted parent_span_id matches LookupStage result")
	assert.Equal(t, types.SpanStatusDone, found.Status)
	assert.NotNil(t, found.Output, "EndSpan must record the output map")
}

// TestPostprocessSubspan_MissingParentFallsThrough covers the legacy
// path: an in-flight async task may carry attempt=0 (queued before the
// span-tracking field was added) or hit a knowledge whose postprocess
// stage row is missing (parse predates tracker). LookupStage returning
// nil must NOT crash the handler — the caller is expected to skip span
// recording and continue normal processing.
func TestPostprocessSubspan_MissingParentFallsThrough(t *testing.T) {
	tracker, _ := setupSpanTrackerTest(t)
	ctx := context.Background()

	// No OpenAttempt → no rows for kid. LookupStage must return nil.
	parent := tracker.LookupStage(ctx, "kid-without-attempt", 7, types.StagePostProcess)
	assert.Nil(t, parent, "missing parent attempt yields nil, not an error")

	// Open an attempt but never begin postprocess. Lookup must still nil.
	_, attempt, err := tracker.OpenAttempt(ctx, "kid-no-postprocess", "")
	require.NoError(t, err)
	parent = tracker.LookupStage(ctx, "kid-no-postprocess", attempt, types.StagePostProcess)
	assert.Nil(t, parent, "missing postprocess stage row yields nil")
}

// TestChunkExtractPayload_AttemptRoundTrip verifies the new fields
// added to ExtractChunkPayload survive JSON marshal/unmarshal so a
// cross-process asynq worker can recover the parent attempt + chunk
// ordinal on the receiving side. Skipping this would let a typo in
// the JSON tag silently zero the attempt and disable span recording.
func TestChunkExtractPayload_AttemptRoundTrip(t *testing.T) {
	in := types.ExtractChunkPayload{
		TenantID:    42,
		ChunkID:     "chunk-x",
		ModelID:     "m1",
		KnowledgeID: "kid-7",
		Attempt:     3,
		ChunkIndex:  9,
	}
	bytes, err := json.Marshal(in)
	require.NoError(t, err)

	var out types.ExtractChunkPayload
	require.NoError(t, json.Unmarshal(bytes, &out))

	assert.Equal(t, in.KnowledgeID, out.KnowledgeID)
	assert.Equal(t, in.Attempt, out.Attempt)
	assert.Equal(t, in.ChunkIndex, out.ChunkIndex)
}

// TestSummaryQuestionPayload_AttemptRoundTrip mirrors the above for the
// summary + question payloads to keep the contract documented.
func TestSummaryQuestionPayload_AttemptRoundTrip(t *testing.T) {
	sumIn := types.SummaryGenerationPayload{
		TenantID:        42,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "kid-7",
		Language:        "zh-CN",
		Attempt:         5,
	}
	sumBytes, err := json.Marshal(sumIn)
	require.NoError(t, err)
	var sumOut types.SummaryGenerationPayload
	require.NoError(t, json.Unmarshal(sumBytes, &sumOut))
	assert.Equal(t, 5, sumOut.Attempt)

	qIn := types.QuestionGenerationPayload{
		TenantID:        42,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "kid-7",
		QuestionCount:   3,
		Language:        "zh-CN",
		Attempt:         5,
	}
	qBytes, err := json.Marshal(qIn)
	require.NoError(t, err)
	var qOut types.QuestionGenerationPayload
	require.NoError(t, json.Unmarshal(qBytes, &qOut))
	assert.Equal(t, 5, qOut.Attempt)
}
