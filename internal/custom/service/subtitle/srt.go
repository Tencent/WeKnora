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

// ValidateParagraphs checks the minimum timeline contract required by SRT and indexing.
func ValidateParagraphs(paragraphs []TranscriptParagraph) error {
	validSentences := 0
	for paragraphIndex, paragraph := range paragraphs {
		for sentenceIndex, sentence := range paragraph.Sentences {
			if strings.TrimSpace(sentence.Text) == "" {
				continue
			}
			if sentence.StartMs < 0 || sentence.EndMs <= sentence.StartMs {
				return fmt.Errorf("invalid subtitle timeline at paragraph=%d sentence=%d: start_ms=%d end_ms=%d", paragraphIndex, sentenceIndex, sentence.StartMs, sentence.EndMs)
			}
			validSentences++
		}
	}
	if validSentences == 0 {
		return fmt.Errorf("transcript contains no non-empty timed sentences")
	}
	return nil
}

// ValidateTranscriptQuality checks cross-sentence invariants before content is published.
func ValidateTranscriptQuality(paragraphs []TranscriptParagraph, durationSeconds int) error {
	var previousStart, previousEnd int
	var firstStart, lastEnd int
	validSentences := 0
	for paragraphIndex, paragraph := range paragraphs {
		for sentenceIndex, sentence := range paragraph.Sentences {
			if strings.TrimSpace(sentence.Text) == "" {
				continue
			}
			if sentence.StartMs < 0 || sentence.EndMs <= sentence.StartMs {
				return fmt.Errorf("invalid transcript timeline at paragraph=%d sentence=%d", paragraphIndex, sentenceIndex)
			}
			if validSentences > 0 && (sentence.StartMs < previousStart || sentence.EndMs < previousEnd) {
				return fmt.Errorf("transcript timestamps are not monotonic at paragraph=%d sentence=%d", paragraphIndex, sentenceIndex)
			}
			if validSentences == 0 {
				firstStart = sentence.StartMs
			}
			if validSentences > 0 && sentence.StartMs-previousEnd > 30*60*1000 {
				return fmt.Errorf("transcript contains an abnormal timeline gap before paragraph=%d sentence=%d", paragraphIndex, sentenceIndex)
			}
			previousEnd = sentence.EndMs
			previousStart = sentence.StartMs
			lastEnd = sentence.EndMs
			validSentences++
		}
	}
	if validSentences == 0 {
		return fmt.Errorf("transcript quality gate found no non-empty sentences")
	}
	if durationSeconds > 0 && (firstStart > durationSeconds*1000+5*60*1000 || lastEnd > durationSeconds*1000+5*60*1000) {
		return fmt.Errorf("transcript timeline exceeds video duration: first_start_ms=%d last_end_ms=%d duration_seconds=%d", firstStart, lastEnd, durationSeconds)
	}
	if durationSeconds > 0 {
		tolerance := durationSeconds * 1000 / 10
		if tolerance < 30*1000 {
			tolerance = 30 * 1000
		}
		if tolerance > 5*60*1000 {
			tolerance = 5 * 60 * 1000
		}
		if firstStart > tolerance || lastEnd < durationSeconds*1000-tolerance {
			return fmt.Errorf("transcript does not cover video boundaries: first_start_ms=%d last_end_ms=%d duration_seconds=%d", firstStart, lastEnd, durationSeconds)
		}
	}
	return nil
}

// TranscriptParagraph 听悟转写段落（最小可用字段）
//
// 实际字段以听悟协议为准；此处只取生成 SRT 必需的字段。
type TranscriptParagraph struct {
	ParagraphID string               `json:"paragraph_id"`
	SpeakerID   string               `json:"speaker_id"`
	StartMs     int                  `json:"start_ms"`
	EndMs       int                  `json:"end_ms"`
	Sentences   []TranscriptSentence `json:"sentences"`
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
