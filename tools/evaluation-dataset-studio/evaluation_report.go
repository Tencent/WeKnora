package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const evaluationReportSchemaVersion = 1

type EvaluationReportSummary struct {
	Total        int `json:"total"`
	Executed     int `json:"executed"`
	Failed       int `json:"failed"`
	Missing      int `json:"missing"`
	Uncomputable int `json:"uncomputable"`
	ZeroRecall   int `json:"zero_recall"`
}

type RetrievalMetricAggregate struct {
	Computable int     `json:"computable"`
	Precision  float64 `json:"precision"`
	Recall     float64 `json:"recall"`
	Hit        float64 `json:"hit"`
	MRR        float64 `json:"mrr"`
}

type AnswerAssessmentSummary struct {
	Reviewed     int     `json:"reviewed"`
	Correct      int     `json:"correct"`
	Partial      int     `json:"partial"`
	Incorrect    int     `json:"incorrect"`
	Unreviewed   int     `json:"unreviewed"`
	Scored       int     `json:"scored"`
	AverageScore float64 `json:"average_score"`
}

type EvaluationReportPassage struct {
	ID    int64  `json:"id"`
	Text  string `json:"text,omitempty"`
	Known bool   `json:"known"`
}

type EvaluationReportItem struct {
	QuestionID         int64                      `json:"question_id"`
	Question           string                     `json:"question"`
	StandardAnswer     string                     `json:"standard_answer"`
	ActualAnswer       string                     `json:"actual_answer,omitempty"`
	State              string                     `json:"state"`
	MetricComputable   bool                       `json:"metric_computable"`
	Precision          float64                    `json:"precision"`
	Recall             float64                    `json:"recall"`
	Hit                float64                    `json:"hit"`
	ReciprocalRank     float64                    `json:"reciprocal_rank"`
	GoldPassages       []EvaluationReportPassage  `json:"gold_passages"`
	RetrievedPassages  []EvaluationReportPassage  `json:"retrieved_passages"`
	RetrievedDocuments []string                   `json:"retrieved_documents,omitempty"`
	LatencyMS          int64                      `json:"latency_ms,omitempty"`
	ErrorMessage       string                     `json:"error_message,omitempty"`
	AnswerJudgement    string                     `json:"answer_judgement,omitempty"`
	AnswerScore        *float64                   `json:"answer_score,omitempty"`
	AssessmentSource   string                     `json:"assessment_source,omitempty"`
	ReviewComment      string                     `json:"review_comment,omitempty"`
	Metadata           EvaluationQuestionMetadata `json:"metadata"`
}

type EvaluationReportGroup struct {
	Dimension string                   `json:"dimension"`
	Value     string                   `json:"value"`
	Count     int                      `json:"count"`
	Metrics   RetrievalMetricAggregate `json:"metrics"`
}

type EvaluationReport struct {
	SchemaVersion        int                      `json:"schema_version"`
	GeneratedAt          time.Time                `json:"generated_at"`
	ProjectID            string                   `json:"project_id"`
	RunID                string                   `json:"run_id"`
	ExportID             string                   `json:"export_id"`
	ExportSHA256         string                   `json:"export_sha256,omitempty"`
	ResultSHA256         string                   `json:"result_sha256,omitempty"`
	Status               EvaluationTaskStatus     `json:"status"`
	Summary              EvaluationReportSummary  `json:"summary"`
	Metrics              RetrievalMetricAggregate `json:"metrics"`
	AnswerAssessment     AnswerAssessmentSummary  `json:"answer_assessment"`
	ImportedMetrics      *EvaluationMetricResult  `json:"imported_metrics,omitempty"`
	Groups               []EvaluationReportGroup  `json:"groups"`
	Items                []EvaluationReportItem   `json:"items"`
	Warnings             []string                 `json:"warnings"`
	AnswerAssessmentNote string                   `json:"answer_assessment_note"`
}

type EvaluationComparisonSummary struct {
	Improved     int `json:"improved"`
	Regressed    int `json:"regressed"`
	Unchanged    int `json:"unchanged"`
	NewlyMissing int `json:"newly_missing"`
	Incomparable int `json:"incomparable"`
}

