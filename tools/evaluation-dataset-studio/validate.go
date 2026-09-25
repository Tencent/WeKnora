package main

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

type ValidationIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
}

type ValidationReport struct {
	Valid        bool               `json:"valid"`
	ErrorCount   int                `json:"error_count"`
	WarningCount int                `json:"warning_count"`
	Issues       []ValidationIssue  `json:"issues"`
	Statistics   *DatasetStatistics `json:"statistics,omitempty"`
}

type DatasetStatistics struct {
	PassageCount           int            `json:"passage_count"`
	QuestionCount          int            `json:"question_count"`
	QrelCount              int            `json:"qrel_count"`
	ReferencedPassageCount int            `json:"referenced_passage_count"`
	PassageCoveragePercent float64        `json:"passage_coverage_percent"`
	AverageRelations       float64        `json:"average_relations"`
	AveragePassageLength   float64        `json:"average_passage_length"`
	AverageQuestionLength  float64        `json:"average_question_length"`
	AverageAnswerLength    float64        `json:"average_answer_length"`
	Sources                map[string]int `json:"sources"`
	Categories             map[string]int `json:"categories"`
	Difficulties           map[string]int `json:"difficulties"`
	ReviewStates           map[string]int `json:"review_states"`
}

func validateProject(project *DatasetProject) ValidationReport {
	report := ValidationReport{Valid: true, Issues: []ValidationIssue{}}
	add := func(severity, code, field, message string) {
		report.Issues = append(report.Issues, ValidationIssue{
			Severity: severity,
			Code:     code,
			Field:    field,
			Message:  message,
		})
		if severity == "error" {
			report.ErrorCount++
			report.Valid = false
		} else {
			report.WarningCount++
		}
	}

	if project == nil {
		add("error", "project_required", "", "数据集项目不能为空")
		return report
	}
	if project.SchemaVersion < 1 || project.SchemaVersion > projectSchemaVersion {
		add("error", "schema_version_unsupported", "schema_version",
			fmt.Sprintf("不支持的项目 Schema 版本：%d", project.SchemaVersion))
	}
	if strings.TrimSpace(project.Name) == "" {
		add("error", "name_required", "name", "数据集名称不能为空")
	}
	if strings.TrimSpace(project.Version) == "" {
		add("error", "version_required", "version", "数据集版本不能为空")
	}
	if len(project.Passages) == 0 {
		add("error", "passages_required", "passages", "数据集至少需要一条语料")
	}
	if len(project.Questions) == 0 {
		add("error", "questions_required", "questions", "数据集至少需要一个问题")
	}

	passageIDs := make(map[int64]struct{}, len(project.Passages))
	passageTexts := make(map[string]int64, len(project.Passages))
	referenced := make(map[int64]struct{})
	for i, passage := range project.Passages {
		field := fmt.Sprintf("passages[%d]", i)
		if passage.ID < 0 {
			add("error", "passage_id_negative", field+".id", "语料 ID 不能为负数")
		}
		if _, exists := passageIDs[passage.ID]; exists {
			add("error", "passage_id_duplicate", field+".id",
				fmt.Sprintf("语料 ID %d 重复", passage.ID))
		}
		passageIDs[passage.ID] = struct{}{}
		text := strings.TrimSpace(passage.Text)
		if text == "" {
			add("error", "passage_text_required", field+".text", "语料文本不能为空")
		} else if previousID, exists := passageTexts[text]; exists {
			add("warning", "passage_text_duplicate", field+".text",
				fmt.Sprintf("语料文本与 ID %d 完全重复", previousID))
		} else {
			passageTexts[text] = passage.ID
		}
		length := utf8.RuneCountInString(text)
		if text != "" && (length < 20 || length > 10000) {
			add("warning", "passage_length_unusual", field+".text",
				fmt.Sprintf("语料 ID %d 长度为 %d 字符，建议检查是否过短或过长", passage.ID, length))
		}
		if passage.ReviewState != "" && passage.ReviewState != "approved" {
			add("warning", "passage_not_approved", field+".review_state",
				fmt.Sprintf("语料 ID %d 尚未审核通过", passage.ID))
		}
	}

	questionIDs := make(map[int64]struct{}, len(project.Questions))
	questionTexts := make(map[string]int64, len(project.Questions))
	for i, question := range project.Questions {
		field := fmt.Sprintf("questions[%d]", i)
		if question.ID < 0 {
			add("error", "question_id_negative", field+".id", "问题 ID 不能为负数")
		}
		if _, exists := questionIDs[question.ID]; exists {
			add("error", "question_id_duplicate", field+".id",
				fmt.Sprintf("问题 ID %d 重复", question.ID))
		}
		questionIDs[question.ID] = struct{}{}
		text := strings.TrimSpace(question.Text)
		if text == "" {
			add("error", "question_text_required", field+".text", "问题文本不能为空")
		} else if previousID, exists := questionTexts[text]; exists {
			add("warning", "question_text_duplicate", field+".text",
				fmt.Sprintf("问题文本与 ID %d 完全重复", previousID))
		} else {
			questionTexts[text] = question.ID
		}
		questionLength := utf8.RuneCountInString(text)
		if text != "" && (questionLength < 4 || questionLength > 500) {
			add("warning", "question_length_unusual", field+".text",
				fmt.Sprintf("问题 ID %d 长度为 %d 字符，建议检查", question.ID, questionLength))
		}
		answer := strings.TrimSpace(question.Answer)
		if answer == "" {
			add("error", "answer_required", field+".answer", "标准答案不能为空")
		} else if length := utf8.RuneCountInString(answer); length < 2 || length > 4000 {
			add("warning", "answer_length_unusual", field+".answer",
				fmt.Sprintf("问题 ID %d 的标准答案长度为 %d 字符，建议检查", question.ID, length))
		}
		if len(question.RelevantPassageIDs) == 0 {
			add("error", "qrels_required", field+".relevant_passage_ids", "问题至少需要关联一条语料")
		}
		if len(question.RelevantPassageIDs) > 10 {
			add("warning", "qrel_count_unusual", field+".relevant_passage_ids",
				fmt.Sprintf("问题 ID %d 关联了 %d 条语料，建议确认相关性范围", question.ID, len(question.RelevantPassageIDs)))
		}
		if question.ReviewState != "" && question.ReviewState != "approved" {
			add("warning", "question_not_approved", field+".review_state",
				fmt.Sprintf("问题 ID %d 尚未审核通过", question.ID))
		}
		seen := make(map[int64]struct{}, len(question.RelevantPassageIDs))
		for _, passageID := range question.RelevantPassageIDs {
			if _, duplicate := seen[passageID]; duplicate {
				add("error", "qrel_duplicate", field+".relevant_passage_ids",
					fmt.Sprintf("问题重复关联语料 ID %d", passageID))
				continue
			}
			seen[passageID] = struct{}{}
			if _, exists := passageIDs[passageID]; !exists {
				add("error", "qrel_passage_missing", field+".relevant_passage_ids",
					fmt.Sprintf("关联的语料 ID %d 不存在", passageID))
				continue
			}
			referenced[passageID] = struct{}{}
		}
	}

	for _, passage := range project.Passages {
		if _, ok := referenced[passage.ID]; !ok {
			add("warning", "passage_unreferenced", fmt.Sprintf("passage:%d", passage.ID),
				fmt.Sprintf("语料 ID %d 未被任何问题引用", passage.ID))
		}
	}
	report.Statistics = buildDatasetStatistics(project, referenced)
	return report
}

