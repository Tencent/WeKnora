package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newWikiExtractionCheckpointFixture(t *testing.T) (*gorm.DB, *wikiIngestService, WikiPendingOp, context.Context) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.TaskPendingOp{}))
	repo := apprepo.NewTaskPendingOpsRepository(db)
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "document", Language: "zh-CN"}
	payload, err := json.Marshal(op)
	require.NoError(t, err)
	row := &types.TaskPendingOp{
		TenantID:  1,
		TaskType:  wikiTaskType,
		Scope:     wikiTaskScope,
		ScopeID:   "kb",
		Op:        WikiOpIngest,
		DedupKey:  op.KnowledgeID,
		Payload:   payload,
		FailCount: 4,
	}
	require.NoError(t, repo.Enqueue(context.Background(), row))
	svc := &wikiIngestService{pendingRepo: repo}
	return db, svc, reloadWikiExtractionCheckpoint(
			t,
			db,
			svc,
			row.ID,
		), context.WithValue(
			context.Background(),
			types.TenantIDContextKey,
			uint64(1),
		)
}

func reloadWikiExtractionCheckpoint(t *testing.T, db *gorm.DB, svc *wikiIngestService, id int64) WikiPendingOp {
	t.Helper()
	var row types.TaskPendingOp
	require.NoError(t, db.First(&row, id).Error)
	ops, _ := svc.decodePendingRows(context.Background(), []*types.TaskPendingOp{&row})
	require.Len(t, ops, 1)
	return ops[0]
}

func checkpointArticle() string {
	return "## 甲\n\n" + strings.Repeat(
		"甲",
		4500,
	) + "\n\n## 乙\n\n" + strings.Repeat(
		"乙",
		4500,
	) + "\n\n## 丙\n\n" + strings.Repeat(
		"丙",
		4500,
	)
}

func TestWikiExtractionCheckpointResumesAfterRestartAndKeepsEvidence(t *testing.T) {
	db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
	content := checkpointArticle()
	require.Len(t, splitWikiExtractionDocument(content), 3)
	failed := errors.New("invalid JSON in second segment")
	var calls atomic.Int32
	runner, err := svc.resumableWikiExtraction(
		ctx,
		&scriptedTemplateChatModel{},
		&op,
		1,
		map[string]string{"Content": content},
		func(_ context.Context, segment string) (combinedExtraction, error) {
			calls.Add(1)
			if strings.HasPrefix(segment, "## 乙") {
				return combinedExtraction{}, failed
			}
			return combinedExtraction{
				Entities: []extractedItem{
					{Name: "左宗棠", Slug: "entity/zuo", Details: "甲段独有事实", SourceChunks: []string{"甲"}},
				},
			}, nil
		},
	)
	require.NoError(t, err)
	got, err := extractWikiSegments(ctx, content, 1, runner)
	require.ErrorIs(t, err, failed)
	require.Empty(t, got.Entities)
	require.EqualValues(t, 2, calls.Load())
	// New service + a fresh DB read stand in for a worker/process restart.
	svc = &wikiIngestService{pendingRepo: apprepo.NewTaskPendingOpsRepository(db)}
	op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
	require.Len(t, op.Extraction.Segments, 1)
	var row types.TaskPendingOp
	require.NoError(t, db.First(&row, op.dbID).Error)
	require.Zero(t, row.FailCount, "completed progress resets consecutive failures")
	calls.Store(0)
	runner, err = svc.resumableWikiExtraction(
		ctx,
		&scriptedTemplateChatModel{},
		&op,
		1,
		map[string]string{"Content": content},
		func(_ context.Context, segment string) (combinedExtraction, error) {
			calls.Add(1)
			require.False(t, strings.HasPrefix(segment, "## 甲"), "must not regenerate completed segment")
			label := "乙"
			if strings.HasPrefix(segment, "## 丙") {
				label = "丙"
			}
			return combinedExtraction{
				Entities: []extractedItem{
					{Name: "左宗棠", Slug: "entity/zuo-" + label, Details: label + "段独有事实", SourceChunks: []string{label}},
				},
			}, nil
		},
	)
	require.NoError(t, err)
	got, err = extractWikiSegments(ctx, content, 3, runner)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load())
	require.Len(t, got.Entities, 1)
	require.Equal(t, "entity/zuo", got.Entities[0].Slug)
	require.Equal(t, []string{"甲", "乙", "丙"}, got.Entities[0].SourceChunks)
	require.Equal(t, "甲段独有事实\n乙段独有事实\n丙段独有事实", got.Entities[0].Details)
	op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
	require.Len(t, op.Extraction.Segments, 3)
}

