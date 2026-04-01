package chatpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// PluginIntoChatMessage handles the transformation of search results into chat messages
type PluginIntoChatMessage struct {
	messageService interfaces.MessageService
}

// NewPluginIntoChatMessage creates and registers a new PluginIntoChatMessage instance
func NewPluginIntoChatMessage(eventManager *EventManager, messageService interfaces.MessageService) *PluginIntoChatMessage {
	res := &PluginIntoChatMessage{messageService: messageService}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginIntoChatMessage) ActivationEvents() []types.EventType {
	return []types.EventType{types.INTO_CHAT_MESSAGE}
}

// OnEvent processes the INTO_CHAT_MESSAGE event to format chat message content
func (p *PluginIntoChatMessage) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	pipelineInfo(ctx, "IntoChatMessage", "input", map[string]interface{}{
		"session_id":       chatManage.SessionID,
		"merge_result_cnt": len(chatManage.MergeResult),
		"template_len":     len(chatManage.SummaryConfig.ContextTemplate),
	})

	// Separate FAQ and document results when FAQ priority is enabled
	var faqResults, docResults []*types.SearchResult
	var hasHighConfidenceFAQ bool

	if chatManage.FAQPriorityEnabled {
		for _, result := range chatManage.MergeResult {
			if result.ChunkType == string(types.ChunkTypeFAQ) {
				faqResults = append(faqResults, result)
				// Check if this FAQ has high confidence (above direct answer threshold)
				if result.Score >= chatManage.FAQDirectAnswerThreshold && !hasHighConfidenceFAQ {
					hasHighConfidenceFAQ = true
					pipelineInfo(ctx, "IntoChatMessage", "high_confidence_faq", map[string]interface{}{
						"chunk_id":  result.ID,
						"score":     fmt.Sprintf("%.4f", result.Score),
						"threshold": chatManage.FAQDirectAnswerThreshold,
					})
				}
			} else {
				docResults = append(docResults, result)
			}
		}
		pipelineInfo(ctx, "IntoChatMessage", "faq_separation", map[string]interface{}{
			"faq_count":           len(faqResults),
			"doc_count":           len(docResults),
			"has_high_confidence": hasHighConfidenceFAQ,
		})
	}

	// 验证用户查询的安全性
	safeQuery, isValid := utils.ValidateInput(chatManage.Query)
	if !isValid {
		pipelineWarn(ctx, "IntoChatMessage", "invalid_query", map[string]interface{}{
			"session_id": chatManage.SessionID,
		})
		return ErrTemplateExecute.WithError(fmt.Errorf("user query contains invalid content"))
	}

	// Intent-based no-search path: no retrieval results, but still render
	// through the context template so runtime metadata (current_time, etc.) is injected.
	if !chatManage.NeedsRetrieval() {
		userContent := safeQuery
		if rewrite := strings.TrimSpace(chatManage.RewriteQuery); rewrite != "" {
			if safeRewrite, ok := utils.ValidateInput(rewrite); ok {
				userContent = safeRewrite
			} else {
				pipelineWarn(ctx, "IntoChatMessage", "invalid_rewrite_query_fallback", map[string]interface{}{
					"session_id": chatManage.SessionID,
				})
			}
		}
		if chatManage.ImageDescription != "" && !chatManage.ChatModelSupportsVision {
			userContent += "\n\n[用户上传图片内容]\n" + chatManage.ImageDescription
		}
		if chatManage.QuotedContext != "" {
			userContent += "\n\n" + chatManage.QuotedContext
		}

		if tpl := chatManage.SummaryConfig.ContextTemplate; tpl != "" {
			chatManage.UserContent = types.RenderPromptPlaceholders(tpl, types.PlaceholderValues{
				"query":    userContent,
				"contexts": "",
				"language": chatManage.Language,
			})
		} else {
			chatManage.UserContent = userContent
		}

		pipelineInfo(ctx, "IntoChatMessage", "no_search_with_template", map[string]interface{}{
			"session_id":       chatManage.SessionID,
			"user_content_len": len(chatManage.UserContent),
			"has_template":     chatManage.SummaryConfig.ContextTemplate != "",
		})
		return next()
	}

	var contextsBuilder strings.Builder

	// Collect unique document metadata (title + description), once per knowledge
	allResults := chatManage.MergeResult
	if chatManage.FAQPriorityEnabled && len(faqResults) > 0 {
		allResults = append(faqResults, docResults...)
	}
	docHeader := buildDocumentHeader(allResults)
	if docHeader != "" {
		contextsBuilder.WriteString(docHeader)
		contextsBuilder.WriteString("\n")
	}

	// Build contexts string based on FAQ priority strategy
	if chatManage.FAQPriorityEnabled && len(faqResults) > 0 {
		// Build structured context with FAQ prioritization
		contextsBuilder.WriteString("### Source 1: FAQ Knowledge Base\n")
		contextsBuilder.WriteString("[High Confidence - Prioritize these results]\n")
		for i, result := range faqResults {
			passage := getEnrichedPassageForChat(ctx, result)
			if hasHighConfidenceFAQ && i == 0 {
				contextsBuilder.WriteString(fmt.Sprintf("[FAQ-%d] Exact Match: %s\n", i+1, passage))
			} else {
				contextsBuilder.WriteString(fmt.Sprintf("[FAQ-%d] %s\n", i+1, passage))
			}
		}

		if len(docResults) > 0 {
			contextsBuilder.WriteString("\n### Source 2: Reference Documents\n")
			contextsBuilder.WriteString("[Supplementary - Use only when FAQ cannot answer the question]\n")
			for i, result := range docResults {
				passage := getEnrichedPassageForChat(ctx, result)
				contextsBuilder.WriteString(fmt.Sprintf("[DOC-%d] %s\n", i+1, passage))
			}
		}
	} else {
		// Original behavior: simple numbered list
		passages := make([]string, len(chatManage.MergeResult))
		for i, result := range chatManage.MergeResult {
			passages[i] = getEnrichedPassageForChat(ctx, result)
		}
		for i, passage := range passages {
			if i > 0 {
				contextsBuilder.WriteString("\n\n")
			}
			contextsBuilder.WriteString(fmt.Sprintf("[%d] %s", i+1, passage))
		}
	}

	chatManage.RenderedContexts = contextsBuilder.String()

	// Replace placeholders in context template
	userContent := types.RenderPromptPlaceholders(chatManage.SummaryConfig.ContextTemplate, types.PlaceholderValues{
		"query":    safeQuery,
		"contexts": chatManage.RenderedContexts,
		"language": chatManage.Language,
	})

	// Append image description as text fallback only when the chat model cannot
	// process images directly. Vision-capable models see images via MultiContent.
	if chatManage.ImageDescription != "" && !chatManage.ChatModelSupportsVision {
		userContent += "\n\n[用户上传图片内容]\n" + chatManage.ImageDescription
	}
	if chatManage.QuotedContext != "" {
		userContent += "\n\n" + chatManage.QuotedContext
	}

	// Set formatted content back to chat management
	chatManage.UserContent = userContent
	pipelineInfo(ctx, "IntoChatMessage", "output", map[string]interface{}{
		"session_id":                 chatManage.SessionID,
		"user_content_len":           len(chatManage.UserContent),
		"faq_priority":               chatManage.FAQPriorityEnabled,
		"intent":                     chatManage.Intent,
		"image_description":          chatManage.ImageDescription,
		"chat_model_supports_vision": chatManage.ChatModelSupportsVision,
	})

	p.persistRenderedContent(ctx, chatManage)
	return next()
}