func buildDatasetStatistics(project *DatasetProject, referenced map[int64]struct{}) *DatasetStatistics {
	stats := &DatasetStatistics{
		PassageCount:           len(project.Passages),
		QuestionCount:          len(project.Questions),
		ReferencedPassageCount: len(referenced),
		Sources:                map[string]int{},
		Categories:             map[string]int{},
		Difficulties:           map[string]int{},
		ReviewStates:           map[string]int{},
	}
	var passageLength, questionLength, answerLength int
	for _, passage := range project.Passages {
		passageLength += utf8.RuneCountInString(strings.TrimSpace(passage.Text))
		incrementDistribution(stats.Sources, passage.Source, "未设置")
		incrementDistribution(stats.ReviewStates, passage.ReviewState, "未设置")
	}
	for _, question := range project.Questions {
		questionLength += utf8.RuneCountInString(strings.TrimSpace(question.Text))
		answerLength += utf8.RuneCountInString(strings.TrimSpace(question.Answer))
		stats.QrelCount += len(question.RelevantPassageIDs)
		incrementDistribution(stats.Categories, question.Category, "未设置")
		incrementDistribution(stats.Difficulties, question.Difficulty, "未设置")
		incrementDistribution(stats.ReviewStates, question.ReviewState, "未设置")
	}
	if stats.PassageCount > 0 {
		stats.PassageCoveragePercent = roundOne(float64(stats.ReferencedPassageCount) * 100 / float64(stats.PassageCount))
		stats.AveragePassageLength = roundOne(float64(passageLength) / float64(stats.PassageCount))
	}
	if stats.QuestionCount > 0 {
		stats.AverageRelations = roundOne(float64(stats.QrelCount) / float64(stats.QuestionCount))
		stats.AverageQuestionLength = roundOne(float64(questionLength) / float64(stats.QuestionCount))
		stats.AverageAnswerLength = roundOne(float64(answerLength) / float64(stats.QuestionCount))
	}
	return stats
}

func incrementDistribution(distribution map[string]int, value, fallback string) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	distribution[value]++
}

func roundOne(value float64) float64 {
	return math.Round(value*10) / 10
}
