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
	copy, err := copyKnowledgeForModel(ctx, kbSvc, cfg, knowledge)
	if err != nil || copy == nil {
		return "", "", "", err
	}
	return copy.Title, copy.FileName, copy.Description, nil
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
	copy := *knowledge
	if knowledge.KnowledgeBaseID == "" || kbSvc == nil {
		return &copy, nil
	}
	kb, err := kbSvc.GetKnowledgeBaseByIDOnly(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	deps := desensitizationDeps(cfg)
	copy.Title, err = desensitization.MaskIfEnabled(ctx, kb, knowledge.Title, deps)
	if err != nil {
		return nil, err
	}
	copy.FileName, err = desensitization.MaskIfEnabled(ctx, kb, knowledge.FileName, deps)
	if err != nil {
		return nil, err
	}
	copy.Description, err = desensitization.MaskIfEnabled(ctx, kb, knowledge.Description, deps)
	if err != nil {
		return nil, err
	}
	if !kb.DesensitizationConfig.IsEnabled() {
		return &copy, nil
	}
	copy.Metadata = nil
	if copy.Type == "url" {
		copy.Source = ""
	} else {
		copy.Source, err = desensitization.MaskIfEnabled(ctx, kb, knowledge.Source, deps)
		if err != nil {
			return nil, err
		}
	}
	return &copy, nil
}
