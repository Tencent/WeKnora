package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/desensitization"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func (s *knowledgeService) maskParsedMarkdown(ctx context.Context, kb *types.KnowledgeBase, text string) (string, error) {
	if kb == nil || !kb.DesensitizationConfig.IsEnabled() {
		return text, nil
	}
	cfg := *kb.DesensitizationConfig
	cfg.Normalize()
	deps := desensitization.Deps{}
	if s.config != nil && s.config.Desensitization != nil {
		deps.PresidioAnalyzerURL = s.config.Desensitization.PresidioAnalyzerURL
	}
	if cfg.Engine == types.DesensitizationEngineLLM {
		deps.Complete = s.desensitizationCompleter(kb)
	}
	out, report, err := desensitization.Apply(ctx, text, cfg, deps)
	if err != nil {
		return "", err
	}
	if report != nil {
		logger.Infof(ctx, "Desensitized markdown for kb=%s engine=%v spans=%v", kb.ID, report["engine"], report["span_count"])
	}
	return out, nil
}

func (s *knowledgeService) desensitizationCompleter(kb *types.KnowledgeBase) desensitization.Completer {
	return func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		if s.modelService == nil {
			return "", fmt.Errorf("%w: model service unavailable", desensitization.ErrEngineUnavailable)
		}
		modelID := ""
		if kb.DesensitizationConfig != nil {
			modelID = strings.TrimSpace(kb.DesensitizationConfig.LLMModelID)
		}
		if modelID == "" {
			modelID = strings.TrimSpace(kb.SummaryModelID)
		}
		if modelID == "" {
			return "", fmt.Errorf("%w: llm model is not configured", desensitization.ErrEngineUnavailable)
		}
		chatModel, err := s.modelService.GetChatModel(ctx, modelID)
		if err != nil {
			return "", err
		}
		resp, err := chatModel.Chat(ctx, []chat.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		}, nil)
		if err != nil {
			return "", err
		}
		if resp == nil {
			return "", fmt.Errorf("%w: empty llm response", desensitization.ErrEngineUnavailable)
		}
		return resp.Content, nil
	}
}

func persistDesensitizationFailure(ctx context.Context, repo interfaces.KnowledgeRepository, knowledge *types.Knowledge, err error) error {
	out := failDesensitization(knowledge, err)
	if knowledge != nil && repo != nil {
		knowledge.UpdatedAt = time.Now()
		_ = repo.UpdateKnowledge(ctx, knowledge)
	}
	return out
}

func failDesensitization(knowledge *types.Knowledge, err error) error {
	if knowledge != nil {
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "desensitization failed: " + err.Error()
	}
	return fmt.Errorf("desensitization failed: %w", err)
}
