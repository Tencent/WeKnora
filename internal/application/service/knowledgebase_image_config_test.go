package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// imageConfiguredKB is a knowledge base whose image settings have been filled
// in: batching, the describe-round downscale, a class policy and the
// post-processing engine all carry non-zero values, so a reset is visible.
func imageConfiguredKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:       "kb-1",
		TenantID: 1,
		Name:     "before",
		Type:     types.KnowledgeBaseTypeDocument,
		ImageProcessingConfig: types.ImageProcessingConfig{
			ModelID:         "vlm-image",
			BatchSize:       16,
			ClassifyMaxEdge: 640,
			ClassPolicies: map[string]types.ImageClassPolicy{
				string(types.ImageClassDecorative): {OCR: false, Caption: true},
			},
			PostProcessImageEnabled: true,
			PostProcessImageRules: []types.ImageRule{{
				ID:     "retire-decorative-images",
				Match:  types.ImageMatchSpec{Classes: []string{string(types.ImageClassDecorative)}},
				Action: DropImageReferenceActionName,
			}},
		},
	}
}

// A request that does not mention image_processing_config must leave the image
// settings alone. Clients that predate batching, the classify downscale, the
// class policies or the post-processing mode send no such field at all, and
// without this contract saving such a knowledge base silently resets every one
// of them — which turns "rename a knowledge base" into data loss.
func TestUpdateKnowledgeBase_KeepsImageConfigWhenRequestOmitsIt(t *testing.T) {
	t.Parallel()

	repo := newFakeKBRepo()
	svc := newPR3KBService(repo, &fakeRegistry{registered: map[string]struct{}{}}, &fakeOwnership{})
	stored := imageConfiguredKB()
	repo.rows[stored.ID] = stored

	// The whole config is present but the image section is absent — this is the
	// shape every existing client sends.
	updated, err := svc.UpdateKnowledgeBase(ctxWithTenant(1), stored.ID, "renamed", "desc",
		&types.KnowledgeBaseConfig{})
	require.NoError(t, err)

	got := updated.ImageProcessingConfig
	assert.Equal(t, "vlm-image", got.ModelID, "image model was reset")
	assert.Equal(t, 16, got.BatchSize, "batch size was reset")
	assert.Equal(t, 640, got.ClassifyMaxEdge, "classify downscale was reset")
	assert.True(t, got.PostProcessImageEnabled, "post-process switch was reset")
	assert.Len(t, got.PostProcessImageRules, 1, "post-process rules were reset")
	policy, ok := got.ClassPolicies[string(types.ImageClassDecorative)]
	assert.True(t, ok, "class policies were reset")
	assert.False(t, policy.OCR, "class policy contents were reset")
	assert.True(t, policy.Caption, "class policy contents were reset")

	// The fields the request did carry still land.
	assert.Equal(t, "renamed", updated.Name)
	assert.Equal(t, "desc", updated.Description)
}

// Sending the object replaces the configuration wholesale, empty included —
// that is how a caller clears the settings on purpose. Pinned so the
// nil-means-unchanged rule is not mistaken for a merge.
func TestUpdateKnowledgeBase_ReplacesImageConfigWhenRequestCarriesIt(t *testing.T) {
	t.Parallel()

	repo := newFakeKBRepo()
	svc := newPR3KBService(repo, &fakeRegistry{registered: map[string]struct{}{}}, &fakeOwnership{})
	stored := imageConfiguredKB()
	repo.rows[stored.ID] = stored

	updated, err := svc.UpdateKnowledgeBase(ctxWithTenant(1), stored.ID, stored.Name, stored.Description,
		&types.KnowledgeBaseConfig{
			ImageProcessingConfig: &types.ImageProcessingConfig{ModelID: "vlm-image-2"},
		})
	require.NoError(t, err)

	got := updated.ImageProcessingConfig
	assert.Equal(t, "vlm-image-2", got.ModelID)
	assert.Zero(t, got.BatchSize, "an explicit object replaces the whole configuration, it does not merge")
	assert.Zero(t, got.ClassifyMaxEdge)
	assert.Empty(t, got.PostProcessImageRules)
	assert.Nil(t, got.ClassPolicies)
}
