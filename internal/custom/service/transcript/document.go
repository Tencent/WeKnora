package transcript

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FullVideoDocument is the stable, Wiki-only representation of one complete
// transcript generation. It is deliberately separate from subtitle chunks:
// paragraphs retain the source mapping while continuous_text gives the Wiki
// pipeline a readable whole-document context.
type FullVideoDocument struct {
	SchemaVersion        int       `json:"schema_version"`
	VideoID              string    `json:"video_id"`
	TranscriptGeneration string    `json:"transcript_generation"`
	Title                string    `json:"title"`
	DurationSeconds      int       `json:"duration_seconds"`
	Chapters             []Chapter `json:"chapters"`
	ContinuousText       string    `json:"continuous_text"`
}

type Chapter struct {
	Index          int         `json:"index"`
	Title          string      `json:"title"`
	StartMs        int         `json:"start_ms"`
	EndMs          int         `json:"end_ms"`
	Paragraphs     []Paragraph `json:"paragraphs"`
	ContinuousText string      `json:"continuous_text"`
}

type Paragraph struct {
	ParagraphID         string     `json:"paragraph_id,omitempty"`
	Index               int        `json:"index"`
	SpeakerID           string     `json:"speaker_id,omitempty"`
	Text                string     `json:"text"`
	StartMs             int        `json:"start_ms"`
	EndMs               int        `json:"end_ms"`
	SourceSentenceIDs   []string   `json:"source_sentence_ids"`
	EvidenceSentenceIDs []string   `json:"evidence_sentence_ids"`
	TimeMarks           []TimeMark `json:"time_marks"`
}

type TimeMark struct {
	SourceSentenceID   string `json:"source_sentence_id"`
	EvidenceSentenceID string `json:"evidence_sentence_id"`
	Text               string `json:"text"`
	StartMs            int    `json:"start_ms"`
	EndMs              int    `json:"end_ms"`
}

// Input is the normalized shape consumed by Build. The adapter in the next
// task maps provider JSON into this shape; this package owns no provider API.
type Input struct {
	VideoID              string
	TranscriptGeneration string
	Title                string
	DurationSeconds      int
	Chapters             []InputChapter
}

type InputChapter struct {
	Index      int
	Title      string
	Paragraphs []InputParagraph
}

type InputParagraph struct {
	ParagraphID string
	Index       int
	SpeakerID   string
	Sentences   []InputSentence
}

type InputSentence struct {
	SourceSentenceID   string
	EvidenceSentenceID string
	Text               string
	SpeakerID          string
	StartMs            int
	EndMs              int
}

const DocumentSchemaVersion = 1

