package chatpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginQueryUnderstand performs query rewriting and response/evidence classification.
// It uses conversation history and an LLM to optimise the user's original query
// and determine the downstream pipeline behaviour.
type PluginQueryUnderstand struct {
	modelService   interfaces.ModelService
	messageService interfaces.MessageService
	config         *config.Config
}

var rewriteImageSepPattern = regexp.MustCompile(`(?s)^(.*?)\s*\n?---\n(.*)$`)

type queryUnderstandOutput struct {
	RewriteQuery      string                     `json:"rewrite_query"`
	ResponseMode      types.ResponseMode         `json:"response_mode"`
	RetrievalNeed     types.RetrievalNeed        `json:"retrieval_need"`
	SourceRequirement types.SourceRequirement    `json:"source_requirement"`
	Freshness         types.FreshnessRequirement `json:"freshness"`
	ImageDescription  string                     `json:"image_description"`
}

// NewPluginQueryUnderstand creates a new query-understanding plugin instance
// and registers it with the event manager.
func NewPluginQueryUnderstand(eventManager *EventManager,
	modelService interfaces.ModelService, messageService interfaces.MessageService,
	config *config.Config,
) *PluginQueryUnderstand {
	res := &PluginQueryUnderstand{
		modelService:   modelService,
		messageService: messageService,
		config:         config,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the list of event types this plugin responds to.
func (p *PluginQueryUnderstand) ActivationEvents() []types.EventType {
	return []types.EventType{types.QUERY_UNDERSTAND}
}

// OnEvent processes triggered events.
// Handles three input combinations:
//   - Text only: standard rewrite + response/evidence classification (uses chat model)
//   - Text + images: multimodal rewrite + semantic routing + image description (uses VLM/vision model)
//   - Images only: multimodal analysis + semantic routing + image description (uses VLM/vision model)
func (p *PluginQueryUnderstand) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	chatManage.RewriteQuery = chatManage.Query
	chatManage.Understanding = types.DefaultQueryUnderstanding()
	p.resolveRetrievalPlan(chatManage)

	hasImages := len(chatManage.Images) > 0

	pipelineInfo(ctx, "QueryUnderstand", "input", map[string]interface{}{
		"session_id":     chatManage.SessionID,
		"tenant_id":      chatManage.TenantID,
		"user_query":     chatManage.Query,
		"has_images":     hasImages,
		"enable_rewrite": chatManage.EnableRewrite,
	})

	// --- Load and prepare conversation history ---
	var historyList []*types.History
	if len(chatManage.History) > 0 {
		historyList = chatManage.History
		pipelineInfo(ctx, "QueryUnderstand", "history_reused", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"rounds":     len(historyList),
		})
	} else {
		historyList = p.loadHistory(ctx, chatManage)
	}

	// --- Select the appropriate model ---
	rewriteModel, useImages := p.selectModel(ctx, chatManage, hasImages)
	if rewriteModel == nil {
		pipelineError(ctx, "QueryUnderstand", "get_model", map[string]interface{}{
			"session_id": chatManage.SessionID,
		})
		p.applyManagedResponsePrompt(ctx, chatManage)
		return next()
	}

	// --- Build prompts ---
	systemContent, userContent := p.buildPrompts(chatManage, historyList)

	userMsg := chat.Message{Role: "user", Content: userContent}
	if useImages {
		userMsg.Images = chatManage.Images
	}

	maxTokens := 250
	if useImages {
		maxTokens = 600
	}

	// --- Call model ---
	thinking := false
	response, err := rewriteModel.Chat(ctx, []chat.Message{
		{Role: "system", Content: systemContent},
		userMsg,
	}, &chat.ChatOptions{
		Temperature:         0.3,
		MaxCompletionTokens: maxTokens,
		Thinking:            &thinking,
	})
	if err != nil {
		pipelineError(ctx, "QueryUnderstand", "model_call", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"error":      err.Error(),
		})
		p.applyManagedResponsePrompt(ctx, chatManage)
		return next()
	}

	// --- Parse structured output ---
	p.parseOutput(chatManage, response.Content)
	p.resolveRetrievalPlan(chatManage)

	// Persist image description asynchronously — this DB write does not affect
	// the current pipeline result, so it can run in the background.
	if chatManage.ImageDescription != "" && chatManage.UserMessageID != "" {
		go p.updateUserMessageImageCaption(context.WithoutCancel(ctx), chatManage)
	}

	// --- Apply response-mode or source-unavailable system prompt override ---
	p.applyManagedResponsePrompt(ctx, chatManage)

	pipelineInfo(ctx, "QueryUnderstand", "output", map[string]interface{}{
		"session_id":                  chatManage.SessionID,
		"rewrite_query":               chatManage.RewriteQuery,
		"response_mode":               chatManage.Understanding.ResponseMode,
		"retrieval_need":              chatManage.Understanding.RetrievalNeed,
		"source_requirement":          chatManage.Understanding.SourceRequirement,
		"freshness":                   chatManage.Understanding.Freshness,
		"retrieval_plan":              chatManage.RetrievalPlan.Mode,
		"plan_reason":                 chatManage.RetrievalPlan.ReasonCode,
		"has_image_desc":              chatManage.ImageDescription != "",
		"has_managed_response_prompt": chatManage.ManagedResponsePrompt != "",
		"original_output":             response.Content,
	})
	return next()
}

