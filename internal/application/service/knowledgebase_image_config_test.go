package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// customImageActions is a table that differs from the built-in one, so a
// preserved table can be told from a re-applied default.
func customImageActions() *types.ImageActionsConfig {
	return &types.ImageActionsConfig{
		OCR: types.ImageOCRAction{
			On: []types.ImageAttrCondition{
				{Prop: "contain.text", Is: "block"},
			},
		},
	}
}

// imageConfiguredKB is a knowledge base whose image settings have been filled
// in: the attribute-observed pipeline is on and the action table carries a value
// that differs from the built-in default, so a reset is visible.
func imageConfiguredKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:       "kb-1",
		TenantID: 1,
		Name:     "before",
		Type:     types.KnowledgeBaseTypeDocument,
		ImageProcessingConfig: types.ImageProcessingConfig{
			ModelID:           "vlm-image",
			ImageAttrsEnabled: true,
			ImageActions:      customImageActions(),
		},
	}
}

// A request that does not mention image_processing_config must leave the image
// settings alone. Clients that predate the attribute pipeline send no such
// field at all, and without this contract saving such a knowledge base silently
// resets the switch and the action table — which turns "rename a knowledge base"
// into data loss.
func TestUpdateKnowledgeBase_KeepsImageConfigWhenRequestOmitsIt(t *testing.T) {
	t.Parallel()

	repo := newFakeKBRepo()
	svc := newPR3KBService(repo, &fakeRegistry{registered: map[string]struct{}{}}, &fakeOwnership{})
	stored := imageConfiguredKB()
	repo.rows[stored.ID] = stored

	// The whole config is present but the image section is absent — this is the
	// shape every existing client sends.
	updated, err := svc.UpdateKnowledgeBase(ctxWithTenant(1), stored.ID, "renamed", "desc",
		&types.KnowledgeBaseConfig{}, nil)
	require.NoError(t, err)

	got := updated.ImageProcessingConfig
	assert.Equal(t, "vlm-image", got.ModelID, "image model was reset")
	assert.True(t, got.ImageAttrsEnabled, "the attribute switch was reset")
	require.NotNil(t, got.ImageActions, "image actions were reset")
	assert.Equal(t, customImageActions().OCR.On, got.ImageActions.OCR.On, "image action contents were reset")

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
		}, nil)
	require.NoError(t, err)

	got := updated.ImageProcessingConfig
	assert.Equal(t, "vlm-image-2", got.ModelID)
	assert.False(t, got.ImageAttrsEnabled, "an explicit object replaces the whole configuration, it does not merge")
	assert.Nil(t, got.ImageActions)
}