// persistRenderedContent asynchronously writes the RAG-augmented UserContent back
// to the user message so that subsequent conversation turns can see the full
// retrieval context in history.
func (p *PluginIntoChatMessage) persistRenderedContent(ctx context.Context, chatManage *types.ChatManage) {
	if chatManage.UserMessageID == "" || chatManage.UserContent == "" {
		pipelineInfo(ctx, "IntoChatMessage", "persist_rendered_content_skip", map[string]interface{}{
			"session_id":       chatManage.SessionID,
			"user_message_id":  chatManage.UserMessageID,
			"has_user_content": chatManage.UserContent != "",
			"reason":           "empty_id_or_content",
		})
		return
	}
	if chatManage.UserContent == chatManage.Query {
		return
	}
	pipelineInfo(ctx, "IntoChatMessage", "persist_rendered_content", map[string]interface{}{
		"session_id":           chatManage.SessionID,
		"user_message_id":      chatManage.UserMessageID,
		"rendered_content_len": len(chatManage.UserContent),
	})
	bgCtx := context.WithoutCancel(ctx)
	go func() {
		if err := p.messageService.UpdateMessageRenderedContent(
			bgCtx, chatManage.SessionID, chatManage.UserMessageID, chatManage.UserContent,
		); err != nil {
			pipelineWarn(bgCtx, "IntoChatMessage", "persist_rendered_content_error", map[string]interface{}{
				"session_id":      chatManage.SessionID,
				"user_message_id": chatManage.UserMessageID,
				"error":           err.Error(),
			})
		}
	}()
}

