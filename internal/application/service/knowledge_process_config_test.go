package service

import (
	"context"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func processConfigBoolPtr(v bool) *bool {
	return &v
}

func testKBWithGraphEnabled(enabled bool) *types.KnowledgeBase {
	return &types.KnowledgeBase{
		IndexingStrategy: types.IndexingStrategy{GraphEnabled: enabled},
		ExtractConfig:    &types.ExtractConfig{Enabled: enabled},
	}
}

func TestResolveProcessConfig_OverridesChunkSize(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		ChunkingConfig: types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: 50},
	}
	overrides := &types.KnowledgeProcessOverrides{
		ChunkingConfig: &types.ChunkingConfig{ChunkSize: 2048},
	}
	eff := ResolveProcessConfig(kb, overrides)
	require.Equal(t, 2048, eff.ChunkingConfig.ChunkSize)
	require.Equal(t, 50, eff.ChunkingConfig.ChunkOverlap)
}

func TestResolveProcessConfig_GraphDisabled(t *testing.T) {
	t.Parallel()

	kb := testKBWithGraphEnabled(true)
	overrides := &types.KnowledgeProcessOverrides{GraphEnabled: processConfigBoolPtr(false)}
	eff := ResolveProcessConfig(kb, overrides)
	require.False(t, eff.GraphEnabled)
}

func TestResolveProcessConfig_GraphRequiresExtractEnabled(t *testing.T) {
	t.Parallel()

	kb := testKBWithGraphEnabled(true)
	overrides := &types.KnowledgeProcessOverrides{
		GraphEnabled:  processConfigBoolPtr(true),
		ExtractConfig: &types.ExtractConfig{Enabled: false},
	}
	eff := ResolveProcessConfig(kb, overrides)
	require.False(t, eff.ExtractConfig.Enabled)
	require.False(t, eff.GraphEnabled)
}

func TestResolveProcessConfig_NilOverridesUsesKBDefaults(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		ChunkingConfig: types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: 50},
		VLMConfig:      types.VLMConfig{Enabled: true, ModelID: "vlm-1"},
		ASRConfig:      types.ASRConfig{Enabled: true, ModelID: "asr-1"},
		QuestionGenerationConfig: &types.QuestionGenerationConfig{
			Enabled:       true,
			QuestionCount: 3,
		},
		IndexingStrategy: types.IndexingStrategy{GraphEnabled: true},
		ExtractConfig:    &types.ExtractConfig{Enabled: true, Tags: []string{"tag-a"}},
	}
	kb.ChunkingConfig.EnableMultimodal = true

	eff := ResolveProcessConfig(kb, nil)

	require.Equal(t, 512, eff.ChunkingConfig.ChunkSize)
	require.Equal(t, 50, eff.ChunkingConfig.ChunkOverlap)
	require.True(t, eff.EnableMultimodel)
	require.Equal(t, "vlm-1", eff.VLMConfig.ModelID)
	require.Equal(t, "asr-1", eff.ASRConfig.ModelID)
	require.True(t, eff.QuestionGenerationConfig.Enabled)
	require.Equal(t, 3, eff.QuestionGenerationConfig.QuestionCount)
	require.True(t, eff.GraphEnabled)
	require.True(t, eff.ExtractConfig.Enabled)
	require.Equal(t, []string{"tag-a"}, eff.ExtractConfig.Tags)
}

func TestBuildSplitterConfigFromChunking_UsesEffectiveChunkingConfig(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		ChunkingConfig: types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: 50, Strategy: "token"},
	}
	overrides := &types.KnowledgeProcessOverrides{
		ChunkingConfig: &types.ChunkingConfig{ChunkSize: 1500, ChunkOverlap: 120, Strategy: "character"},
	}
	eff := ResolveProcessConfig(kb, overrides)
	cfg := buildSplitterConfigFromChunking(eff.ChunkingConfig)

	require.Equal(t, 1500, cfg.ChunkSize)
	require.Equal(t, 120, cfg.ChunkOverlap)
	require.Equal(t, "character", cfg.Strategy)
}

func TestEffectiveChunkingConfig_ResolveParserEngineFromOverrides(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		ChunkingConfig: types.ChunkingConfig{
			ParserEngineRules: []types.ParserEngineRule{
				{FileTypes: []string{"pdf"}, Engine: "builtin"},
			},
		},
	}
	overrides := &types.KnowledgeProcessOverrides{
		ParserEngineRules: []types.ParserEngineRule{
			{FileTypes: []string{"pdf"}, Engine: "mineru"},
		},
	}
	eff := ResolveProcessConfig(kb, overrides)
	require.Equal(t, "mineru", eff.ChunkingConfig.ResolveParserEngine("pdf"))
}

