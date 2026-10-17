package service

import (
	"context"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestResolveProcessConfig_LegacyOverrideSelectsImagePipeline(t *testing.T) {
	for _, base := range []bool{false, true} {
		for _, explicitKB := range []bool{false, true} {
			kb := &types.KnowledgeBase{ImageProcessingConfig: types.ImageProcessingConfig{
				ImageAttrsEnabled:   base,
				ImagePipelineParams: map[string]any{"default.caption.thinking_enabled": true},
			}}
			if explicitKB {
				kb.ImageProcessingConfig.ImagePipelineID = string(types.ImagePipelineIDFor(base))
			}
			for _, override := range []bool{false, true} {
				eff := ResolveProcessConfig(kb, &types.KnowledgeProcessOverrides{ImageAttrsEnabled: &override})
				pipeline := selectImagePipeline(&types.ImageMultimodalPayload{
					ImagePipelineID: eff.ImagePipelineID, ImageAttrsEnabled: eff.ImageAttrsEnabled,
				})
				require.Equal(t, types.ImagePipelineIDFor(override), pipeline.ID())
				if base == override {
					require.Equal(t, kb.ImageProcessingConfig.ImagePipelineParams, eff.ImagePipelineParams)
				} else {
					require.Empty(t, eff.ImagePipelineParams,
						"a different pipeline must not inherit the old parameters")
				}
			}
		}
	}
	// A new client's explicit choice takes precedence over its legacy switch.
	id := types.ImagePipelineLegacyCaptionOCR
	eff := ResolveProcessConfig(&types.KnowledgeBase{}, &types.KnowledgeProcessOverrides{
		ImageAttrsEnabled: processConfigBoolPtr(true), ImagePipelineID: &id,
		ImagePipelineParams: map[string]any{manualCaptionKey: false},
	})
	require.Equal(t, types.ImagePipelineDefault, eff.ImagePipelineID)
	require.False(t, eff.ImageAttrsEnabled)
	require.Equal(t, false, eff.ImagePipelineParams[manualCaptionKey])
}

func TestProcessOverrides_ValidateImagePipelineBeforePersistence(t *testing.T) {
	id := types.ImagePipelineDefault
	for _, enabled := range []bool{false, true} {
		for _, caption := range []bool{false, true} {
			kb := &types.KnowledgeBase{}
			overrides := &types.KnowledgeProcessOverrides{
				EnableMultimodel: &enabled, ImagePipelineID: &id,
				ImagePipelineParams: map[string]any{manualCaptionKey: caption, manualOCRKey: false},
			}
			knowledge := &types.Knowledge{}
			_, uploadErr := resolveFileImportProcessConfig(context.Background(), kb, "pdf", overrides, nil)
			_, reparseErr := ApplyKnowledgeProcessOverrides(
				context.Background(), kb, knowledge, overrides, []string{"pdf"}, nil)
			validationErr := ValidateProcessOverrides(context.Background(), kb, overrides, []string{"pdf"})
			for _, err := range []error{uploadErr, reparseErr, validationErr} {
				if enabled && !caption {
					var badRequest *werrors.AppError
					require.ErrorAs(t, err, &badRequest)
					require.Equal(t, werrors.ErrBadRequest, badRequest.Code)
					require.Contains(t, err.Error(), "noActionEnabled")
				} else {
					require.NoError(t, err)
				}
			}
			if enabled && !caption {
				got, err := knowledge.ProcessOverrides()
				require.NoError(t, err)
				require.Nil(t, got, "invalid reparse settings must not be persisted")
			}
		}
	}
}

func TestProcessOverrides_ValidateLegacyMultimodalFlag(t *testing.T) {
	id := types.ImagePipelineDefault
	overrides := &types.KnowledgeProcessOverrides{
		ImagePipelineID:     &id,
		ImagePipelineParams: map[string]any{manualCaptionKey: false, manualOCRKey: false},
	}
	enabled := true
	_, err := resolveFileImportProcessConfig(context.Background(), &types.KnowledgeBase{}, "pdf", overrides, &enabled)
	require.ErrorContains(t, err, "noActionEnabled")
	_, err = ApplyKnowledgeProcessOverrides(
		context.Background(), &types.KnowledgeBase{}, &types.Knowledge{}, overrides, []string{"pdf"}, &enabled)
	require.ErrorContains(t, err, "noActionEnabled")
}
