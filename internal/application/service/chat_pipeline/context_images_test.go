package chatpipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/searchutil"
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
					ID: "caption-photo", Content: "a photo", ChunkType: string(types.ChunkTypeImageCaption),
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
	assert.Equal(t, []string{"v1"}, cm.ContextImageChunkIDs)
	assert.NotContains(t, cm.UserContent, "对应 context")

	cm.Images = []string{"data:image/png;base64,user"}
	msgs, registry := prepareMessagesWithModelContext(context.Background(), cm)
	last := msgs[len(msgs)-1]
	assert.Contains(t, last.Content, "Image 2: chunk "+registry.ChunkHandle("v1")+".")
	assert.NotContains(t, last.Content, "对应 context")
	assert.Equal(t, append([]string{"data:image/png;base64,user"}, cm.ContextImages...), last.Images,
		"the user's own image first, then the retrieved one")
}

func TestIntoChatMessageAttachesNoImagesForATextModel(t *testing.T) {
	kb := &imageKBService{}
	cm := contextImagesManage(false)
	cm.ContextImages = []string{"stale"}
	cm.ContextImageChunkIDs = []string{"stale-id"}
	plugin := &PluginIntoChatMessage{kbService: kb}
	next := func() *PluginError { return nil }
	require.Nil(t, plugin.OnEvent(context.Background(), types.INTO_CHAT_MESSAGE, cm, next))
	assert.Empty(t, kb.reads)
	assert.Empty(t, cm.ContextImages)
	assert.Empty(t, cm.ContextImageChunkIDs)
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

func TestExpandedImageEvidenceKeepsItsOwnImageAndStorage(t *testing.T) {
	info := func(url string) string { b, _ := json.Marshal([]types.ImageInfo{{URL: url}}); return string(b) }
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{"text": {
			ID: "text", ChunkType: types.ChunkTypeText, Content: "![other](resource://a)\n\n![matched](resource://b)",
		}},
		children: map[string][]*types.Chunk{"text": {
			{
				ID: "a", ParentChunkID: "text", ChunkType: types.ChunkTypeImageCaption,
				IsEnabled: true, ImageInfo: info("resource://a"),
			},
			{
				ID: "b", ParentChunkID: "text", ChunkType: types.ChunkTypeImageCaption,
				IsEnabled: true, ImageInfo: info("resource://b"),
			},
		}},
	}
	for range 20 { // Parent image order comes from a map.
		r := &types.SearchResult{
			ID: "vector-b", KnowledgeID: "doc", KnowledgeBaseID: "owner-kb", ParentChunkID: "text",
			ChunkType: string(types.ChunkTypeImageVector), Content: "matched chart", ImageInfo: info("resource://b"),
		}
		expanded := (&PluginMerge{chunkRepo: repo}).resolveParentChunks(
			t.Context(), &types.ChatManage{}, []*types.SearchResult{r})
		caption := withImage(&types.SearchResult{
			ID: "copy", KnowledgeBaseID: "other-kb", Content: expanded[0].Content,
			ChunkType: string(types.ChunkTypeImageCaption),
		}, "resource://wrong")
		deduped := removeDuplicateResults([]*types.SearchResult{caption, expanded[0]})
		require.Len(t, deduped, 1)
		// Stored history must retain the matched image identity too.
		raw, err := json.Marshal(deduped)
		require.NoError(t, err)
		var restored []*types.SearchResult
		require.NoError(t, json.Unmarshal(raw, &restored))
		kb := &imageKBService{}
		read := func(ctx context.Context, source *types.SearchResult) ([]byte, error) {
			require.Equal(t, "vector-b", source.ID)
			require.Equal(t, "owner-kb", source.KnowledgeBaseID)
			require.JSONEq(t, info("resource://b"), source.ImageInfo)
			return kb.ReadChunkImage(ctx, source)
		}
		images, positions := searchutil.ContextImages(t.Context(), restored, read, 3)
		require.Len(t, images, 1)
		require.Equal(t, []int{0}, positions)
	}
}

func TestSequentialMergeKeepsAllMatchedImages(t *testing.T) {
	a := withImage(&types.SearchResult{
		ID: "first", ChunkIndex: 0, ChunkType: string(types.ChunkTypeImageVector), Content: "first chart",
	}, "resource://a")
	b := withImage(&types.SearchResult{
		ID: "second", ChunkIndex: 1, ChunkType: string(types.ChunkTypeImageVector), Content: "second chart",
	}, "resource://b")
	merged := (&PluginMerge{}).mergeSequentialChunks(t.Context(), "doc", []*types.SearchResult{a, b})
	require.Len(t, merged, 1)
	kb := &imageKBService{}
	images, positions := searchutil.ContextImages(t.Context(), merged, kb.ReadChunkImage, 3)
	require.Len(t, images, 2)
	require.Equal(t, []string{"first", "second"}, kb.reads)
	require.Equal(t, []int{0, 0}, positions)
}

func TestFAQImageNoteUsesFinalHandlesAfterCitationExpansion(t *testing.T) {
	cm := contextImagesManage(true)
	cm.FAQPriorityEnabled = true
	faq := &types.SearchResult{ID: "faq-answer", ChunkType: string(types.ChunkTypeFAQ), Content: "faq answer"}
	cm.MergeResult = append(cm.MergeResult, faq)
	// The retained image source is not the first source in the rendered body.
	cm.MergeResult[1].CitationSources = []*types.SearchResult{
		{ID: "earlier-source", Content: "context before the image"}, cm.MergeResult[1],
	}
	plugin := &PluginIntoChatMessage{kbService: &imageKBService{}}
	require.Nil(t, plugin.OnEvent(t.Context(), types.INTO_CHAT_MESSAGE, cm, func() *PluginError { return nil }))
	messages, registry := prepareMessagesWithModelContext(t.Context(), cm)
	last := messages[len(messages)-1]
	require.Len(t, last.Images, 1)
	require.Contains(t, last.Content, "Image 1: chunk "+registry.ChunkHandle("v1")+".")
	require.NotContains(t, last.Content, "对应 context")
	clone := cm.Clone()
	clone.ContextImageChunkIDs[0] = "changed"
	require.Equal(t, "v1", cm.ContextImageChunkIDs[0])
}
