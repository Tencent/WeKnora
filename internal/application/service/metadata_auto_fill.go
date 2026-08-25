package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

const metadataExtractionContentLimit = 60000

type metadataAutoFillService struct {
	repo             interfaces.KnowledgeMetadataRepository
	metadataService  interfaces.KnowledgeMetadataService
	knowledgeRepo    interfaces.KnowledgeRepository
	knowledgeBaseSvc interfaces.KnowledgeBaseService
	chunkService     interfaces.ChunkService
	modelService     interfaces.ModelService
	taskEnqueuer     interfaces.TaskEnqueuer
}

func NewMetadataAutoFillService(
	repo interfaces.KnowledgeMetadataRepository,
	metadataService interfaces.KnowledgeMetadataService,
	knowledgeRepo interfaces.KnowledgeRepository,
	knowledgeBaseSvc interfaces.KnowledgeBaseService,
	chunkService interfaces.ChunkService,
	modelService interfaces.ModelService,
	taskEnqueuer interfaces.TaskEnqueuer,
) interfaces.MetadataAutoFillService {
	return &metadataAutoFillService{
		repo: repo, metadataService: metadataService, knowledgeRepo: knowledgeRepo,
		knowledgeBaseSvc: knowledgeBaseSvc, chunkService: chunkService,
		modelService: modelService, taskEnqueuer: taskEnqueuer,
	}
}

func (s *metadataAutoFillService) Enqueue(
	ctx context.Context,
	payload types.MetadataAutoFillPayload,
) (string, error) {
	knowledge, err := s.knowledgeRepo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		return "", err
	}
	if payload.KnowledgeBaseID == "" {
		payload.KnowledgeBaseID = knowledge.KnowledgeBaseID
	} else if knowledge.KnowledgeBaseID != payload.KnowledgeBaseID {
		return "", fmt.Errorf("knowledge does not belong to metadata knowledge base")
	}
	definitions, err := s.repo.ListDefinitions(ctx, payload.TenantID, payload.KnowledgeBaseID, false)
	if err != nil {
		return "", err
	}
	ruleVersions := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if definition.AutoRule != nil && definition.AutoRule.Enabled {
			ruleVersions = append(ruleVersions, fmt.Sprintf(
				"%s:%d",
				definition.AutoRule.ID,
				definition.AutoRule.Revision,
			))
		}
	}
	if len(ruleVersions) == 0 {
		return "", nil
	}
	sort.Strings(ruleVersions)
	ruleFingerprint := sha256.Sum256([]byte(strings.Join(ruleVersions, ",")))
	taskID := fmt.Sprintf(
		"metadata-auto-fill:%d:%s:%s:%x",
		payload.TenantID,
		payload.KnowledgeBaseID,
		payload.KnowledgeID,
		ruleFingerprint,
	)
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal metadata auto-fill payload: %w", err)
	}
	task := asynq.NewTask(types.TypeMetadataAutoFill, payloadBytes)
	info, err := s.taskEnqueuer.Enqueue(
		task,
		asynq.Queue(types.QueueMaintenance),
		asynq.MaxRetry(3),
		asynq.TaskID(taskID),
	)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return taskID, nil
	}
	if err != nil {
		return "", fmt.Errorf("enqueue metadata auto-fill: %w", err)
	}
	if info != nil && info.ID != "" {
		return info.ID, nil
	}
	return taskID, nil
}

func (s *metadataAutoFillService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.MetadataAutoFillPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal metadata auto-fill payload: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	knowledge, err := s.knowledgeRepo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if errors.Is(err, repository.ErrKnowledgeNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if knowledge.KnowledgeBaseID != payload.KnowledgeBaseID {
		return asynq.SkipRetry
	}
	definitions, err := s.repo.ListDefinitions(ctx, payload.TenantID, payload.KnowledgeBaseID, false)
	if err != nil {
		return err
	}

	sourceResults := automaticSourceMappingResults(knowledge, definitions)
	if _, err := s.apply(ctx, payload, sourceResults); err != nil {
		return err
	}

	groups := make(map[string][]*types.MetadataDefinition)
	knowledgeBase, err := s.knowledgeBaseSvc.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if definition.AutoRule == nil || !definition.AutoRule.Enabled ||
			definition.AutoRule.Strategy != types.MetadataRuleStrategyLLMExtract {
			continue
		}
		modelID, _ := definition.AutoRule.Config["model_id"].(string)
		if strings.TrimSpace(modelID) == "" {
			modelID = knowledgeBase.SummaryModelID
		}
		groups[modelID] = append(groups[modelID], definition)
	}
	if len(groups) == 0 {
		return nil
	}

	content, err := s.documentContent(ctx, payload.KnowledgeID)
	if err != nil {
		return err
	}
	var groupErrors []error
	for modelID, group := range groups {
		if strings.TrimSpace(modelID) == "" {
			groupErrors = append(groupErrors, errors.New("metadata extraction model is not configured"))
			continue
		}
		results, extractErr := s.extractWithLLM(ctx, modelID, knowledge, content, group)
		if extractErr != nil {
			groupErrors = append(groupErrors, extractErr)
			continue
		}
		if _, applyErr := s.apply(ctx, payload, results); applyErr != nil {
			groupErrors = append(groupErrors, applyErr)
		}
	}
	return errors.Join(groupErrors...)
}

