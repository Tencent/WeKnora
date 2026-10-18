package runtime_test

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registerVideoVendor registers an isolated vendor whose catalog has one chat
// entry that accepts video and one that does not. The runtime's video type has
// to resolve against the chat entries, exactly like VLM does against the image
// ones, or a video row would come out with no protocol and no capabilities.
func registerVideoVendor(t *testing.T) {
	t.Helper()
	before := modelruntime.SnapshotCurrent()
	t.Cleanup(func() { modelruntime.RestoreSnapshot(before) })
	modelruntime.Register(&providers.Definition{
		ID:   "acme-video",
		Name: "Acme Video",
		API:  api.APIOpenAICompletions,
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeVideo: "https://video.acme.ai/v1",
		},
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA, types.ModelTypeVideo},
	}, []models.ModelSpec{
		{
			ID: "acme-vid", Name: "Acme Video Model",
			Input:           []string{"text", "image", "video"},
			ContextWindow:   128000,
			MaxOutputTokens: 8192,
		},
		{ID: "acme-txt", Name: "Acme Text Model", Input: []string{"text"}},
		{ID: "acme-embed", Type: types.ModelTypeEmbedding, Dimension: 1024},
	}...)
}

// The video row resolves the chat protocol, the vendor's video default URL and
// the capabilities the UI needs to offer it — including `video` input.
func TestVideoRowResolvesThroughTheChatCatalog(t *testing.T) {
	registerVideoVendor(t)

	resolved, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: "acme-video", Model: "acme-vid", ModelType: types.ModelTypeVideo,
	})
	require.NoError(t, err)
	assert.True(t, resolved.Cataloged, "the chat entry must describe the video row")
	assert.Equal(t, "acme-vid", resolved.Spec.ID)
	assert.Equal(t, api.APIOpenAICompletions, resolved.API)
	assert.Equal(t, "https://video.acme.ai/v1", resolved.BaseURL)
	assert.Equal(t, "acme-vid", resolved.RemoteModel, "the name is sent on the wire as-is")

	caps := resolved.Capabilities()
	assert.Contains(t, caps.Input, "video")
	assert.Contains(t, caps.Input, "text")
	assert.Equal(t, 128000, caps.ContextWindow)
	// The token field comes from the chat protocol overlay, which is exactly
	// why a video row has to resolve as a chat row.
	assert.NotEmpty(t, caps.MaxTokensField)
}

// The catalog picker for the video type lists the entries that declare video
// input and nothing else.
func TestModelsByTypeVideoListsOnlyVideoCapableEntries(t *testing.T) {
	registerVideoVendor(t)
	provider, ok := modelruntime.Get("acme-video")
	require.True(t, ok)

	ids := make([]string, 0)
	for _, entry := range provider.ModelsByType(types.ModelTypeVideo) {
		ids = append(ids, entry.ID)
	}
	assert.Equal(t, []string{"acme-vid"}, ids)
}

// Saving a video row validates through the same gate as every other type: an
// unknown compat key has to fail here, not at the first video call.
func TestValidateRowAcceptsAVideoRow(t *testing.T) {
	registerVideoVendor(t)

	require.NoError(t, modelruntime.ValidateRow(
		"acme-vid",
		types.ModelTypeVideo,
		&types.ModelParameters{Provider: "acme-video"},
	))
	require.NoError(t, modelruntime.ValidateRow(
		"acme-vid",
		types.ModelTypeVideo,
		&types.ModelParameters{Provider: "acme-video", BaseURL: "https://relay.example/v1"},
	))
}

// The vendor's video type folds onto the chat entries, which is what makes the
// typed lookup and the connection endpoint agree with the VLM path.
func TestEntryTypeFoldsVideoOntoChat(t *testing.T) {
	assert.Equal(t, types.ModelTypeKnowledgeQA, catalog.EntryType(types.ModelTypeVideo))
	assert.Equal(t, types.ModelTypeKnowledgeQA, catalog.EntryType(types.ModelTypeVLLM))
	assert.Equal(t, types.ModelTypeEmbedding, catalog.EntryType(types.ModelTypeEmbedding))
}
