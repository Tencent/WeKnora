package main

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	externalResultSchemaVersion = 1
	maxExternalResultSize       = 8 << 20
)

type externalResultContentRequest struct {
	Format  string `json:"format"`
	Content string `json:"content"`
}

type ExternalEvaluationItem struct {
	QuestionID                  int64    `json:"question_id"`
	ActualAnswer                string   `json:"actual_answer,omitempty"`
	RetrievedPassageIDs         []int64  `json:"retrieved_passage_ids,omitempty"`
	RetrievedPassageIDsProvided bool     `json:"retrieved_passage_ids_provided,omitempty"`
	RetrievedDocuments          []string `json:"retrieved_documents,omitempty"`
	LatencyMS                   int64    `json:"latency_ms,omitempty"`
	ErrorMessage                string   `json:"error_message,omitempty"`
	AnswerJudgement             string   `json:"answer_judgement,omitempty"`
	AnswerScore                 *float64 `json:"answer_score,omitempty"`
	AssessmentSource            string   `json:"assessment_source,omitempty"`
	ReviewComment               string   `json:"review_comment,omitempty"`
}

func (item *ExternalEvaluationItem) UnmarshalJSON(data []byte) error {
	type itemAlias ExternalEvaluationItem
	var decoded itemAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*item = ExternalEvaluationItem(decoded)
	if _, exists := fields["retrieved_passage_ids"]; exists {
		item.RetrievedPassageIDsProvided = true
	}
	return nil
}

type externalResultSummary struct {
	Total             int                `json:"total,omitempty"`
	Finished          int                `json:"finished,omitempty"`
	ErrorMessage      string             `json:"error_message,omitempty"`
	RetrievalMetrics  map[string]float64 `json:"retrieval_metrics,omitempty"`
	GenerationMetrics map[string]float64 `json:"generation_metrics,omitempty"`
}

type externalResultPayload struct {
	SchemaVersion int                      `json:"schema_version"`
	Status        string                   `json:"status"`
	Summary       externalResultSummary    `json:"summary"`
	Items         []ExternalEvaluationItem `json:"items,omitempty"`
}

type ExternalResultPreview struct {
	Valid           bool     `json:"valid"`
	Format          string   `json:"format"`
	SchemaVersion   int      `json:"schema_version"`
	Status          string   `json:"status,omitempty"`
	SHA256          string   `json:"sha256"`
	ItemCount       int      `json:"item_count"`
	MatchedCount    int      `json:"matched_count"`
	MissingIDs      []int64  `json:"missing_question_ids"`
	UnknownIDs      []int64  `json:"unknown_question_ids"`
	DuplicateIDs    []int64  `json:"duplicate_question_ids"`
	CreatesRevision bool     `json:"creates_revision"`
	Issues          []string `json:"issues"`
}

func (s *projectStore) previewExternalEvaluationResult(projectID, runID string, req externalResultContentRequest) (*ExternalResultPreview, error) {
	run, err := s.getEvaluationRun(projectID, runID)
	if err != nil {
		return nil, err
	}
	if run.AdapterID != manualExportAdapterID {
		return nil, errors.New("只允许向 manual-export 评测记录导入外部结果")
	}
	project, err := s.get(projectID)
	if err != nil {
		return nil, err
	}
	preview, _, err := parseAndPreviewExternalResult(req, run, project)
	return preview, err
}