type EvaluationComparisonItem struct {
	QuestionID   int64    `json:"question_id"`
	Question     string   `json:"question"`
	State        string   `json:"state"`
	BaseRecall   *float64 `json:"base_recall,omitempty"`
	TargetRecall *float64 `json:"target_recall,omitempty"`
}

type EvaluationComparison struct {
	BaseRunID   string                      `json:"base_run_id"`
	TargetRunID string                      `json:"target_run_id"`
	Summary     EvaluationComparisonSummary `json:"summary"`
	Items       []EvaluationComparisonItem  `json:"items"`
}

func (s *projectStore) buildEvaluationReport(projectID, runID string) (*EvaluationReport, error) {
	run, err := s.getEvaluationRun(projectID, runID)
	if err != nil {
		return nil, err
	}
	zipPath, _, err := s.exportHistoryPath(projectID, run.ExportID)
	if err != nil {
		return nil, fmt.Errorf("读取评测关联的导出包失败：%w", err)
	}
	zipData, err := os.ReadFile(zipPath)
	if err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp(s.root, ".evaluation-report-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)
	if err := extractWeKnoraZIP(zipData, tempDir); err != nil {
		return nil, err
	}
	rows, err := readWeKnoraRows(tempDir)
	if err != nil {
		return nil, err
	}

	queries, err := uniqueTextRows("query", rows.Queries)
	if err != nil {
		return nil, err
	}
	corpus, err := uniqueTextRows("corpus", rows.Corpus)
	if err != nil {
		return nil, err
	}
	answers, err := uniqueTextRows("answer", rows.Answers)
	if err != nil {
		return nil, err
	}
	answerByQuestion := make(map[int64]string, len(rows.QAs))
	for _, relation := range rows.QAs {
		answerByQuestion[relation.QID] = answers[relation.AID]
	}
	goldByQuestion := make(map[int64][]int64, len(rows.Qrels))
	for _, relation := range rows.Qrels {
		goldByQuestion[relation.QID] = append(goldByQuestion[relation.QID], relation.PID)
	}
	externalByQuestion := make(map[int64]ExternalEvaluationItem, len(run.ExternalItems))
	for _, item := range run.ExternalItems {
		externalByQuestion[item.QuestionID] = item
	}

	metadata := run.QuestionMetadata
	warnings := []string{}
	if len(metadata) == 0 {
		project, loadErr := s.get(projectID)
		if loadErr == nil {
			metadata = evaluationQuestionMetadata(project)
			warnings = append(warnings, "该历史记录未保存评测元数据快照；分类、难度、标签和来源使用当前数据集值，可能与运行时不同。")
		} else {
			metadata = map[int64]EvaluationQuestionMetadata{}
			warnings = append(warnings, "该历史记录未保存评测元数据快照，且当前数据集元数据不可用。")
		}
	}
	if len(run.ExternalItems) == 0 {
		warnings = append(warnings, "该记录没有逐题结果，无法计算本地检索指标；仅保留外部导入的汇总指标。")
	}

	questionIDs := make([]int64, 0, len(queries))
	for id := range queries {
		questionIDs = append(questionIDs, id)
	}
	sort.Slice(questionIDs, func(i, j int) bool { return questionIDs[i] < questionIDs[j] })
	report := &EvaluationReport{
		SchemaVersion: evaluationReportSchemaVersion, GeneratedAt: s.now().UTC(), ProjectID: projectID,
		RunID: run.ID, ExportID: run.ExportID, ExportSHA256: run.ExportSHA256, ResultSHA256: run.ExternalResultSHA256,
		Status: run.Status, ImportedMetrics: run.Metric, Groups: []EvaluationReportGroup{}, Items: []EvaluationReportItem{},
		Warnings: warnings, AnswerAssessmentNote: "本报告不使用答案字符串相等判断正确性；实际答案仅供人工审核或交给已确认的评分器。",
	}
	report.Summary.Total = len(questionIDs)
	for _, questionID := range questionIDs {
		goldIDs := goldByQuestion[questionID]
		item := EvaluationReportItem{
			QuestionID: questionID, Question: queries[questionID], StandardAnswer: answerByQuestion[questionID],
			State: "missing", Metadata: metadata[questionID], GoldPassages: reportPassages(goldIDs, corpus),
			RetrievedPassages: []EvaluationReportPassage{},
		}
		external, exists := externalByQuestion[questionID]
		if !exists {
			report.Summary.Missing++
			report.Summary.Uncomputable++
			report.Items = append(report.Items, item)
			continue
		}
		report.Summary.Executed++
		item.ActualAnswer = external.ActualAnswer
		item.RetrievedDocuments = external.RetrievedDocuments
		item.LatencyMS = external.LatencyMS
		item.ErrorMessage = external.ErrorMessage
		item.AnswerJudgement = strings.ToLower(strings.TrimSpace(external.AnswerJudgement))
		item.AnswerScore = external.AnswerScore
		item.AssessmentSource = strings.TrimSpace(external.AssessmentSource)
		item.ReviewComment = strings.TrimSpace(external.ReviewComment)
		item.RetrievedPassages = reportPassages(external.RetrievedPassageIDs, corpus)
		if strings.TrimSpace(external.ErrorMessage) != "" {
			item.State = "failed"
			report.Summary.Failed++
			report.Summary.Uncomputable++
		} else if !external.RetrievedPassageIDsProvided || len(goldIDs) == 0 {
			item.State = "uncomputable"
			report.Summary.Uncomputable++
		} else {
			item.State = "completed"
			item.MetricComputable = true
			item.Precision, item.Recall, item.Hit, item.ReciprocalRank = calculateRetrievalMetrics(goldIDs, external.RetrievedPassageIDs)
			if item.Recall == 0 {
				report.Summary.ZeroRecall++
			}
		}
		report.Items = append(report.Items, item)
	}
	report.Metrics = aggregateReportItems(report.Items)
	report.AnswerAssessment = aggregateAnswerAssessments(report.Items)
	report.Groups = groupReportItems(report.Items)
	return report, nil
}

func aggregateAnswerAssessments(items []EvaluationReportItem) AnswerAssessmentSummary {
	result := AnswerAssessmentSummary{}
	for _, item := range items {
		switch item.AnswerJudgement {
		case "correct":
			result.Reviewed++
			result.Correct++
		case "partial":
			result.Reviewed++
			result.Partial++
		case "incorrect":
			result.Reviewed++
			result.Incorrect++
		default:
			result.Unreviewed++
		}
		if item.AnswerScore != nil {
			result.Scored++
			result.AverageScore += *item.AnswerScore
		}
	}
	if result.Scored > 0 {
		result.AverageScore /= float64(result.Scored)
	}
	return result
}

func reportPassages(ids []int64, corpus map[int64]string) []EvaluationReportPassage {
	result := make([]EvaluationReportPassage, 0, len(ids))
	for _, id := range ids {
		text, known := corpus[id]
		result = append(result, EvaluationReportPassage{ID: id, Text: text, Known: known})
	}
	return result
}

func calculateRetrievalMetrics(gold, retrieved []int64) (float64, float64, float64, float64) {
	relevant := make(map[int64]struct{}, len(gold))
	for _, id := range gold {
		relevant[id] = struct{}{}
	}
	hits := 0
	firstRank := 0
	for index, id := range retrieved {
		if _, ok := relevant[id]; ok {
			hits++
			if firstRank == 0 {
				firstRank = index + 1
			}
		}
	}
	precision := 0.0
	if len(retrieved) > 0 {
		precision = float64(hits) / float64(len(retrieved))
	}
	recall := float64(hits) / float64(len(relevant))
	hit, mrr := 0.0, 0.0
	if firstRank > 0 {
		hit = 1
		mrr = 1 / float64(firstRank)
	}
	return precision, recall, hit, mrr
}

func aggregateReportItems(items []EvaluationReportItem) RetrievalMetricAggregate {
	result := RetrievalMetricAggregate{}
	for _, item := range items {
		if !item.MetricComputable {
			continue
		}
		result.Computable++
		result.Precision += item.Precision
		result.Recall += item.Recall
		result.Hit += item.Hit
		result.MRR += item.ReciprocalRank
	}
	if result.Computable > 0 {
		count := float64(result.Computable)
		result.Precision /= count
		result.Recall /= count
		result.Hit /= count
		result.MRR /= count
	}
	return result
}

func groupReportItems(items []EvaluationReportItem) []EvaluationReportGroup {
	type groupKey struct{ dimension, value string }
	grouped := map[groupKey][]EvaluationReportItem{}
	add := func(item EvaluationReportItem, dimension, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			value = "未设置"
		}
		key := groupKey{dimension, value}
		grouped[key] = append(grouped[key], item)
	}
	for _, item := range items {
		add(item, "category", item.Metadata.Category)
		add(item, "difficulty", item.Metadata.Difficulty)
		answerability := "未设置"
		if item.Metadata.Answerable != nil {
			answerability = strconv.FormatBool(*item.Metadata.Answerable)
		}
		add(item, "answerable", answerability)
		if len(item.Metadata.Tags) == 0 {
			add(item, "tag", "未设置")
		} else {
			for _, tag := range item.Metadata.Tags {
				add(item, "tag", tag)
			}
		}
		if len(item.Metadata.GoldSources) == 0 {
			add(item, "source", "未设置")
		} else {
			for _, source := range item.Metadata.GoldSources {
				add(item, "source", source)
			}
		}
	}
	keys := make([]groupKey, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dimension == keys[j].dimension {
			return keys[i].value < keys[j].value
		}
		return keys[i].dimension < keys[j].dimension
	})
	result := make([]EvaluationReportGroup, 0, len(keys))
	for _, key := range keys {
		result = append(result, EvaluationReportGroup{Dimension: key.dimension, Value: key.value, Count: len(grouped[key]), Metrics: aggregateReportItems(grouped[key])})
	}
	return result
}

