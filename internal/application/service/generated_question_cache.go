package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

const generatedQuestionBindingVersion = "generated-question-binding-v1"

func generatedQuestionArtifactPolicy(questionCount int) chatArtifactValuePolicy {
	return chatArtifactValuePolicy{
		canonicalize: func(completion string) (string, bool, error) {
			questions := parseGeneratedQuestions(completion, questionCount)
			value, err := json.Marshal(questions)
			return string(value), len(questions) > 0, err
		},
		validate: func(value string) error {
			questions, err := decodeGeneratedQuestionArtifact(value)
			if err != nil {
				return err
			}
			if len(questions) == 0 || len(questions) > questionCount {
				return fmt.Errorf("generated question artifact has invalid question count")
			}
			return nil
		},
	}
}

func decodeGeneratedQuestionArtifact(value string) ([]string, error) {
	var questions []string
	if err := json.Unmarshal([]byte(value), &questions); err != nil {
		return nil, err
	}
	for _, question := range questions {
		if strings.TrimSpace(question) == "" {
			return nil, fmt.Errorf("generated question artifact contains an empty question")
		}
	}
	return questions, nil
}

func parseGeneratedQuestions(completion string, questionCount int) []string {
	questions := make([]string, 0, questionCount)
	for _, line := range strings.Split(completion, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "0123456789.-*) "))
		if len(line) <= 5 {
			continue
		}
		questions = append(questions, line)
		if len(questions) >= questionCount {
			break
		}
	}
	return questions
}

func buildGeneratedQuestions(chunkID string, questions []string) []types.GeneratedQuestion {
	occurrences := make(map[string]int)
	generated := make([]types.GeneratedQuestion, 0, len(questions))
	for _, question := range questions {
		question = strings.TrimSpace(question)
		if question == "" {
			continue
		}
		normalized := types.NormalizeChunkContent(question)
		occurrence := occurrences[normalized]
		occurrences[normalized] = occurrence + 1
		digest := sha256.Sum256([]byte(strings.Join([]string{
			generatedQuestionBindingVersion,
			chunkID,
			normalized,
			strconv.Itoa(occurrence),
		}, "\x00")))
		generated = append(generated, types.GeneratedQuestion{
			ID:       "q" + hex.EncodeToString(digest[:12]),
			Question: question,
		})
	}
	return generated
}

func generatedQuestionsFromMetadata(metadata types.JSON) ([]types.GeneratedQuestion, error) {
	if len(metadata) == 0 {
		return nil, nil
	}
	var value struct {
		GeneratedQuestions []types.GeneratedQuestion `json:"generated_questions"`
	}
	if err := json.Unmarshal(metadata, &value); err != nil {
		return nil, err
	}
	return value.GeneratedQuestions, nil
}

func withGeneratedQuestions(metadata types.JSON, questions []types.GeneratedQuestion) (types.JSON, error) {
	fields := make(map[string]json.RawMessage)
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &fields); err != nil {
			return nil, err
		}
	}
	if fields == nil {
		fields = make(map[string]json.RawMessage)
	}
	encoded, err := json.Marshal(questions)
	if err != nil {
		return nil, err
	}
	fields["generated_questions"] = encoded
	result, err := json.Marshal(fields)
	return types.JSON(result), err
}

func staleGeneratedQuestionSourceIDs(
	chunkID string,
	oldQuestions, desiredQuestions []types.GeneratedQuestion,
) []string {
	desired := make(map[string]struct{}, len(desiredQuestions))
	for _, question := range desiredQuestions {
		desired[question.ID] = struct{}{}
	}
	stale := make([]string, 0)
	for _, question := range oldQuestions {
		if _, ok := desired[question.ID]; !ok && question.ID != "" {
			stale = append(stale, chunkID+"-"+question.ID)
		}
	}
	return stale
}

type generatedQuestionUpdate struct {
	chunk        *types.Chunk
	metadata     types.JSON
	oldQuestions []types.GeneratedQuestion
	newQuestions []types.GeneratedQuestion
}

type generatedQuestionIndexReconciler interface {
	DeleteBySourceIDList(context.Context, []string, int, string) error
}

func prepareGeneratedQuestionUpdate(
	chunk *types.Chunk,
	knowledge *types.Knowledge,
	questions []string,
) (generatedQuestionUpdate, []*types.IndexInfo, error) {
	oldQuestions, err := generatedQuestionsFromMetadata(chunk.Metadata)
	if err != nil {
		return generatedQuestionUpdate{}, nil, fmt.Errorf("parse chunk %s metadata: %w", chunk.ID, err)
	}
	newQuestions := buildGeneratedQuestions(chunk.ID, questions)
	metadata, err := withGeneratedQuestions(chunk.Metadata, newQuestions)
	if err != nil {
		return generatedQuestionUpdate{}, nil, fmt.Errorf("update chunk %s metadata: %w", chunk.ID, err)
	}
	update := generatedQuestionUpdate{
		chunk:        chunk,
		metadata:     metadata,
		oldQuestions: oldQuestions,
		newQuestions: newQuestions,
	}
	indexInfo := make([]*types.IndexInfo, 0, len(newQuestions))
	for _, question := range newQuestions {
		indexInfo = append(indexInfo, &types.IndexInfo{
			Content:         question.Question,
			SourceID:        chunk.ID + "-" + question.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			IsEnabled:       true,
		})
	}
	return update, indexInfo, nil
}

func (s *knowledgeService) applyGeneratedQuestionUpdates(
	ctx context.Context,
	retrieveEngine generatedQuestionIndexReconciler,
	embeddingModel embedding.Embedder,
	kb *types.KnowledgeBase,
	updates []generatedQuestionUpdate,
) error {
	for _, update := range updates {
		stale := staleGeneratedQuestionSourceIDs(update.chunk.ID, update.oldQuestions, update.newQuestions)
		if len(stale) > 0 {
			if err := retrieveEngine.DeleteBySourceIDList(
				ctx, stale, embeddingModel.GetDimensions(), kb.Type,
			); err != nil {
				return fmt.Errorf("delete stale generated questions for chunk %s: %w", update.chunk.ID, err)
			}
		}
		update.chunk.Metadata = update.metadata
		if err := s.chunkService.UpdateChunk(ctx, update.chunk); err != nil {
			return fmt.Errorf("update generated questions for chunk %s: %w", update.chunk.ID, err)
		}
	}
	return nil
}
