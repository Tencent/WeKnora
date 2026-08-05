package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

const (
	defaultAutoTagMaxTags      = 3
	maximumAutoTagMaxTags      = 10
	maximumAutoTagCandidates   = 100
	maximumAutoTagContentRunes = 16000
	minimumAutoTagConfidence   = 0.75
	autoTagSpanName            = "postprocess.auto_tag"
)

// KnowledgeAutoTagService classifies processed documents against existing knowledge-base tags.
type KnowledgeAutoTagService struct {
	knowledgeRepo interfaces.KnowledgeRepository
	tagRepo       interfaces.KnowledgeTagRepository
	kbService     interfaces.KnowledgeBaseService
	chunkService  interfaces.ChunkService
	modelService  interfaces.ModelService
	spanTracker   SpanTracker
}

// NewKnowledgeAutoTagService creates the asynchronous automatic-tagging task handler.
func NewKnowledgeAutoTagService(
	knowledgeRepo interfaces.KnowledgeRepository,
	tagRepo interfaces.KnowledgeTagRepository,
	kbService interfaces.KnowledgeBaseService,
	chunkService interfaces.ChunkService,
	modelService interfaces.ModelService,
	spanTracker SpanTracker,
) interfaces.TaskHandler {
	return &KnowledgeAutoTagService{
		knowledgeRepo: knowledgeRepo,
		tagRepo:       tagRepo,
		kbService:     kbService,
		chunkService:  chunkService,
		modelService:  modelService,
		spanTracker:   spanTracker,
	}
}

func (s *KnowledgeAutoTagService) tracker() SpanTracker {
	if s.spanTracker == nil {
		return noopSpanTracker{}
	}
	return s.spanTracker
}

type autoTagModelMatch struct {
	TagID      string  `json:"tag_id"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason,omitempty"`
}

type autoTagModelResponse struct {
	Matches []autoTagModelMatch `json:"matches"`
}

