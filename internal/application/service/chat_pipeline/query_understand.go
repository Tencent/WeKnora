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

// PluginQueryUnderstand performs query rewriting and intent classification.
// It uses conversation history and an LLM to optimise the user's original query
// and determine the downstream pipeline behaviour.
type PluginQueryUnderstand struct {
	modelService   interfaces.ModelService
	messageService interfaces.MessageService
	memoryService  interfaces.MemoryService
	config         *config.Config
}

var rewriteImageSepPattern = regexp.MustCompile(`(?s)^(.*?)\s*\n?---\n(.*)$`)

type queryUnderstandOutput struct {
	RewriteQuery     string            `json:"rewrite_query"`
	Intent           types.QueryIntent `json:"intent"`
	ImageDescription string            `json:"image_description"`
}

// NewPluginQueryUnderstand creates a new query-understanding plugin instance
// and registers it with the event manager.
func NewPluginQueryUnderstand(eventManager *EventManager,
	modelService interfaces.ModelService, messageService interfaces.MessageService,
	memoryService interfaces.MemoryService,
	config *config.Config,
) *PluginQueryUnderstand {
	res := &PluginQueryUnderstand{
		modelService:   modelService,
		messageService: messageService,
		memoryService:  memoryService,
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
//   - Text only: standard rewrite + intent classification (uses chat model)
//   - Text + images: multimodal rewrite + intent + image description (uses VLM/vision model)
//   - Images only: multimodal analysis + intent + image description (uses VLM/vision model)
func (p *PluginQueryUnderstand) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	chatManage.RewriteQuery = chatManage.Query

	hasImages := len(chatManage.Images) > 0
	needRewrite := chatManage.EnableRewrite
	if !needRewrite && !hasImages {
		pipelineInfo(ctx, "QueryUnderstand", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "rewrite_disabled_no_images",
		})
		return next()
	}

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
		return next()
	}

	// --- Build prompts ---
	systemContent, userContent := p.buildPrompts(ctx, chatManage, historyList)

	userMsg := chat.Message{Role: "user", Content: userContent}
	if useImages {
		userMsg.Images = chatManage.Images
	}

	maxTokens := 150
	if useImages {
		maxTokens = 500
	}

	// --- Call model ---
	thinking := false
	modelCtx := types.WithLLMCallMetadata(ctx, "query_rewrite", "")
	response, err := rewriteModel.Chat(modelCtx, []chat.Message{
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
		return next()
	}

	// --- Parse structured output ---
	p.parseOutput(chatManage, response.Content)

	// Persist image description asynchronously — this DB write does not affect
	// the current pipeline result, so it can run in the background.
	if chatManage.ImageDescription != "" && chatManage.UserMessageID != "" {
		go p.updateUserMessageImageCaption(context.WithoutCancel(ctx), chatManage)
	}

	// --- Apply intent-specific system prompt override ---
	if !chatManage.NeedsRetrieval() {
		if applyIntentPromptOverride(chatManage, p.config.Conversation.IntentSystemPrompts) {
			pipelineInfo(ctx, "QueryUnderstand", "prompt_override", map[string]interface{}{
				"session_id": chatManage.SessionID,
				"intent":     chatManage.Intent,
			})
		}
	}

	pipelineInfo(ctx, "QueryUnderstand", "output", map[string]interface{}{
		"session_id":          chatManage.SessionID,
		"rewrite_query":       chatManage.RewriteQuery,
		"intent":              chatManage.Intent,
		"has_image_desc":      chatManage.ImageDescription != "",
		"has_prompt_override": chatManage.SystemPromptOverride != "",
		"original_output":     response.Content,
	})
	return next()
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
func (p *PluginQueryUnderstand) buildPrompts(
	ctx context.Context, chatManage *types.ChatManage, historyList []*types.History,
) (string, string) {
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
	queryContent += p.memoryBackground(ctx, chatManage)

	vals := types.PlaceholderValues{
		"conversation": conversationText,
		"query":        queryContent,
		"language":     chatManage.Language,
	}

	return types.RenderPromptPlaceholders(systemPrompt, vals),
		types.RenderPromptPlaceholders(userPrompt, vals)
}

// memoryBackground gives the rewriter who is asking.
//
// This is the point where long-term memory stops being a paragraph appended to
// the answer prompt and starts changing what gets retrieved. "How do I tune the
// segmentation" is a different search for someone who works on medical imaging
// than for someone who works on autonomous driving, and the only place that
// difference can be applied is before retrieval runs.
//
// It is deliberately advisory rather than a filter. Memory narrows nothing and
// excludes no knowledge base: a stale note about last quarter's project must
// not be able to make this quarter's documents unreachable.
func (p *PluginQueryUnderstand) memoryBackground(ctx context.Context, chatManage *types.ChatManage) string {
	if p.memoryService == nil {
		return ""
	}
	memCtx := p.memoryService.RetrievalContextFor(ctx)
	if memCtx.Empty() {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n<asker_background note=\"背景仅用于消解指代和补全检索词，不要当作问题的一部分\">")
	if memCtx.Background != "" {
		b.WriteString("\n" + memCtx.Background)
	}
	if len(memCtx.Interests) > 0 {
		b.WriteString("\n长期关注：" + strings.Join(memCtx.Interests, "、"))
	}
	if len(memCtx.Documents) > 0 {
		b.WriteString("\n常查资料：" + strings.Join(memCtx.Documents, "、"))
	}
	b.WriteString("\n</asker_background>")

	// Deliberately does not add to chatManage.UsedMemories. What this reads is
	// the whole standing background, unfiltered — that is the right input for a
	// rewriter, but reporting it would claim every turn recalled memories that
	// have nothing to do with the question. Which memories this turn actually
	// used is decided in MEMORY_RECALL, by relevance, and the profile entries
	// here are already reported from there.
	fields := map[string]interface{}{
		"session_id": chatManage.SessionID,
		"interests":  len(memCtx.Interests),
		"documents":  len(memCtx.Documents),
		"items":      len(memCtx.Items),
	}
	if len(memCtx.Interests) > 0 {
		fields["interest_previews"] = memCtx.Interests
	}
	pipelineInfo(ctx, "QueryUnderstand", "memory_background", fields)
	return b.String()
}

// parseOutput extracts the rewritten query, intent classification, and optional
// image description from the model's structured JSON output.
//
// Expected format: {"rewrite_query":"...","intent":"kb_search","image_description":"..."}
func (p *PluginQueryUnderstand) parseOutput(chatManage *types.ChatManage, raw string) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return
	}

	if output, ok := parseStructuredQueryOutput(content); ok {
		if rewrite := strings.TrimSpace(output.RewriteQuery); rewrite != "" {
			chatManage.RewriteQuery = rewrite
		}
		chatManage.Intent = output.Intent
		chatManage.ImageDescription = strings.TrimSpace(output.ImageDescription)
		return
	}

	// On parse failure, keep the original query and intent.
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

	intentStr := strings.TrimSpace(firstStringField(obj, "intent"))
	if intentStr != "" {
		out.Intent = types.QueryIntent(intentStr)
	}

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

// applyIntentPromptOverride resolves the system-prompt override for the current
// non-retrieval intent. Agent-level overrides take precedence; otherwise the
// tenant/global IntentSystemPrompts map is consulted. Whitespace-only agent
// overrides are treated as unset and fall through to the global default. Returns
// true when a non-empty override was applied.
func applyIntentPromptOverride(chatManage *types.ChatManage, globalPrompts map[string]string) bool {
	intentKey := string(chatManage.Intent)
	if raw, ok := chatManage.IntentPromptOverrides[intentKey]; ok && strings.TrimSpace(raw) != "" {
		chatManage.SystemPromptOverride = raw
	}
	if chatManage.SystemPromptOverride == "" {
		if prompt, ok := globalPrompts[intentKey]; ok {
			chatManage.SystemPromptOverride = prompt
		}
	}
	return chatManage.SystemPromptOverride != ""
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
