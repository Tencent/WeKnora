package service

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type evaluationExportBounds struct {
	PageSize     int
	MaxQuestions int
	MaxBytes     int64
	TempDir      string
}

var defaultEvaluationExportBounds = evaluationExportBounds{
	PageSize:     types.EvaluationExportPageSize,
	MaxQuestions: types.EvaluationExportMaxQuestions,
	MaxBytes:     types.EvaluationExportMaxBytes,
}

type evaluationExportLimitWriter struct {
	writer  io.Writer
	written int64
	limit   int64
}

func (w *evaluationExportLimitWriter) Write(payload []byte) (int, error) {
	if int64(len(payload)) > w.limit-w.written {
		return 0, types.ErrEvaluationExportLimitExceeded
	}
	written, err := w.writer.Write(payload)
	w.written += int64(written)
	return written, err
}

type evaluationExportContext struct {
	ExportedAt     time.Time
	Task           types.EvaluationExportTask
	Labels         []string
	Experiment     *types.EvaluationExperimentSnapshot
	Metrics        json.RawMessage
	RuntimeMetrics json.RawMessage
}

// PrepareEvaluationExport builds a bounded audit artifact before an HTTP response starts.
func (e *EvaluationService) PrepareEvaluationExport(
	ctx context.Context,
	taskID string,
	format string,
) (*types.EvaluationPreparedExport, error) {
	return e.prepareEvaluationExport(ctx, taskID, format, defaultEvaluationExportBounds)
}

func (e *EvaluationService) prepareEvaluationExport(
	ctx context.Context,
	taskID string,
	rawFormat string,
	bounds evaluationExportBounds,
) (_ *types.EvaluationPreparedExport, returnedErr error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("%w: task_id is required", types.ErrEvaluationExportFormatInvalid)
	}
	format, err := types.NormalizeEvaluationExportFormat(rawFormat)
	if err != nil {
		return nil, err
	}
	if bounds.PageSize < 1 || bounds.PageSize > types.EvaluationQuestionPageMaxSize ||
		bounds.MaxQuestions < 1 || bounds.MaxBytes < 1 {
		return nil, errors.New("prepare evaluation export: invalid bounds")
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	entity, err := e.evaluationTaskRepository.GetTask(ctx, tenantID, taskID)
	if err != nil {
		return nil, err
	}
	if err := AuthorizeEvaluationTaskForAPIKey(ctx, entity); err != nil {
		return nil, err
	}
	if !types.IsEvaluationTerminalStatus(entity.Status) {
		return nil, fmt.Errorf("%w: task %s is active", types.ErrEvaluationExportTaskConflict, taskID)
	}
	labelsByTask, err := e.evaluationTaskRepository.ListTaskLabels(ctx, tenantID, []string{taskID})
	if err != nil {
		return nil, err
	}
	labels := labelsByTask[taskID]
	if labels == nil {
		labels = []string{}
	}
	experiment, provenanceComplete, err := decodeEvaluationExperiment(entity)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	exportContext := evaluationExportContext{
		ExportedAt:     time.Now().UTC(),
		Task:           evaluationExportTaskFromEntity(entity, provenanceComplete),
		Labels:         labels,
		Experiment:     experiment,
		Metrics:        normalizedEvaluationExportJSON(entity.Metric),
		RuntimeMetrics: normalizedEvaluationExportJSON(entity.RuntimeMetrics),
	}

	file, err := os.CreateTemp(bounds.TempDir, "weknora-evaluation-export-*")
	if err != nil {
		return nil, fmt.Errorf("create evaluation export file: %w", err)
	}
	path := file.Name()
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = file.Close()
		_ = os.Remove(path)
	}()

	limited := &evaluationExportLimitWriter{writer: file, limit: bounds.MaxBytes}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch format {
	case types.EvaluationExportFormatJSON:
		err = e.writeEvaluationJSONExport(ctx, limited, tenantID, taskID, exportContext, bounds)
	case types.EvaluationExportFormatCSV:
		err = e.writeEvaluationCSVExport(ctx, limited, tenantID, taskID, exportContext, bounds)
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close evaluation export file: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat evaluation export file: %w", err)
	}
	completed = true
	contentType := "application/json; charset=utf-8"
	if format == types.EvaluationExportFormatCSV {
		contentType = "text/csv; charset=utf-8"
	}
	return &types.EvaluationPreparedExport{
		Path:        path,
		Filename:    "evaluation-" + safeEvaluationExportFilename(taskID) + "." + format,
		ContentType: contentType,
		Size:        info.Size(),
	}, nil
}