// Handle processes one automatic-tagging task.
func (s *KnowledgeAutoTagService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.KnowledgeAutoTagPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal knowledge auto tag payload: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	parent := s.tracker().LookupStage(ctx, payload.KnowledgeID, payload.Attempt, types.StagePostProcess)
	span := s.tracker().BeginSubSpan(ctx, parent, autoTagSpanName, types.SpanKindSubSpan, types.JSONMap{
		"knowledge_base_id": payload.KnowledgeBaseID,
	})
	skip := func(reason string, extra types.JSONMap) error {
		if extra == nil {
			extra = types.JSONMap{}
		}
		extra["skipped"] = reason
		s.tracker().EndSpan(ctx, span, extra)
		return nil
	}

	knowledge, err := s.knowledgeRepo.GetKnowledgeByIDOnly(ctx, payload.KnowledgeID)
	if err != nil {
		s.tracker().FailSpan(ctx, span, "AUTO_TAG_KNOWLEDGE_LOAD_FAILED", err.Error(), err)
		return fmt.Errorf("get knowledge for auto tag: %w", err)
	}
	if knowledge == nil || knowledge.TenantID != payload.TenantID || knowledge.KnowledgeBaseID != payload.KnowledgeBaseID {
		return skip("knowledge_not_in_scope", nil)
	}
	if knowledge.ParseStatus == types.ParseStatusCancelled ||
		knowledge.ParseStatus == types.ParseStatusDeleting ||
		knowledge.ParseStatus == types.ParseStatusFailed {
		return skip("knowledge_not_active", types.JSONMap{"parse_status": knowledge.ParseStatus})
	}

	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil {
		s.tracker().FailSpan(ctx, span, "AUTO_TAG_KB_LOAD_FAILED", err.Error(), err)
		return fmt.Errorf("get knowledge base for auto tag: %w", err)
	}
	if kb == nil || kb.TenantID != payload.TenantID || kb.Type != types.KnowledgeBaseTypeDocument {
		return skip("knowledge_base_not_eligible", nil)
	}
	config := kb.AutoTagConfig
	if config == nil || !config.Enabled {
		return skip("auto_tag_disabled", nil)
	}
	config.Normalize()

	tags, _, err := s.tagRepo.ListByKB(
		ctx,
		payload.TenantID,
		payload.KnowledgeBaseID,
		&types.Pagination{Page: 1, PageSize: maximumAutoTagCandidates + 1},
		"",
	)
	if err != nil {
		s.tracker().FailSpan(ctx, span, "AUTO_TAG_LIST_TAGS_FAILED", err.Error(), err)
		return fmt.Errorf("list auto tag candidates: %w", err)
	}
	if len(tags) == 0 {
		return skip("no_candidate_tags", nil)
	}
	if len(tags) > maximumAutoTagCandidates {
		return skip("too_many_candidate_tags", types.JSONMap{"candidate_tag_count": len(tags)})
	}

	modelID := strings.TrimSpace(config.ModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(kb.SummaryModelID)
	}
	if modelID == "" {
		return skip("model_not_configured", nil)
	}

	chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, payload.KnowledgeID)
	if err != nil {
		s.tracker().FailSpan(ctx, span, "AUTO_TAG_LIST_CHUNKS_FAILED", err.Error(), err)
		return fmt.Errorf("list chunks for auto tag: %w", err)
	}
	content := buildAutoTagDocumentContent(knowledge, chunks)
	if strings.TrimSpace(content) == "" {
		return skip("no_text_chunks", nil)
	}

	chatModel, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil {
		s.tracker().FailSpan(ctx, span, "AUTO_TAG_MODEL_LOAD_FAILED", err.Error(), err)
		return fmt.Errorf("get auto tag model: %w", err)
	}
	response, err := classifyExistingTags(ctx, chatModel, tags, content, config.MaxTags)
	if err != nil {
		s.tracker().FailSpan(ctx, span, "AUTO_TAG_MODEL_CALL_FAILED", err.Error(), err)
		return err
	}

	validIDs := validateAutoTagMatches(tags, response.Matches, config.MaxTags)
	if len(validIDs) == 0 {
		return skip("no_matching_tags", types.JSONMap{"candidate_tag_count": len(tags), "model_id": modelID})
	}

	existing, err := s.knowledgeRepo.GetKnowledgeTags(ctx, []string{payload.KnowledgeID})
	if err != nil {
		s.tracker().FailSpan(ctx, span, "AUTO_TAG_LOAD_EXISTING_FAILED", err.Error(), err)
		return fmt.Errorf("get existing knowledge tags: %w", err)
	}
	existingIDs := make(map[string]struct{}, len(existing[payload.KnowledgeID]))
	for _, tag := range existing[payload.KnowledgeID] {
		existingIDs[tag.ID] = struct{}{}
	}
	added := make([]string, 0, len(validIDs))
	for _, id := range validIDs {
		if _, ok := existingIDs[id]; !ok {
			added = append(added, id)
		}
	}
	if len(added) == 0 {
		return skip("all_matches_already_attached", types.JSONMap{"matched_tag_count": len(validIDs)})
	}
	adder, ok := s.knowledgeRepo.(interface {
		AddKnowledgeTagRelations(context.Context, uint64, string, string, []string) error
	})
	if !ok {
		err := fmt.Errorf("knowledge repository does not support incremental tag relations")
		s.tracker().FailSpan(ctx, span, "AUTO_TAG_PERSIST_UNSUPPORTED", err.Error(), err)
		return err
	}
	if err := adder.AddKnowledgeTagRelations(
		ctx,
		payload.TenantID,
		payload.KnowledgeBaseID,
		payload.KnowledgeID,
		added,
	); err != nil {
		s.tracker().FailSpan(ctx, span, "AUTO_TAG_PERSIST_FAILED", err.Error(), err)
		return fmt.Errorf("add automatic knowledge tags: %w", err)
	}

	logger.Infof(ctx, "[KnowledgeAutoTag] Added %d tag(s) to knowledge %s", len(added), payload.KnowledgeID)
	s.tracker().EndSpan(ctx, span, types.JSONMap{
		"candidate_tag_count": len(tags),
		"matched_tag_count":   len(validIDs),
		"added_tag_count":     len(added),
		"added_tag_ids":       added,
		"model_id":            modelID,
	})
	return nil
}

