package chatpipline

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
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

	// 사용자 입력 검증
	safeQuery, isValid := utils.ValidateInput(chatManage.Query)
	if !isValid {
		pipelineWarn(ctx, "IntoChatMessage", "invalid_query", map[string]interface{}{
			"session_id": chatManage.SessionID,
		})
		return ErrTemplateExecute.WithError(fmt.Errorf("사용자 쿼리에 잘못된 내용이 포함되어 있습니다"))
	}

	// Prepare weekday names
	weekdayName := []string{"일요일", "월요일", "화요일", "수요일", "목요일", "금요일", "토요일"}

	var contextsBuilder strings.Builder

	// Build contexts string based on FAQ priority strategy
	if chatManage.FAQPriorityEnabled && len(faqResults) > 0 {
		// Build structured context with FAQ prioritization
		contextsBuilder.WriteString("### 출처 1: 표준 질문 및 답변 (FAQ)\n")
		contextsBuilder.WriteString("【높은 신뢰도 - 우선 참조】\n")
		for i, result := range faqResults {
			passage := getEnrichedPassageForChat(ctx, result)
			if hasHighConfidenceFAQ && i == 0 {
				contextsBuilder.WriteString(fmt.Sprintf("[FAQ-%d] ⭐ 정확한 일치: %s\n", i+1, passage))
			} else {
				contextsBuilder.WriteString(fmt.Sprintf("[FAQ-%d] %s\n", i+1, passage))
			}
		}

		if len(docResults) > 0 {
			contextsBuilder.WriteString("\n### 출처 2: 참조 문서\n")
			contextsBuilder.WriteString("【보충 자료 - FAQ로 답변할 수 없는 경우에만 참조】\n")
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

	// Replace placeholders in context template
	userContent := chatManage.SummaryConfig.ContextTemplate
	userContent = strings.ReplaceAll(userContent, "{{query}}", safeQuery)
	userContent = strings.ReplaceAll(userContent, "{{contexts}}", contextsBuilder.String())
	userContent = strings.ReplaceAll(userContent, "{{current_time}}", time.Now().Format("2006-01-02 15:04:05"))
	userContent = strings.ReplaceAll(userContent, "{{current_week}}", weekdayName[time.Now().Weekday()])

	// Set formatted content back to chat management
	chatManage.UserContent = userContent
	pipelineInfo(ctx, "IntoChatMessage", "output", map[string]interface{}{
		"session_id":       chatManage.SessionID,
		"user_content_len": len(chatManage.UserContent),
		"faq_priority":     chatManage.FAQPriorityEnabled,
	})
	return next()
}

// getEnrichedPassageForChat 채팅 메시지 준비를 위해 Content와 ImageInfo의 텍스트 내용 병합
func getEnrichedPassageForChat(ctx context.Context, result *types.SearchResult) string {
	// 이미지 정보가 없으면 내용을 직접 반환
	if result.Content == "" && result.ImageInfo == "" {
		return ""
	}

	// 내용만 있고 이미지 정보가 없는 경우
	if result.ImageInfo == "" {
		return result.Content
	}

	// 이미지 정보를 처리하고 내용과 병합
	return enrichContentWithImageInfo(ctx, result.Content, result.ImageInfo)
}

// 마크다운 이미지 링크를 매칭하는 정규 표현식
var markdownImageRegex = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

// enrichContentWithImageInfo 이미지 정보를 텍스트 내용과 병합
func enrichContentWithImageInfo(ctx context.Context, content string, imageInfoJSON string) string {
	// ImageInfo 파싱
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

	// 이미지 URL에서 정보로의 매핑 생성
	imageInfoMap := make(map[string]*types.ImageInfo)
	for i := range imageInfos {
		if imageInfos[i].URL != "" {
			imageInfoMap[imageInfos[i].URL] = &imageInfos[i]
		}
		// 원본 URL도 확인
		if imageInfos[i].OriginalURL != "" {
			imageInfoMap[imageInfos[i].OriginalURL] = &imageInfos[i]
		}
	}

	// 내용에서 모든 마크다운 이미지 링크 찾기
	matches := markdownImageRegex.FindAllStringSubmatch(content, -1)

	// 처리된 이미지 URL 저장용
	processedURLs := make(map[string]bool)

	pipelineInfo(ctx, "IntoChatMessage", "image_markdown_links", map[string]interface{}{
		"match_count": len(matches),
	})

	// 각 이미지 링크를 대체하고 설명 및 OCR 텍스트 추가
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		// 이미지 URL 추출, alt 텍스트 무시
		imgURL := match[2]

		// 해당 URL이 처리되었음을 표시
		processedURLs[imgURL] = true

		// 일치하는 이미지 정보 찾기
		imgInfo, found := imageInfoMap[imgURL]

		// 일치하는 이미지 정보를 찾으면 설명 및 OCR 텍스트 추가
		if found && imgInfo != nil {
			replacement := match[0] + "\n"
			if imgInfo.Caption != "" {
				replacement += fmt.Sprintf("이미지 설명: %s\n", imgInfo.Caption)
			}
			if imgInfo.OCRText != "" {
				replacement += fmt.Sprintf("이미지 텍스트: %s\n", imgInfo.OCRText)
			}
			content = strings.Replace(content, match[0], replacement, 1)
		}
	}

	// 내용에서 찾을 수 없지만 ImageInfo에 존재하는 이미지 처리
	var additionalImageTexts []string
	for _, imgInfo := range imageInfos {
		// 이미지 URL이 이미 처리된 경우 건너뜀
		if processedURLs[imgInfo.URL] || processedURLs[imgInfo.OriginalURL] {
			continue
		}

		var imgTexts []string
		if imgInfo.Caption != "" {
			imgTexts = append(imgTexts, fmt.Sprintf("이미지 %s 의 설명 정보: %s", imgInfo.URL, imgInfo.Caption))
		}
		if imgInfo.OCRText != "" {
			imgTexts = append(imgTexts, fmt.Sprintf("이미지 %s 의 텍스트: %s", imgInfo.URL, imgInfo.OCRText))
		}

		if len(imgTexts) > 0 {
			additionalImageTexts = append(additionalImageTexts, imgTexts...)
		}
	}

	// 추가 이미지 정보가 있는 경우 내용 끝에 추가
	if len(additionalImageTexts) > 0 {
		if content != "" {
			content += "\n\n"
		}
		content += "추가 이미지 정보:\n" + strings.Join(additionalImageTexts, "\n")
	}

	pipelineInfo(ctx, "IntoChatMessage", "image_enrich_summary", map[string]interface{}{
		"markdown_images": len(matches),
		"additional_imgs": len(additionalImageTexts),
	})

	return content
}
