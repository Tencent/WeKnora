// Package chatpipline provides chat pipeline processing capabilities
// Including query rewriting, history processing, model invocation and other features
package chatpipline

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// PluginRewrite is a plugin for rewriting user queries
// It uses historical dialog context and large language models to optimize the user's original query
type PluginRewrite struct {
	modelService   interfaces.ModelService   // Model service for calling large language models
	messageService interfaces.MessageService // Message service for retrieving historical messages
	config         *config.Config            // System configuration
}

// reg is a regular expression used to match and remove content between <think></think> tags
var reg = regexp.MustCompile(`(?s)<think>.*?</think>`)
var rewriteImageSepPattern = regexp.MustCompile(`(?s)^(.*?)\s*\n?---\n(.*)$`)

const (
	noSearchPrefix = "[NO_SEARCH]"
)

type rewriteOutput struct {
	RewriteQuery     string
	SkipKBSearch     bool
	ImageDescription string
}

// NewPluginRewrite creates a new query rewriting plugin instance
// Also registers the plugin with the event manager
func NewPluginRewrite(eventManager *EventManager,
	modelService interfaces.ModelService, messageService interfaces.MessageService,
	config *config.Config,
) *PluginRewrite {
	res := &PluginRewrite{
		modelService:   modelService,
		messageService: messageService,
		config:         config,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the list of event types this plugin responds to
// This plugin only responds to REWRITE_QUERY events
func (p *PluginRewrite) ActivationEvents() []types.EventType {
	return []types.EventType{types.REWRITE_QUERY}
}

// OnEvent processes triggered events.
// Handles three input combinations:
//   - Text only: standard rewrite + intent classification (uses chat model)
//   - Text + images: multimodal rewrite + intent + image description (uses VLM/vision model)
//   - Images only: multimodal analysis + intent + image description (uses VLM/vision model)
func (p *PluginRewrite) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	chatManage.RewriteQuery = chatManage.Query

	hasImages := len(chatManage.Images) > 0
	needRewrite := chatManage.EnableRewrite
	// When images are present we always run the step for image analysis + intent,
	// even without history or rewrite enabled.
	if !needRewrite && !hasImages {
		pipelineInfo(ctx, "Rewrite", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "rewrite_disabled_no_images",
		})
		return next()
	}

	pipelineInfo(ctx, "Rewrite", "input", map[string]interface{}{
		"session_id":     chatManage.SessionID,
		"tenant_id":      chatManage.TenantID,
		"user_query":     chatManage.Query,
		"has_images":     hasImages,
		"enable_rewrite": chatManage.EnableRewrite,
	})

	// --- Load and prepare conversation history ---
	// If history was already loaded by a prior LOAD_HISTORY step, reuse it
	// instead of fetching again. This allows rag_stream to work correctly
	// even when rewrite is disabled.
	var historyList []*types.History
	if len(chatManage.History) > 0 {
		historyList = chatManage.History
		pipelineInfo(ctx, "Rewrite", "history_reused", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"rounds":     len(historyList),
		})
	} else {
		historyList = p.loadHistory(ctx, chatManage)
	}

	// Skip if there's nothing to do: no history to rewrite AND no images to analyse
	if len(historyList) == 0 && !hasImages {
		pipelineInfo(ctx, "Rewrite", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "empty_history_no_images",
		})
		return next()
	}

	// --- Select the appropriate model ---
	rewriteModel, useImages := p.selectModel(ctx, chatManage, hasImages)
	if rewriteModel == nil {
		pipelineError(ctx, "Rewrite", "get_model", map[string]interface{}{
			"session_id": chatManage.SessionID,
		})
		return next()
	}

	// --- Build prompts ---
	systemContent, userContent := p.buildPrompts(chatManage, historyList)

	// Build user message (with images when using a vision-capable model)
	userMsg := chat.Message{Role: "user", Content: userContent}
	if useImages {
		userMsg.Images = chatManage.Images
	}

	maxTokens := 60
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
		pipelineError(ctx, "Rewrite", "model_call", map[string]interface{}{
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
	p.parseRewriteOutput(chatManage, response.Content)

	// Persist image description back to the user message so that future turns
	// can see it when loading conversation history.
	if chatManage.ImageDescription != "" && chatManage.UserMessageID != "" {
		p.updateUserMessageImageCaption(ctx, chatManage)
	}

	pipelineInfo(ctx, "Rewrite", "output", map[string]interface{}{
		"session_id":      chatManage.SessionID,
		"rewrite_query":   chatManage.RewriteQuery,
		"skip_kb_search":  chatManage.SkipKBSearch,
		"has_image_desc":  chatManage.ImageDescription != "",
		"original_output": response.Content,
	})
	return next()
}

// updateUserMessageImageCaption writes the generated ImageDescription back to
// the stored user message's Images so that subsequent turns can see it in history.
func (p *PluginRewrite) updateUserMessageImageCaption(ctx context.Context, chatManage *types.ChatManage) {
	msg, err := p.messageService.GetMessage(ctx, chatManage.SessionID, chatManage.UserMessageID)
	if err != nil {
		pipelineWarn(ctx, "Rewrite", "get_user_message", map[string]interface{}{
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

	// Use the targeted UpdateMessageImages to reliably persist the JSONB column.
	// GORM's struct-based Updates may silently skip custom Valuer types.
	if err := p.messageService.UpdateMessageImages(ctx, chatManage.SessionID, chatManage.UserMessageID, msg.Images); err != nil {
		pipelineWarn(ctx, "Rewrite", "update_image_caption", map[string]interface{}{
			"session_id":      chatManage.SessionID,
			"user_message_id": chatManage.UserMessageID,
			"error":           err.Error(),
		})
	}
}

// loadHistory fetches and processes conversation history for rewrite context.
func (p *PluginRewrite) loadHistory(ctx context.Context, chatManage *types.ChatManage) []*types.History {
	history, err := p.messageService.GetRecentMessagesBySession(ctx, chatManage.SessionID, 20)
	if err != nil {
		pipelineWarn(ctx, "Rewrite", "history_fetch", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"error":      err.Error(),
		})
	}

	historyMap := make(map[string]*types.History)
	for _, message := range history {
		h, ok := historyMap[message.RequestID]
		if !ok {
			h = &types.History{}
		}
		if message.Role == "user" {
			h.Query = message.Content
			h.CreateAt = message.CreatedAt
			if desc := extractImageCaptions(message.Images); desc != "" {
				h.Query += "\n\n[用户上传图片内容]\n" + desc
			}
		} else {
			h.Answer = reg.ReplaceAllString(message.Content, "")
			h.KnowledgeReferences = message.KnowledgeReferences
		}
		historyMap[message.RequestID] = h
	}

	historyList := make([]*types.History, 0)
	for _, h := range historyMap {
		if h.Answer != "" && h.Query != "" {
			historyList = append(historyList, h)
		}
	}

	sort.Slice(historyList, func(i, j int) bool {
		return historyList[i].CreateAt.After(historyList[j].CreateAt)
	})

	maxRounds := p.config.Conversation.MaxRounds
	if chatManage.MaxRounds > 0 {
		maxRounds = chatManage.MaxRounds
	}
	if len(historyList) > maxRounds {
		historyList = historyList[:maxRounds]
	}

	slices.Reverse(historyList)
	chatManage.History = historyList

	if len(historyList) > 0 {
		pipelineInfo(ctx, "Rewrite", "history_ready", map[string]interface{}{
			"session_id":     chatManage.SessionID,
			"history_rounds": len(historyList),
		})
	}

	return historyList
}

// selectModel picks the model for rewrite. When images are present it prefers
// a vision-capable model (either the chat model itself, or the agent's VLM).
// Returns (model, useImages).
func (p *PluginRewrite) selectModel(ctx context.Context, chatManage *types.ChatManage, hasImages bool) (chat.Chat, bool) {
	if hasImages {
		if chatManage.ChatModelSupportsVision {
			m, err := p.modelService.GetChatModel(ctx, chatManage.ChatModelID)
			if err == nil {
				return m, true
			}
			pipelineWarn(ctx, "Rewrite", "vision_model_fallback", map[string]interface{}{
				"session_id": chatManage.SessionID,
				"error":      err.Error(),
			})
		}
		if chatManage.VLMModelID != "" {
			m, err := p.modelService.GetChatModel(ctx, chatManage.VLMModelID)
			if err == nil {
				return m, true
			}
			pipelineWarn(ctx, "Rewrite", "vlm_model_fallback", map[string]interface{}{
				"session_id":   chatManage.SessionID,
				"vlm_model_id": chatManage.VLMModelID,
				"error":        err.Error(),
			})
		}
		pipelineWarn(ctx, "Rewrite", "no_vision_model", map[string]interface{}{
			"session_id": chatManage.SessionID,
		})
	}

	// Fallback: text-only rewrite with chat model
	m, err := p.modelService.GetChatModel(ctx, chatManage.ChatModelID)
	if err != nil {
		pipelineError(ctx, "Rewrite", "get_model", map[string]interface{}{
			"session_id":    chatManage.SessionID,
			"chat_model_id": chatManage.ChatModelID,
			"error":         err.Error(),
		})
		return nil, false
	}
	return m, false
}

// buildPrompts constructs system and user prompts with placeholder replacement.
func (p *PluginRewrite) buildPrompts(chatManage *types.ChatManage, historyList []*types.History) (string, string) {
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

// parseRewriteOutput extracts intent classification, rewritten query, and
// optional image description from the model's structured output.
//
// Expected formats:
//
//	Preferred: {"rewrite_query":"...","skip_kb_search":false,"image_description":"..."}
//	Legacy fallback:
//	  - Text only:  "[NO_SEARCH] rewritten question"  or  "rewritten question"
//	  - With images: "[NO_SEARCH]\nrewritten question\n---\nimage description"
func (p *PluginRewrite) parseRewriteOutput(chatManage *types.ChatManage, raw string) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return
	}

	if output, ok := parseStructuredRewriteOutput(content); ok {
		if rewrite := strings.TrimSpace(output.RewriteQuery); rewrite != "" {
			chatManage.RewriteQuery = rewrite
		}
		chatManage.SkipKBSearch = output.SkipKBSearch
		chatManage.ImageDescription = strings.TrimSpace(output.ImageDescription)
		return
	}

	// Legacy fallback parsing for older prompts/models.
	if strings.HasPrefix(content, noSearchPrefix) {
		chatManage.SkipKBSearch = true
		content = strings.TrimSpace(strings.TrimPrefix(content, noSearchPrefix))
	}

	if m := rewriteImageSepPattern.FindStringSubmatch(content); len(m) == 3 {
		chatManage.RewriteQuery = strings.TrimSpace(m[1])
		chatManage.ImageDescription = strings.TrimSpace(m[2])
		return
	}
	if content != "" {
		chatManage.RewriteQuery = content
	}
}

func parseStructuredRewriteOutput(raw string) (rewriteOutput, bool) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return rewriteOutput{}, false
	}

	var out rewriteOutput
	if parsed, ok := parseStructuredRewriteOutputJSON(content); ok {
		return parsed, true
	}

	// Be tolerant to occasional markdown wrappers or extra prose.
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return rewriteOutput{}, false
	}
	candidate := content[start : end+1]
	if parsed, ok := parseStructuredRewriteOutputJSON(candidate); ok {
		return parsed, true
	}
	return out, false
}

func parseStructuredRewriteOutputJSON(content string) (rewriteOutput, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &obj); err != nil {
		return rewriteOutput{}, false
	}

	out := rewriteOutput{
		RewriteQuery: strings.TrimSpace(firstStringField(obj,
			"rewrite_query", "rewritten_query", "query", "question")),
	}

	// Support common variants and semantic inversion for need_search.
	if v, ok := firstBoolField(obj, "skip_kb_search", "skip_search", "no_search"); ok {
		out.SkipKBSearch = v
	} else if v, ok := firstBoolField(obj, "need_search", "requires_search"); ok {
		out.SkipKBSearch = !v
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

func firstBoolField(obj map[string]json.RawMessage, keys ...string) (bool, bool) {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok || len(raw) == 0 {
			continue
		}
		if v, ok := parseBoolJSON(raw); ok {
			return v, true
		}
	}
	return false, false
}

func parseBoolJSON(raw json.RawMessage) (bool, bool) {
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, true
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "yes", "y":
			return true, true
		case "false", "0", "no", "n":
			return false, true
		}
	}

	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n != 0, true
	}

	return false, false
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

// formatConversationHistory formats conversation history for prompt template
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