func parseAndPreviewExternalResult(req externalResultContentRequest, run *EvaluationRun, project *DatasetProject) (*ExternalResultPreview, *externalResultPayload, error) {
	req.Format = strings.ToLower(strings.TrimSpace(req.Format))
	content := strings.TrimSpace(req.Content)
	preview := &ExternalResultPreview{
		Format: req.Format, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(req.Content))),
		Issues: []string{}, MissingIDs: []int64{}, UnknownIDs: []int64{}, DuplicateIDs: []int64{},
		CreatesRevision: run.Status != EvaluationTaskPending || run.ResultSource != "external_pending",
	}
	if content == "" {
		preview.Issues = append(preview.Issues, "外部结果内容不能为空")
		return preview, nil, nil
	}
	if len(req.Content) > maxExternalResultSize {
		preview.Issues = append(preview.Issues, "外部结果不能超过 8 MiB")
		return preview, nil, nil
	}
	var payload *externalResultPayload
	var err error
	switch req.Format {
	case "json":
		payload, err = parseExternalResultJSON([]byte(content))
	case "csv":
		payload, err = parseExternalResultCSV(content)
	default:
		preview.Issues = append(preview.Issues, "结果格式必须是 json 或 csv")
		return preview, nil, nil
	}
	if err != nil {
		preview.Issues = append(preview.Issues, err.Error())
		return preview, nil, nil
	}
	preview.SchemaVersion = payload.SchemaVersion
	preview.Status = payload.Status
	preview.ItemCount = len(payload.Items)
	validateExternalResultPayload(payload, preview)

	expectedIDs := run.ExportQuestionIDs
	if len(expectedIDs) == 0 {
		expectedIDs = make([]int64, 0, len(project.Questions))
		for _, question := range project.Questions {
			expectedIDs = append(expectedIDs, question.ID)
		}
		sort.Slice(expectedIDs, func(i, j int) bool { return expectedIDs[i] < expectedIDs[j] })
	}
	expected := make(map[int64]struct{}, len(expectedIDs))
	for _, id := range expectedIDs {
		expected[id] = struct{}{}
	}
	seen := make(map[int64]int, len(payload.Items))
	unknownPassages := make(map[int64]struct{})
	for _, item := range payload.Items {
		seen[item.QuestionID]++
		if seen[item.QuestionID] > 1 {
			preview.DuplicateIDs = appendUniqueInt64(preview.DuplicateIDs, item.QuestionID)
			continue
		}
		if _, ok := expected[item.QuestionID]; !ok {
			preview.UnknownIDs = append(preview.UnknownIDs, item.QuestionID)
		} else {
			preview.MatchedCount++
		}
		for _, passageID := range item.RetrievedPassageIDs {
			if len(run.ExportPassageIDMap) > 0 {
				if _, ok := run.ExportPassageIDMap[passageID]; !ok {
					unknownPassages[passageID] = struct{}{}
				}
			}
		}
	}
	for _, id := range expectedIDs {
		if seen[id] == 0 {
			preview.MissingIDs = append(preview.MissingIDs, id)
		}
	}
	sort.Slice(preview.UnknownIDs, func(i, j int) bool { return preview.UnknownIDs[i] < preview.UnknownIDs[j] })
	sort.Slice(preview.DuplicateIDs, func(i, j int) bool { return preview.DuplicateIDs[i] < preview.DuplicateIDs[j] })
	if len(preview.UnknownIDs) > 0 {
		preview.Issues = append(preview.Issues, fmt.Sprintf("包含 %d 个不属于所选导出版本的问题 ID", len(preview.UnknownIDs)))
	}
	if len(preview.DuplicateIDs) > 0 {
		preview.Issues = append(preview.Issues, fmt.Sprintf("包含 %d 个重复问题 ID", len(preview.DuplicateIDs)))
	}
	if len(unknownPassages) > 0 {
		ids := make([]int64, 0, len(unknownPassages))
		for id := range unknownPassages {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		preview.Issues = append(preview.Issues, fmt.Sprintf("包含未知的导出 passage ID：%v", ids))
	}
	preview.Valid = len(preview.Issues) == 0
	return preview, payload, nil
}

