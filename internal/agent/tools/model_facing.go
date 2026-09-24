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
	facing, err := copyKnowledgeForModel(ctx, kbSvc, cfg, knowledge)
	if err != nil || facing == nil {
		return "", "", "", err
	}
	return facing.Title, facing.FileName, facing.Description, nil
}

// copyKnowledgeForModel returns a shallow copy safe to send to the chat model.
// Stored Title / Metadata / Source are left unchanged. When desensitization is
// on, ingestion internals (manual Markdown in Metadata.content, transfer
// markers, raw URL sources) are omitted rather than copied through.
func copyKnowledgeForModel(
	ctx context.Context, kbSvc interfaces.KnowledgeBaseService, cfg *config.Config, knowledge *types.Knowledge,
) (*types.Knowledge, error) {
	if knowledge == nil {
		return nil, nil
	}
	facing := *knowledge
	if knowledge.KnowledgeBaseID == "" || kbSvc == nil {
		return &facing, nil
	}
	kb, err := kbSvc.GetKnowledgeBaseByIDOnly(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	deps := desensitizationDeps(cfg)
	facing.Title, err = desensitization.MaskIfEnabled(ctx, kb, knowledge.Title, deps)
	if err != nil {
		return nil, err
	}
	facing.FileName, err = desensitization.MaskIfEnabled(ctx, kb, knowledge.FileName, deps)
	if err != nil {
		return nil, err
	}
	facing.Description, err = desensitization.MaskIfEnabled(ctx, kb, knowledge.Description, deps)
	if err != nil {
		return nil, err
	}
	if !kb.DesensitizationConfig.IsEnabled() {
		return &facing, nil
	}
	facing.Metadata = nil
	if facing.Type == "url" {
		facing.Source = ""
	} else {
		facing.Source, err = desensitization.MaskIfEnabled(ctx, kb, knowledge.Source, deps)
		if err != nil {
			return nil, err
		}
	}
	return &facing, nil
}
