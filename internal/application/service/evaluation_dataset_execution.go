package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// loadEvaluationDataset resolves the exact immutable version frozen in the
// experiment manifest. Tasks without a manifest use the dataset service.
func (e *EvaluationService) loadEvaluationDataset(
	ctx context.Context,
	detail *types.EvaluationDetail,
) ([]*types.QAPair, error) {
	if detail == nil || detail.Task == nil {
		return nil, errors.New("load evaluation dataset: task detail is required")
	}
	if detail.Experiment == nil {
		return e.dataset.GetDatasetByID(ctx, detail.Task.DatasetID)
	}
	if e.datasetRegistry == nil {
		return nil, errors.New("load evaluation dataset: dataset registry is unavailable")
	}

	frozen := detail.Experiment.Dataset
	if frozen.DatasetVersionID == "" || frozen.ContentSHA256 == "" {
		return nil, fmt.Errorf("load evaluation dataset: incomplete frozen dataset binding: %w",
			interfaces.ErrEvaluationDatasetInvalid)
	}
	content, err := e.datasetRegistry.GetVersionContent(ctx, detail.Task.TenantID, frozen.DatasetVersionID)
	if err != nil {
		return nil, fmt.Errorf("load evaluation dataset version %s: %w", frozen.DatasetVersionID, err)
	}
	if content == nil || content.Version == nil ||
		content.Version.ID != frozen.DatasetVersionID ||
		content.Version.DatasetID != frozen.DatasetID ||
		content.Version.ContentSHA256 != frozen.ContentSHA256 {
		return nil, fmt.Errorf("load evaluation dataset version %s: frozen identity mismatch: %w",
			frozen.DatasetVersionID, interfaces.ErrEvaluationDatasetInvalid)
	}

	input := evaluationDatasetVersionInput(content)
	if hash := types.CanonicalEvaluationDatasetContentSHA256(input); hash != frozen.ContentSHA256 {
		return nil, fmt.Errorf("load evaluation dataset version %s: content SHA-256 mismatch: %w",
			frozen.DatasetVersionID, interfaces.ErrEvaluationDatasetInvalid)
	}
	pairs, err := evaluationDatasetVersionToQAPairs(content)
	if err != nil {
		return nil, fmt.Errorf("load evaluation dataset version %s: %w", frozen.DatasetVersionID, err)
	}
	return pairs, nil
}

func evaluationDatasetVersionInput(
	content *types.EvaluationDatasetVersionContent,
) *types.EvaluationDatasetVersionInput {
	input := &types.EvaluationDatasetVersionInput{
		Passages:  make([]types.EvaluationDatasetPassageInput, 0, len(content.Passages)),
		Questions: make([]types.EvaluationDatasetQuestionInput, 0, len(content.Questions)),
		Relevance: make([]types.EvaluationDatasetRelevanceInput, 0, len(content.Relevance)),
	}
	for _, passage := range content.Passages {
		input.Passages = append(input.Passages, types.EvaluationDatasetPassageInput{
			PID: passage.PID, Content: passage.Content, Metadata: append(types.JSON(nil), passage.Metadata...),
		})
	}
	for _, question := range content.Questions {
		input.Questions = append(input.Questions, types.EvaluationDatasetQuestionInput{
			QID: question.QID, Question: question.Question, Answer: question.Answer,
		})
	}
	for _, relevance := range content.Relevance {
		input.Relevance = append(input.Relevance, types.EvaluationDatasetRelevanceInput{
			QID: relevance.QID, PID: relevance.PID, Grade: relevance.Grade,
		})
	}
	return input
}

func evaluationDatasetVersionToQAPairs(
	content *types.EvaluationDatasetVersionContent,
) ([]*types.QAPair, error) {
	if content == nil || content.Version == nil {
		return nil, fmt.Errorf("dataset version content is required: %w", interfaces.ErrEvaluationDatasetInvalid)
	}
	corpus := make([]string, len(content.Passages))
	pidIndex := make(map[string]int, len(content.Passages))
	for index, passage := range content.Passages {
		if passage.PID == "" {
			return nil, fmt.Errorf("empty passage ID: %w", interfaces.ErrEvaluationDatasetInvalid)
		}
		if _, duplicate := pidIndex[passage.PID]; duplicate {
			return nil, fmt.Errorf("duplicate passage ID %q: %w",
				passage.PID, interfaces.ErrEvaluationDatasetInvalid)
		}
		pidIndex[passage.PID] = index
		corpus[index] = passage.Content
	}

	relevanceByQID := make(map[string][]types.EvaluationDatasetRelevance)
	for _, relevance := range content.Relevance {
		relevanceByQID[relevance.QID] = append(relevanceByQID[relevance.QID], relevance)
	}
	pairs := make([]*types.QAPair, len(content.Questions))
	for index, question := range content.Questions {
		if question.SampleIndex != index || question.QID == "" {
			return nil, fmt.Errorf("invalid question order at sample_index %d: %w",
				index, interfaces.ErrEvaluationDatasetInvalid)
		}
		pair := &types.QAPair{
			QID:                      index,
			DatasetQID:               question.QID,
			Question:                 question.Question,
			Answer:                   question.Answer,
			Corpus:                   corpus,
			PIDGrades:                make(map[int]int),
			RetrievalLabelsAvailable: len(relevanceByQID[question.QID]) > 0,
		}
		for _, relevance := range relevanceByQID[question.QID] {
			pid, ok := pidIndex[relevance.PID]
			if !ok {
				return nil, fmt.Errorf("question %s references unknown passage %s: %w",
					question.QID, relevance.PID, interfaces.ErrEvaluationDatasetInvalid)
			}
			pair.PIDGrades[pid] = relevance.Grade
			if relevance.Grade > 0 {
				pair.PIDs = append(pair.PIDs, pid)
				pair.Passages = append(pair.Passages, corpus[pid])
			}
		}
		pairs[index] = pair
	}
	return pairs, nil
}