func evaluationExportTaskFromEntity(
	entity *types.EvaluationTaskEntity,
	provenanceComplete bool,
) types.EvaluationExportTask {
	task := types.EvaluationExportTask{
		ID: entity.ID, DatasetID: entity.DatasetID, DatasetVersionID: entity.DatasetVersionID,
		DatasetContentSHA256: entity.DatasetContentSHA256, ExperimentSHA256: entity.ExperimentSHA256,
		ProvenanceComplete: provenanceComplete, Status: entity.Status, StartTime: entity.StartTime.UTC(),
		Total: entity.Total, Finished: entity.Finished, ErrorMessage: entity.ErrMsg,
		CleanupErrors: types.JSON(normalizedEvaluationExportJSON(entity.CleanupErrors)),
	}
	if entity.EndTime != nil {
		endTime := entity.EndTime.UTC()
		task.EndTime = &endTime
	}
	return task
}

func normalizedEvaluationExportJSON(value types.JSON) json.RawMessage {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" {
		return json.RawMessage("null")
	}
	return json.RawMessage(trimmed)
}

func safeEvaluationExportFilename(taskID string) string {
	var builder strings.Builder
	for _, character := range taskID {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('_')
		}
	}
	name := strings.Trim(builder.String(), "_")
	if name == "" {
		return "task"
	}
	if len(name) > 96 {
		return name[:96]
	}
	return name
}

