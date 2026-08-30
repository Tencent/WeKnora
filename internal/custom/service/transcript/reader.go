package transcript

import (
	"context"
	"encoding/json"
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
	StartMs int
	EndMs   int
}

type chunkMetadata struct {
	StartMs int `json:"start_ms"`
	EndMs   int `json:"end_ms"`
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
		content, metadata, err := selectTimedChunk(results, checkpoint.KnowledgeID)
		if err != nil {
			return nil, fmt.Errorf("transcript chunk %d has invalid timing metadata: %w", index, err)
		}
		chunks = append(chunks, Chunk{ID: checkpoint.KnowledgeID, Index: index, Content: content, StartMs: metadata.StartMs, EndMs: metadata.EndMs})
	}
	return chunks, nil
}

func selectTimedChunk(results []weknora.SearchResult, knowledgeID string) (string, chunkMetadata, error) {
	var lastErr error
	for _, result := range results {
		if result.KnowledgeID != knowledgeID || strings.TrimSpace(result.Content) == "" {
			continue
		}
		metadata, err := parseChunkMetadata(result.Content)
		if err == nil {
			return result.Content, metadata, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return "", chunkMetadata{}, fmt.Errorf("转写内容缺失")
	}
	return "", chunkMetadata{}, lastErr
}

func parseChunkMetadata(content string) (chunkMetadata, error) {
	const section = "## 视频定位信息"
	const fence = "```json"
	sectionStart := strings.Index(content, section)
	if sectionStart < 0 {
		return chunkMetadata{}, fmt.Errorf("定位信息段落缺失")
	}
	fenceStart := strings.Index(content[sectionStart+len(section):], fence)
	if fenceStart < 0 {
		return chunkMetadata{}, fmt.Errorf("定位信息 JSON 缺失")
	}
	fenceStart += sectionStart + len(section) + len(fence)
	fenceEnd := strings.Index(content[fenceStart:], "```")
	if fenceEnd < 0 {
		return chunkMetadata{}, fmt.Errorf("定位信息 JSON 代码围栏未闭合")
	}
	var metadata chunkMetadata
	if err := json.Unmarshal([]byte(strings.TrimSpace(content[fenceStart:fenceStart+fenceEnd])), &metadata); err != nil {
		return chunkMetadata{}, fmt.Errorf("解析定位信息 JSON: %w", err)
	}
	if metadata.StartMs < 0 || metadata.EndMs <= metadata.StartMs {
		return chunkMetadata{}, fmt.Errorf("时间范围无效: start_ms=%d end_ms=%d", metadata.StartMs, metadata.EndMs)
	}
	return metadata, nil
}

func EffectiveEndSeconds(chunks []Chunk) (int, error) {
	maxEndMs := 0
	for _, chunk := range chunks {
		if chunk.EndMs > maxEndMs {
			maxEndMs = chunk.EndMs
		}
	}
	if maxEndMs <= 0 {
		return 0, fmt.Errorf("transcript has no valid end timestamp")
	}
	return (maxEndMs + 999) / 1000, nil
}