func (s *projectStore) compareEvaluationReports(projectID, baseRunID, targetRunID string) (*EvaluationComparison, error) {
	if baseRunID == targetRunID {
		return nil, errors.New("请选择两个不同的评测记录")
	}
	base, err := s.buildEvaluationReport(projectID, baseRunID)
	if err != nil {
		return nil, err
	}
	target, err := s.buildEvaluationReport(projectID, targetRunID)
	if err != nil {
		return nil, err
	}
	baseItems := map[int64]EvaluationReportItem{}
	for _, item := range base.Items {
		baseItems[item.QuestionID] = item
	}
	targetItems := map[int64]EvaluationReportItem{}
	for _, item := range target.Items {
		targetItems[item.QuestionID] = item
	}
	ids := make([]int64, 0, len(baseItems)+len(targetItems))
	seen := map[int64]struct{}{}
	for id := range baseItems {
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range targetItems {
		if _, ok := seen[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := &EvaluationComparison{BaseRunID: baseRunID, TargetRunID: targetRunID, Items: []EvaluationComparisonItem{}}
	for _, id := range ids {
		left, leftOK := baseItems[id]
		right, rightOK := targetItems[id]
		item := EvaluationComparisonItem{QuestionID: id}
		if rightOK {
			item.Question = right.Question
		} else {
			item.Question = left.Question
		}
		switch {
		case leftOK && left.MetricComputable && rightOK && right.State == "missing":
			item.State = "newly_missing"
			result.Summary.NewlyMissing++
		case !leftOK || !rightOK || !left.MetricComputable || !right.MetricComputable:
			item.State = "incomparable"
			result.Summary.Incomparable++
		default:
			baseRecall, targetRecall := left.Recall, right.Recall
			item.BaseRecall, item.TargetRecall = &baseRecall, &targetRecall
			if targetRecall > baseRecall {
				item.State = "improved"
				result.Summary.Improved++
			} else if targetRecall < baseRecall {
				item.State = "regressed"
				result.Summary.Regressed++
			} else {
				item.State = "unchanged"
				result.Summary.Unchanged++
			}
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func renderEvaluationReportHTML(report *EvaluationReport) ([]byte, error) {
	const page = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>离线评测报告</title><style>body{font:14px/1.5 system-ui;margin:32px;color:#172033}table{border-collapse:collapse;width:100%}th,td{border:1px solid #d9dfeb;padding:8px;vertical-align:top}th{background:#f4f7fb}.muted{color:#64748b}.metric{display:inline-block;margin-right:24px}.bad{color:#b42318}</style></head><body><h1>离线评测报告</h1><p class="muted">运行 {{.RunID}} · 导出 {{.ExportID}} · 生成于 {{.GeneratedAt}}</p><p>{{.AnswerAssessmentNote}}</p><h2>覆盖情况</h2><p><span class="metric">总数 {{.Summary.Total}}</span><span class="metric">已运行 {{.Summary.Executed}}</span><span class="metric">失败 {{.Summary.Failed}}</span><span class="metric">缺失 {{.Summary.Missing}}</span><span class="metric">不可计算 {{.Summary.Uncomputable}}</span></p><h2>本地检索指标</h2><p><span class="metric">Precision {{printf "%.4f" .Metrics.Precision}}</span><span class="metric">Recall {{printf "%.4f" .Metrics.Recall}}</span><span class="metric">Hit {{printf "%.4f" .Metrics.Hit}}</span><span class="metric">MRR {{printf "%.4f" .Metrics.MRR}}</span><span class="muted">可计算 {{.Metrics.Computable}}</span></p><h2>显式答案评价</h2><p><span class="metric">已评价 {{.AnswerAssessment.Reviewed}}</span><span class="metric">正确 {{.AnswerAssessment.Correct}}</span><span class="metric">部分正确 {{.AnswerAssessment.Partial}}</span><span class="metric">错误 {{.AnswerAssessment.Incorrect}}</span><span class="metric">未评价 {{.AnswerAssessment.Unreviewed}}</span><span class="metric">平均分 {{printf "%.4f" .AnswerAssessment.AverageScore}}（{{.AnswerAssessment.Scored}} 条）</span></p>{{range .Warnings}}<p class="bad">{{.}}</p>{{end}}<h2>逐题详情</h2><table><thead><tr><th>ID / 状态</th><th>问题与答案</th><th>答案评价</th><th>检索指标</th><th>黄金 / 实际召回</th><th>错误 / 耗时</th></tr></thead><tbody>{{range .Items}}<tr><td>{{.QuestionID}}<br>{{.State}}</td><td><b>{{.Question}}</b><br>标准：{{.StandardAnswer}}<br>实际：{{.ActualAnswer}}</td><td>{{if .AnswerJudgement}}{{.AnswerJudgement}}{{else}}未评价{{end}}{{if .AnswerScore}}<br>分数 {{printf "%.4f" .AnswerScore}}{{end}}<br>{{.AssessmentSource}}<br>{{.ReviewComment}}</td><td>{{if .MetricComputable}}P {{printf "%.4f" .Precision}}<br>R {{printf "%.4f" .Recall}}<br>Hit {{printf "%.0f" .Hit}}<br>RR {{printf "%.4f" .ReciprocalRank}}{{else}}不可计算{{end}}</td><td>黄金：{{range .GoldPassages}}[{{.ID}}] {{.Text}}<br>{{end}}实际：{{range .RetrievedPassages}}[{{.ID}}] {{if .Known}}{{.Text}}{{else}}未知片段{{end}}<br>{{end}}</td><td>{{.ErrorMessage}}<br>{{.LatencyMS}} ms</td></tr>{{end}}</tbody></table></body></html>`
	tmpl, err := template.New("report").Parse(page)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, report); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func renderEvaluationFailuresCSV(report *EvaluationReport) ([]byte, error) {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{"question_id", "state", "question", "standard_answer", "actual_answer", "answer_judgement", "answer_score", "assessment_source", "review_comment", "recall", "error_message", "latency_ms"}); err != nil {
		return nil, err
	}
	for _, item := range report.Items {
		if item.State == "completed" && item.Recall > 0 && item.AnswerJudgement != "incorrect" && item.AnswerJudgement != "partial" {
			continue
		}
		recall := ""
		if item.MetricComputable {
			recall = strconv.FormatFloat(item.Recall, 'f', 6, 64)
		}
		score := ""
		if item.AnswerScore != nil {
			score = strconv.FormatFloat(*item.AnswerScore, 'f', 6, 64)
		}
		if err := writer.Write([]string{strconv.FormatInt(item.QuestionID, 10), item.State, item.Question, item.StandardAnswer, item.ActualAnswer, item.AnswerJudgement, score, item.AssessmentSource, item.ReviewComment, recall, item.ErrorMessage, strconv.FormatInt(item.LatencyMS, 10)}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return output.Bytes(), writer.Error()
}

func marshalEvaluationReport(report *EvaluationReport) ([]byte, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
