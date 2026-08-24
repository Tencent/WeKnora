// Package subtitle 听悟 JSON → SRT 生成（VP-T006）。
//
// 设计要点：
//   - SRT 行首加 `[说话人 X]` 标识（FR-004）
//   - 时间戳以 hh:mm:ss,mmm 表示（标准 SRT）
//   - 输入沿用听悟 paragraph/sentence 结构（sentenceId 聚合后输出）
package subtitle

import (
	"fmt"
	"strings"
	"time"
)

// TranscriptParagraph 听悟转写段落（最小可用字段）
//
// 实际字段以听悟协议为准；此处只取生成 SRT 必需的字段。
type TranscriptParagraph struct {
	ParagraphID string                 `json:"paragraph_id"`
	SpeakerID   string                 `json:"speaker_id"`
	StartMs     int                    `json:"start_ms"`
	EndMs       int                    `json:"end_ms"`
	Sentences   []TranscriptSentence   `json:"sentences"`
}

// TranscriptSentence 听悟单句
type TranscriptSentence struct {
	SentenceID string `json:"sentence_id"`
	Text       string `json:"text"`
	StartMs    int    `json:"start_ms"`
	EndMs      int    `json:"end_ms"`
	ChannelID  int    `json:"channel_id"`
}

// FormatTimestamp 把毫秒转 hh:mm:ss,mmm
func FormatTimestamp(ms int) string {
	if ms < 0 {
		ms = 0
	}
	d := time.Duration(ms) * time.Millisecond
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	mil := ms % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, mil)
}

// ParagraphsToSRT 把段落数组转 SRT（VP-T006）
//
// 每条字幕格式：
//
//	N
//	hh:mm:ss,mmm --> hh:mm:ss,mmm
//	[说话人 X] 文本
//
//	（空行）
func ParagraphsToSRT(paragraphs []TranscriptParagraph) string {
	var sb strings.Builder
	idx := 1
	for _, p := range paragraphs {
		// 每段按 sentence 拆分（更细粒度，方便前端跳转）
		sentences := p.Sentences
		if len(sentences) == 0 {
			// 没有 sentence 退化为整段
			sentences = []TranscriptSentence{{
				SentenceID: p.ParagraphID,
				Text:       "",
				StartMs:    p.StartMs,
				EndMs:      p.EndMs,
			}}
		}
		for _, s := range sentences {
			text := strings.TrimSpace(s.Text)
			if text == "" {
				continue
			}
			speaker := p.SpeakerID
			if speaker == "" {
				speaker = "0"
			}
			fmt.Fprintf(&sb, "%d\n", idx)
			fmt.Fprintf(&sb, "%s --> %s\n", FormatTimestamp(s.StartMs), FormatTimestamp(s.EndMs))
			fmt.Fprintf(&sb, "[说话人 %s] %s\n\n", speaker, text)
			idx++
		}
	}
	return sb.String()
}