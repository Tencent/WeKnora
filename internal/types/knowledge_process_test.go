package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestKnowledgeProcessOverridesRoundtrip(t *testing.T) {
	k := &Knowledge{}
	overrides := &KnowledgeProcessOverrides{
		EnableMultimodel:      boolPtr(true),
		ChunkingConfig:        &ChunkingConfig{ChunkSize: 1024},
		ParserEngineOverrides: map[string]string{"pdf_force_scanned": "true"},
	}
	require.NoError(t, k.SetProcessOverrides(overrides))
	got, err := k.ProcessOverrides()
	require.NoError(t, err)
	require.NotNil(t, got)
	require.True(t, *got.EnableMultimodel)
	require.Equal(t, 1024, got.ChunkingConfig.ChunkSize)
	require.Equal(t, "true", got.ParserEngineOverrides["pdf_force_scanned"])
}

func TestSetProcessOverridesPreservesOtherMetadata(t *testing.T) {
	k := &Knowledge{}
	manualMeta := NewManualKnowledgeMetadata("# hello", ManualKnowledgeStatusDraft, 1)
	require.NoError(t, k.SetManualMetadata(manualMeta))

	overrides := &KnowledgeProcessOverrides{
		EnableMultimodel: boolPtr(false),
	}
	require.NoError(t, k.SetProcessOverrides(overrides))

	gotManual, err := k.ManualMetadata()
	require.NoError(t, err)
	require.NotNil(t, gotManual)
	require.Equal(t, "# hello", gotManual.Content)
	require.Equal(t, ManualKnowledgeFormatMarkdown, gotManual.Format)
	require.Equal(t, ManualKnowledgeStatusDraft, gotManual.Status)

	gotOverrides, err := k.ProcessOverrides()
	require.NoError(t, err)
	require.NotNil(t, gotOverrides)
	require.False(t, *gotOverrides.EnableMultimodel)
}
