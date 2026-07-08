package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type mergeExpandChunkRepo struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
}

func (r *mergeExpandChunkRepo) ListChunksByID(
	_ context.Context,
	_ uint64,
	ids []string,
) ([]*types.Chunk, error) {
	out := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if chunk := r.chunks[id]; chunk != nil {
			out = append(out, chunk)
		}
	}
	return out, nil
}

func TestExpandContextWithNeighbors_AppendsNextChunkForDanglingEmptyAltImage(t *testing.T) {
	baseContent := strings.Repeat("这是一段较长的视网膜静脉阻塞说明，确保长度超过普通短块阈值。", 12) +
		"\n\n![](local://10000/exports/figure-ad.jpg)"
	nextContent := "![图 3-3-3 视盘血管炎](local://10000/exports/figure-ef.jpg)\n\n" +
		"患者女性，25岁。左眼视网膜中央静脉阻塞。图点评: 年轻 CRVO 的发生常与炎症相关。"

	if runeLen(baseContent) < 350 {
		t.Fatalf("test setup must use a long base chunk, got len=%d", runeLen(baseContent))
	}

	repo := &mergeExpandChunkRepo{
		chunks: map[string]*types.Chunk{
			"base": {
				ID:          "base",
				KnowledgeID: "knowledge-1",
				Content:     baseContent,
				ChunkType:   types.ChunkTypeText,
				StartAt:     0,
				EndAt:       runeLen(baseContent),
				NextChunkID: "next",
			},
			"next": {
				ID:          "next",
				KnowledgeID: "knowledge-1",
				Content:     nextContent,
				ChunkType:   types.ChunkTypeText,
				StartAt:     runeLen(baseContent),
				EndAt:       runeLen(baseContent) + runeLen(nextContent),
			},
		},
	}

	merge := &PluginMerge{chunkRepo: repo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	results := []*types.SearchResult{{
		ID:          "base",
		KnowledgeID: "knowledge-1",
		Content:     baseContent,
		ChunkType:   string(types.ChunkTypeText),
		StartAt:     0,
		EndAt:       runeLen(baseContent),
	}}

	out := merge.expandShortContextWithNeighbors(ctx, &types.ChatManage{}, results)
	if len(out) != 1 {
		t.Fatalf("unexpected result count: %d", len(out))
	}
	if !strings.Contains(out[0].Content, "图 3-3-3 视盘血管炎") {
		t.Fatalf("expected next chunk figure title in expanded content, got: %q", out[0].Content)
	}
	if !containsID(out[0].SubChunkID, "next") {
		t.Fatalf("expected next chunk id in sub chunks, got: %#v", out[0].SubChunkID)
	}
}

func TestHasDanglingEmptyAltImage(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "empty alt image at end",
			content: "前文\n\n![](local://img.jpg)",
			want:    true,
		},
		{
			name:    "titled image at end",
			content: "前文\n\n![图 3-3-3 视盘血管炎](local://img.jpg)",
			want:    false,
		},
		{
			name:    "empty alt image already followed by figure context",
			content: "![](local://img.jpg)\n\n图 3-3-3 视盘血管炎",
			want:    false,
		},
		{
			name:    "empty alt image far from tail",
			content: "![](local://img.jpg)\n\n" + strings.Repeat("后续正文。", 130),
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasDanglingEmptyAltImage(tt.content); got != tt.want {
				t.Fatalf("hasDanglingEmptyAltImage() = %v, want %v", got, tt.want)
			}
		})
	}
}
