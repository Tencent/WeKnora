package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type fakeEvaluationExportQuestionRepository struct {
	rows   []*types.EvaluationQuestionResultEntity
	limits []int
}

func (r *fakeEvaluationExportQuestionRepository) PublishQuestionResult(
	context.Context,
	interfaces.EvaluationQuestionResultCommand,
) (*types.EvaluationTaskEntity, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (r *fakeEvaluationExportQuestionRepository) ListQuestionResults(
	_ context.Context,
	tenantID uint64,
	taskID string,
	sampleIndexFrom int,
	limit int,
) ([]*types.EvaluationQuestionResultEntity, error) {
	r.limits = append(r.limits, limit)
	rows := make([]*types.EvaluationQuestionResultEntity, 0, limit)
	for _, row := range r.rows {
		if row.TenantID != tenantID || row.TaskID != taskID || row.SampleIndex < sampleIndexFrom {
			continue
		}
		clone := *row
		rows = append(rows, &clone)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].SampleIndex < rows[j].SampleIndex })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func evaluationExportQuestionFixture(t *testing.T, index int, question string) *types.EvaluationQuestionResultEntity {
	t.Helper()
	return &types.EvaluationQuestionResultEntity{
		TenantID: 7, TaskID: "task-a", SampleIndex: index, QID: "qid-" + jsonNumber(float64(index)),
		Question: question, ReferenceAnswer: "reference", GroundTruthPIDs: types.JSON(`[1]`),
		SearchResults: types.JSON(`[{"rank":1,"pid":-1,"score":0.7,"provenance":"unknown"}]`),
		RerankResults: types.JSON(`[]`), GenerationPIDs: types.JSON(`[-1]`),
		GeneratedText: "+SUM(1,1)", PerSampleMetrics: types.JSON(`{}`),
		MetricObservations: types.JSON(`[]`), Status: types.EvaluationQuestionStatusSuccess,
		ResultHash: "fixture-result-hash",
	}
}

func evaluationExportServiceFixture(
	t *testing.T,
	status types.EvaluationStatue,
	rows ...*types.EvaluationQuestionResultEntity,
) (*EvaluationService, *fakeEvaluationTaskRepository, *fakeEvaluationExportQuestionRepository) {
	t.Helper()
	taskRepo := newFakeEvaluationTaskRepository()
	task := comparisonTaskFixture(t, "task-a", 5, 0.5)
	task.Status = status
	task.Total = 4
	task.Finished = len(rows)
	if status == types.EvaluationStatueSuccess {
		task.Total = len(rows)
	}
	endTime := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if types.IsEvaluationTerminalStatus(status) {
		task.EndTime = &endTime
	}
	taskRepo.register(task)
	require.NoError(t, taskRepo.ReplaceTaskLabels(
		context.Background(), 7, "task-a", []string{"baseline", "nightly"}, endTime,
	))
	questionRepo := &fakeEvaluationExportQuestionRepository{rows: rows}
	return &EvaluationService{
		evaluationTaskRepository: taskRepo,
		questionResultRepository: questionRepo,
	}, taskRepo, questionRepo
}

func TestPrepareEvaluationJSONExportContainsStoredFacts(t *testing.T) {
	svc, _, questions := evaluationExportServiceFixture(
		t,
		types.EvaluationStatueSuccess,
		evaluationExportQuestionFixture(t, 0, "question zero"),
		evaluationExportQuestionFixture(t, 1, "question one"),
	)
	prepared, err := svc.prepareEvaluationExport(
		comparisonServiceContext(), "task-a", "json",
		evaluationExportBounds{PageSize: 1, MaxQuestions: 10, MaxBytes: 1 << 20, TempDir: t.TempDir()},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(prepared.Path) })
	payload, err := os.ReadFile(prepared.Path)
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), prepared.Size)
	require.Equal(t, "application/json; charset=utf-8", prepared.ContentType)

	var document types.EvaluationExportDocument
	require.NoError(t, json.Unmarshal(payload, &document))
	require.Equal(t, types.EvaluationExportSchemaVersion, document.SchemaVersion)
	require.Equal(t, []string{"baseline", "nightly"}, document.Labels)
	require.True(t, document.Task.ProvenanceComplete)
	require.NotNil(t, document.Experiment)
	require.Len(t, document.Questions, 2)
	require.Equal(t, 1, document.Questions[1].SampleIndex)
	require.JSONEq(t,
		`[{"rank":1,"pid":-1,"score":0.7,"provenance":"unknown"}]`,
		document.Questions[0].SearchResults.ToString(),
	)
	require.Len(t, questions.limits, 3)
	for _, limit := range questions.limits {
		require.Equal(t, 1, limit)
	}
}