func TestWikiExtractionCheckpointParallelSavesDoNotOverwriteEachOther(t *testing.T) {
	db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
	var calls atomic.Int32
	runner, err := svc.resumableWikiExtraction(
		ctx,
		&scriptedTemplateChatModel{},
		&op,
		1,
		map[string]string{"Content": checkpointArticle()},
		func(_ context.Context, segment string) (combinedExtraction, error) {
			calls.Add(1)
			return combinedExtraction{
				Entities: []extractedItem{{Name: segment[:len("## 甲")], Slug: "entity/item"}},
			}, nil
		},
	)
	require.NoError(t, err)
	_, err = extractWikiSegments(ctx, checkpointArticle(), 3, runner)
	require.NoError(t, err)
	require.EqualValues(t, 3, calls.Load())
	op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
	require.Len(t, op.Extraction.Segments, 3)
}

func TestWikiExtractionCheckpointInvalidatesChangedInputAndSettings(t *testing.T) {
	for _, field := range []string{"Content", "Language", "Granularity", "PromptTemplate", "CustomInstructions"} {
		t.Run(field, func(t *testing.T) {
			db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
			recipe := map[string]string{
				"Content":            "原文",
				"Language":           "zh",
				"Granularity":        "fine",
				"PromptTemplate":     "native",
				"CustomInstructions": "",
			}
			calls := 0
			extract := func(context.Context, string) (combinedExtraction, error) {
				calls++
				return combinedExtraction{}, nil
			}
			runner, err := svc.resumableWikiExtraction(ctx, &scriptedTemplateChatModel{}, &op, 1, recipe, extract)
			require.NoError(t, err)
			_, err = runner(ctx, "原文")
			require.NoError(t, err)
			op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
			recipe[field] += "changed"
			runner, err = svc.resumableWikiExtraction(ctx, &scriptedTemplateChatModel{}, &op, 1, recipe, extract)
			require.NoError(t, err)
			_, err = runner(ctx, "原文")
			require.NoError(t, err)
			require.Equal(t, 2, calls)
		})
	}
}

func TestWikiExtractionCheckpointCannotOverwriteOrRecreateReplacedOperation(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprint(deleted), func(t *testing.T) {
			db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
			runner, err := svc.resumableWikiExtraction(
				ctx,
				&scriptedTemplateChatModel{},
				&op,
				1,
				map[string]string{"Content": "原文"},
				func(context.Context, string) (combinedExtraction, error) {
					if deleted {
						require.NoError(t, db.Delete(&types.TaskPendingOp{}, op.dbID).Error)
					} else {
						update := db.Model(&types.TaskPendingOp{}).Where("id = ?", op.dbID).
							Update("payload", json.RawMessage(`{"newer":true}`))
						require.NoError(t, update.Error)
					}
					return combinedExtraction{}, nil
				},
			)
			require.NoError(t, err)
			_, err = runner(ctx, "原文")
			require.ErrorContains(t, err, "operation deleted or changed")
			var rows []types.TaskPendingOp
			require.NoError(t, db.Find(&rows).Error)
			if deleted {
				require.Empty(t, rows)
			} else {
				require.JSONEq(t, `{"newer":true}`, string(rows[0].Payload))
			}
		})
	}
}

type failingWikiCheckpointRepo struct {
	interfaces.TaskPendingOpsRepository
}

func (f failingWikiCheckpointRepo) CompareAndSwapPayload(
	context.Context,
	uint64,
	int64,
	json.RawMessage,
	json.RawMessage,
) (bool, error) {
	return false, errors.New("disk unavailable")
}

func TestWikiExtractionCheckpointSaveFailureIsNotPartialSuccess(t *testing.T) {
	_, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
	svc.pendingRepo = failingWikiCheckpointRepo{}
	runner, err := svc.resumableWikiExtraction(
		ctx,
		&scriptedTemplateChatModel{},
		&op,
		1,
		map[string]string{"Content": "原文"},
		func(context.Context, string) (combinedExtraction, error) {
			return combinedExtraction{Entities: []extractedItem{{Name: "甲", Slug: "entity/a"}}}, nil
		},
	)
	require.NoError(t, err)
	got, err := extractWikiSegments(ctx, "原文", 1, runner)
	require.ErrorContains(t, err, "disk unavailable")
	require.Empty(t, got.Entities)
	require.Empty(t, op.Extraction.Segments)
}