func (e *EvaluationService) writeEvaluationJSONExport(
	ctx context.Context,
	writer io.Writer,
	tenantID uint64,
	taskID string,
	exportContext evaluationExportContext,
	bounds evaluationExportBounds,
) error {
	writeField := func(name string, value any, first bool) error {
		if !first {
			if _, err := io.WriteString(writer, ","); err != nil {
				return err
			}
		}
		nameJSON, _ := json.Marshal(name)
		if _, err := writer.Write(nameJSON); err != nil {
			return err
		}
		if _, err := io.WriteString(writer, ":"); err != nil {
			return err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = writer.Write(encoded)
		return err
	}
	if _, err := io.WriteString(writer, "{"); err != nil {
		return err
	}
	fields := []struct {
		name  string
		value any
	}{
		{name: "schema_version", value: types.EvaluationExportSchemaVersion},
		{name: "exported_at", value: exportContext.ExportedAt},
		{name: "task", value: exportContext.Task},
		{name: "labels", value: exportContext.Labels},
		{name: "experiment", value: exportContext.Experiment},
		{name: "aggregate_metrics", value: exportContext.Metrics},
		{name: "runtime_metrics", value: exportContext.RuntimeMetrics},
	}
	for index, field := range fields {
		if err := writeField(field.name, field.value, index == 0); err != nil {
			return fmt.Errorf("write evaluation JSON metadata: %w", err)
		}
	}
	if _, err := io.WriteString(writer, `,"questions":[`); err != nil {
		return err
	}
	questionIndex := 0
	err := e.forEachEvaluationExportQuestion(ctx, tenantID, taskID, bounds, func(
		row *types.EvaluationQuestionResultEntity,
	) error {
		if questionIndex > 0 {
			if _, err := io.WriteString(writer, ","); err != nil {
				return err
			}
		}
		encoded, err := json.Marshal(evaluationExportQuestionFromEntity(row))
		if err != nil {
			return err
		}
		if _, err := writer.Write(encoded); err != nil {
			return err
		}
		questionIndex++
		return nil
	})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(writer, "]}"); err != nil {
		return fmt.Errorf("finish evaluation JSON export: %w", err)
	}
	return nil
}

var evaluationExportCSVHeader = []string{
	"schema_version", "record_type", "exported_at", "task_id", "dataset_id_json",
	"dataset_version_id_json", "dataset_content_sha256", "experiment_sha256", "provenance_complete",
	"run_status", "start_time", "end_time", "total", "finished", "run_error_message_json",
	"cleanup_errors_json", "labels_json", "experiment_json", "aggregate_metrics_json",
	"sample_index", "qid_json", "question_json", "reference_answer_json", "ground_truth_pids_json",
	"search_results_json", "rerank_results_json", "generation_pids_json", "generated_text_json",
	"per_sample_metrics_json", "metric_observations_json", "question_error_code", "question_status",
	"result_hash", "retrieval_ms", "rerank_ms", "generation_ms", "total_ms", "prompt_tokens", "completion_tokens",
	"total_tokens", "usage_reported", "runtime_metrics_json",
}

func (e *EvaluationService) writeEvaluationCSVExport(
	ctx context.Context,
	writer io.Writer,
	tenantID uint64,
	taskID string,
	exportContext evaluationExportContext,
	bounds evaluationExportBounds,
) error {
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write(evaluationExportCSVHeader); err != nil {
		return fmt.Errorf("write evaluation CSV header: %w", err)
	}
	runValues := map[string]string{
		"schema_version": strconv.Itoa(types.EvaluationExportSchemaVersion),
		"record_type":    "run", "exported_at": exportContext.ExportedAt.Format(time.RFC3339Nano),
		"task_id": taskID, "dataset_content_sha256": stringValue(exportContext.Task.DatasetContentSHA256),
		"experiment_sha256":   stringValue(exportContext.Task.ExperimentSHA256),
		"provenance_complete": strconv.FormatBool(exportContext.Task.ProvenanceComplete),
		"run_status":          strconv.Itoa(int(exportContext.Task.Status)),
		"start_time":          exportContext.Task.StartTime.Format(time.RFC3339Nano),
		"end_time":            timePointerValue(exportContext.Task.EndTime),
		"total":               strconv.Itoa(exportContext.Task.Total),
		"finished":            strconv.Itoa(exportContext.Task.Finished),
	}
	var err error
	runValues["dataset_id_json"], err = evaluationExportJSONLiteral(exportContext.Task.DatasetID)
	if err != nil {
		return err
	}
	runValues["dataset_version_id_json"], err = evaluationExportJSONLiteral(exportContext.Task.DatasetVersionID)
	if err != nil {
		return err
	}
	runValues["run_error_message_json"], err = evaluationExportJSONLiteral(exportContext.Task.ErrorMessage)
	if err != nil {
		return err
	}
	for name, value := range map[string]any{
		"cleanup_errors_json": exportContext.Task.CleanupErrors,
		"labels_json":         exportContext.Labels, "experiment_json": exportContext.Experiment,
		"aggregate_metrics_json": exportContext.Metrics,
		"runtime_metrics_json":   exportContext.RuntimeMetrics,
	} {
		runValues[name], err = evaluationExportJSONLiteral(value)
		if err != nil {
			return fmt.Errorf("encode evaluation CSV %s: %w", name, err)
		}
	}
	if err := csvWriter.Write(evaluationExportCSVRecord(runValues)); err != nil {
		return fmt.Errorf("write evaluation CSV run row: %w", err)
	}
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return err
	}

	err = e.forEachEvaluationExportQuestion(ctx, tenantID, taskID, bounds, func(
		row *types.EvaluationQuestionResultEntity,
	) error {
		values, err := evaluationExportCSVQuestionValues(taskID, exportContext.ExportedAt, row)
		if err != nil {
			return err
		}
		if err := csvWriter.Write(evaluationExportCSVRecord(values)); err != nil {
			return err
		}
		csvWriter.Flush()
		return csvWriter.Error()
	})
	if err != nil {
		return err
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func evaluationExportCSVRecord(values map[string]string) []string {
	record := make([]string, len(evaluationExportCSVHeader))
	for index, name := range evaluationExportCSVHeader {
		record[index] = values[name]
	}
	return record
}

func evaluationExportCSVQuestionValues(
	taskID string,
	exportedAt time.Time,
	row *types.EvaluationQuestionResultEntity,
) (map[string]string, error) {
	values := map[string]string{
		"schema_version": strconv.Itoa(types.EvaluationExportSchemaVersion), "record_type": "question",
		"exported_at": exportedAt.Format(time.RFC3339Nano), "task_id": taskID,
		"sample_index": strconv.Itoa(row.SampleIndex), "question_error_code": row.ErrorCode,
		"question_status": row.Status, "result_hash": row.ResultHash,
		"retrieval_ms": int64PointerValue(row.RetrievalMs),
		"rerank_ms":    int64PointerValue(row.RerankMs), "generation_ms": int64PointerValue(row.GenerationMs),
		"total_ms": int64PointerValue(row.TotalMs), "prompt_tokens": intPointerValue(row.PromptTokens),
		"completion_tokens": intPointerValue(row.CompletionTokens), "total_tokens": intPointerValue(row.TotalTokens),
		"usage_reported": strconv.FormatBool(row.UsageReported),
	}
	jsonValues := map[string]any{
		"qid_json": row.QID, "question_json": row.Question, "reference_answer_json": row.ReferenceAnswer,
		"ground_truth_pids_json": row.GroundTruthPIDs, "search_results_json": row.SearchResults,
		"rerank_results_json": row.RerankResults, "generation_pids_json": row.GenerationPIDs,
		"generated_text_json": row.GeneratedText, "per_sample_metrics_json": row.PerSampleMetrics,
		"metric_observations_json": row.MetricObservations,
	}
	for name, value := range jsonValues {
		literal, err := evaluationExportJSONLiteral(value)
		if err != nil {
			return nil, fmt.Errorf("encode evaluation CSV %s: %w", name, err)
		}
		values[name] = literal
	}
	return values, nil
}

func evaluationExportJSONLiteral(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (e *EvaluationService) forEachEvaluationExportQuestion(
	ctx context.Context,
	tenantID uint64,
	taskID string,
	bounds evaluationExportBounds,
	visit func(*types.EvaluationQuestionResultEntity) error,
) error {
	sampleIndexFrom := 0
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := e.questionResultRepository.ListQuestionResults(
			ctx, tenantID, taskID, sampleIndexFrom, bounds.PageSize,
		)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			count++
			if count > bounds.MaxQuestions {
				return fmt.Errorf(
					"%w: task %s has more than %d questions",
					types.ErrEvaluationExportLimitExceeded,
					taskID,
					bounds.MaxQuestions,
				)
			}
			if err := visit(row); err != nil {
				return err
			}
		}
		if len(rows) < bounds.PageSize {
			return nil
		}
		next := rows[len(rows)-1].SampleIndex + 1
		if next <= sampleIndexFrom {
			return errors.New("evaluation export question pagination did not advance")
		}
		sampleIndexFrom = next
	}
}

func evaluationExportQuestionFromEntity(row *types.EvaluationQuestionResultEntity) types.EvaluationExportQuestion {
	return types.EvaluationExportQuestion{
		SampleIndex: row.SampleIndex, QID: row.QID, Question: row.Question,
		ReferenceAnswer:    row.ReferenceAnswer,
		GroundTruthPIDs:    types.JSON(normalizedEvaluationExportJSON(row.GroundTruthPIDs)),
		SearchResults:      types.JSON(normalizedEvaluationExportJSON(row.SearchResults)),
		RerankResults:      types.JSON(normalizedEvaluationExportJSON(row.RerankResults)),
		GenerationPIDs:     types.JSON(normalizedEvaluationExportJSON(row.GenerationPIDs)),
		GeneratedText:      row.GeneratedText,
		PerSampleMetrics:   types.JSON(normalizedEvaluationExportJSON(row.PerSampleMetrics)),
		MetricObservations: types.JSON(normalizedEvaluationExportJSON(row.MetricObservations)),
		ErrorCode:          row.ErrorCode, Status: row.Status, ResultHash: row.ResultHash,
		RetrievalMs: row.RetrievalMs, RerankMs: row.RerankMs,
		GenerationMs: row.GenerationMs, TotalMs: row.TotalMs, PromptTokens: row.PromptTokens,
		CompletionTokens: row.CompletionTokens, TotalTokens: row.TotalTokens,
		UsageReported: row.UsageReported,
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timePointerValue(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func int64PointerValue(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func intPointerValue(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}