func TestPrepareEvaluationExportRejectsInvalidQuestionJSON(t *testing.T) {
	tempDir := t.TempDir()
	row := evaluationExportQuestionFixture(t, 0, "question")
	row.SearchResults = types.JSON(`{`)
	svc, _, _ := evaluationExportServiceFixture(t, types.EvaluationStatueSuccess, row)

	_, err := svc.prepareEvaluationExport(
		comparisonServiceContext(), "task-a", "json",
		evaluationExportBounds{PageSize: 1, MaxQuestions: 10, MaxBytes: 1 << 20, TempDir: tempDir},
	)
	require.Error(t, err)
	require.Empty(t, directoryEntries(t, tempDir))
}

func TestPrepareEvaluationExportPreservesRuntimeAccounting(t *testing.T) {
	for _, format := range []string{"json", "csv"} {
		for _, runtime := range []string{
			`null`,
			`{"schema_version":1,"durations":{"total_ms":125},"cost":{"call_count":2,` +
				`"accounting_complete_calls":1,"unpriced_calls":1,"usage_unreported_calls":1,` +
				`"started_calls":0,"totals":[{"currency":"CNY","cost_microunits":0}]}}`,
		} {
			t.Run(format+"/"+runtime, func(t *testing.T) {
				svc, taskRepo, _ := evaluationExportServiceFixture(t, types.EvaluationStatueSuccess)
				entity, err := taskRepo.GetTask(comparisonServiceContext(), 7, "task-a")
				require.NoError(t, err)
				entity.RuntimeMetrics = types.JSON(runtime)
				taskRepo.register(entity)
				prepared, err := svc.prepareEvaluationExport(
					comparisonServiceContext(), "task-a", format,
					evaluationExportBounds{PageSize: 1, MaxQuestions: 10, MaxBytes: 1 << 20, TempDir: t.TempDir()},
				)
				require.NoError(t, err)
				t.Cleanup(func() { _ = os.Remove(prepared.Path) })
				payload, err := os.ReadFile(prepared.Path)
				require.NoError(t, err)
				var exported string
				if format == "json" {
					var document map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(payload, &document))
					exported = string(document["runtime_metrics"])
				} else {
					records, err := csv.NewReader(bytes.NewReader(payload)).ReadAll()
					require.NoError(t, err)
					for column, name := range records[0] {
						if name == "runtime_metrics_json" {
							exported = records[1][column]
						}
					}
				}
				require.JSONEq(t, runtime, exported)
			})
		}
	}
}

func TestPrepareEvaluationCSVExportUsesJSONLiteralsForUserText(t *testing.T) {
	svc, _, _ := evaluationExportServiceFixture(
		t,
		types.EvaluationStatueFailed,
		evaluationExportQuestionFixture(t, 0, `=HYPERLINK("https://example.test")`),
	)
	prepared, err := svc.prepareEvaluationExport(
		comparisonServiceContext(), "task-a", "csv",
		evaluationExportBounds{PageSize: 500, MaxQuestions: 10, MaxBytes: 1 << 20, TempDir: t.TempDir()},
	)
	require.NoError(t, err, "a failed terminal task may export its completed rows")
	t.Cleanup(func() { _ = os.Remove(prepared.Path) })
	file, err := os.Open(prepared.Path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	records, err := csv.NewReader(file).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 3)
	header := make(map[string]int, len(records[0]))
	for index, name := range records[0] {
		header[name] = index
	}
	require.Equal(t, "run", records[1][header["record_type"]])
	require.Equal(t, "question", records[2][header["record_type"]])
	questionLiteral := records[2][header["question_json"]]
	require.NotEmpty(t, questionLiteral)
	require.Equal(t, '"', rune(questionLiteral[0]), "formula-like text must be a JSON string literal")
	var question string
	require.NoError(t, json.Unmarshal([]byte(questionLiteral), &question))
	require.Equal(t, `=HYPERLINK("https://example.test")`, question)
	generatedLiteral := records[2][header["generated_text_json"]]
	require.Equal(t, '"', rune(generatedLiteral[0]))
	require.Equal(t, "false", records[2][header["usage_reported"]])
}