// Build deterministically assembles a complete document. It does not merge
// subtitles or generate titles; those policy decisions belong to the adapter.
func Build(input Input) (FullVideoDocument, error) {
	doc := FullVideoDocument{
		SchemaVersion:        DocumentSchemaVersion,
		VideoID:              strings.TrimSpace(input.VideoID),
		TranscriptGeneration: strings.TrimSpace(input.TranscriptGeneration),
		Title:                strings.TrimSpace(input.Title),
		DurationSeconds:      input.DurationSeconds,
	}
	if doc.VideoID == "" || doc.TranscriptGeneration == "" || doc.Title == "" {
		return FullVideoDocument{}, fmt.Errorf("video id, transcript generation and title are required")
	}
	if doc.DurationSeconds < 0 {
		return FullVideoDocument{}, fmt.Errorf("duration seconds must not be negative")
	}
	if len(input.Chapters) == 0 {
		return FullVideoDocument{}, fmt.Errorf("document chapters are empty")
	}

	for chapterIndex, inputChapter := range input.Chapters {
		if inputChapter.Index != chapterIndex {
			return FullVideoDocument{}, fmt.Errorf("chapter index is not contiguous: expected=%d actual=%d", chapterIndex, inputChapter.Index)
		}
		chapter := Chapter{Index: chapterIndex, Title: strings.TrimSpace(inputChapter.Title)}
		if chapter.Title == "" {
			return FullVideoDocument{}, fmt.Errorf("chapter %d title is required", chapterIndex)
		}
		for paragraphIndex, inputParagraph := range inputChapter.Paragraphs {
			if inputParagraph.Index != paragraphIndex {
				return FullVideoDocument{}, fmt.Errorf("chapter %d paragraph index is not contiguous", chapterIndex)
			}
			if len(inputParagraph.Sentences) == 0 {
				return FullVideoDocument{}, fmt.Errorf("chapter %d paragraph %d has no sentences", chapterIndex, paragraphIndex)
			}
			paragraph := Paragraph{ParagraphID: strings.TrimSpace(inputParagraph.ParagraphID), Index: paragraphIndex, SpeakerID: strings.TrimSpace(inputParagraph.SpeakerID)}
			var textParts []string
			var previousStart, previousEnd int
			for sentenceIndex, sentence := range inputParagraph.Sentences {
				text := strings.TrimSpace(sentence.Text)
				if text == "" {
					return FullVideoDocument{}, fmt.Errorf("chapter %d paragraph %d sentence %d text is empty", chapterIndex, paragraphIndex, sentenceIndex)
				}
				if strings.TrimSpace(sentence.SourceSentenceID) == "" || strings.TrimSpace(sentence.EvidenceSentenceID) == "" {
					return FullVideoDocument{}, fmt.Errorf("chapter %d paragraph %d sentence %d mapping is incomplete", chapterIndex, paragraphIndex, sentenceIndex)
				}
				if sentence.StartMs < 0 || sentence.EndMs <= sentence.StartMs {
					return FullVideoDocument{}, fmt.Errorf("chapter %d paragraph %d sentence %d time range is invalid", chapterIndex, paragraphIndex, sentenceIndex)
				}
				if sentenceIndex > 0 && (sentence.StartMs < previousStart || sentence.EndMs < previousEnd) {
					return FullVideoDocument{}, fmt.Errorf("chapter %d paragraph %d sentence %d timeline is out of order", chapterIndex, paragraphIndex, sentenceIndex)
				}
				speaker := strings.TrimSpace(sentence.SpeakerID)
				if speaker == "" {
					speaker = paragraph.SpeakerID
				}
				if paragraph.SpeakerID == "" {
					paragraph.SpeakerID = speaker
				}
				if speaker != "" && paragraph.SpeakerID != "" && speaker != paragraph.SpeakerID {
					return FullVideoDocument{}, fmt.Errorf("chapter %d paragraph %d contains multiple speakers", chapterIndex, paragraphIndex)
				}
				if sentenceIndex == 0 {
					paragraph.StartMs = sentence.StartMs
				}
				paragraph.EndMs = sentence.EndMs
				paragraph.SourceSentenceIDs = append(paragraph.SourceSentenceIDs, strings.TrimSpace(sentence.SourceSentenceID))
				paragraph.EvidenceSentenceIDs = append(paragraph.EvidenceSentenceIDs, strings.TrimSpace(sentence.EvidenceSentenceID))
				paragraph.TimeMarks = append(paragraph.TimeMarks, TimeMark{
					SourceSentenceID: strings.TrimSpace(sentence.SourceSentenceID), EvidenceSentenceID: strings.TrimSpace(sentence.EvidenceSentenceID),
					Text:    text,
					StartMs: sentence.StartMs, EndMs: sentence.EndMs,
				})
				textParts = append(textParts, text)
				previousStart, previousEnd = sentence.StartMs, sentence.EndMs
			}
			paragraph.Text = joinTranscriptText(textParts)
			chapter.Paragraphs = append(chapter.Paragraphs, paragraph)
		}
		if len(chapter.Paragraphs) == 0 {
			return FullVideoDocument{}, fmt.Errorf("chapter %d has no paragraphs", chapterIndex)
		}
		chapter.StartMs = chapter.Paragraphs[0].StartMs
		chapter.EndMs = chapter.Paragraphs[len(chapter.Paragraphs)-1].EndMs
		chapter.ContinuousText = joinTranscriptText(paragraphTexts(chapter.Paragraphs))
		doc.Chapters = append(doc.Chapters, chapter)
	}
	doc.ContinuousText = joinTranscriptText(chapterTexts(doc.Chapters))
	if err := Validate(doc); err != nil {
		return FullVideoDocument{}, err
	}
	return doc, nil
}

