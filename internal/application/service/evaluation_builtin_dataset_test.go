package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinEvaluationDatasetRegistrationIsPinnedAndDeterministic(t *testing.T) {
	first, err := BuiltinEvaluationDatasetRegistration()
	require.NoError(t, err)
	second, err := BuiltinEvaluationDatasetRegistration()
	require.NoError(t, err)

	assert.Equal(t, "default", first.DatasetID)
	assert.Equal(t, "d598d48018f1da0705920f4432b68a9309c391adbe1403bb1278771b22fe923f",
		first.ExpectedArtifactSHA256)
	assert.Equal(t, first.ExpectedArtifactSHA256, types.EvaluationDatasetArtifactSHA256(first.ArtifactBytes))
	require.NotEmpty(t, first.Content.Passages)
	require.NotEmpty(t, first.Content.Questions)
	require.NotEmpty(t, first.Content.Relevance)
	assert.Equal(t, types.CanonicalEvaluationDatasetContentSHA256(first.Content),
		types.CanonicalEvaluationDatasetContentSHA256(second.Content))
	assert.Equal(t, first.ArtifactBytes, second.ArtifactBytes)
}

func TestBuiltinEvaluationDatasetRegistrationPersistsCanonicalContent(t *testing.T) {
	db := setupEvaluationDatasetServiceTestDB(t)
	registry := newEvaluationDatasetRegistryService(db)
	registration, err := BuiltinEvaluationDatasetRegistration()
	require.NoError(t, err)

	version, err := registry.RegisterBuiltinDataset(t.Context(), registration)
	require.NoError(t, err)
	stored, err := registry.GetVersionContent(t.Context(), 17, version.ID)
	require.NoError(t, err)
	assert.Equal(t, registration.ExpectedArtifactSHA256, stored.Version.ArtifactSHA256)
	assert.Equal(t, types.CanonicalEvaluationDatasetContentSHA256(registration.Content),
		stored.Version.ContentSHA256)
	assert.Len(t, stored.Passages, len(registration.Content.Passages))
	assert.Len(t, stored.Questions, len(registration.Content.Questions))
	assert.Len(t, stored.Relevance, len(registration.Content.Relevance))
}
