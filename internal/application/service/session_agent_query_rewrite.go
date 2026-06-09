package service

import (
	"context"
	"fmt"
	"strings"

	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

func (s *sessionService) rewriteAgentQueryIfNeeded(
	ctx context.Context,
	req *types.QARequest,
	chatModelID string,
	llmContext []chat.Message,
) string {
	if req.CustomAgent == nil || !req.CustomAgent.Config.EnableRewrite {
		return req.Query
	}

	systemPrompt := ""
	userPrompt := ""
	if s.cfg != nil && s.cfg.Conversation != nil {
		systemPrompt = s.cfg.Conversation.RewritePromptSystem
		userPrompt = s.cfg.Conversation.RewritePromptUser
	}
	if req.CustomAgent.Config.RewritePromptSystem != "" {
		systemPrompt = req.CustomAgent.Config.RewritePromptSystem
	}
	if req.CustomAgent.Config.RewritePromptUser != "" {
		userPrompt = req.CustomAgent.Config.RewritePromptUser
	}
	if strings.TrimSpace(systemPrompt) == "" && strings.TrimSpace(userPrompt) == "" {
		logger.Warnf(ctx, "Agent query rewrite enabled but rewrite prompts are empty, using original query")
		return req.Query
	}

	modelID := chatModelID
	if req.CustomAgent.Config.QueryUnderstandModelID != "" {
		modelID = req.CustomAgent.Config.QueryUnderstandModelID
	}
	rewriteModel, err := s.modelService.GetChatModel(ctx, modelID)
	if err != nil {
		if req.CustomAgent.Config.QueryUnderstandModelID != "" && modelID != chatModelID {
			logger.Warnf(ctx, "Failed to get agent query-understand model %s: %v, falling back to chat model",
				modelID, err)
			rewriteModel, err = s.modelService.GetChatModel(ctx, chatModelID)
		}
		if err != nil {
			logger.Warnf(ctx, "Failed to get agent query rewrite model: %v, using original query", err)
			return req.Query
		}
	}

	vals := types.PlaceholderValues{
		"conversation": formatAgentRewriteHistory(llmContext),
		"query":        buildAgentRewriteQueryContent(req),
		"language":     "",
	}
	thinking := false
	resp, err := rewriteModel.Chat(ctx, []chat.Message{
		{Role: "system", Content: types.RenderPromptPlaceholders(systemPrompt, vals)},
		{Role: "user", Content: types.RenderPromptPlaceholders(userPrompt, vals)},
	}, &chat.ChatOptions{
		Temperature:         0.3,
		MaxCompletionTokens: 150,
		Thinking:            &thinking,
	})
	if err != nil || resp == nil {
		logger.Warnf(ctx, "Agent query rewrite failed: %v, using original query", err)
		return req.Query
	}

	rewrite := strings.TrimSpace(chatpipeline.ExtractRewriteQuery(resp.Content))
	if rewrite == "" {
		logger.Warnf(ctx, "Agent query rewrite returned empty query, using original query")
		return req.Query
	}
	logger.Infof(ctx, "Agent query rewritten for session %s", req.Session.ID)
	return rewrite
}

func buildAgentRewriteQueryContent(req *types.QARequest) string {
	queryContent := req.Query
	if req.ImageDescription != "" {
		queryContent += "\n\n[用户上传图片内容]\n" + req.ImageDescription
	} else if len(req.ImageURLs) > 0 {
		queryContent += fmt.Sprintf("\n\n<images_uploaded count=\"%d\" />", len(req.ImageURLs))
	} else {
		queryContent += "\n\n<no_image_attached />"
	}
	if len(req.Attachments) > 0 {
		queryContent += req.Attachments.BuildPrompt()
	} else {
		queryContent += "\n<no_document_attached />"
	}
	return queryContent
}

func formatAgentRewriteHistory(llmContext []chat.Message) string {
	if len(llmContext) == 0 {
		return ""
	}

	var builder strings.Builder
	for _, msg := range llmContext {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		switch msg.Role {
		case "user":
			builder.WriteString("------BEGIN------\n")
			builder.WriteString("User question: ")
			builder.WriteString(content)
			builder.WriteString("\n")
		case "assistant":
			builder.WriteString("Assistant answer: ")
			builder.WriteString(content)
			builder.WriteString("\n------END------\n")
		}
	}
	return builder.String()
}