func (p *PluginQueryUnderstand) applyManagedResponsePrompt(ctx context.Context, chatManage *types.ChatManage) {
	if chatManage.NeedsRetrieval() {
		return
	}
	if applyManagedResponsePrompt(chatManage, p.config.Conversation.ResponseModePrompts) {
		pipelineInfo(ctx, "QueryUnderstand", "managed_response_prompt", map[string]interface{}{
			"session_id":    chatManage.SessionID,
			"response_mode": chatManage.Understanding.ResponseMode,
			"plan_reason":   chatManage.RetrievalPlan.ReasonCode,
		})
	}
}

// updateUserMessageImageCaption writes the generated ImageDescription back to
// the stored user message so that subsequent turns can see it in history.
func (p *PluginQueryUnderstand) updateUserMessageImageCaption(ctx context.Context, chatManage *types.ChatManage) {
	msg, err := p.messageService.GetMessage(ctx, chatManage.SessionID, chatManage.UserMessageID)
	if err != nil {
		pipelineWarn(ctx, "QueryUnderstand", "get_user_message", map[string]interface{}{
			"session_id":      chatManage.SessionID,
			"user_message_id": chatManage.UserMessageID,
			"error":           err.Error(),
		})
		return
	}

	if len(msg.Images) == 0 {
		return
	}

	msg.Images[0].Caption = chatManage.ImageDescription

	if err := p.messageService.UpdateMessageImages(ctx, chatManage.SessionID, chatManage.UserMessageID, msg.Images); err != nil {
		pipelineWarn(ctx, "QueryUnderstand", "update_image_caption", map[string]interface{}{
			"session_id":      chatManage.SessionID,
			"user_message_id": chatManage.UserMessageID,
			"error":           err.Error(),
		})
	}
}

// loadHistory fetches and processes conversation history for rewrite context.
func (p *PluginQueryUnderstand) loadHistory(ctx context.Context, chatManage *types.ChatManage) []*types.History {
	// Honor the multi-turn-disabled signal: chatManage.MaxRounds == 0 is set
	// explicitly by applyAgentOverridesToChatManage when the custom agent has
	// MultiTurnEnabled=false. We must not silently fall back to the global
	// default, otherwise rewrite + image analysis would still pull old turns
	// into the context and leak through chatManage.History.
	if chatManage.MaxRounds <= 0 {
		return nil
	}
	maxRounds := chatManage.MaxRounds

	historyList, err := loadAndProcessHistory(ctx, p.messageService, chatManage.SessionID, maxRounds, 20)
	if err != nil {
		pipelineWarn(ctx, "QueryUnderstand", "history_fetch", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"error":      err.Error(),
		})
		return nil
	}

	chatManage.History = historyList

	if len(historyList) > 0 {
		pipelineInfo(ctx, "QueryUnderstand", "history_ready", map[string]interface{}{
			"session_id":     chatManage.SessionID,
			"history_rounds": len(historyList),
		})
	}

	return historyList
}

