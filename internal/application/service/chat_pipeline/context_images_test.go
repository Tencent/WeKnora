package chatpipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type imageKBService struct {
	interfaces.KnowledgeBaseService
	reads []string
}

func (s *imageKBService) ReadChunkImage(_ context.Context, r *types.SearchResult) ([]byte, error) {
	s.reads = append(s.reads, r.ID)
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func withImage(r *types.SearchResult, url string) *types.SearchResult {
	info, _ := json.Marshal([]types.ImageInfo{{URL: url, Caption: r.Content}})
	r.ImageInfo = string(info)
	return r
}

func contextImagesManage(vision bool) *types.ChatManage {
	return &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:                   "which quarter peaked",
			ChatModelSupportsVision: vision,
			SummaryConfig:           types.SummaryConfig{ContextTemplate: "{{contexts}}"},
		},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "t1", Content: "quarterly review", ChunkType: string(types.ChunkTypeText)},
				withImage(&types.SearchResult{
					ID: "v1", Content: "a bar chart", ChunkType: string(types.ChunkTypeImageVector),
				}, "resource://chart"),
				withImage(&types.SearchResult{
					ID: "c1", Content: "a photo", ChunkType: string(types.ChunkTypeImageCaption),
				}, "resource://photo"),
			},
		},
	}
}

func TestIntoChatMessageShowsAVisionModelTheImagesItsContextsRestOn(t *testing.T) {
	kb := &imageKBService{}
	cm := contextImagesManage(true)
	plugin := &PluginIntoChatMessage{kbService: kb}
	next := func() *PluginError { return nil }
	require.Nil(t, plugin.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm, next))

	assert.Equal(t, []string{"v1"}, kb.reads, "only the image matched by its own vector")
	require.Len(t, cm.ContextImages, 1)
	assert.True(t, strings.HasPrefix(cm.ContextImages[0], "data:image/png;base64,"))
	assert.Contains(t, cm.UserContent, "附带 1 张检索到的图片，依次对应 context 2")

	cm.Images = []string{"data:image/png;base64,user"}
	msgs := prepareMessagesWithHistory(cm)
	last := msgs[len(msgs)-1]
	assert.Equal(t, append([]string{"data:image/png;base64,user"}, cm.ContextImages...), last.Images,
		"the user's own image first, then the retrieved one")
}

func TestIntoChatMessageAttachesNoImagesForATextModel(t *testing.T) {
	kb := &imageKBService{}
	cm := contextImagesManage(false)
	cm.ContextImages = []string{"stale"}
	plugin := &PluginIntoChatMessage{kbService: kb}
	next := func() *PluginError { return nil }
	require.Nil(t, plugin.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm, next))
	assert.Empty(t, kb.reads)
	assert.Empty(t, cm.ContextImages)
	assert.NotContains(t, cm.UserContent, "检索到的图片")
	assert.Empty(t, prepareMessagesWithHistory(cm)[0].Images)
}

func TestDeduplicationKeepsTheImageWithTheCopyItKeeps(t *testing.T) {
	const merged = "same merged text"
	caption := &types.SearchResult{ID: "c1", Content: merged, ChunkType: string(types.ChunkTypeImageCaption)}
	vector := &types.SearchResult{ID: "v1", Content: merged, ChunkType: string(types.ChunkTypeImageVector)}
	out := removeDuplicateResults([]*types.SearchResult{caption, vector})
	require.Len(t, out, 1)
	assert.Equal(t, "c1", out[0].ID)
	assert.Equal(t, "true", out[0].Metadata[types.MetadataImageVectorMatch])
}

func TestFilterTopKLetsKeptImagesRideAlong(t *testing.T) {
	kept := &types.SearchResult{ID: "kept", Score: 0.1, Metadata: map[string]string{
		types.MetadataKeptBy: types.KeptByImageVector,
	}}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 1, KnowledgeBaseIDs: []string{"kb"}},
		PipelineState: types.PipelineState{MergeResult: []*types.SearchResult{
			{ID: "a", Score: 0.9}, {ID: "b", Score: 0.8}, kept,
		}},
	}
	plugin := &PluginFilterTopK{}
	require.Nil(t, plugin.OnEvent(context.Background(), types.FILTER_TOP_K, cm, func() *PluginError { return nil }))
	ids := make([]string, len(cm.MergeResult))
	for i, r := range cm.MergeResult {
		ids[i] = r.ID
	}
	assert.Equal(t, []string{"a", "kept"}, ids)
}
