package searchutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func evidenceResult(id, chunkType, url string) *types.SearchResult {
	info, _ := json.Marshal([]types.ImageInfo{{URL: url}})
	return &types.SearchResult{ID: id, ChunkType: chunkType, ImageInfo: string(info)}
}

func TestImageEvidenceFollowsTheCopyDeduplicationKeeps(t *testing.T) {
	image := evidenceResult("v", string(types.ChunkTypeImageVector), "u")
	shared := map[string]string{"source": "x"}
	caption := evidenceResult("c", string(types.ChunkTypeImageCaption), "u")
	caption.Metadata = shared

	assert.True(t, IsImageEvidence(image))
	assert.False(t, IsImageEvidence(caption))
	InheritImageEvidence(caption, image)
	assert.True(t, IsImageEvidence(caption))
	assert.NotContains(t, shared, types.MetadataImageVectorMatch, "a shared metadata map is not written through")

	plain := evidenceResult("t", string(types.ChunkTypeText), "")
	InheritImageEvidence(plain, evidenceResult("o", string(types.ChunkTypeImageOCR), "u"))
	assert.False(t, IsImageEvidence(plain), "only an image matched by its own vector passes evidence on")
}

func TestTopKKeepingKeptLetsKeptResultsRideAlong(t *testing.T) {
	kept := evidenceResult("k", string(types.ChunkTypeImageVector), "u")
	kept.Metadata = map[string]string{types.MetadataKeptBy: types.KeptByImageVector}
	in := []*types.SearchResult{{ID: "a"}, {ID: "b"}, kept, {ID: "c"}}
	out := TopKKeepingKept(in, 2)
	ids := make([]string, len(out))
	for i, r := range out {
		ids[i] = r.ID
	}
	assert.Equal(t, []string{"a", "b", "k"}, ids)
}

func pngData(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return buf.Bytes()
}

func TestContextImagesReadsDistinctEvidenceImagesUpToTheCap(t *testing.T) {
	results := []*types.SearchResult{
		{ID: "text", ChunkType: string(types.ChunkTypeText)},
		evidenceResult("v1", string(types.ChunkTypeImageVector), "u1"),
		evidenceResult("v1-copy", string(types.ChunkTypeImageVector), "u1"),
		evidenceResult("broken", string(types.ChunkTypeImageVector), "u2"),
		evidenceResult("caption", string(types.ChunkTypeImageCaption), "u3"),
		evidenceResult("v3", string(types.ChunkTypeImageVector), "u4"),
		evidenceResult("v4", string(types.ChunkTypeImageVector), "u5"),
	}
	var reads []string
	read := func(_ context.Context, r *types.SearchResult) ([]byte, error) {
		reads = append(reads, r.ID)
		if r.ID == "broken" {
			return nil, errors.New("gone")
		}
		return pngData(t), nil
	}
	images, positions := ContextImages(context.Background(), results, read, 2)
	assert.Equal(t, []string{"v1", "broken", "v3"}, reads, "one read per image, stopping at the cap")
	assert.Equal(t, []int{1, 5}, positions)
	require.Len(t, images, 2)
	for _, img := range images {
		assert.True(t, strings.HasPrefix(img, "data:image/png;base64,"))
	}

	images, _ = ContextImages(context.Background(), results, nil, 2)
	assert.Empty(t, images)
}

func TestLegacyHistoryDoesNotGuessExpandedImageIdentity(t *testing.T) {
	r := evidenceResult("v", string(types.ChunkTypeImageVector), "unrelated-parent-image")
	r.MatchType = types.MatchTypeHistory
	CaptureImageEvidence(r)
	assert.Empty(t, r.MatchedImages)

	r.MatchedImages = []types.MatchedImage{{ChunkID: "v", URL: "original-image"}}
	images, positions := ContextImages(context.Background(), []*types.SearchResult{r},
		func(_ context.Context, source *types.SearchResult) ([]byte, error) {
			assert.Contains(t, source.ImageInfo, "original-image")
			assert.NotContains(t, source.ImageInfo, "unrelated-parent-image")
			return pngData(t), nil
		}, 1)
	require.Len(t, images, 1)
	assert.Equal(t, []int{0}, positions)
}
