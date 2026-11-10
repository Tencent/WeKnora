package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/imageprep"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type imageReadingKBService struct {
	stubKnowledgeBaseService
	reads []string
}

func (s *imageReadingKBService) ReadChunkImage(_ context.Context, r *types.SearchResult) ([]byte, error) {
	s.reads = append(s.reads, r.ID)
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func imageRow(id string, chunkType types.ChunkType, url string) *searchResultWithMeta {
	info, _ := json.Marshal([]types.ImageInfo{{URL: url}})
	return &searchResultWithMeta{SearchResult: &types.SearchResult{
		ID: id, ChunkType: string(chunkType), ImageInfo: string(info), Content: id,
	}}
}

func TestSearchKnowledgeAttachesImagesOnlyForAVisionModel(t *testing.T) {
	shown := []*searchResultWithMeta{
		imageRow("t1", types.ChunkTypeText, ""),
		imageRow("v1", types.ChunkTypeImageVector, "resource://chart"),
	}
	kb := &imageReadingKBService{}
	tool := &SearchKnowledgeTool{knowledgeBaseService: kb}

	result := &types.ToolResult{Success: true, Output: "results", Data: map[string]interface{}{}}
	tool.attachContextImages(context.Background(), result, shown)
	assert.Empty(t, result.Images, "off unless the agent's model can see images")
	assert.Empty(t, kb.reads)

	tool.WithContextImages(true).attachContextImages(context.Background(), result, shown)
	require.Len(t, result.Images, 1)
	assert.Equal(t, []string{"v1"}, kb.reads)
	assert.Equal(t, []string{"v1"}, result.Data["context_images"])
	assert.Contains(t, result.Output, "Attached 1 retrieved image(s), in order, for chunk_id v1")
}

func TestKeptImagesSkipTheResultLimit(t *testing.T) {
	kept := imageRow("kept", types.ChunkTypeImageVector, "resource://a")
	kept.Metadata = map[string]string{types.MetadataKeptBy: types.KeptByImageVector}
	ranked, keptOut := splitKeptOutsideTopK([]*searchResultWithMeta{
		imageRow("a", types.ChunkTypeText, ""), kept, imageRow("b", types.ChunkTypeText, ""),
	})
	require.Len(t, ranked, 2)
	require.Len(t, keptOut, 1)
	assert.Equal(t, "kept", keptOut[0].ID)
}

func TestStoredSearchStepsDropRetrievedImages(t *testing.T) {
	steps := SanitizeAgentStepsForStorage([]types.AgentStep{{ToolCalls: []types.ToolCall{{
		Name:   ToolSearchKnowledge,
		Result: &types.ToolResult{Success: true, Output: "o", Images: []string{"data:image/png;base64,AA=="}},
	}}}})
	assert.Empty(t, steps[0].ToolCalls[0].Result.Images, "read again from storage, never stored")
}

func TestImageCandidatesSurviveDedupBeforeRerank(t *testing.T) {
	for _, vision := range []bool{false, true} {
		t.Run(fmt.Sprint(vision), func(t *testing.T) {
			text := &stubReranker{scores: []float64{0.01, 0.01}}
			var model rerank.Reranker = text
			imageModel := &imageScoringReranker{stubReranker: text}
			if vision {
				model = imageModel
			}
			tool := newRerankTestTool(model)
			tool.knowledgeBaseService = &imageReadingKBService{}
			caption := imageRow("caption", types.ChunkTypeImageCaption, "resource://chart")
			vector := imageRow("vector", types.ChunkTypeImageVector, "resource://chart")
			caption.Content, vector.Content = "a quarterly chart", "a quarterly chart"
			caption.Score, vector.Score, vector.VectorScore = 0.8, 0.4, 0.8
			candidates := tool.deduplicateResultsForRerank([]*searchResultWithMeta{caption, vector, vector})
			require.Len(t, candidates, 2, "dedup only repeated image IDs before judging the pixels")
			got, err := tool.rerankResults(t.Context(), "which quarter peaked", candidates, false)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, "vector", got[0].ID)
			if vision {
				assert.Equal(t, 1, imageModel.images)
			} else {
				assert.Equal(t, types.KeptByImageVector, got[0].Metadata[types.MetadataKeptBy])
			}
		})
	}
}

type imageScoringReranker struct {
	*stubReranker
	images int
}

func (s *imageScoringReranker) AcceptsImages() bool           { return true }
func (s *imageScoringReranker) ImageLimits() imageprep.Limits { return imageprep.Limits{} }
func (s *imageScoringReranker) RerankImages(
	_ context.Context, _ string, images []rerank.Image,
) ([]rerank.RankResult, error) {
	s.images += len(images)
	out := make([]rerank.RankResult, len(images))
	for i := range images {
		out[i] = rerank.RankResult{Index: i, RelevanceScore: 0.9}
	}
	return out, nil
}

func TestDifferentImagesWithIdenticalCaptionsRemainSeparateCandidates(t *testing.T) {
	a := imageRow("a", types.ChunkTypeImageVector, "resource://a")
	b := imageRow("b", types.ChunkTypeImageVector, "resource://b")
	a.Content, b.Content = "a chart", "a chart"
	tool := &SearchKnowledgeTool{}
	require.Len(t, tool.deduplicateResultsForRerank([]*searchResultWithMeta{a, b}), 2)
	final := tool.deduplicateResults([]*searchResultWithMeta{a, b})
	require.Len(t, final, 1)
	require.Len(t, final[0].MatchedImages, 2, "merging text after rerank must retain both images")
}
