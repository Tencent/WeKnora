package transcript

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

type Chunk struct {
	ID      string
	Index   int
	Content string
}

type Reader struct {
	DB      *gorm.DB
	WeKnora *weknora.Client
	KBID    string
}

func NewReader(db *gorm.DB, client *weknora.Client) *Reader {
	kbID := ""
	if client != nil {
		kbID = client.KBID()
	}
	return &Reader{DB: db, WeKnora: client, KBID: kbID}
}

func (r *Reader) Read(ctx context.Context, videoID, generation string) ([]Chunk, error) {
	if r.DB == nil || r.WeKnora == nil {
		return nil, fmt.Errorf("transcript reader dependencies are not configured")
	}
	if strings.TrimSpace(videoID) == "" || strings.TrimSpace(generation) == "" {
		return nil, fmt.Errorf("video id and transcript generation are required")
	}
	var checkpoints []model.VideoTranscriptChunk
	if err := r.DB.WithContext(ctx).
		Where("video_id = ? AND generation = ?", videoID, generation).
		Order("chunk_index ASC").Find(&checkpoints).Error; err != nil {
		return nil, fmt.Errorf("load transcript checkpoints: %w", err)
	}
	if len(checkpoints) == 0 {
		return nil, fmt.Errorf("video %s has no transcript chunks for generation %s", videoID, generation)
	}

	chunks := make([]Chunk, 0, len(checkpoints))
	for index, checkpoint := range checkpoints {
		if checkpoint.ChunkIndex != index || checkpoint.Status != "completed" || strings.TrimSpace(checkpoint.KnowledgeID) == "" {
			return nil, fmt.Errorf("transcript chunk manifest is incomplete at index %d", index)
		}
		results, err := r.WeKnora.HybridSearch(ctx, r.KBID, weknora.SearchParams{
			QueryText:            "视频定位信息 原文",
			MatchCount:           10,
			DisableVectorMatch:   true,
			DisableKeywordsMatch: false,
			KnowledgeIDs:         []string{checkpoint.KnowledgeID},
		})
		if err != nil {
			return nil, fmt.Errorf("read transcript chunk %d: %w", index, err)
		}
		content := ""
		for _, result := range results {
			if result.KnowledgeID == checkpoint.KnowledgeID && strings.TrimSpace(result.Content) != "" {
				content = result.Content
				break
			}
		}
		if content == "" {
			return nil, fmt.Errorf("transcript chunk %d is not readable", index)
		}
		chunks = append(chunks, Chunk{ID: checkpoint.KnowledgeID, Index: index, Content: content})
	}
	return chunks, nil
}
