package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"gorm.io/gorm"
)

// Record is the independently addressable evidence unit exposed to chapter,
// summary and retrieval code. Knowledge-layer pages are deliberately absent
// from this contract.
type Record struct {
	VideoID              string `json:"video_id"`
	TranscriptGeneration string `json:"transcript_generation"`
	ChunkIndex           int    `json:"chunk_index"`
	KnowledgeID          string `json:"knowledge_id"`
	EvidenceSentenceID   string `json:"evidence_sentence_id"`
	SourceSentenceID     string `json:"source_sentence_id"`
	Text                 string `json:"text"`
	SpeakerID            string `json:"speaker_id,omitempty"`
	StartMs              int    `json:"start_ms"`
	EndMs                int    `json:"end_ms"`
}

// Index reads the immutable evidence manifest and delegates text search to
// WeKnora. It never searches Wiki pages or graph results.
type Index struct {
	DB      *gorm.DB
	WeKnora *weknora.Client
	KBID    string
}

// EvidenceIndex is the descriptive alias used by higher-level services.
type EvidenceIndex = Index

func NewIndex(db *gorm.DB, client *weknora.Client) *Index {
	kbID := ""
	if client != nil {
		kbID = client.KBID()
	}
	return &Index{DB: db, WeKnora: client, KBID: kbID}
}

func NewEvidenceIndex(db *gorm.DB, client *weknora.Client) *Index {
	return NewIndex(db, client)
}