// selectModel picks the model for query understanding. When images are present
// it prefers a vision-capable model. Returns (model, useImages).
func (p *PluginQueryUnderstand) selectModel(ctx context.Context, chatManage *types.ChatManage, hasImages bool) (chat.Chat, bool) {
	if hasImages {
		if chatManage.ChatModelSupportsVision {
			m, err := p.modelService.GetChatModel(ctx, chatManage.ChatModelID)
			if err == nil {
				return m, true
			}
			pipelineWarn(ctx, "QueryUnderstand", "vision_model_fallback", map[string]interface{}{
				"session_id": chatManage.SessionID,
				"error":      err.Error(),
			})
		}
		if chatManage.VLMModelID != "" {
			m, err := p.modelService.GetChatModel(ctx, chatManage.VLMModelID)
			if err == nil {
				return m, true
			}
			pipelineWarn(ctx, "QueryUnderstand", "vlm_model_fallback", map[string]interface{}{
				"session_id":   chatManage.SessionID,
				"vlm_model_id": chatManage.VLMModelID,
				"error":        err.Error(),
			})
		}
		pipelineWarn(ctx, "QueryUnderstand", "no_vision_model", map[string]interface{}{
			"session_id": chatManage.SessionID,
		})
	}

	textModelID := chatManage.ChatModelID
	if chatManage.QueryUnderstandModelID != "" {
		textModelID = chatManage.QueryUnderstandModelID
	}
	m, err := p.modelService.GetChatModel(ctx, textModelID)
	if err != nil {
		// Fall back to ChatModelID when a dedicated query-understand model was
		// configured but cannot be resolved (e.g. deleted / disabled).
		if chatManage.QueryUnderstandModelID != "" && textModelID != chatManage.ChatModelID {
			pipelineWarn(ctx, "QueryUnderstand", "query_understand_model_fallback", map[string]interface{}{
				"session_id":                chatManage.SessionID,
				"query_understand_model_id": chatManage.QueryUnderstandModelID,
				"error":                     err.Error(),
			})
			if fallback, fbErr := p.modelService.GetChatModel(ctx, chatManage.ChatModelID); fbErr == nil {
				return fallback, false
			}
		}
		pipelineError(ctx, "QueryUnderstand", "get_model", map[string]interface{}{
			"session_id":    chatManage.SessionID,
			"chat_model_id": textModelID,
			"error":         err.Error(),
		})
		return nil, false
	}
	return m, false
}

// buildPrompts constructs system and user prompts with placeholder replacement.
func (p *PluginQueryUnderstand) buildPrompts(chatManage *types.ChatManage, historyList []*types.History) (string, string) {
	userPrompt := p.config.Conversation.RewritePromptUser
	if chatManage.RewritePromptUser != "" {
		userPrompt = chatManage.RewritePromptUser
	}
	systemPrompt := p.config.Conversation.RewritePromptSystem
	if chatManage.RewritePromptSystem != "" {
		systemPrompt = chatManage.RewritePromptSystem
	}

	conversationText := formatConversationHistory(historyList)

	queryContent := chatManage.Query
	if len(chatManage.Images) > 0 {
		queryContent += fmt.Sprintf("\n\n<images_uploaded count=\"%d\" />", len(chatManage.Images))
	} else {
		queryContent += "\n\n<no_image_attached />"
	}
	if len(chatManage.Attachments) > 0 {
		queryContent += chatManage.Attachments.BuildPrompt()
	} else {
		queryContent += "\n<no_document_attached />"
	}

	vals := types.PlaceholderValues{
		"conversation": conversationText,
		"query":        queryContent,
		"language":     chatManage.Language,
	}

	return types.RenderPromptPlaceholders(systemPrompt, vals),
		types.RenderPromptPlaceholders(userPrompt, vals)
}

