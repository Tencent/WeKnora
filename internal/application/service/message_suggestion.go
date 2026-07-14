package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var suggestionThinkBlock = regexp.MustCompile(`(?s)<think>.*?</think>`)
var trailingCitationTags = regexp.MustCompile(`(?s)(?:\s*<(?:kb|web)>.*?</(?:kb|web)>)+\s*$`)

type messageSuggestionService struct {
	repo               interfaces.MessageSuggestionRepository
	messageService     interfaces.MessageService
	modelService       interfaces.ModelService
	customAgentService interfaces.CustomAgentService
}

func NewMessageSuggestionService(
	repo interfaces.MessageSuggestionRepository,
	messageService interfaces.MessageService,
	modelService interfaces.ModelService,
	customAgentService interfaces.CustomAgentService,
) interfaces.MessageSuggestionService {
	return &messageSuggestionService{
		repo:               repo,
		messageService:     messageService,
		modelService:       modelService,
		customAgentService: customAgentService,
	}
}

func (s *messageSuggestionService) EnsureFollowUps(
	ctx context.Context,
	sessionID string,
	assistantMessageID string,
	regenerate bool,
) (*types.MessageSuggestionSet, error) {
	message, err := s.messageService.GetMessage(ctx, sessionID, assistantMessageID)
	if err != nil {
		return nil, err
	}
	if message.Role != "assistant" || !message.IsCompleted {
		return nil, errors.New("follow-up suggestions require a completed assistant message")
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	locale := message.ExecutionContext.Locale
	if locale == "" {
		locale, _ = types.LanguageFromContext(ctx)
	}
	if locale == "" {
		locale = types.DefaultLanguage()
	}
	configHash := message.ExecutionContext.AgentConfigHash
	if configHash == "" {
		configHash = "no-agent-config"
	}
	config := message.ExecutionContext.QuestionSuggestions
	if regenerate && (config == nil || !config.FollowUps.Enabled || !config.FollowUps.AllowRegenerate) {
		return nil, errors.New("suggestion regeneration is not allowed")
	}
	candidate := &types.MessageSuggestionSet{
		TenantID:           tenantID,
		SessionID:          sessionID,
		AssistantMessageID: assistantMessageID,
		AgentID:            message.AgentID,
		AgentTenantID:      message.AgentTenantID,
		Placement:          types.SuggestionPlacementAfterAnswer,
		ConfigHash:         configHash,
		Locale:             locale,
		AllowRegenerate:    config != nil && config.FollowUps.AllowRegenerate,
	}
	set, acquired, err := s.repo.AcquireGeneration(ctx, candidate, regenerate)
	if err != nil || !acquired {
		return set, err
	}

	if config == nil || !config.FollowUps.Enabled {
		return s.suppress(ctx, set, "disabled")
	}
	if message.IsFallback && config.FollowUps.SuppressOnFallback {
		return s.suppress(ctx, set, "fallback_answer")
	}
	answer := strings.TrimSpace(suggestionThinkBlock.ReplaceAllString(message.Content, ""))
	if answer == "" {
		return s.suppress(ctx, set, "empty_answer")
	}
	if config.FollowUps.SuppressWhenAnswerAsksQuestion && answerEndsWithQuestion(answer) {
		return s.suppress(ctx, set, "answer_asks_question")
	}

	startedAt := time.Now()
	set.ModelID = config.FollowUps.ModelID
	if set.ModelID == "" {
		set.ModelID = message.ModelID
	}
	questions, usage, generateErr := s.generate(
		ctx,
		message,
		answer,
		config.FollowUps,
	)
	set.LatencyMs = time.Since(startedAt).Milliseconds()
	set.PromptTokens = usage.PromptTokens
	set.CompletionTokens = usage.CompletionTokens
	set.LeaseUntil = nil
	generatedAt := time.Now()
	set.GeneratedAt = &generatedAt

	if generateErr != nil {
		set.Status = types.SuggestionStatusFailed
		set.ErrorCode = suggestionErrorCode(generateErr)
		if saveErr := s.repo.Save(ctx, set); saveErr != nil {
			return nil, saveErr
		}
		logger.ErrorWithFields(ctx, generateErr, map[string]interface{}{
			"session_id": sessionID,
			"message_id": assistantMessageID,
			"set_id":     set.ID,
		})
		return set, nil
	}
	if len(questions) == 0 {
		return s.suppress(ctx, set, "no_candidates")
	}
	set.Questions = questions
	set.Status = types.SuggestionStatusReady
	set.ErrorCode = ""
	if err := s.repo.Save(ctx, set); err != nil {
		return nil, err
	}
	if regenerate {
		_ = s.createEvent(ctx, set, "", types.SuggestionEventRegenerate)
	}
	return set, nil
}

func (s *messageSuggestionService) GetFollowUps(
	ctx context.Context,
	sessionID string,
	assistantMessageID string,
) (*types.MessageSuggestionSet, error) {
	message, err := s.messageService.GetMessage(ctx, sessionID, assistantMessageID)
	if err != nil {
		return nil, err
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	locale := message.ExecutionContext.Locale
	if locale == "" {
		locale, _ = types.LanguageFromContext(ctx)
	}
	if locale == "" {
		locale = types.DefaultLanguage()
	}
	configHash := message.ExecutionContext.AgentConfigHash
	if configHash == "" {
		configHash = "no-agent-config"
	}
	return s.repo.GetByCacheKey(
		ctx,
		tenantID,
		assistantMessageID,
		types.SuggestionPlacementAfterAnswer,
		configHash,
		locale,
	)
}

func (s *messageSuggestionService) RecordEvent(
	ctx context.Context,
	sessionID string,
	setID string,
	questionID string,
	eventType string,
) error {
	if eventType != types.SuggestionEventImpression &&
		eventType != types.SuggestionEventClick &&
		eventType != types.SuggestionEventDismiss {
		return errors.New("invalid suggestion event type")
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	set, err := s.repo.GetByID(ctx, tenantID, sessionID, setID)
	if err != nil {
		return err
	}
	if questionID != "" && !containsSuggestionID(set.Questions, questionID) {
		return errors.New("question does not belong to suggestion set")
	}
	if eventType == types.SuggestionEventClick && questionID == "" {
		return errors.New("click event requires question_id")
	}
	return s.createEvent(ctx, set, questionID, eventType)
}

func (s *messageSuggestionService) ValidateAttribution(
	ctx context.Context,
	sessionID string,
	query string,
	attribution *types.SuggestionAttribution,
) error {
	if attribution == nil {
		return nil
	}
	if strings.TrimSpace(attribution.SuggestionSetID) == "" || strings.TrimSpace(attribution.QuestionID) == "" {
		return errors.New("invalid suggestion attribution")
	}
	set, err := s.repo.GetByID(
		ctx,
		types.MustTenantIDFromContext(ctx),
		sessionID,
		attribution.SuggestionSetID,
	)
	if err != nil {
		return err
	}
	if set.Status != types.SuggestionStatusReady {
		return errors.New("invalid suggestion attribution")
	}
	found := false
	for _, question := range set.Questions {
		if question.ID == attribution.QuestionID && strings.TrimSpace(question.Text) == strings.TrimSpace(query) {
			found = true
			break
		}
	}
	if !found {
		return errors.New("invalid suggestion attribution")
	}
	return nil
}

func (s *messageSuggestionService) generate(
	ctx context.Context,
	message *types.Message,
	answer string,
	config types.FollowUpSuggestionConfig,
) (types.SuggestionItems, types.TokenUsage, error) {
	count := config.Count
	if count < 1 {
		count = 3
	}
	var generated types.SuggestionItems
	var usage types.TokenUsage
	var modelErr error
	if config.Mode == types.SuggestionModeGenerated || config.Mode == types.SuggestionModeHybrid {
		generated, usage, modelErr = s.generateWithModel(ctx, message, answer, config, count)
	}

	needKnowledge := config.Mode == types.SuggestionModeKnowledge ||
		(config.Mode == types.SuggestionModeHybrid && len(generated) < count) ||
		(modelErr != nil && config.KnowledgeFallback)
	if needKnowledge {
		knowledge, err := s.generateFromKnowledge(ctx, message, count-len(generated))
		if err != nil && modelErr == nil {
			modelErr = err
		}
		generated = mergeSuggestionItems(generated, knowledge, count)
	}
	if len(generated) > 0 {
		return generated, usage, nil
	}
	if modelErr != nil {
		return nil, usage, modelErr
	}
	return generated, usage, nil
}

func (s *messageSuggestionService) generateWithModel(
	ctx context.Context,
	message *types.Message,
	answer string,
	config types.FollowUpSuggestionConfig,
	count int,
) (types.SuggestionItems, types.TokenUsage, error) {
	modelID := config.ModelID
	if modelID == "" {
		modelID = message.ModelID
	}
	if modelID == "" {
		return nil, types.TokenUsage{}, errors.New("suggestion model is not configured")
	}

	modelCtx := ctx
	if message.AgentTenantID != 0 {
		modelCtx = context.WithValue(modelCtx, types.TenantIDContextKey, message.AgentTenantID)
	}
	chatModel, err := s.modelService.GetChatModel(modelCtx, modelID)
	if err != nil {
		return nil, types.TokenUsage{}, err
	}
	history, err := s.buildHistory(ctx, message.SessionID, config.MaxContextTurns)
	if err != nil {
		return nil, types.TokenUsage{}, err
	}
	categories := strings.Join(config.Categories, ", ")
	if categories == "" {
		categories = "clarify, deepen, action"
	}
	language := types.LanguageLocaleName(message.ExecutionContext.Locale)
	systemPrompt := fmt.Sprintf(
		"You generate exactly %d short follow-up questions after an assistant answer. "+
			"Return JSON only as {\"questions\":[{\"text\":\"...\",\"category\":\"...\"}]}. "+
			"Use %s. Allowed categories: %s. Questions must be answerable from the conversation, "+
			"must not repeat prior user questions, must not claim unavailable capabilities, and must not include numbering.",
		count, language, categories,
	)
	if instruction := strings.TrimSpace(config.AdditionalInstruction); instruction != "" {
		systemPrompt += " Additional agent instruction: " + instruction
	}
	userPrompt := "Conversation:\n" + history + "\n\nLatest assistant answer:\n" + truncateRunes(answer, 6000)
	thinking := false
	response, err := chatModel.Chat(modelCtx, []chat.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, &chat.ChatOptions{
		Temperature:         0.3,
		MaxCompletionTokens: 700,
		Thinking:            &thinking,
	})
	if err != nil {
		return nil, types.TokenUsage{}, err
	}
	items, err := parseGeneratedSuggestions(response.Content, config.Categories, count)
	return items, response.Usage, err
}

func (s *messageSuggestionService) generateFromKnowledge(
	ctx context.Context,
	message *types.Message,
	count int,
) (types.SuggestionItems, error) {
	if count <= 0 || message.AgentID == "" {
		return types.SuggestionItems{}, nil
	}
	knowledgeCtx := ctx
	if message.AgentTenantID != 0 {
		knowledgeCtx = context.WithValue(knowledgeCtx, types.TenantIDContextKey, message.AgentTenantID)
	}
	candidates, err := s.customAgentService.GetKnowledgeSuggestedQuestions(
		knowledgeCtx,
		message.AgentID,
		message.ExecutionContext.KnowledgeBaseIDs,
		message.ExecutionContext.KnowledgeIDs,
		message.ExecutionContext.TagIDs,
		count,
	)
	if err != nil {
		return nil, err
	}
	items := make(types.SuggestionItems, 0, len(candidates))
	for _, candidate := range candidates {
		text := strings.TrimSpace(candidate.Question)
		if text == "" {
			continue
		}
		item := types.SuggestionItem{
			ID:     uuid.NewString(),
			Text:   text,
			Source: candidate.Source,
		}
		if candidate.KnowledgeBaseID != "" {
			item.KnowledgeBaseIDs = []string{candidate.KnowledgeBaseID}
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *messageSuggestionService) buildHistory(ctx context.Context, sessionID string, maxTurns int) (string, error) {
	if maxTurns < 1 {
		maxTurns = 2
	}
	messages, err := s.messageService.GetRecentMessagesBySession(ctx, sessionID, maxTurns*2+4)
	if err != nil {
		return "", err
	}
	start := 0
	if len(messages) > maxTurns*2 {
		start = len(messages) - maxTurns*2
	}
	var builder strings.Builder
	for _, message := range messages[start:] {
		content := strings.TrimSpace(suggestionThinkBlock.ReplaceAllString(message.Content, ""))
		if content == "" {
			continue
		}
		builder.WriteString(message.Role)
		builder.WriteString(": ")
		builder.WriteString(truncateRunes(content, 3000))
		builder.WriteByte('\n')
	}
	return builder.String(), nil
}

func (s *messageSuggestionService) suppress(
	ctx context.Context,
	set *types.MessageSuggestionSet,
	reason string,
) (*types.MessageSuggestionSet, error) {
	set.Status = types.SuggestionStatusSuppressed
	set.SuppressionReason = reason
	set.Questions = types.SuggestionItems{}
	set.LeaseUntil = nil
	now := time.Now()
	set.GeneratedAt = &now
	if err := s.repo.Save(ctx, set); err != nil {
		return nil, err
	}
	return set, nil
}

func (s *messageSuggestionService) createEvent(
	ctx context.Context,
	set *types.MessageSuggestionSet,
	questionID string,
	eventType string,
) error {
	actorID := types.SessionOwnerIDFromContext(ctx)
	if principal, ok := types.PrincipalFromContext(ctx); ok {
		actorID = principal.StorageID()
	}
	return s.repo.CreateEvent(ctx, &types.MessageSuggestionEvent{
		TenantID:        set.TenantID,
		SessionID:       set.SessionID,
		SuggestionSetID: set.ID,
		QuestionID:      questionID,
		EventType:       eventType,
		ActorID:         actorID,
	})
}

type generatedSuggestionEnvelope struct {
	Questions []struct {
		Text     string `json:"text"`
		Category string `json:"category"`
	} `json:"questions"`
}

func parseGeneratedSuggestions(content string, allowedCategories []string, limit int) (types.SuggestionItems, error) {
	content = strings.TrimSpace(suggestionThinkBlock.ReplaceAllString(content, ""))
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return nil, errors.New("model returned invalid suggestion JSON")
	}
	var envelope generatedSuggestionEnvelope
	if err := json.Unmarshal([]byte(content[start:end+1]), &envelope); err != nil {
		return nil, fmt.Errorf("decode suggestion JSON: %w", err)
	}
	allowed := make(map[string]struct{}, len(allowedCategories))
	for _, category := range allowedCategories {
		allowed[category] = struct{}{}
	}
	seen := make(map[string]struct{})
	items := make(types.SuggestionItems, 0, limit)
	for _, question := range envelope.Questions {
		text := strings.TrimSpace(question.Text)
		if text == "" || len([]rune(text)) > 200 {
			continue
		}
		key := normalizeSuggestionText(text)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		category := question.Category
		if len(allowed) > 0 {
			if _, ok := allowed[category]; !ok {
				category = ""
			}
		}
		items = append(items, types.SuggestionItem{
			ID:       uuid.NewString(),
			Text:     text,
			Category: category,
			Source:   "model",
		})
		if len(items) == limit {
			break
		}
	}
	return items, nil
}

func mergeSuggestionItems(primary, fallback types.SuggestionItems, limit int) types.SuggestionItems {
	result := make(types.SuggestionItems, 0, limit)
	seen := make(map[string]struct{})
	for _, group := range []types.SuggestionItems{primary, fallback} {
		for _, item := range group {
			key := normalizeSuggestionText(item.Text)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, item)
			if len(result) == limit {
				return result
			}
		}
	}
	return result
}

func normalizeSuggestionText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("?？!！,，.。:：;；\"'", r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(value))
}

func containsSuggestionID(items types.SuggestionItems, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func answerEndsWithQuestion(answer string) bool {
	answer = strings.TrimSpace(trailingCitationTags.ReplaceAllString(answer, ""))
	return strings.HasSuffix(answer, "?") || strings.HasSuffix(answer, "？")
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func suggestionErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "not_found"
	}
	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "model"):
		return "model_error"
	case strings.Contains(value, "json"):
		return "invalid_model_output"
	default:
		return "generation_error"
	}
}
