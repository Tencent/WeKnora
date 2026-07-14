package service

import (
	"context"
	"strings"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
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

func TestResolveProcessConfig_OverrideTogglesParentChild(t *testing.T) {
	t.Parallel()

	// KB has parent-child on; override snapshot turns it off.
	kbOn := &types.KnowledgeBase{
		ChunkingConfig: types.ChunkingConfig{ChunkSize: 512, EnableParentChild: true},
	}
	effOff := ResolveProcessConfig(kbOn, &types.KnowledgeProcessOverrides{
		ChunkingConfig: &types.ChunkingConfig{ChunkSize: 512, EnableParentChild: false},
	})
	require.False(t, effOff.ChunkingConfig.EnableParentChild)

	// KB has parent-child off; override snapshot turns it on.
	kbOff := &types.KnowledgeBase{
		ChunkingConfig: types.ChunkingConfig{ChunkSize: 512, EnableParentChild: false},
	}
	effOn := ResolveProcessConfig(kbOff, &types.KnowledgeProcessOverrides{
		ChunkingConfig: &types.ChunkingConfig{ChunkSize: 512, EnableParentChild: true},
	})
	require.True(t, effOn.ChunkingConfig.EnableParentChild)
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

func TestResolveProcessConfig_PreservesKnowledgeBasePromptInstructions(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{
		ChunkingConfig: types.ChunkingConfig{TableMetadataInstructions: "table context"},
		VLMConfig: types.VLMConfig{
			Enabled: true, ModelID: "vlm-1", DescriptionLanguage: "English", CustomInstructions: "read labels",
		},
		QuestionGenerationConfig: &types.QuestionGenerationConfig{
			Enabled: true, QuestionCount: 3, CustomInstructions: "customer questions",
		},
		ExtractConfig: &types.ExtractConfig{Enabled: true, CustomInstructions: "contract entities"},
	}
	overrides := &types.KnowledgeProcessOverrides{
		ChunkingConfig:           &types.ChunkingConfig{ChunkSize: 256},
		VLMConfig:                &types.VLMConfig{Enabled: true, ModelID: "vlm-2"},
		QuestionGenerationConfig: &types.QuestionGenerationConfig{Enabled: true, QuestionCount: 5},
		ExtractConfig:            &types.ExtractConfig{Enabled: true},
	}

	eff := ResolveProcessConfig(kb, overrides)
	require.Equal(t, "table context", eff.ChunkingConfig.TableMetadataInstructions)
	require.Equal(t, "English", eff.VLMConfig.DescriptionLanguage)
	require.Equal(t, "read labels", eff.VLMConfig.CustomInstructions)
	require.Equal(t, "customer questions", eff.QuestionGenerationConfig.CustomInstructions)
	require.Equal(t, "contract entities", eff.ExtractConfig.CustomInstructions)
}

func TestValidateProcessOverrides_RejectsOversizedInstructions(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{}
	overrides := &types.KnowledgeProcessOverrides{
		VLMConfig: &types.VLMConfig{
			CustomInstructions: strings.Repeat("x", types.MaxCustomPromptInstructionsLength+1),
		},
	}
	err := ValidateProcessOverrides(context.Background(), kb, overrides, []string{"txt"})
	require.Error(t, err)
	var badReq *werrors.AppError
	require.ErrorAs(t, err, &badReq)
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

func TestMergeParserEngineOverrides(t *testing.T) {
	t.Parallel()

	// 1. Both nil/empty
	merged := MergeParserEngineOverrides(nil, nil)
	require.Empty(t, merged)

	// 2. Tenant only
	merged = MergeParserEngineOverrides(map[string]string{"k1": "v1"}, nil)
	require.Equal(t, map[string]string{"k1": "v1"}, merged)

	// 3. Upload only
	merged = MergeParserEngineOverrides(nil, map[string]string{"k2": "v2"})
	require.Equal(t, map[string]string{"k2": "v2"}, merged)

	// 4. Overlap priority (upload override should take priority over tenant config)
	tenant := map[string]string{"k1": "tenant_val", "k2": "v2"}
	upload := map[string]string{"k1": "upload_val", "k3": "v3"}
	merged = MergeParserEngineOverrides(tenant, upload)
	require.Equal(t, map[string]string{
		"k1": "upload_val",
		"k2": "v2",
		"k3": "v3",
	}, merged)
}

func TestBuildParentChildConfigs_PropagatesStrategy(t *testing.T) {
	t.Parallel()

	base := chunker.SplitterConfig{
		ChunkSize:    1000,
		ChunkOverlap: 100,
		Separators:   []string{"\n\n", "\n"},
		Strategy:     chunker.StrategyAuto,
	}
	cc := types.ChunkingConfig{
		EnableParentChild: true,
		ParentChunkSize:   4096,
		ChildChunkSize:    512,
	}

	parent, child := buildParentChildConfigs(cc, base)

	require.Equal(t, chunker.StrategyAuto, parent.Strategy,
		"parent splitting must honour the configured strategy; empty resolves to the legacy tier")
	require.Equal(t, chunker.StrategyAuto, child.Strategy,
		"child splitting must honour the configured strategy; empty resolves to the legacy tier")
	require.Equal(t, 4096, parent.ChunkSize)
	require.Equal(t, 512, child.ChunkSize)
	require.Equal(t, base.ChunkOverlap, parent.ChunkOverlap)
	require.Equal(t, 512/5, child.ChunkOverlap)
	require.Equal(t, base.Separators, parent.Separators)
	require.Equal(t, base.Separators, child.Separators)
}
