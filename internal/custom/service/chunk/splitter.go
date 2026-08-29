// Package chunk 字幕分块（VP-T007 句子级聚合 + VP-T008 12 字段 metadata）。
//
// 设计要点：
//   - 按 `sentenceId` 聚合句子（D3：句子级粒度）
//   - 不强制与 WeKnora 默认分块一致（FR-005）
//   - 每块带 12 字段 metadata 写入 WeKnora KB
package chunk

import (
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/subtitle"
	"github.com/Tencent/WeKnora/internal/custom/service/videotype"
)

// Splitter 句子级分块器
type Splitter struct{}

// NewSplitter 构造
func NewSplitter() *Splitter { return &Splitter{} }

// SplitResult 一个分块（内容 + metadata）
type SplitResult struct {
	Content  string
	Metadata model.ChunkMetadata
}

// SplitInputs 拆分入参（视频元数据 + 字幕段落）
type SplitInputs struct {
	VideoID         string
	VideoType       string
	SourceFilename  string
	DurationSeconds int
	Language        string
	Paragraphs      []subtitle.TranscriptParagraph
}

// Split 按句子聚合输出分块列表
func (s *Splitter) Split(in SplitInputs) []SplitResult {
	out := make([]SplitResult, 0, len(in.Paragraphs)*2)
	chunkIdx := 0
	for pIdx, p := range in.Paragraphs {
		speaker := p.SpeakerID
		if speaker == "" {
			speaker = "0"
		}
		for _, s := range p.Sentences {
			text := strings.TrimSpace(s.Text)
			if text == "" {
				continue
			}
			md := model.ChunkMetadata{
				VideoID:         in.VideoID,
				VideoType:       videotype.Normalize(in.VideoType),
				SourceFilename:  filepath.Base(in.SourceFilename),
				StartMs:         s.StartMs,
				EndMs:           s.EndMs,
				DurationSeconds: in.DurationSeconds,
				SpeakerID:       speaker,
				SentenceID:      s.SentenceID,
				ParagraphIndex:  pIdx,
				ChunkIndex:      chunkIdx,
				Language:        in.Language,
				ChunkText:       text,
			}
			out = append(out, SplitResult{Content: text, Metadata: md})
			chunkIdx++
		}
	}
	return out
}