// Read returns the current generation's evidence in transcript order.
func (i *Index) Read(ctx context.Context, videoID, generation string) ([]Record, error) {
	checkpoints, err := i.loadCheckpoints(ctx, videoID, generation)
	if err != nil {
		return nil, err
	}
	if i.WeKnora == nil {
		return nil, fmt.Errorf("evidence index WeKnora dependency is not configured")
	}
	records := make([]Record, 0, len(checkpoints))
	for index, checkpoint := range checkpoints {
		content, metadata, err := i.readKnowledge(ctx, checkpoint.KnowledgeID)
		if err != nil {
			return nil, fmt.Errorf("evidence chunk %d: %w", index, err)
		}
		checkpoint, err = i.ensureEvidenceSentenceID(ctx, checkpoint, content, metadata)
		if err != nil {
			return nil, fmt.Errorf("evidence chunk %d: %w", index, err)
		}
		record, err := i.recordFromCheckpoint(checkpoint, content, metadata, index)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// Search performs keyword/vector hybrid search constrained to the current
// video's evidence knowledge IDs, then restores stable sentence timing from
// the local manifest.
func (i *Index) Search(ctx context.Context, videoID, generation, query string, limit int) ([]Record, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("evidence search query is required")
	}
	if limit < 0 {
		return nil, fmt.Errorf("evidence search limit must not be negative")
	}
	checkpoints, err := i.loadCheckpoints(ctx, videoID, generation)
	if err != nil {
		return nil, err
	}
	if i.WeKnora == nil {
		return nil, fmt.Errorf("evidence index WeKnora dependency is not configured")
	}
	byKnowledge := make(map[string]model.VideoTranscriptChunk, len(checkpoints))
	knowledgeIDs := make([]string, 0, len(checkpoints))
	for index, checkpoint := range checkpoints {
		if strings.TrimSpace(checkpoint.KnowledgeID) == "" {
			return nil, fmt.Errorf("evidence chunk %d has no knowledge ID", checkpoint.ChunkIndex)
		}
		if strings.TrimSpace(checkpoint.EvidenceSentenceID) == "" {
			content, metadata, readErr := i.readKnowledge(ctx, checkpoint.KnowledgeID)
			if readErr != nil {
				return nil, fmt.Errorf("evidence chunk %d: %w", index, readErr)
			}
			checkpoint, readErr = i.ensureEvidenceSentenceID(ctx, checkpoint, content, metadata)
			if readErr != nil {
				return nil, fmt.Errorf("evidence chunk %d: %w", index, readErr)
			}
		}
		if _, exists := byKnowledge[checkpoint.KnowledgeID]; exists {
			return nil, fmt.Errorf("duplicate evidence knowledge ID %q", checkpoint.KnowledgeID)
		}
		byKnowledge[checkpoint.KnowledgeID] = checkpoint
		knowledgeIDs = append(knowledgeIDs, checkpoint.KnowledgeID)
	}
	results, err := i.WeKnora.HybridSearch(ctx, i.KBID, weknora.SearchParams{
		QueryText: query, MatchCount: limit, KnowledgeIDs: knowledgeIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("search evidence index: %w", err)
	}
	seen := make(map[string]struct{}, len(results))
	records := make([]Record, 0, len(results))
	for _, result := range results {
		knowledgeID := strings.TrimSpace(result.KnowledgeID)
		checkpoint, ok := byKnowledge[knowledgeID]
		if !ok {
			return nil, fmt.Errorf("search result knowledge %q is outside video %q generation %q", knowledgeID, videoID, generation)
		}
		if _, exists := seen[knowledgeID]; exists {
			return nil, fmt.Errorf("search returned duplicate evidence knowledge %q", knowledgeID)
		}
		seen[knowledgeID] = struct{}{}
		content := strings.TrimSpace(result.Content)
		if content == "" {
			return nil, fmt.Errorf("search result knowledge %q has empty content", knowledgeID)
		}
		metadata, err := parseMetadata(content)
		if err != nil {
			return nil, fmt.Errorf("search result knowledge %q has invalid evidence metadata: %w", knowledgeID, err)
		}
		record, err := i.recordFromCheckpoint(checkpoint, content, metadata, checkpoint.ChunkIndex)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (i *Index) loadCheckpoints(ctx context.Context, videoID, generation string) ([]model.VideoTranscriptChunk, error) {
	videoID = strings.TrimSpace(videoID)
	generation = strings.TrimSpace(generation)
	if i.DB == nil {
		return nil, fmt.Errorf("evidence index database dependency is not configured")
	}
	if videoID == "" || generation == "" {
		return nil, fmt.Errorf("video id and transcript generation are required")
	}
	var video model.Video
	videoErr := i.DB.WithContext(ctx).Select("id", "transcript_generation").First(&video, "id = ?", videoID).Error
	if videoErr == nil && strings.TrimSpace(video.TranscriptGeneration) != "" && video.TranscriptGeneration != generation {
		return nil, fmt.Errorf("video %q active transcript generation is %q, not %q", videoID, video.TranscriptGeneration, generation)
	}
	if videoErr != nil && videoErr != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("load video generation: %w", videoErr)
	}
	var checkpoints []model.VideoTranscriptChunk
	if err := i.DB.WithContext(ctx).Where("video_id = ? AND generation = ?", videoID, generation).Order("chunk_index ASC").Find(&checkpoints).Error; err != nil {
		return nil, fmt.Errorf("load evidence manifest: %w", err)
	}
	if len(checkpoints) == 0 {
		return nil, fmt.Errorf("video %q has no evidence chunks for generation %q", videoID, generation)
	}
	for index, checkpoint := range checkpoints {
		if checkpoint.ChunkIndex != index {
			return nil, fmt.Errorf("evidence manifest is not contiguous at index %d", index)
		}
		if checkpoint.Status != "completed" {
			return nil, fmt.Errorf("evidence chunk %d is not searchable: status=%q", index, checkpoint.Status)
		}
		if checkpoint.StartMs < 0 || checkpoint.EndMs <= checkpoint.StartMs {
			return nil, fmt.Errorf("evidence chunk %d has invalid time range", index)
		}
	}
	return checkpoints, nil
}

// ensureEvidenceSentenceID upgrades legacy transcript manifests lazily. The
// source sentence, text, speaker and timing are immutable inputs, so the
// derived ID is stable and can be safely persisted only when the field is
// still empty.
func (i *Index) ensureEvidenceSentenceID(ctx context.Context, checkpoint model.VideoTranscriptChunk, content string, parsed metadata) (model.VideoTranscriptChunk, error) {
	if strings.TrimSpace(checkpoint.EvidenceSentenceID) != "" {
		return checkpoint, nil
	}
	sourceSentenceID := firstNonEmpty(checkpoint.SourceSegmentID, parsed.SourceSentenceID)
	speakerID := firstNonEmpty(checkpoint.SpeakerID, parsed.SpeakerID)
	sentence, err := BuildSentence(Input{
		VideoID: checkpoint.VideoID, TranscriptGeneration: checkpoint.Generation,
		Ordinal: checkpoint.ChunkIndex, SourceSentenceID: sourceSentenceID,
		Text: originalText(content), SpeakerID: speakerID,
		StartMs: checkpoint.StartMs, EndMs: checkpoint.EndMs,
	})
	if err != nil {
		return checkpoint, fmt.Errorf("derive legacy evidence sentence ID: %w", err)
	}
	checkpoint.EvidenceSentenceID = sentence.ID
	if i.DB == nil {
		return checkpoint, nil
	}
	result := i.DB.WithContext(ctx).Model(&model.VideoTranscriptChunk{}).
		Where("video_id = ? AND generation = ? AND chunk_index = ? AND evidence_sentence_id = ''", checkpoint.VideoID, checkpoint.Generation, checkpoint.ChunkIndex).
		Update("evidence_sentence_id", sentence.ID)
	if result.Error != nil {
		return checkpoint, fmt.Errorf("persist legacy evidence sentence ID: %w", result.Error)
	}
	return checkpoint, nil
}

type metadata struct {
	StartMs              int    `json:"start_ms"`
	EndMs                int    `json:"end_ms"`
	SourceSentenceID     string `json:"sentence_id"`
	EvidenceSentenceID   string `json:"evidence_sentence_id"`
	SpeakerID            string `json:"speaker_id"`
	TranscriptGeneration string `json:"transcript_generation"`
}

func (i *Index) readKnowledge(ctx context.Context, knowledgeID string) (string, metadata, error) {
	chunks, err := i.WeKnora.ListKnowledgeChunks(ctx, knowledgeID)
	if err != nil {
		return "", metadata{}, err
	}
	ordered := append([]weknora.KnowledgeChunk(nil), chunks...)
	sort.SliceStable(ordered, func(left, right int) bool { return ordered[left].ChunkIndex < ordered[right].ChunkIndex })
	var builder strings.Builder
	for index, chunk := range ordered {
		if chunk.KnowledgeID != "" && chunk.KnowledgeID != knowledgeID {
			return "", metadata{}, fmt.Errorf("knowledge chunk belongs to another knowledge")
		}
		if chunk.ChunkIndex != index {
			return "", metadata{}, fmt.Errorf("knowledge chunk order is not contiguous")
		}
		builder.WriteString(chunk.Content)
	}
	content := strings.TrimSpace(builder.String())
	parsed, err := parseMetadata(content)
	if err != nil {
		return "", metadata{}, err
	}
	return content, parsed, nil
}

func parseMetadata(content string) (metadata, error) {
	const section = "## 视频定位信息"
	const fence = "```json"
	sectionStart := strings.Index(content, section)
	if sectionStart < 0 {
		return metadata{}, fmt.Errorf("定位信息段落缺失")
	}
	fenceStart := strings.Index(content[sectionStart+len(section):], fence)
	if fenceStart < 0 {
		return metadata{}, fmt.Errorf("定位信息 JSON 缺失")
	}
	fenceStart += sectionStart + len(section) + len(fence)
	fenceEnd := strings.Index(content[fenceStart:], "```")
	if fenceEnd < 0 {
		return metadata{}, fmt.Errorf("定位信息 JSON 代码围栏未闭合")
	}
	var parsed metadata
	if err := json.Unmarshal([]byte(strings.TrimSpace(content[fenceStart:fenceStart+fenceEnd])), &parsed); err != nil {
		return metadata{}, fmt.Errorf("解析定位信息 JSON: %w", err)
	}
	if parsed.StartMs < 0 || parsed.EndMs <= parsed.StartMs {
		return metadata{}, fmt.Errorf("时间范围无效")
	}
	return parsed, nil
}

func originalText(content string) string {
	const marker = "## 原文"
	index := strings.Index(content, marker)
	if index < 0 {
		return strings.TrimSpace(content)
	}
	text := strings.TrimSpace(content[index+len(marker):])
	if summary := strings.Index(text, "\n# Summary"); summary >= 0 {
		text = strings.TrimSpace(text[:summary])
	}
	return text
}

func (i *Index) recordFromCheckpoint(checkpoint model.VideoTranscriptChunk, content string, parsed metadata, index int) (Record, error) {
	if parsed.EvidenceSentenceID != "" && parsed.EvidenceSentenceID != checkpoint.EvidenceSentenceID {
		return Record{}, fmt.Errorf("evidence chunk %d sentence ID does not match manifest", index)
	}
	if parsed.TranscriptGeneration != "" && parsed.TranscriptGeneration != checkpoint.Generation {
		return Record{}, fmt.Errorf("evidence chunk %d generation does not match manifest", index)
	}
	if parsed.StartMs != checkpoint.StartMs || parsed.EndMs != checkpoint.EndMs {
		return Record{}, fmt.Errorf("evidence chunk %d timing does not match manifest", index)
	}
	if checkpoint.SourceSegmentID != "" && parsed.SourceSentenceID != checkpoint.SourceSegmentID {
		return Record{}, fmt.Errorf("evidence chunk %d source sentence does not match manifest", index)
	}
	if checkpoint.SpeakerID != "" && parsed.SpeakerID != checkpoint.SpeakerID {
		return Record{}, fmt.Errorf("evidence chunk %d speaker does not match manifest", index)
	}
	text := originalText(content)
	if text == "" {
		return Record{}, fmt.Errorf("evidence chunk %d has empty original text", index)
	}
	return Record{
		VideoID: checkpoint.VideoID, TranscriptGeneration: checkpoint.Generation,
		ChunkIndex: checkpoint.ChunkIndex, KnowledgeID: checkpoint.KnowledgeID,
		EvidenceSentenceID: checkpoint.EvidenceSentenceID, SourceSentenceID: firstNonEmpty(checkpoint.SourceSegmentID, parsed.SourceSentenceID),
		Text: text, SpeakerID: firstNonEmpty(checkpoint.SpeakerID, parsed.SpeakerID), StartMs: checkpoint.StartMs, EndMs: checkpoint.EndMs,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