func (s *metadataAutoFillService) apply(
	ctx context.Context,
	payload types.MetadataAutoFillPayload,
	results []types.AutomaticMetadataResult,
) (*types.ApplyAutomaticMetadataReport, error) {
	if len(results) == 0 {
		return &types.ApplyAutomaticMetadataReport{}, nil
	}
	report, err := s.metadataService.ApplyAutomaticResults(ctx, types.ApplyAutomaticMetadataResults{
		TenantID: payload.TenantID, KnowledgeBaseID: payload.KnowledgeBaseID,
		KnowledgeID: payload.KnowledgeID, Results: results,
	})
	if err == nil {
		logger.Infof(ctx, "[MetadataAutoFill] knowledge=%s trigger=%s applied=%d skipped=%d invalid=%d",
			payload.KnowledgeID, payload.Trigger, report.Applied, report.Skipped, report.Invalid)
	}
	return report, err
}

func (s *metadataAutoFillService) documentContent(ctx context.Context, knowledgeID string) (string, error) {
	chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, knowledgeID)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.Content) != "" {
			parts = append(parts, chunk.Content)
		}
	}
	runes := []rune(strings.Join(parts, "\n\n"))
	if len(runes) > metadataExtractionContentLimit {
		runes = runes[:metadataExtractionContentLimit]
	}
	return string(runes), nil
}

func (s *metadataAutoFillService) extractWithLLM(
	ctx context.Context,
	modelID string,
	knowledge *types.Knowledge,
	content string,
	definitions []*types.MetadataDefinition,
) ([]types.AutomaticMetadataResult, error) {
	model, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil {
		return nil, err
	}
	definitionPayload := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		options := make([]map[string]string, 0, len(definition.Options))
		for _, option := range definition.Options {
			if option.Status == types.MetadataStatusActive {
				options = append(options, map[string]string{"id": option.ID, "label": option.Label})
			}
		}
		definitionPayload = append(definitionPayload, map[string]any{
			"id": definition.ID, "name": definition.Name, "description": definition.Description,
			"type": definition.ValueType, "instruction": definition.AutoRule.Config["instruction"],
			"options": options,
		})
	}
	definitionJSON, _ := json.Marshal(definitionPayload)
	format := json.RawMessage(`{"type":"object","additionalProperties":true}`)
	response, err := model.Chat(ctx, []chat.Message{
		{Role: "system", Content: "Extract document metadata. Return one JSON object keyed by definition ID. Use option IDs for select fields, YYYY-MM-DD for dates, JSON numbers and booleans. Omit fields that cannot be determined."},
		{Role: "user", Content: fmt.Sprintf("Title: %s\nDefinitions: %s\nDocument:\n%s", knowledge.Title, definitionJSON, content)},
	}, &chat.ChatOptions{Temperature: 0, Format: format})
	if err != nil {
		return nil, err
	}
	values, err := decodeMetadataExtraction(response.Content)
	if err != nil {
		return nil, err
	}
	results := make([]types.AutomaticMetadataResult, 0, len(definitions))
	for _, definition := range definitions {
		value, ok := values[definition.ID]
		if !ok {
			continue
		}
		results = append(results, types.AutomaticMetadataResult{
			MetadataDefinitionID: definition.ID, Value: value,
			AutoRuleID: definition.AutoRule.ID, AutoRuleRevision: definition.AutoRule.Revision,
		})
	}
	return results, nil
}

func automaticSourceMappingResults(
	knowledge *types.Knowledge,
	definitions []*types.MetadataDefinition,
) []types.AutomaticMetadataResult {
	metadata := knowledge.GetMetadata()
	results := make([]types.AutomaticMetadataResult, 0)
	for _, definition := range definitions {
		rule := definition.AutoRule
		if rule == nil || !rule.Enabled || rule.Strategy != types.MetadataRuleStrategySourceMapping {
			continue
		}
		sourceKey, _ := rule.Config["source_key"].(string)
		raw := metadata[sourceKey]
		switch sourceKey {
		case "title":
			raw = knowledge.Title
		case "source":
			raw = knowledge.Source
		case "file_name":
			raw = knowledge.FileName
		case "file_type":
			raw = knowledge.FileType
		case "channel":
			raw = knowledge.Channel
		}
		value, ok := sourceValueForDefinition(raw, definition)
		if !ok {
			continue
		}
		results = append(results, types.AutomaticMetadataResult{
			MetadataDefinitionID: definition.ID, Value: value,
			AutoRuleID: rule.ID, AutoRuleRevision: rule.Revision,
		})
	}
	return results
}

func sourceValueForDefinition(raw string, definition *types.MetadataDefinition) (any, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	switch definition.ValueType {
	case types.MetadataValueTypeText, types.MetadataValueTypeDate:
		return raw, true
	case types.MetadataValueTypeNumber:
		value, err := strconv.ParseFloat(raw, 64)
		return value, err == nil
	case types.MetadataValueTypeBoolean:
		value, err := strconv.ParseBool(strings.ToLower(raw))
		return value, err == nil
	case types.MetadataValueTypeSingleSelect:
		return metadataOptionID(raw, definition)
	case types.MetadataValueTypeMultiSelect:
		parts := strings.Split(raw, ",")
		ids := make([]string, 0, len(parts))
		for _, part := range parts {
			id, ok := metadataOptionID(strings.TrimSpace(part), definition)
			if !ok {
				return nil, false
			}
			ids = append(ids, id)
		}
		return ids, len(ids) > 0
	default:
		return nil, false
	}
}

func metadataOptionID(raw string, definition *types.MetadataDefinition) (string, bool) {
	for _, option := range definition.Options {
		if option.Status == types.MetadataStatusActive &&
			(option.ID == raw || strings.EqualFold(option.Label, raw)) {
			return option.ID, true
		}
	}
	return "", false
}

func decodeMetadataExtraction(content string) (map[string]any, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(strings.TrimSpace(content), "```")
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("decode metadata extraction: %w", err)
	}
	return values, nil
}
