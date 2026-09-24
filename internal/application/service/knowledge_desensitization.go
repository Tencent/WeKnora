package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/desensitization"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func maskModelFacingText(
	ctx context.Context, kb *types.KnowledgeBase, text string, deps desensitization.Deps,
) (string, error) {
	return desensitization.MaskIfEnabled(ctx, kb, text, deps)
}

func desensitizationDepsFromConfig(cfg *config.Config) desensitization.Deps {
	deps := desensitization.Deps{}
	if cfg != nil && cfg.Desensitization != nil {
		deps.PresidioAnalyzerURL = cfg.Desensitization.PresidioAnalyzerURL
	}
	return deps
}

func (s *knowledgeService) desensitizationDeps(kb *types.KnowledgeBase) desensitization.Deps {
	deps := desensitizationDepsFromConfig(nil)
	if s != nil {
		deps = desensitizationDepsFromConfig(s.config)
	}
	if kb != nil && kb.DesensitizationConfig != nil &&
		kb.DesensitizationConfig.Engine == types.DesensitizationEngineLLM {
		deps.Complete = s.desensitizationCompleter(kb)
	}
	return deps
}

func (s *knowledgeService) maskParsedMarkdown(
	ctx context.Context, kb *types.KnowledgeBase, text string,
) (string, error) {
	if kb == nil || !kb.DesensitizationConfig.IsEnabled() {
		return text, nil
	}
	out, err := maskModelFacingText(ctx, kb, text, s.desensitizationDeps(kb))
	if err != nil {
		return "", err
	}
	logger.Infof(ctx, "Desensitized markdown for kb=%s", kb.ID)
	return out, nil
}

// indexKnowledge returns a model-facing copy whose Title is masked. The stored
// knowledge.Title is left unchanged so operators can still match the original file.
func (s *knowledgeService) indexKnowledge(
	ctx context.Context, kb *types.KnowledgeBase, knowledge *types.Knowledge,
) (*types.Knowledge, error) {
	if knowledge == nil {
		return nil, nil
	}
	masked, err := s.maskParsedMarkdown(ctx, kb, knowledge.Title)
	if err != nil {
		return nil, err
	}
	return knowledgeWithIndexTitle(knowledge, masked), nil
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

func maskStringSlice(
	ctx context.Context, kb *types.KnowledgeBase, items []string, deps desensitization.Deps,
) ([]string, error) {
	if len(items) == 0 {
		return items, nil
	}
	out := make([]string, len(items))
	for i, item := range items {
		masked, err := maskModelFacingText(ctx, kb, item, deps)
		if err != nil {
			return nil, err
		}
		out[i] = masked
	}
	return out, nil
}

func maskProfileAggregate(
	ctx context.Context, kb *types.KnowledgeBase, agg *types.KnowledgeBaseProfileAggregate, cfg *config.Config,
) (*types.KnowledgeBaseProfileAggregate, error) {
	if agg == nil || kb == nil || !kb.DesensitizationConfig.IsEnabled() {
		return agg, nil
	}
	out := *agg
	deps := desensitizationDepsFromConfig(cfg)
	var err error
	if out.SampleTitles, err = maskStringSlice(ctx, kb, agg.SampleTitles, deps); err != nil {
		return nil, err
	}
	if out.SampleGists, err = maskStringSlice(ctx, kb, agg.SampleGists, deps); err != nil {
		return nil, err
	}
	if out.SampleQuestions, err = maskStringSlice(ctx, kb, agg.SampleQuestions, deps); err != nil {
		return nil, err
	}
	return &out, nil
}

func persistDesensitizationFailure(
	ctx context.Context, repo interfaces.KnowledgeRepository, knowledge *types.Knowledge, err error,
) error {
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
