package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/desensitization"
	"github.com/Tencent/WeKnora/internal/types"
)

func enabledBuiltinKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID: "kb-1",
		DesensitizationConfig: &types.DesensitizationConfig{
			Enabled:     true,
			Engine:      types.DesensitizationEngineBuiltin,
			EntityTypes: []string{types.DesensitizationEntityCNMobile},
		},
	}
}

func TestMaskModelFacingTextBuiltin(t *testing.T) {
	t.Parallel()
	out, err := maskModelFacingText(context.Background(), enabledBuiltinKB(), "劳动合同-张三-13800138000", desensitization.Deps{})
	require.NoError(t, err)
	assert.Contains(t, out, "<手机号>")
	assert.NotContains(t, out, "13800138000")
}

func TestMaskModelFacingTextPresidioWithoutURLFailsClosed(t *testing.T) {
	t.Parallel()
	kb := &types.KnowledgeBase{
		ID: "kb-1",
		DesensitizationConfig: &types.DesensitizationConfig{
			Enabled: true,
			Engine:  types.DesensitizationEnginePresidio,
		},
	}
	_, err := maskModelFacingText(context.Background(), kb, "劳动合同-张三-13800138000", desensitizationDepsFromConfig(nil))
	require.ErrorIs(t, err, desensitization.ErrEngineUnavailable)
}

func TestMaskProfileAggregateMasksTitlesAndLeavesOriginal(t *testing.T) {
	t.Parallel()
	agg := &types.KnowledgeBaseProfileAggregate{
		SampleTitles:    []string{"劳动合同-张三-13800138000"},
		SampleGists:     []string{"联系 13800138000"},
		SampleQuestions: []string{"13800138000 是谁的电话"},
	}
	masked, err := maskProfileAggregate(context.Background(), enabledBuiltinKB(), agg, nil)
	require.NoError(t, err)
	require.NotNil(t, masked)
	assert.NotContains(t, masked.SampleTitles[0], "13800138000")
	assert.Contains(t, masked.SampleTitles[0], "<手机号>")
	assert.NotContains(t, masked.SampleGists[0], "13800138000")
	assert.NotContains(t, masked.SampleQuestions[0], "13800138000")
	assert.Equal(t, "劳动合同-张三-13800138000", agg.SampleTitles[0])
}
