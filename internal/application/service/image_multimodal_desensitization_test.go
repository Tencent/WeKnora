package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/desensitization"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubVLMModelService struct {
	interfaces.ModelService
	model *types.Model
}

func (s *stubVLMModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	return s.model, nil
}

func TestRejectRemoteVLMIfDesensitized(t *testing.T) {
	t.Parallel()
	enabled := &types.KnowledgeBase{
		DesensitizationConfig: &types.DesensitizationConfig{Enabled: true, Engine: types.DesensitizationEngineBuiltin},
	}
	svc := &ImageMultimodalService{
		modelService: &stubVLMModelService{model: &types.Model{ID: "vlm-remote", Source: types.ModelSourceRemote}},
	}

	require.NoError(t, svc.rejectRemoteVLMIfDesensitized(context.Background(), &types.KnowledgeBase{}, types.VLMConfig{ModelID: "vlm-remote"}))
	require.ErrorIs(t, svc.rejectRemoteVLMIfDesensitized(context.Background(), enabled, types.VLMConfig{}), desensitization.ErrCloudVLMForbidden)
	require.ErrorIs(t, svc.rejectRemoteVLMIfDesensitized(context.Background(), enabled, types.VLMConfig{ModelID: "vlm-remote"}), desensitization.ErrCloudVLMForbidden)

	local := &ImageMultimodalService{
		modelService: &stubVLMModelService{model: &types.Model{ID: "vlm-local", Source: types.ModelSourceLocal}},
	}
	require.NoError(t, local.rejectRemoteVLMIfDesensitized(context.Background(), enabled, types.VLMConfig{ModelID: "vlm-local"}))
}

func TestMaskOCRTextBeforePersist(t *testing.T) {
	t.Parallel()
	kb := &types.KnowledgeBase{
		DesensitizationConfig: &types.DesensitizationConfig{
			Enabled:     true,
			Engine:      types.DesensitizationEngineBuiltin,
			EntityTypes: []string{types.DesensitizationEntityCNMobile},
		},
	}
	out, err := maskModelFacingText(context.Background(), kb, "身份证背面 13800138000", desensitizationDepsFromConfig(nil))
	require.NoError(t, err)
	require.NotContains(t, out, "13800138000")
	require.Contains(t, out, "<手机号>")
}
