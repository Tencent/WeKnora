package service

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestFingerprintEvaluationDatasetIsContentSensitive(t *testing.T) {
	dataset := []*types.QAPair{{QID: 1, Question: "q", PIDs: []int{2}, Passages: []string{"p"}, AID: 3, Answer: "a"}}
	first, err := fingerprintEvaluationDataset(dataset)
	require.NoError(t, err)
	second, err := fingerprintEvaluationDataset(dataset)
	require.NoError(t, err)
	require.Equal(t, first, second)

	dataset[0].Answer = "changed"
	changed, err := fingerprintEvaluationDataset(dataset)
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
}

func TestSnapshotEvaluationModelExcludesCredentials(t *testing.T) {
	updatedAt := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	model := &types.Model{
		ID: "embed-1", Name: "embedding-v1", DisplayName: "Embedding",
		Type: types.ModelTypeEmbedding, Source: types.ModelSourceRemote, UpdatedAt: updatedAt,
		Parameters: types.ModelParameters{
			APIKey: "secret-one", AppSecret: "secret-two", Provider: "openai",
			CustomHeaders:       map[string]string{"Authorization": "secret-three"},
			EmbeddingParameters: types.EmbeddingParameters{Dimension: 1024, TruncatePromptTokens: 512},
		},
	}
	first := snapshotEvaluationModel("embedding", model)
	model.Parameters.APIKey = "different-secret"
	model.Parameters.AppSecret = "different-app-secret"
	model.Parameters.CustomHeaders["Authorization"] = "different-header"
	second := snapshotEvaluationModel("embedding", model)

	require.Equal(t, first.ConfigFingerprint, second.ConfigFingerprint)
	require.Equal(t, "embedding", first.Role)
	require.Equal(t, 1024, first.Dimensions)
	require.NotContains(t, first.ConfigFingerprint, "secret")
}
