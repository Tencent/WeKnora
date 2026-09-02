// Package model 字幕分块元数据（VP-T008：12 字段 metadata，方案二合并版 §6.5 为权威）。
//
// 设计原则：
//   - 必含字段：start_ms / end_ms / source_filename（分块定位 + 溯源）
//   - 选填字段：speaker_id / sentence_id / paragraph_index / chunk_index
//   - 业务字段：video_id / video_type / duration_seconds / language / chunk_text
package model

// ChunkMetadata 字幕分块 metadata（写入 WeKnora KB 的元数据字典）
//
// 字段集以 [方案二合并版 §6.5] 为准；本结构仅给 Go 侧用，最终以 map[string]any 上传
type ChunkMetadata struct {
	VideoID              string `json:"video_id"`
	VideoType            string `json:"video_type"`
	TranscriptGeneration string `json:"transcript_generation,omitempty"`
	SourceFilename       string `json:"source_filename"`  // 必含
	StartMs              int    `json:"start_ms"`         // 必含：分块起始时间（毫秒）
	EndMs                int    `json:"end_ms"`           // 必含：分块结束时间（毫秒）
	DurationSeconds      int    `json:"duration_seconds"` // 视频总时长（秒）
	SpeakerID            string `json:"speaker_id,omitempty"`
	SentenceID           string `json:"sentence_id,omitempty"`
	EvidenceSentenceID   string `json:"evidence_sentence_id,omitempty"`
	ParagraphIndex       int    `json:"paragraph_index,omitempty"`
	ChunkIndex           int    `json:"chunk_index"` // 块在整段字幕中的顺序
	Language             string `json:"language,omitempty"`
	ChunkText            string `json:"chunk_text"` // 分块原文（冗余存便于搜索）
}

// ToMap 转 WeKnora 入库用的 map
func (m ChunkMetadata) ToMap() map[string]any {
	return map[string]any{
		"video_id":              m.VideoID,
		"video_type":            m.VideoType,
		"transcript_generation": m.TranscriptGeneration,
		"source_filename":       m.SourceFilename,
		"start_ms":              m.StartMs,
		"end_ms":                m.EndMs,
		"duration_seconds":      m.DurationSeconds,
		"speaker_id":            m.SpeakerID,
		"sentence_id":           m.SentenceID,
		"evidence_sentence_id":  m.EvidenceSentenceID,
		"paragraph_index":       m.ParagraphIndex,
		"chunk_index":           m.ChunkIndex,
		"language":              m.Language,
		"chunk_text":            m.ChunkText,
	}
}
