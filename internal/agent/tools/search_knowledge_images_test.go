package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"testing"

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
