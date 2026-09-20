package tools

import (
	"context"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/desensitization"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func desensitizationDeps(cfg *config.Config) desensitization.Deps {
	deps := desensitization.Deps{}
	if cfg != nil && cfg.Desensitization != nil {
		deps.PresidioAnalyzerURL = cfg.Desensitization.PresidioAnalyzerURL
	}
	return deps
}

func maskModelFacing(
	ctx context.Context, kbSvc interfaces.KnowledgeBaseService, cfg *config.Config, kbID, text string,
) (string, error) {
	if text == "" || kbSvc == nil || kbID == "" {
		return text, nil
	}
	kb, err := kbSvc.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		return "", err
	}
	return desensitization.MaskIfEnabled(ctx, kb, text, desensitizationDeps(cfg))
}

func maskKnowledgeForModel(
	ctx context.Context, kbSvc interfaces.KnowledgeBaseService, cfg *config.Config, knowledge *types.Knowledge,
) (title, filename, description string, err error) {
	if knowledge == nil {
		return "", "", "", nil
	}
	title, err = maskModelFacing(ctx, kbSvc, cfg, knowledge.KnowledgeBaseID, knowledge.Title)
	if err != nil {
		return "", "", "", err
	}
	filename, err = maskModelFacing(ctx, kbSvc, cfg, knowledge.KnowledgeBaseID, knowledge.FileName)
	if err != nil {
		return "", "", "", err
	}
	description, err = maskModelFacing(ctx, kbSvc, cfg, knowledge.KnowledgeBaseID, knowledge.Description)
	if err != nil {
		return "", "", "", err
	}
	return title, filename, description, nil
}