func TestResolveProcessConfig_ParserEngineRulesReplaced(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		ChunkingConfig: types.ChunkingConfig{
			ParserEngineRules: []types.ParserEngineRule{
				{FileTypes: []string{"pdf"}, Engine: "builtin"},
			},
		},
	}
	overrides := &types.KnowledgeProcessOverrides{
		ParserEngineRules: []types.ParserEngineRule{
			{FileTypes: []string{"docx"}, Engine: "custom"},
		},
	}
	eff := ResolveProcessConfig(kb, overrides)
	require.Len(t, eff.ChunkingConfig.ParserEngineRules, 1)
	require.Equal(t, []string{"docx"}, eff.ChunkingConfig.ParserEngineRules[0].FileTypes)
	require.Equal(t, "custom", eff.ChunkingConfig.ParserEngineRules[0].Engine)
}

func TestResolveProcessConfig_EnableMultimodelOverride(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm-1"},
	}
	overrides := &types.KnowledgeProcessOverrides{
		EnableMultimodel: processConfigBoolPtr(false),
	}
	eff := ResolveProcessConfig(kb, overrides)
	require.False(t, eff.EnableMultimodel)
}

func TestResolveProcessConfig_ExtractConfigFieldMerge(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		ExtractConfig: &types.ExtractConfig{
			Enabled: true,
			Text:    "base text",
			Tags:    []string{"base-tag"},
		},
	}
	overrides := &types.KnowledgeProcessOverrides{
		ExtractConfig: &types.ExtractConfig{
			Enabled: true,
			Tags:    []string{"override-tag"},
		},
	}
	eff := ResolveProcessConfig(kb, overrides)
	require.True(t, eff.ExtractConfig.Enabled)
	require.Equal(t, "base text", eff.ExtractConfig.Text)
	require.Equal(t, []string{"override-tag"}, eff.ExtractConfig.Tags)
}

func TestValidateProcessOverrides_NilOverrides(t *testing.T) {
	t.Parallel()

	err := ValidateProcessOverrides(context.Background(), &types.KnowledgeBase{}, nil, []string{"png"})
	require.NoError(t, err)
}

func TestValidateProcessOverrides_ImageRequiresVLM(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		VLMConfig: types.VLMConfig{Enabled: false},
	}
	err := ValidateProcessOverrides(context.Background(), kb, &types.KnowledgeProcessOverrides{}, []string{"png"})
	require.Error(t, err)
	var badReq *werrors.AppError
	require.ErrorAs(t, err, &badReq)
}

func TestValidateProcessOverrides_ImageWithEffectiveVLM(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		VLMConfig: types.VLMConfig{Enabled: false},
	}
	overrides := &types.KnowledgeProcessOverrides{
		VLMConfig: &types.VLMConfig{Enabled: true, ModelID: "vlm-1"},
	}
	err := ValidateProcessOverrides(context.Background(), kb, overrides, []string{"jpg"})
	require.NoError(t, err)
}

func TestValidateProcessOverrides_AudioRequiresASR(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		ASRConfig: types.ASRConfig{Enabled: false},
	}
	err := ValidateProcessOverrides(context.Background(), kb, &types.KnowledgeProcessOverrides{}, []string{"mp3"})
	require.Error(t, err)
}

func TestValidateProcessOverrides_AudioWithEffectiveASR(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		ASRConfig: types.ASRConfig{Enabled: false},
	}
	overrides := &types.KnowledgeProcessOverrides{
		ASRConfig: &types.ASRConfig{Enabled: true, ModelID: "asr-1"},
	}
	err := ValidateProcessOverrides(context.Background(), kb, overrides, []string{"wav"})
	require.NoError(t, err)
}

func TestValidateProcessOverrides_NonMediaFileTypes(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{}
	err := ValidateProcessOverrides(context.Background(), kb, &types.KnowledgeProcessOverrides{}, []string{"pdf", "txt"})
	require.NoError(t, err)
}

func TestValidateProcessOverrides_COSIncompleteForImage(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			COS: &types.COSEngineConfig{SecretID: "id"},
		},
	})
	kb := &types.KnowledgeBase{
		VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm-1"},
	}
	kb.SetStorageProvider("cos")

	err := ValidateProcessOverrides(ctx, kb, &types.KnowledgeProcessOverrides{}, []string{"png"})
	require.Error(t, err)
}