func TestPrepareEvaluationExportRejectsActiveTaskBeforeCreatingFile(t *testing.T) {
	tempDir := t.TempDir()
	svc, _, _ := evaluationExportServiceFixture(t, types.EvaluationStatueRunning)
	_, err := svc.prepareEvaluationExport(
		comparisonServiceContext(), "task-a", "json",
		evaluationExportBounds{PageSize: 1, MaxQuestions: 2, MaxBytes: 1024, TempDir: tempDir},
	)
	require.ErrorIs(t, err, types.ErrEvaluationExportTaskConflict)
	require.Empty(t, directoryEntries(t, tempDir))
}

func TestPrepareEvaluationExportEnforcesAPIKeySourceKnowledgeBaseScope(t *testing.T) {
	tempDir := t.TempDir()
	svc, _, _ := evaluationExportServiceFixture(t, types.EvaluationStatueSuccess)
	ctx := types.WithTenantAPIKeyScope(comparisonServiceContext(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: []string{"kb-other"},
	})
	_, err := svc.prepareEvaluationExport(
		ctx, "task-a", "json",
		evaluationExportBounds{PageSize: 1, MaxQuestions: 2, MaxBytes: 1024, TempDir: tempDir},
	)
	require.ErrorIs(t, err, interfaces.ErrEvaluationTaskNotFound)
	require.Empty(t, directoryEntries(t, tempDir))
}

func TestPrepareEvaluationExportCleansTemporaryFileWhenContextIsCanceled(t *testing.T) {
	tempDir := t.TempDir()
	svc, _, _ := evaluationExportServiceFixture(
		t, types.EvaluationStatueSuccess, evaluationExportQuestionFixture(t, 0, "question"),
	)
	ctx, cancel := context.WithCancel(comparisonServiceContext())
	cancel()
	_, err := svc.prepareEvaluationExport(
		ctx, "task-a", "json",
		evaluationExportBounds{PageSize: 1, MaxQuestions: 2, MaxBytes: 1024, TempDir: tempDir},
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, directoryEntries(t, tempDir))
}

func TestPrepareEvaluationExportCleansTemporaryFileOnQuestionAndSizeLimits(t *testing.T) {
	for _, fixture := range []struct {
		name         string
		maxQuestions int
		maxBytes     int64
	}{
		{name: "questions", maxQuestions: 1, maxBytes: 1 << 20},
		{name: "bytes", maxQuestions: 10, maxBytes: 64},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			tempDir := t.TempDir()
			svc, _, _ := evaluationExportServiceFixture(
				t,
				types.EvaluationStatueSuccess,
				evaluationExportQuestionFixture(t, 0, "first"),
				evaluationExportQuestionFixture(t, 1, "second"),
			)
			_, err := svc.prepareEvaluationExport(
				comparisonServiceContext(), "task-a", "json",
				evaluationExportBounds{
					PageSize: 1, MaxQuestions: fixture.maxQuestions, MaxBytes: fixture.maxBytes, TempDir: tempDir,
				},
			)
			require.ErrorIs(t, err, types.ErrEvaluationExportLimitExceeded)
			require.Empty(t, directoryEntries(t, tempDir))
		})
	}
}

func TestEvaluationExportLimitWriterRejectsOverflowWithoutPartialPayload(t *testing.T) {
	var destination limitedTestWriter
	writer := &evaluationExportLimitWriter{writer: &destination, limit: 4}
	written, err := writer.Write([]byte("12345"))
	require.ErrorIs(t, err, types.ErrEvaluationExportLimitExceeded)
	require.Zero(t, written)
	require.Empty(t, destination.payload)
}

type limitedTestWriter struct{ payload []byte }

func (w *limitedTestWriter) Write(payload []byte) (int, error) {
	w.payload = append(w.payload, payload...)
	return len(payload), nil
}

func directoryEntries(t *testing.T, path string) []string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(path, "*"))
	require.NoError(t, err)
	return entries
}

var _ interfaces.EvaluationQuestionResultRepository = (*fakeEvaluationExportQuestionRepository)(nil)

var _ io.Writer = (*limitedTestWriter)(nil)
