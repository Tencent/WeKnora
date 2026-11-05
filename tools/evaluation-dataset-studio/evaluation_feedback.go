package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type evaluationFeedbackRequest struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Version     string  `json:"version"`
	QuestionIDs []int64 `json:"question_ids"`
}

func (s *projectStore) createEvaluationFeedbackDataset(projectID, runID string, req evaluationFeedbackRequest) (*DatasetProject, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	req.Version = strings.TrimSpace(req.Version)
	if err := validateDatasetID(req.ID); err != nil {
		return nil, err
	}
	if req.Name == "" {
		return nil, errors.New("回流数据集名称不能为空")
	}
	if req.Version == "" {
		return nil, errors.New("回流数据集版本不能为空")
	}
	if len(req.QuestionIDs) == 0 {
		return nil, errors.New("请至少选择一个失败问题")
	}
	report, err := s.buildEvaluationReport(projectID, runID)
	if err != nil {
		return nil, err
	}
	run, err := s.getEvaluationRun(projectID, runID)
	if err != nil {
		return nil, err
	}
	sourceProject, err := s.get(projectID)
	if err != nil {
		return nil, err
	}
	items := make(map[int64]EvaluationReportItem, len(report.Items))
	for _, item := range report.Items {
		items[item.QuestionID] = item
	}
	selected := make([]int64, 0, len(req.QuestionIDs))
	seen := map[int64]struct{}{}
	for _, questionID := range req.QuestionIDs {
		if _, duplicate := seen[questionID]; duplicate {
			continue
		}
		seen[questionID] = struct{}{}
		item, exists := items[questionID]
		if !exists {
			return nil, fmt.Errorf("问题 ID %d 不属于该评测记录", questionID)
		}
		if feedbackFailureReason(item) == "" {
			return nil, fmt.Errorf("问题 ID %d 没有失败、缺失或低召回，不能回流为失败样本", questionID)
		}
		if len(item.GoldPassages) == 0 {
			return nil, fmt.Errorf("问题 ID %d 缺少黄金语料，不能进入回流数据集", questionID)
		}
		for _, passage := range item.GoldPassages {
			if !passage.Known || strings.TrimSpace(passage.Text) == "" {
				return nil, fmt.Errorf("问题 ID %d 的黄金语料 %d 无法从导出包读取", questionID, passage.ID)
			}
			if len(run.ExportPassageIDMap) > 0 {
				if _, mapped := run.ExportPassageIDMap[passage.ID]; !mapped {
					return nil, fmt.Errorf("问题 ID %d 的黄金语料 %d 缺少导出 PID 映射", questionID, passage.ID)
				}
			}
		}
		selected = append(selected, questionID)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i] < selected[j] })

	now := s.now().UTC()
	target := newDatasetProject(createDatasetRequest{ID: req.ID, Name: req.Name, Version: req.Version}, now)
	target.Description = fmt.Sprintf("由数据集 %s 的评测运行 %s 回流生成，共 %d 个失败样本。", projectID, runID, len(selected))
	passageSeen := map[int64]struct{}{}
	for _, questionID := range selected {
		item := items[questionID]
		relevantIDs := make([]int64, 0, len(item.GoldPassages))
		for _, gold := range item.GoldPassages {
			internalID, ok := run.ExportPassageIDMap[gold.ID]
			if !ok {
				internalID = gold.ID
			}
			relevantIDs = append(relevantIDs, internalID)
			if _, exists := passageSeen[internalID]; exists {
				continue
			}
			passageSeen[internalID] = struct{}{}
			passageMetadata := run.PassageMetadata[gold.ID]
			if passageMetadata.Source == "" && len(passageMetadata.Tags) == 0 && len(passageMetadata.Metadata) == 0 && passageMetadata.ReviewState == "" {
				passageMetadata = currentPassageMetadata(sourceProject, internalID)
			}
			target.Passages = append(target.Passages, Passage{
				ID: internalID, Text: gold.Text, Source: passageMetadata.Source, Tags: append([]string(nil), passageMetadata.Tags...),
				Metadata: cloneStringMap(passageMetadata.Metadata), ReviewState: passageMetadata.ReviewState,
			})
		}
		metadata := item.Metadata
		target.Questions = append(target.Questions, Question{
			ID: questionID, Text: item.Question, Answer: item.StandardAnswer, RelevantPassageIDs: relevantIDs,
			Category: metadata.Category, Difficulty: metadata.Difficulty, Tags: append([]string(nil), metadata.Tags...), ReviewState: metadata.ReviewState,
			AnswerKeyPoints: append([]string(nil), metadata.AnswerKeyPoints...), Answerable: cloneBoolPointer(metadata.Answerable),
			ExpectedDocuments: append([]string(nil), metadata.ExpectedDocuments...), ForbiddenDocuments: append([]string(nil), metadata.ForbiddenDocuments...),
			TestRole: metadata.TestRole, RetrievalFilters: cloneStringMap(metadata.RetrievalFilters), DatasetVersion: metadata.DatasetVersion,
			AnnotationSource: metadata.AnnotationSource, SourceRunID: runID, SourceDatasetVersion: sourceProject.Version,
			FailureReason: feedbackFailureReason(item),
		})
	}
	sort.Slice(target.Passages, func(i, j int) bool { return target.Passages[i].ID < target.Passages[j].ID })
	if report := validateProject(target); !report.Valid {
		return nil, &validationFailedError{Report: report}
	}
	if err := s.importProject(target); err != nil {
		return nil, err
	}
	return s.get(target.ID)
}

func feedbackFailureReason(item EvaluationReportItem) string {
	if item.AnswerJudgement == "incorrect" {
		return "答案评价：错误"
	}
	if item.AnswerJudgement == "partial" {
		return "答案评价：部分正确"
	}
	switch item.State {
	case "missing":
		return "缺少外部逐题结果"
	case "failed":
		if strings.TrimSpace(item.ErrorMessage) != "" {
			return "执行失败：" + strings.TrimSpace(item.ErrorMessage)
		}
		return "执行失败"
	case "uncomputable":
		return "缺少可计算的召回结果"
	case "completed":
		if item.Recall == 0 {
			return "零召回"
		}
		if item.Recall < 1 {
			return fmt.Sprintf("召回不完整（Recall=%.4f）", item.Recall)
		}
	}
	return ""
}

func currentPassageMetadata(project *DatasetProject, passageID int64) EvaluationPassageMetadata {
	for _, passage := range project.Passages {
		if passage.ID == passageID {
			return EvaluationPassageMetadata{Source: passage.Source, Tags: passage.Tags, Metadata: passage.Metadata, ReviewState: passage.ReviewState}
		}
	}
	return EvaluationPassageMetadata{}
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