func TestWikiCandidateExtractionRetriesOnlyInvalidJSONSegment(t *testing.T) {
	db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
	model := &scriptedTemplateChatModel{responses: []*types.ChatResponse{
		{Content: `{"entities":[],"concepts":[]}`, FinishReason: "stop"},
		{Content: `{"entities":[`, FinishReason: "stop"},
	}}
	batch := &WikiBatchContext{ExtractionParallel: 1}
	_, _, _, err := svc.extractCandidateSlugs(
		ctx,
		model,
		"kb",
		checkpointArticle(),
		"Chinese",
		map[string]bool{"entity/b": true, "entity/a": true},
		batch,
		&op,
	)
	require.Error(t, err)
	require.Len(t, model.calls, 2)
	op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
	require.Len(t, op.Extraction.Segments, 1)
	model.responses = []*types.ChatResponse{
		{Content: `{"entities":[],"concepts":[]}`, FinishReason: "stop"},
		{Content: `{"entities":[],"concepts":[]}`, FinishReason: "stop"},
	}
	_, _, _, err = svc.extractCandidateSlugs(
		ctx,
		model,
		"kb",
		checkpointArticle(),
		"Chinese",
		map[string]bool{"entity/a": true, "entity/b": true},
		batch,
		&op,
	)
	require.NoError(t, err)
	require.Len(t, model.calls, 4, "two calls on retry, with stable previous-slug ordering")
}

func TestWikiExtractionCheckpointSavesValidatedResultOnCancellation(t *testing.T) {
	db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	runner, err := svc.resumableWikiExtraction(
		ctx,
		&scriptedTemplateChatModel{},
		&op,
		1,
		map[string]string{"Content": "原文"},
		func(context.Context, string) (combinedExtraction, error) { cancel(); return combinedExtraction{}, nil },
	)
	require.NoError(t, err)
	_, err = runner(ctx, "原文")
	require.NoError(t, err)
	op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
	require.Len(t, op.Extraction.Segments, 1)
}

func TestWikiCandidateExtractionDoesNotCheckpointInvalidOrTruncatedOutput(t *testing.T) {
	for _, response := range []types.ChatResponse{
		{Content: `null`, FinishReason: "stop"},
		{Content: `{}`, FinishReason: "stop"},
		{Content: `{"entities":null,"concepts":[]}`, FinishReason: "stop"},
		{Content: `{"entities":[],"concepts":[]}`, FinishReason: "length"},
	} {
		t.Run(response.Content+response.FinishReason, func(t *testing.T) {
			db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
			model := &scriptedTemplateChatModel{responses: []*types.ChatResponse{&response}}
			_, _, _, err := svc.extractCandidateSlugs(
				ctx,
				model,
				"kb",
				"有实质内容的原文",
				"Chinese",
				nil,
				&WikiBatchContext{ExtractionParallel: 1},
				&op,
			)
			require.Error(t, err)
			op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
			require.Empty(t, op.Extraction.Segments)
			require.Len(t, model.calls, 1)
		})
	}
}

type rejectingWikiCheckpointArchive struct {
	interfaces.TaskDeadLetterRepository
}

func (rejectingWikiCheckpointArchive) Insert(context.Context, *types.TaskDeadLetter) error {
	return errors.New("archive unavailable")
}

func TestWikiExtractionCheckpointSurvivesRetryExhaustionAndArchiveFailure(t *testing.T) {
	db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
	runner, err := svc.resumableWikiExtraction(
		ctx,
		&scriptedTemplateChatModel{},
		&op,
		1,
		map[string]string{"Content": "原文"},
		func(context.Context, string) (combinedExtraction, error) { return combinedExtraction{}, nil },
	)
	require.NoError(t, err)
	_, err = runner(ctx, "原文")
	require.NoError(t, err)
	require.NoError(
		t,
		db.Model(&types.TaskPendingOp{}).Where("id = ?", op.dbID).Update("fail_count", wikiMaxFailRetries).Error,
	)
	svc.deadLetterRepo = rejectingWikiCheckpointArchive{}
	err = svc.requeueFailedOps(ctx, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb"}, []WikiPendingOp{op})
	require.ErrorContains(t, err, "archive unavailable")
	saved := reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
	require.Len(t, saved.Extraction.Segments, 1)
	require.NoError(t, db.AutoMigrate(&types.TaskDeadLetter{}))
	svc.deadLetterRepo = apprepo.NewTaskDeadLetterRepository(db)
	require.NoError(
		t,
		svc.requeueFailedOps(ctx, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb"}, []WikiPendingOp{op}),
	)
	var remaining int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&remaining).Error)
	require.Zero(t, remaining)
	var archived types.TaskDeadLetter
	require.NoError(t, db.First(&archived).Error)
	var recovered WikiPendingOp
	require.NoError(t, json.Unmarshal(archived.Payload, &recovered))
	require.Len(t, recovered.Extraction.Segments, 1)
}
