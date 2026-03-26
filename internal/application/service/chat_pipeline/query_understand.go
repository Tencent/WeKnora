package chatpipeline

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// PluginQueryUnderstand performs query rewriting and intent classification.
// It uses conversation history and an LLM to optimise the user's original query
// and determine the downstream pipeline behaviour.
type PluginQueryUnderstand struct {
	modelService   interfaces.ModelService
	messageService interfaces.MessageService
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
	systemContent, userContent := p.buildPrompts(chatManage, historyList)

	userMsg := chat.Message{Role: "user", Content: userContent}
	if useImages {
		userMsg.Images = chatManage.Images
	}

	maxTokens := 150
	if useImages {
		maxTokens = 500
	}

	// --- Emit progress event for image analysis ---
	var toolCallID string
	if useImages && chatManage.EventBus != nil {
		toolCallID = uuid.New().String()
		chatManage.EventBus.Emit(ctx, types.Event{
			Type:      types.EventType(event.EventAgentToolCall),
			SessionID: chatManage.SessionID,
			Data: event.AgentToolCallData{
				ToolCallID: toolCallID,
				ToolName:   "image_analysis",
			},
		})
	}

	// --- Call model ---
	thinking := false
	vlmStart := time.Now()
	response, err := rewriteModel.Chat(ctx, []chat.Message{
		{Role: "system", Content: systemContent},
		userMsg,
	}, &chat.ChatOptions{
		Temperature:         0.3,
		MaxCompletionTokens: maxTokens,
		Thinking:            &thinking,
	})
	if err != nil {
		if toolCallID != "" && chatManage.EventBus != nil {
			chatManage.EventBus.Emit(ctx, types.Event{
				Type:      types.EventType(event.EventAgentToolResult),
				SessionID: chatManage.SessionID,
				Data: event.AgentToolResultData{
					ToolCallID: toolCallID,
					ToolName:   "image_analysis",
					Output:     "图片分析失败",
					Success:    false,
					Duration:   time.Since(vlmStart).Milliseconds(),
				},
			})
		}
		pipelineError(ctx, "QueryUnderstand", "model_call", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"error":      err.Error(),
		})
		return next()
	}

	// --- Emit completion event for image analysis ---
	if toolCallID != "" && chatManage.EventBus != nil {
		chatManage.EventBus.Emit(ctx, types.Event{
			Type:      types.EventType(event.EventAgentToolResult),
			SessionID: chatManage.SessionID,
			Data: event.AgentToolResultData{
				ToolCallID: toolCallID,
				ToolName:   "image_analysis",
				Output:     "已分析图片内容",
				Success:    true,
				Duration:   time.Since(vlmStart).Milliseconds(),
			},
		})
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
		if prompt, ok := p.config.Conversation.IntentSystemPrompts[string(chatManage.Intent)]; ok {
			chatManage.SystemPromptOverride = prompt
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
	maxRounds := p.config.Conversation.MaxRounds
	if chatManage.MaxRounds > 0 {
		maxRounds = chatManage.MaxRounds
	}

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

	m, err := p.modelService.GetChatModel(ctx, chatManage.ChatModelID)
	if err != nil {
		pipelineError(ctx, "QueryUnderstand", "get_model", map[string]interface{}{
			"session_id":    chatManage.SessionID,
			"chat_model_id": chatManage.ChatModelID,
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

	vals := types.PlaceholderValues{
		"conversation": conversationText,
		"query":        chatManage.Query,
		"language":     chatManage.Language,
	}

	return types.RenderPromptPlaceholders(systemPrompt, vals),
		types.RenderPromptPlaceholders(userPrompt, vals)
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

	// If JSON parsing failed entirely, treat the raw text as the rewritten query
	// and default to IntentKBSearch for safety.
	if content != "" {
		chatManage.RewriteQuery = content
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