func buildAutoTagDocumentContent(knowledge *types.Knowledge, chunks []*types.Chunk) string {
	parts := make([]string, 0, len(chunks)+2)
	if name := strings.TrimSpace(knowledge.FileName); name != "" {
		parts = append(parts, "Document name: "+name)
	}
	if summary := strings.TrimSpace(knowledge.Description); summary != "" {
		parts = append(parts, "Existing summary: "+summary)
	}
	orderedChunks := make([]*types.Chunk, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil {
			orderedChunks = append(orderedChunks, chunk)
		}
	}
	sort.SliceStable(orderedChunks, func(i, j int) bool { return orderedChunks[i].StartAt < orderedChunks[j].StartAt })
	for _, chunk := range orderedChunks {
		if chunk.ChunkType != types.ChunkTypeText &&
			chunk.ChunkType != types.ChunkTypeImageOCR &&
			chunk.ChunkType != types.ChunkTypeImageCaption {
			continue
		}
		if text := strings.TrimSpace(chunk.Content); text != "" {
			parts = append(parts, text)
		}
	}
	return sampleLongContent(strings.Join(parts, "\n\n"), maximumAutoTagContentRunes)
}

func classifyExistingTags(
	ctx context.Context,
	model chat.Chat,
	tags []*types.KnowledgeTag,
	content string,
	maxTags int,
) (*autoTagModelResponse, error) {
	if maxTags <= 0 {
		maxTags = defaultAutoTagMaxTags
	}
	if maxTags > maximumAutoTagMaxTags {
		maxTags = maximumAutoTagMaxTags
	}
	candidates := make([]string, 0, len(tags))
	for _, tag := range tags {
		candidates = append(candidates, fmt.Sprintf("- id=%s name=%q", tag.ID, tag.Name))
	}
	systemPrompt := fmt.Sprintf(`You classify one document using only the supplied existing tags.
Return strict JSON only: {"matches":[{"tag_id":"candidate-id","confidence":0.0,"reason":"short reason"}]}.
Rules: never invent a tag; return an empty matches array when uncertain; confidence must be between 0 and 1.
Choose at most %d tags.`, maxTags)
	userPrompt := "Candidate tags:\n" + strings.Join(candidates, "\n") + "\n\nDocument:\n" + content
	thinking := false
	result, err := model.Chat(types.WithLLMCallMetadata(ctx, "document_auto_tag", ""), []chat.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, &chat.ChatOptions{Temperature: 0.1, MaxTokens: 1024, Thinking: &thinking})
	if err != nil {
		return nil, fmt.Errorf("classify automatic tags: %w", err)
	}
	var parsed autoTagModelResponse
	if err := json.Unmarshal([]byte(stripJSONCodeFence(result.Content)), &parsed); err != nil {
		return nil, fmt.Errorf("parse automatic tag response: %w", err)
	}
	return &parsed, nil
}

func stripJSONCodeFence(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```") {
		lines := strings.Split(value, "\n")
		if len(lines) >= 3 &&
			strings.HasPrefix(strings.TrimSpace(lines[0]), "```") &&
			strings.TrimSpace(lines[len(lines)-1]) == "```" {
			value = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	// Be resilient to models that wrap JSON with a short explanation.
	start := strings.IndexAny(value, "{[")
	end := strings.LastIndexAny(value, "}]")
	if start >= 0 && end >= start {
		value = value[start : end+1]
	}
	return strings.TrimSpace(value)
}

func validateAutoTagMatches(tags []*types.KnowledgeTag, matches []autoTagModelMatch, maxTags int) []string {
	if maxTags <= 0 {
		maxTags = defaultAutoTagMaxTags
	}
	if maxTags > maximumAutoTagMaxTags {
		maxTags = maximumAutoTagMaxTags
	}
	allowed := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		allowed[tag.ID] = struct{}{}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Confidence > matches[j].Confidence })
	seen := make(map[string]struct{}, len(matches))
	result := make([]string, 0, maxTags)
	for _, match := range matches {
		if match.Confidence < minimumAutoTagConfidence {
			continue
		}
		if _, ok := allowed[match.TagID]; !ok {
			continue
		}
		if _, ok := seen[match.TagID]; ok {
			continue
		}
		seen[match.TagID] = struct{}{}
		result = append(result, match.TagID)
		if len(result) == maxTags {
			break
		}
	}
	return result
}