func Validate(doc FullVideoDocument) error {
	if doc.SchemaVersion != DocumentSchemaVersion || strings.TrimSpace(doc.VideoID) == "" || strings.TrimSpace(doc.TranscriptGeneration) == "" || strings.TrimSpace(doc.Title) == "" {
		return fmt.Errorf("document identity or schema version is invalid")
	}
	if len(doc.Chapters) == 0 || strings.TrimSpace(doc.ContinuousText) == "" {
		return fmt.Errorf("document content is empty")
	}
	if doc.ContinuousText != joinTranscriptText(chapterTexts(doc.Chapters)) {
		return fmt.Errorf("document continuous text cannot be reconstructed")
	}
	seenSource := map[string]struct{}{}
	seenEvidence := map[string]struct{}{}
	seenParagraphs := map[string]struct{}{}
	var previousStart, previousEnd int
	var hasPreviousMark bool
	maxDurationMs := int64(doc.DurationSeconds) * 1000
	for chapterIndex, chapter := range doc.Chapters {
		if chapter.Index != chapterIndex || len(chapter.Paragraphs) == 0 || chapter.StartMs < 0 || chapter.EndMs <= chapter.StartMs || chapter.ContinuousText == "" {
			return fmt.Errorf("chapter %d is invalid", chapterIndex)
		}
		if chapter.ContinuousText != joinTranscriptText(paragraphTexts(chapter.Paragraphs)) {
			return fmt.Errorf("chapter %d continuous text cannot be reconstructed", chapterIndex)
		}
		if chapter.StartMs != chapter.Paragraphs[0].StartMs || chapter.EndMs != chapter.Paragraphs[len(chapter.Paragraphs)-1].EndMs {
			return fmt.Errorf("chapter %d bounds do not match paragraphs", chapterIndex)
		}
		for paragraphIndex, paragraph := range chapter.Paragraphs {
			if paragraph.Index != paragraphIndex || paragraph.Text == "" || paragraph.StartMs < 0 || paragraph.EndMs <= paragraph.StartMs || len(paragraph.TimeMarks) == 0 {
				return fmt.Errorf("chapter %d paragraph %d is invalid", chapterIndex, paragraphIndex)
			}
			if paragraph.ParagraphID != "" {
				if _, exists := seenParagraphs[paragraph.ParagraphID]; exists {
					return fmt.Errorf("duplicate paragraph ID %q", paragraph.ParagraphID)
				}
				seenParagraphs[paragraph.ParagraphID] = struct{}{}
			}
			if len(paragraph.SourceSentenceIDs) != len(paragraph.TimeMarks) || len(paragraph.EvidenceSentenceIDs) != len(paragraph.TimeMarks) {
				return fmt.Errorf("chapter %d paragraph %d sentence mapping is inconsistent", chapterIndex, paragraphIndex)
			}
			if paragraph.Text != joinTranscriptText(timeMarkTexts(paragraph.TimeMarks)) {
				return fmt.Errorf("chapter %d paragraph %d text cannot be reconstructed", chapterIndex, paragraphIndex)
			}
			if paragraph.StartMs != paragraph.TimeMarks[0].StartMs || paragraph.EndMs != paragraph.TimeMarks[len(paragraph.TimeMarks)-1].EndMs {
				return fmt.Errorf("chapter %d paragraph %d bounds do not match time marks", chapterIndex, paragraphIndex)
			}
			for markIndex, mark := range paragraph.TimeMarks {
				if mark.SourceSentenceID == "" || mark.EvidenceSentenceID == "" || mark.Text == "" || mark.StartMs < 0 || mark.EndMs <= mark.StartMs {
					return fmt.Errorf("chapter %d paragraph %d time mark %d is invalid", chapterIndex, paragraphIndex, markIndex)
				}
				if paragraph.SourceSentenceIDs[markIndex] != mark.SourceSentenceID || paragraph.EvidenceSentenceIDs[markIndex] != mark.EvidenceSentenceID {
					return fmt.Errorf("chapter %d paragraph %d time mark %d does not match sentence mapping", chapterIndex, paragraphIndex, markIndex)
				}
				if hasPreviousMark && (mark.StartMs < previousStart || mark.EndMs < previousEnd) {
					return fmt.Errorf("chapter %d paragraph %d time marks are out of order", chapterIndex, paragraphIndex)
				}
				if maxDurationMs > 0 && int64(mark.EndMs) > maxDurationMs {
					return fmt.Errorf("chapter %d paragraph %d time mark exceeds video duration", chapterIndex, paragraphIndex)
				}
				if _, exists := seenSource[mark.SourceSentenceID]; exists {
					return fmt.Errorf("duplicate source sentence ID %q", mark.SourceSentenceID)
				}
				if _, exists := seenEvidence[mark.EvidenceSentenceID]; exists {
					return fmt.Errorf("duplicate evidence sentence ID %q", mark.EvidenceSentenceID)
				}
				seenSource[mark.SourceSentenceID] = struct{}{}
				seenEvidence[mark.EvidenceSentenceID] = struct{}{}
				previousStart, previousEnd, hasPreviousMark = mark.StartMs, mark.EndMs, true
			}
		}
	}
	return nil
}

// JSON returns the canonical indented representation used as the source
// document payload. Struct field order is stable by declaration order.
func (doc FullVideoDocument) JSON() (string, error) {
	if err := Validate(doc); err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal full video document: %w", err)
	}
	return string(encoded), nil
}

func paragraphTexts(paragraphs []Paragraph) []string {
	texts := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		texts = append(texts, paragraph.Text)
	}
	return texts
}

func chapterTexts(chapters []Chapter) []string {
	texts := make([]string, 0, len(chapters))
	for _, chapter := range chapters {
		texts = append(texts, chapter.ContinuousText)
	}
	return texts
}

func timeMarkTexts(marks []TimeMark) []string {
	texts := make([]string, 0, len(marks))
	for _, mark := range marks {
		texts = append(texts, mark.Text)
	}
	return texts
}

func joinTranscriptText(parts []string) string {
	result := ""
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if result != "" && needsSpace(result[len(result)-1], part[0]) {
			result += " "
		}
		result += part
	}
	return result
}

func needsSpace(left, right byte) bool {
	return (left >= 'a' && left <= 'z' || left >= 'A' && left <= 'Z' || left >= '0' && left <= '9') &&
		(right >= 'a' && right <= 'z' || right >= 'A' && right <= 'Z' || right >= '0' && right <= '9')
}