// buildDocumentHeader generates a document metadata section listing each unique
// knowledge document (by KnowledgeID) with its title and description.
// Returns an empty string when no meaningful metadata is available.
func buildDocumentHeader(results []*types.SearchResult) string {
	type docMeta struct {
		title       string
		description string
	}

	seen := make(map[string]struct{})
	var docs []docMeta

	for _, r := range results {
		if r.KnowledgeID == "" {
			continue
		}
		if _, ok := seen[r.KnowledgeID]; ok {
			continue
		}
		seen[r.KnowledgeID] = struct{}{}

		title := r.KnowledgeTitle
		if title == "" {
			title = r.KnowledgeFilename
		}
		if title == "" {
			continue
		}

		docs = append(docs, docMeta{
			title:       title,
			description: r.KnowledgeDescription,
		})
	}

	if len(docs) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("### Referenced Documents\n")
	for i, d := range docs {
		// if d.description != "" {
		// 	b.WriteString(fmt.Sprintf("%d. %s — %s\n", i+1, d.title, d.description))
		// } else {
		// 	b.WriteString(fmt.Sprintf("%d. %s\n", i+1, d.title))
		// }
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, d.title))
	}
	return b.String()
}

// getEnrichedPassageForChat 合并Content和ImageInfo的文本内容，为聊天消息准备
func getEnrichedPassageForChat(ctx context.Context, result *types.SearchResult) string {
	// 如果没有图片信息，直接返回内容
	if result.Content == "" && result.ImageInfo == "" {
		return ""
	}

	// 如果只有内容，没有图片信息
	if result.ImageInfo == "" {
		return result.Content
	}

	// 处理图片信息并与内容合并
	return enrichContentWithImageInfo(ctx, result.Content, result.ImageInfo)
}

// 正则表达式用于匹配Markdown图片链接
var markdownImageRegex = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

// enrichContentWithImageInfo 将图片信息与文本内容合并
func enrichContentWithImageInfo(ctx context.Context, content string, imageInfoJSON string) string {
	// 解析ImageInfo
	var imageInfos []types.ImageInfo
	err := json.Unmarshal([]byte(imageInfoJSON), &imageInfos)
	if err != nil {
		pipelineWarn(ctx, "IntoChatMessage", "image_parse_error", map[string]interface{}{
			"error": err.Error(),
		})
		return content
	}

	if len(imageInfos) == 0 {
		return content
	}

	// 创建图片URL到信息的映射
	imageInfoMap := make(map[string]*types.ImageInfo)
	for i := range imageInfos {
		if imageInfos[i].URL != "" {
			imageInfoMap[imageInfos[i].URL] = &imageInfos[i]
		}
		// 同时检查原始URL
		if imageInfos[i].OriginalURL != "" {
			imageInfoMap[imageInfos[i].OriginalURL] = &imageInfos[i]
		}
	}

	// 查找内容中的所有Markdown图片链接
	matches := markdownImageRegex.FindAllStringSubmatch(content, -1)

	// 用于存储已处理的图片URL
	processedURLs := make(map[string]bool)

	pipelineInfo(ctx, "IntoChatMessage", "image_markdown_links", map[string]interface{}{
		"match_count": len(matches),
	})

	// 替换每个图片链接，添加描述和OCR文本
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		// 提取图片URL，忽略alt文本
		imgURL := match[2]

		// 标记该URL已处理
		processedURLs[imgURL] = true

		// 查找匹配的图片信息
		imgInfo, found := imageInfoMap[imgURL]

		// 如果找到匹配的图片信息，添加描述和OCR文本
		if found && imgInfo != nil {
			replacement := match[0] + "\n"
			if imgInfo.Caption != "" {
				replacement += fmt.Sprintf("Image Caption: %s\n", imgInfo.Caption)
			}
			if imgInfo.OCRText != "" {
				replacement += fmt.Sprintf("Image Text: %s\n", imgInfo.OCRText)
			}
			content = strings.Replace(content, match[0], replacement, 1)
		}
	}

	// 处理未在内容中找到但存在于ImageInfo中的图片
	var additionalImageTexts []string
	for _, imgInfo := range imageInfos {
		// 如果图片URL已经处理过，跳过
		if processedURLs[imgInfo.URL] || processedURLs[imgInfo.OriginalURL] {
			continue
		}

		var imgTexts []string
		if imgInfo.Caption != "" {
			imgTexts = append(imgTexts, fmt.Sprintf("Image %s caption: %s", imgInfo.URL, imgInfo.Caption))
		}
		if imgInfo.OCRText != "" {
			imgTexts = append(imgTexts, fmt.Sprintf("Image %s text: %s", imgInfo.URL, imgInfo.OCRText))
		}

		if len(imgTexts) > 0 {
			additionalImageTexts = append(additionalImageTexts, imgTexts...)
		}
	}

	// 如果有额外的图片信息，添加到内容末尾
	if len(additionalImageTexts) > 0 {
		if content != "" {
			content += "\n\n"
		}
		content += "Additional Image Info:\n" + strings.Join(additionalImageTexts, "\n")
	}

	pipelineInfo(ctx, "IntoChatMessage", "image_enrich_summary", map[string]interface{}{
		"markdown_images": len(matches),
		"additional_imgs": len(additionalImageTexts),
	})

	return content
}