func validateExternalResultPayload(payload *externalResultPayload, preview *ExternalResultPreview) {
	if payload.SchemaVersion != externalResultSchemaVersion {
		preview.Issues = append(preview.Issues, fmt.Sprintf("不支持的外部结果 schema_version：%d", payload.SchemaVersion))
	}
	payload.Status = strings.TrimSpace(payload.Status)
	if payload.Status != "success" && payload.Status != "failed" {
		preview.Issues = append(preview.Issues, "status 只能是 success 或 failed")
	}
	if payload.Summary.Total < 0 || payload.Summary.Finished < 0 || (payload.Summary.Total > 0 && payload.Summary.Finished > payload.Summary.Total) {
		preview.Issues = append(preview.Issues, "total 和 finished 必须是非负数，且 finished 不能大于 total")
	}
	if err := validateImportedMetrics(payload.Summary.RetrievalMetrics); err != nil {
		preview.Issues = append(preview.Issues, "检索指标无效："+err.Error())
	}
	if err := validateImportedMetrics(payload.Summary.GenerationMetrics); err != nil {
		preview.Issues = append(preview.Issues, "生成指标无效："+err.Error())
	}
	if len(payload.Items) > 100000 {
		preview.Issues = append(preview.Issues, "逐题结果不能超过 100000 条")
	}
	for index, item := range payload.Items {
		if item.LatencyMS < 0 {
			preview.Issues = append(preview.Issues, fmt.Sprintf("第 %d 条结果 latency_ms 不能为负数", index+1))
		}
		seen := map[int64]struct{}{}
		judgement := strings.ToLower(strings.TrimSpace(item.AnswerJudgement))
		if judgement != "" && judgement != "correct" && judgement != "partial" && judgement != "incorrect" {
			preview.Issues = append(preview.Issues, fmt.Sprintf("问题 %d 的 answer_judgement 只能是 correct、partial 或 incorrect", item.QuestionID))
		}
		if item.AnswerScore != nil && (math.IsNaN(*item.AnswerScore) || math.IsInf(*item.AnswerScore, 0) || *item.AnswerScore < 0 || *item.AnswerScore > 1) {
			preview.Issues = append(preview.Issues, fmt.Sprintf("问题 %d 的 answer_score 必须是 0 到 1 的有限数值", item.QuestionID))
		}
		if len([]rune(item.AssessmentSource)) > 100 || len([]rune(item.ReviewComment)) > 2000 {
			preview.Issues = append(preview.Issues, fmt.Sprintf("问题 %d 的评价来源或审核备注过长", item.QuestionID))
		}
		for _, passageID := range item.RetrievedPassageIDs {
			if passageID < 0 {
				preview.Issues = append(preview.Issues, fmt.Sprintf("问题 %d 包含负数 passage ID", item.QuestionID))
			}
			if _, duplicate := seen[passageID]; duplicate {
				preview.Issues = append(preview.Issues, fmt.Sprintf("问题 %d 重复召回 passage ID %d", item.QuestionID, passageID))
			}
			seen[passageID] = struct{}{}
		}
	}
}

func parseExternalResultJSON(data []byte) (*externalResultPayload, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(data, &shape); err != nil {
		return nil, fmt.Errorf("JSON 格式错误：%w", err)
	}
	if _, canonical := shape["schema_version"]; canonical || shape["items"] != nil || shape["summary"] != nil {
		var payload externalResultPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, fmt.Errorf("JSON 字段格式错误：%w", err)
		}
		return &payload, nil
	}
	var legacy evaluationResultImportRequest
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("旧版汇总 JSON 格式错误：%w", err)
	}
	return &externalResultPayload{
		SchemaVersion: externalResultSchemaVersion,
		Status:        legacy.Status,
		Summary: externalResultSummary{
			Total: legacy.Total, Finished: legacy.Finished, ErrorMessage: legacy.ErrorMessage,
			RetrievalMetrics: legacy.RetrievalMetrics, GenerationMetrics: legacy.GenerationMetrics,
		},
	}, nil
}

func parseExternalResultCSV(content string) (*externalResultPayload, error) {
	reader := csv.NewReader(strings.NewReader(content))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV 格式错误：%w", err)
	}
	if len(records) < 2 {
		return nil, errors.New("CSV 至少需要表头和一行逐题结果")
	}
	header, err := csvHeader(records[0])
	if err != nil {
		return nil, err
	}
	questionColumn, ok := findCSVColumn(header, "question_id", "问题id")
	if !ok {
		return nil, errors.New("CSV 缺少 question_id 列")
	}
	items := make([]ExternalEvaluationItem, 0, len(records)-1)
	for index, record := range records[1:] {
		if csvRecordEmpty(record) {
			continue
		}
		row := index + 2
		questionID, err := strconv.ParseInt(strings.TrimSpace(csvValue(record, questionColumn)), 10, 64)
		if err != nil || questionID < 0 {
			return nil, fmt.Errorf("CSV 第 %d 行：question_id 必须是非负整数", row)
		}
		passageIDs, err := parseExternalIDList(csvNamedValue(record, header, "retrieved_passage_ids", "召回片段id"))
		if err != nil {
			return nil, fmt.Errorf("CSV 第 %d 行：%w", row, err)
		}
		latency := int64(0)
		if value := csvNamedValue(record, header, "latency_ms", "耗时毫秒"); value != "" {
			latency, err = strconv.ParseInt(value, 10, 64)
			if err != nil || latency < 0 {
				return nil, fmt.Errorf("CSV 第 %d 行：latency_ms 必须是非负整数", row)
			}
		}
		var answerScore *float64
		if value := csvNamedValue(record, header, "answer_score", "答案分数"); value != "" {
			score, parseErr := strconv.ParseFloat(value, 64)
			if parseErr != nil {
				return nil, fmt.Errorf("CSV 第 %d 行：answer_score 必须是 0 到 1 的数值", row)
			}
			answerScore = &score
		}
		items = append(items, ExternalEvaluationItem{
			QuestionID: questionID, ActualAnswer: csvNamedValue(record, header, "actual_answer", "实际答案"),
			RetrievedPassageIDs: passageIDs, RetrievedPassageIDsProvided: true,
			RetrievedDocuments: splitCSVList(csvNamedValue(record, header, "retrieved_documents", "召回文档")),
			LatencyMS:          latency, ErrorMessage: csvNamedValue(record, header, "error_message", "错误信息"),
			AnswerJudgement: csvNamedValue(record, header, "answer_judgement", "答案结论"), AnswerScore: answerScore,
			AssessmentSource: csvNamedValue(record, header, "assessment_source", "评价来源"), ReviewComment: csvNamedValue(record, header, "review_comment", "审核备注"),
		})
	}
	if len(items) == 0 {
		return nil, errors.New("CSV 没有可导入的逐题结果")
	}
	return &externalResultPayload{
		SchemaVersion: externalResultSchemaVersion, Status: "success",
		Summary: externalResultSummary{Finished: len(items)}, Items: items,
	}, nil
}

