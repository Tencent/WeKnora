package chatpipline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// PluginIntoChatMessage handles the transformation of search results into chat messages
type PluginIntoChatMessage struct{}

// NewPluginIntoChatMessage creates and registers a new PluginIntoChatMessage instance
func NewPluginIntoChatMessage(eventManager *EventManager) *PluginIntoChatMessage {
	res := &PluginIntoChatMessage{}
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

	// Extract content from merge results
	passages := make([]string, len(chatManage.MergeResult))
	for i, result := range chatManage.MergeResult {
		// 合并内容和图片信息
		passages[i] = getEnrichedPassageForChat(ctx, result)
	}

	// Parse the context template
	tmpl, err := template.New("searchContent").Parse(chatManage.SummaryConfig.ContextTemplate)
	if err != nil {
		pipelineError(ctx, "IntoChatMessage", "parse_template", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"error":      err.Error(),
		})
		return ErrTemplateParse.WithError(err)
	}

	// Prepare weekday names for template
	weekdayName := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	var userContent bytes.Buffer

	// Execute template with context data
	err = tmpl.Execute(&userContent, map[string]interface{}{
		"Query":       chatManage.Query,                         // User's original query
		"Contexts":    passages,                                 // Extracted passages from search results
		"CurrentTime": time.Now().Format("2006-01-02 15:04:05"), // Formatted current time
		"CurrentWeek": weekdayName[time.Now().Weekday()],        // Current weekday in Chinese
	})
	if err != nil {
		pipelineError(ctx, "IntoChatMessage", "render_template", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"error":      err.Error(),
		})
		return ErrTemplateExecute.WithError(err)
	}

	// Set formatted content back to chat management
	chatManage.UserContent = userContent.String()
	pipelineInfo(ctx, "IntoChatMessage", "output", map[string]interface{}{
		"session_id":       chatManage.SessionID,
		"user_content_len": len(chatManage.UserContent),
	})
	return next()
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
				replacement += fmt.Sprintf("图片描述: %s\n", imgInfo.Caption)
			}
			if imgInfo.OCRText != "" {
				replacement += fmt.Sprintf("图片文本: %s\n", imgInfo.OCRText)
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
			imgTexts = append(imgTexts, fmt.Sprintf("图片 %s 的描述信息: %s", imgInfo.URL, imgInfo.Caption))
		}
		if imgInfo.OCRText != "" {
			imgTexts = append(imgTexts, fmt.Sprintf("图片 %s 的文本: %s", imgInfo.URL, imgInfo.OCRText))
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
		content += "附加图片信息:\n" + strings.Join(additionalImageTexts, "\n")
	}

	pipelineInfo(ctx, "IntoChatMessage", "image_enrich_summary", map[string]interface{}{
		"markdown_images": len(matches),
		"additional_imgs": len(additionalImageTexts),
	})

	return content
}
