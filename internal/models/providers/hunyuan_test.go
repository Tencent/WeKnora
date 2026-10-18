package providers_test

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tencent serves video understanding from TokenHub, a different host from the
// Hunyuan chat endpoint. The vendor has to declare both, or internal/models/video
// refuses a hunyuan video row before it ever reads the base URL.
func TestHunyuanDeclaresVideoOnTokenHub(t *testing.T) {
	v, ok := modelruntime.Get(providers.HunyuanID)
	require.True(t, ok)

	assert.True(t, v.SupportsType(types.ModelTypeVideo))
	assert.Equal(t, providers.TokenHubBaseURL, v.GetDefaultURL(types.ModelTypeVideo))
	// TokenHub is the video path only: the other capabilities keep the host
	// their documentation describes.
	assert.Equal(t, providers.HunyuanBaseURL, v.GetDefaultURL(types.ModelTypeKnowledgeQA))
	assert.Equal(t, providers.HunyuanBaseURL, v.GetDefaultURL(types.ModelTypeEmbedding))

	// A row saved before providers carried an id, but pointed at TokenHub, is
	// detected as this vendor and inherits the video declaration.
	assert.Equal(t, providers.HunyuanID, modelruntime.DetectByURL(providers.TokenHubBaseURL))

	// The video picker lists the catalog entry whose input declares video ...
	ids := make([]string, 0)
	for _, entry := range v.ModelsByType(types.ModelTypeVideo) {
		ids = append(ids, entry.ID)
	}
	assert.Contains(t, ids, "hunyuan-turbos-vision-video")

	// ... and resolution reads it as the chat call the video client makes,
	// against the TokenHub default.
	resolved, err := modelruntime.Resolve(modelruntime.Ref{
		Provider:  providers.HunyuanID,
		Model:     "hunyuan-turbos-vision-video",
		ModelType: types.ModelTypeVideo,
	})
	require.NoError(t, err)
	assert.True(t, resolved.Cataloged)
	assert.Equal(t, api.APIOpenAICompletions, resolved.API)
	assert.Equal(t, providers.TokenHubBaseURL, resolved.BaseURL)
	assert.Contains(t, resolved.Capabilities().Input, "video")
}