// parseOutput extracts the rewritten query, response/evidence classification, and optional
// image description from the model's structured JSON output.
//
// Expected format: {"rewrite_query":"...","response_mode":"answer",
// "retrieval_need":"required","source_requirement":"knowledge_base",
// "freshness":"any","image_description":"..."}
func (p *PluginQueryUnderstand) parseOutput(chatManage *types.ChatManage, raw string) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return
	}

	if output, ok := parseStructuredQueryOutput(content); ok {
		if rewrite := strings.TrimSpace(output.RewriteQuery); chatManage.EnableRewrite && rewrite != "" {
			chatManage.RewriteQuery = rewrite
		}
		chatManage.Understanding = types.QueryUnderstanding{
			ResponseMode:      output.ResponseMode,
			RetrievalNeed:     output.RetrievalNeed,
			SourceRequirement: output.SourceRequirement,
			Freshness:         output.Freshness,
		}
		chatManage.ImageDescription = strings.TrimSpace(output.ImageDescription)
		return
	}

	// If JSON parsing failed entirely, treat the raw text as the rewritten query
	// and retain the safe default understanding.
	if content != "" {
		if chatManage.EnableRewrite {
			chatManage.RewriteQuery = content
		}
	}
}

func parseStructuredQueryOutput(raw string) (queryUnderstandOutput, bool) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return queryUnderstandOutput{}, false
	}

	if parsed, ok := parseStructuredQueryOutputJSON(content); ok {
		return parsed, true
	}

	// Be tolerant to occasional markdown wrappers or extra prose.
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return queryUnderstandOutput{}, false
	}
	candidate := content[start : end+1]
	if parsed, ok := parseStructuredQueryOutputJSON(candidate); ok {
		return parsed, true
	}
	return queryUnderstandOutput{}, false
}

func parseStructuredQueryOutputJSON(content string) (queryUnderstandOutput, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &obj); err != nil {
		return queryUnderstandOutput{}, false
	}

	out := queryUnderstandOutput{
		RewriteQuery: strings.TrimSpace(firstStringField(obj,
			"rewrite_query", "rewritten_query", "query", "question")),
	}

	responseMode := types.ResponseMode(strings.TrimSpace(firstStringField(obj, "response_mode")))
	retrievalNeed := types.RetrievalNeed(strings.TrimSpace(firstStringField(obj, "retrieval_need")))
	sourceRequirement := types.SourceRequirement(strings.TrimSpace(firstStringField(obj, "source_requirement")))
	freshness := types.FreshnessRequirement(strings.TrimSpace(firstStringField(obj, "freshness")))

	if legacyIntent := strings.TrimSpace(firstStringField(obj, "intent")); responseMode == "" && legacyIntent != "" {
		responseMode, retrievalNeed, sourceRequirement, freshness = mapLegacyIntent(legacyIntent)
	}
	if !validResponseMode(responseMode) || !validRetrievalNeed(retrievalNeed) ||
		!validSourceRequirement(sourceRequirement) || !validFreshness(freshness) {
		return queryUnderstandOutput{}, false
	}
	out.ResponseMode = responseMode
	out.RetrievalNeed = retrievalNeed
	out.SourceRequirement = sourceRequirement
	out.Freshness = freshness

	desc := strings.TrimSpace(firstStringField(obj,
		"image_description", "image_desc", "image_text", "image_ocr_text", "description"))
	ocr := strings.TrimSpace(firstStringField(obj,
		"ocr_text", "ocr", "full_ocr", "image_ocr", "ocr_content"))
	combined, set := mergeImageDescAndOCR(desc, ocr)
	if set {
		out.ImageDescription = combined
	}

	return out, true
}

func (p *PluginQueryUnderstand) resolveRetrievalPlan(chatManage *types.ChatManage) {
	hasKB := types.HasKnowledgeRetrievalScope(
		chatManage.SearchTargets,
		chatManage.KnowledgeBaseIDs,
		chatManage.KnowledgeIDs,
	)
	hasWeb := chatManage.WebSearchEnabled && chatManage.WebSearchProviderID != "" &&
		chatManage.WebSearchMode != types.WebSearchModeOff
	chatManage.RetrievalPlan = types.ResolveRetrievalPlan(
		chatManage.Understanding,
		hasKB,
		hasWeb,
		chatManage.WebSearchMode,
	)
}

func validResponseMode(v types.ResponseMode) bool {
	switch v {
	case types.ResponseModeAnswer, types.ResponseModeGreeting, types.ResponseModeChitchat,
		types.ResponseModeFollowUp, types.ResponseModeImageOnly, types.ResponseModeDocOnly,
		types.ResponseModeSummarize:
		return true
	default:
		return false
	}
}