func parseExternalIDList(value string) ([]int64, error) {
	parts := splitCSVRawList(value)
	result := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id < 0 {
			return nil, fmt.Errorf("召回 passage ID %q 必须是非负整数", part)
		}
		result = append(result, id)
	}
	return result, nil
}

func appendUniqueInt64(values []int64, value int64) []int64 {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (s *projectStore) importExternalEvaluationResult(projectID, runID string, req externalResultContentRequest) (*EvaluationRun, error) {
	baseRun, err := s.getEvaluationRun(projectID, runID)
	if err != nil {
		return nil, err
	}
	project, err := s.get(projectID)
	if err != nil {
		return nil, err
	}
	preview, payload, err := parseAndPreviewExternalResult(req, baseRun, project)
	if err != nil {
		return nil, err
	}
	if !preview.Valid || payload == nil {
		return nil, fmt.Errorf("外部结果校验失败：%s", strings.Join(preview.Issues, "；"))
	}
	run := baseRun
	now := s.now().UTC()
	if preview.CreatesRevision {
		copy := *baseRun
		run = &copy
		run.ID = s.nextEvaluationRunID(projectID, now)
		run.TaskID = "manual-" + run.ID
		run.CreatedAt = now
		run.StartTime = now
		run.CorrectionOfRunID = baseRun.ID
	}
	if payload.Status == "success" {
		run.Status = EvaluationTaskSucceeded
	} else {
		run.Status = EvaluationTaskFailed
	}
	run.Total = payload.Summary.Total
	if run.Total == 0 && len(run.ExportQuestionIDs) > 0 {
		run.Total = len(run.ExportQuestionIDs)
	}
	run.Finished = payload.Summary.Finished
	if run.Finished == 0 && len(payload.Items) > 0 {
		run.Finished = len(payload.Items)
	}
	run.ErrorMessage = strings.TrimSpace(payload.Summary.ErrorMessage)
	if len(payload.Summary.RetrievalMetrics) > 0 || len(payload.Summary.GenerationMetrics) > 0 {
		run.Metric = &EvaluationMetricResult{RetrievalMetrics: payload.Summary.RetrievalMetrics, GenerationMetrics: payload.Summary.GenerationMetrics}
	} else {
		run.Metric = nil
	}
	run.ExternalResultSchemaVersion = payload.SchemaVersion
	run.ExternalResultFormat = preview.Format
	run.ExternalResultSHA256 = preview.SHA256
	run.ExternalItems = payload.Items
	run.ResultSource = "external_import"
	run.UpdatedAt = now
	if err := s.writeEvaluationRun(run); err != nil {
		return nil, err
	}
	return run, nil
}

func (s *projectStore) nextEvaluationRunID(projectID string, now time.Time) string {
	for attempt := 0; attempt < 1000; attempt++ {
		id := historyID(now.Add(time.Duration(attempt) * time.Nanosecond))
		if _, err := os.Stat(filepath.Join(s.evaluations, projectID, id+".json")); errors.Is(err, os.ErrNotExist) {
			return id
		}
	}
	return historyID(now.Add(time.Microsecond))
}
