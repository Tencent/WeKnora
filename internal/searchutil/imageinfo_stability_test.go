package searchutil

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type imageInfoChunkRepository struct {
	interfaces.ChunkRepository
	children []*types.Chunk
}

func (r imageInfoChunkRepository) ListChunksByParentIDs(
	context.Context,
	uint64,
	[]string,
) ([]*types.Chunk, error) {
	return r.children, nil
}

func TestMergeImageInfoJSONStableAcrossInputOrder(t *testing.T) {
	forward := map[string]string{
		"chunk": `[{"url":"b","caption":"caption-b"},{"url":"a","caption":"caption-z","ocr_text":"ocr-a"},{"url":"a","caption":"caption-a","ocr_text":"ocr-z"}]`,
	}
	reverse := map[string]string{
		"chunk": `[{"url":"a","caption":"caption-a","ocr_text":"ocr-z"},{"url":"a","caption":"caption-z","ocr_text":"ocr-a"},{"url":"b","caption":"caption-b"}]`,
	}

	got := MergeImageInfoJSON(forward)
	require.Equal(t, got, MergeImageInfoJSON(reverse))
	require.JSONEq(t, `[
		{"url":"a","original_url":"","start_pos":0,"end_pos":0,"caption":"caption-a","ocr_text":"ocr-a"},
		{"url":"b","original_url":"","start_pos":0,"end_pos":0,"caption":"caption-b","ocr_text":""}
	]`, got)
}

func TestCollectImageInfoByChunkIDsStableAcrossChildOrder(t *testing.T) {
	first := &types.Chunk{
		ChunkType:     types.ChunkTypeImageCaption,
		ParentChunkID: "parent",
		ImageInfo:     `[{"url":"image","caption":"caption-z","ocr_text":"ocr-a"}]`,
	}
	second := &types.Chunk{
		ChunkType:     types.ChunkTypeImageOCR,
		ParentChunkID: "parent",
		ImageInfo:     `[{"url":"image","caption":"caption-a","ocr_text":"ocr-z"}]`,
	}

	forward := CollectImageInfoByChunkIDs(
		context.Background(),
		imageInfoChunkRepository{children: []*types.Chunk{first, second}},
		1,
		[]string{"parent"},
	)
	reverse := CollectImageInfoByChunkIDs(
		context.Background(),
		imageInfoChunkRepository{children: []*types.Chunk{second, first}},
		1,
		[]string{"parent"},
	)

	require.Equal(t, forward, reverse)
	require.JSONEq(t, `[
		{"url":"image","original_url":"","start_pos":0,"end_pos":0,"caption":"caption-a","ocr_text":"ocr-a"}
	]`, forward["parent"])
}