func validRetrievalNeed(v types.RetrievalNeed) bool {
	return v == types.RetrievalNeedNone || v == types.RetrievalNeedRequired
}

func validSourceRequirement(v types.SourceRequirement) bool {
	switch v {
	case types.SourceRequirementAuto, types.SourceRequirementKB, types.SourceRequirementWeb, types.SourceRequirementBoth:
		return true
	default:
		return false
	}
}

func validFreshness(v types.FreshnessRequirement) bool {
	return v == types.FreshnessAny || v == types.FreshnessCurrent
}

func mapLegacyIntent(intent string) (types.ResponseMode, types.RetrievalNeed, types.SourceRequirement, types.FreshnessRequirement) {
	switch intent {
	case "greeting":
		return types.ResponseModeGreeting, types.RetrievalNeedNone, types.SourceRequirementAuto, types.FreshnessAny
	case "chitchat":
		return types.ResponseModeChitchat, types.RetrievalNeedNone, types.SourceRequirementAuto, types.FreshnessAny
	case "follow_up":
		return types.ResponseModeFollowUp, types.RetrievalNeedNone, types.SourceRequirementAuto, types.FreshnessAny
	case "image_only":
		return types.ResponseModeImageOnly, types.RetrievalNeedNone, types.SourceRequirementAuto, types.FreshnessAny
	case "doc_only":
		return types.ResponseModeDocOnly, types.RetrievalNeedNone, types.SourceRequirementAuto, types.FreshnessAny
	case "summarize":
		return types.ResponseModeSummarize, types.RetrievalNeedNone, types.SourceRequirementAuto, types.FreshnessAny
	case "web_search":
		return types.ResponseModeAnswer, types.RetrievalNeedRequired, types.SourceRequirementWeb, types.FreshnessCurrent
	case "kb_search":
		return types.ResponseModeAnswer, types.RetrievalNeedRequired, types.SourceRequirementKB, types.FreshnessAny
	case "clarification":
		return types.ResponseModeAnswer, types.RetrievalNeedRequired, types.SourceRequirementAuto, types.FreshnessAny
	default:
		return "", "", "", ""
	}
}

func firstStringField(obj map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok || len(raw) == 0 {
			continue
		}

		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return ""
}

func mergeImageDescAndOCR(desc, ocr string) (string, bool) {
	if desc == "" && ocr == "" {
		return "", false
	}
	if desc == "" {
		return ocr, true
	}
	if ocr == "" {
		return desc, true
	}
	if strings.Contains(desc, ocr) {
		return desc, true
	}
	return desc + "\n\n[OCR]\n" + ocr, true
}

// applyManagedResponsePrompt resolves the managed system prompt for the
// current non-retrieval response mode or unavailable-source outcome. These
// prompts come exclusively from the system catalog.
func applyManagedResponsePrompt(chatManage *types.ChatManage, globalPrompts map[string]string) bool {
	promptKey := string(chatManage.Understanding.ResponseMode)
	switch chatManage.RetrievalPlan.ReasonCode {
	case types.RetrievalReasonWebUnavailable:
		// Transitional catalog key; this is a routing outcome, not a response mode.
		promptKey = "web_search"
	case types.RetrievalReasonKBUnavailable:
		promptKey = "knowledge_base_unavailable"
	case types.RetrievalReasonSourcesUnavailable:
		promptKey = "retrieval_sources_unavailable"
	}
	if prompt, ok := globalPrompts[promptKey]; ok {
		chatManage.ManagedResponsePrompt = prompt
	}
	return chatManage.ManagedResponsePrompt != ""
}

// formatConversationHistory formats conversation history for prompt template.
func formatConversationHistory(historyList []*types.History) string {
	if len(historyList) == 0 {
		return ""
	}

	var builder strings.Builder
	for _, h := range historyList {
		builder.WriteString("------BEGIN------\n")
		builder.WriteString("User question: ")
		builder.WriteString(h.Query)
		builder.WriteString("\nAssistant answer: ")
		builder.WriteString(h.Answer)
		builder.WriteString("\n------END------\n")
	}
	return builder.String()
}
