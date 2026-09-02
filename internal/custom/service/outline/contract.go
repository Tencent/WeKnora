package outline

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

const SchemaVersion = 1

type Document struct {
	SchemaVersion int       `json:"schema_version"`
	Chapters      []Chapter `json:"chapters"`
}

type Chapter struct {
	ChapterIndex        int              `json:"chapter_index"`
	ChapterTitle        string           `json:"chapter_title"`
	StartSeconds        int              `json:"start_seconds"`
	EndSeconds          int              `json:"end_seconds"`
	ChapterSummary      string           `json:"chapter_summary"`
	KnowledgePoints     []KnowledgePoint `json:"knowledge_points"`
	AlignmentStatus     string           `json:"alignment_status,omitempty"`
	EvidenceChunkIDs    []string         `json:"evidence_chunk_ids,omitempty"`
	EvidenceSentenceIDs []string         `json:"evidence_sentence_ids,omitempty"`
}

type KnowledgePoint struct {
	Title               string   `json:"title"`
	Seconds             int      `json:"seconds"`
	EvidenceChunkIDs    []string `json:"evidence_chunk_ids,omitempty"`
	EvidenceSentenceIDs []string `json:"evidence_sentence_ids,omitempty"`
}

var legacyMarkdownHeading = regexp.MustCompile(`(?m)^##\s+\S+`)

func Parse(content string) (Document, error) {
	var document Document
	if err := json.Unmarshal([]byte(stripFrontmatter(content)), &document); err != nil {
		return Document{}, fmt.Errorf("parse outline JSON: %w", err)
	}
	if err := Validate(document, 0, nil); err != nil {
		return Document{}, err
	}
	return document, nil
}

func Marshal(document Document) (string, error) {
	if err := Validate(document, 0, nil); err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal outline JSON: %w", err)
	}
	return string(encoded), nil
}

func Validate(document Document, durationSeconds int, knownChunkIDs map[string]struct{}) error {
	return validate(document, durationSeconds, 0, knownChunkIDs)
}

func ValidateWithTranscriptEnd(document Document, durationSeconds, transcriptEndSeconds int, knownChunkIDs map[string]struct{}) error {
	if transcriptEndSeconds <= 0 {
		return fmt.Errorf("transcript end timestamp is required")
	}
	return validate(document, durationSeconds, transcriptEndSeconds, knownChunkIDs)
}

// ValidateAndResolve validates an outline against the active evidence layer
// and projects every citation onto its immutable sentence ID. It is the
// promotion gate for turning a draft into a final outline.
func ValidateAndResolve(document *Document, durationSeconds int, chunks []transcript.Chunk) error {
	if document == nil {
		return fmt.Errorf("outline document is nil")
	}
	if len(chunks) == 0 {
		return fmt.Errorf("outline evidence chunks are empty")
	}
	knownChunkIDs := make(map[string]struct{}, len(chunks))
	seenEvidenceSentenceIDs := make(map[string]struct{}, len(chunks))
	for index, chunk := range chunks {
		if strings.TrimSpace(chunk.ID) == "" {
			return fmt.Errorf("outline evidence chunk %d has no knowledge ID", index)
		}
		if strings.TrimSpace(chunk.EvidenceSentenceID) == "" {
			return fmt.Errorf("outline evidence chunk %d has no evidence sentence ID", index)
		}
		if _, exists := seenEvidenceSentenceIDs[chunk.EvidenceSentenceID]; exists {
			return fmt.Errorf("outline evidence contains duplicate evidence sentence ID %q", chunk.EvidenceSentenceID)
		}
		if chunk.StartMs < 0 || chunk.EndMs <= chunk.StartMs {
			return fmt.Errorf("outline evidence chunk %d has invalid time range", index)
		}
		if _, exists := knownChunkIDs[chunk.ID]; exists {
			return fmt.Errorf("outline evidence contains duplicate knowledge ID %q", chunk.ID)
		}
		knownChunkIDs[chunk.ID] = struct{}{}
		seenEvidenceSentenceIDs[chunk.EvidenceSentenceID] = struct{}{}
	}
	transcriptEndSeconds, err := transcript.EffectiveEndSeconds(chunks)
	if err != nil {
		return fmt.Errorf("effective transcript end: %w", err)
	}
	if err := ValidateWithTranscriptEnd(*document, durationSeconds, transcriptEndSeconds, knownChunkIDs); err != nil {
		return err
	}
	if err := ResolveEvidence(document, chunks); err != nil {
		return err
	}
	return validateResolvedEvidence(*document, chunks)
}

// ResolveEvidence projects every chapter and point citation onto the
// immutable sentence IDs carried by the current transcript chunks.
func ResolveEvidence(document *Document, chunks []transcript.Chunk) error {
	if document == nil {
		return fmt.Errorf("outline document is nil")
	}
	byID := make(map[string]transcript.Chunk, len(chunks))
	for _, chunk := range chunks {
		byID[chunk.ID] = chunk
	}
	resolve := func(ids []string) ([]string, error) {
		resolved := make([]string, 0, len(ids))
		for _, id := range ids {
			chunk, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("resolve unknown evidence chunk %q", id)
			}
			if chunk.EvidenceSentenceID != "" {
				resolved = append(resolved, chunk.EvidenceSentenceID)
			}
		}
		return resolved, nil
	}
	for chapterIndex := range document.Chapters {
		chapter := &document.Chapters[chapterIndex]
		ids, err := resolve(chapter.EvidenceChunkIDs)
		if err != nil {
			return fmt.Errorf("chapter %d: %w", chapter.ChapterIndex, err)
		}
		chapter.EvidenceSentenceIDs = ids
		for pointIndex := range chapter.KnowledgePoints {
			point := &chapter.KnowledgePoints[pointIndex]
			ids, err := resolve(point.EvidenceChunkIDs)
			if err != nil {
				return fmt.Errorf("chapter %d knowledge point %d: %w", chapter.ChapterIndex, pointIndex+1, err)
			}
			point.EvidenceSentenceIDs = ids
		}
	}
	return nil
}

func validateResolvedEvidence(document Document, chunks []transcript.Chunk) error {
	byChunkID := make(map[string]string, len(chunks))
	for _, chunk := range chunks {
		byChunkID[chunk.ID] = chunk.EvidenceSentenceID
	}
	for _, chapter := range document.Chapters {
		if len(chapter.EvidenceSentenceIDs) != len(chapter.EvidenceChunkIDs) {
			return fmt.Errorf("chapter %d evidence sentence references are incomplete", chapter.ChapterIndex)
		}
		for index, chunkID := range chapter.EvidenceChunkIDs {
			if chapter.EvidenceSentenceIDs[index] != byChunkID[chunkID] {
				return fmt.Errorf("chapter %d evidence sentence reference does not match chunk %q", chapter.ChapterIndex, chunkID)
			}
		}
		for pointIndex, point := range chapter.KnowledgePoints {
			if len(point.EvidenceSentenceIDs) != len(point.EvidenceChunkIDs) {
				return fmt.Errorf("chapter %d knowledge point %d evidence sentence references are incomplete", chapter.ChapterIndex, pointIndex+1)
			}
			for index, chunkID := range point.EvidenceChunkIDs {
				if point.EvidenceSentenceIDs[index] != byChunkID[chunkID] {
					return fmt.Errorf("chapter %d knowledge point %d evidence sentence reference does not match chunk %q", chapter.ChapterIndex, pointIndex+1, chunkID)
				}
			}
		}
	}
	return nil
}

func validate(document Document, durationSeconds, transcriptEndSeconds int, knownChunkIDs map[string]struct{}) error {
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("outline schema_version must be %d", SchemaVersion)
	}
	if len(document.Chapters) == 0 {
		return fmt.Errorf("outline must contain at least one chapter")
	}
	if len(document.Chapters) > 16 {
		return fmt.Errorf("outline contains too many chapters: %d", len(document.Chapters))
	}

	previousStart := -1
	previousEnd := 0
	totalPoints := 0
	for index, chapter := range document.Chapters {
		if chapter.ChapterIndex != index+1 {
			return fmt.Errorf("chapter %d has invalid chapter_index %d", index+1, chapter.ChapterIndex)
		}
		if err := validateShortTitle("chapter", chapter.ChapterTitle); err != nil {
			return err
		}
		if strings.TrimSpace(chapter.ChapterSummary) == "" || isPlaceholder(chapter.ChapterSummary) {
			return fmt.Errorf("chapter %d has empty or placeholder summary", chapter.ChapterIndex)
		}
		if len(chapter.EvidenceChunkIDs) == 0 {
			return fmt.Errorf("chapter %d has no evidence chunk references", chapter.ChapterIndex)
		}
		if chapter.StartSeconds < 0 || chapter.EndSeconds <= chapter.StartSeconds {
			return fmt.Errorf("chapter %d has invalid time range", chapter.ChapterIndex)
		}
		if chapter.StartSeconds <= previousStart {
			return fmt.Errorf("chapter %d is out of chronological order", chapter.ChapterIndex)
		}
		if index > 0 && chapter.StartSeconds < previousEnd {
			return fmt.Errorf("chapter %d overlaps previous chapter", chapter.ChapterIndex)
		}
		if durationSeconds > 0 && chapter.EndSeconds > durationSeconds {
			return fmt.Errorf("chapter %d exceeds video duration", chapter.ChapterIndex)
		}
		previousStart = chapter.StartSeconds
		previousEnd = chapter.EndSeconds

		if len(chapter.KnowledgePoints) == 0 || len(chapter.KnowledgePoints) > 3 {
			return fmt.Errorf("chapter %d must contain 1 to 3 knowledge points", chapter.ChapterIndex)
		}
		totalPoints += len(chapter.KnowledgePoints)
		for pointIndex, point := range chapter.KnowledgePoints {
			if err := validateShortTitle(fmt.Sprintf("chapter %d knowledge point %d", chapter.ChapterIndex, pointIndex+1), point.Title); err != nil {
				return err
			}
			if point.Seconds < chapter.StartSeconds || point.Seconds > chapter.EndSeconds {
				return fmt.Errorf("chapter %d knowledge point %d is outside chapter time range", chapter.ChapterIndex, pointIndex+1)
			}
			if len(point.EvidenceChunkIDs) == 0 {
				return fmt.Errorf("chapter %d knowledge point %d has no evidence chunk references", chapter.ChapterIndex, pointIndex+1)
			}
			if durationSeconds > 0 && point.Seconds > durationSeconds {
				return fmt.Errorf("chapter %d knowledge point %d exceeds video duration", chapter.ChapterIndex, pointIndex+1)
			}
			if knownChunkIDs != nil {
				for _, chunkID := range point.EvidenceChunkIDs {
					if _, ok := knownChunkIDs[chunkID]; !ok {
						return fmt.Errorf("chapter %d knowledge point %d references unknown chunk %q", chapter.ChapterIndex, pointIndex+1, chunkID)
					}
				}
			}
		}
		if knownChunkIDs != nil {
			for _, chunkID := range chapter.EvidenceChunkIDs {
				if _, ok := knownChunkIDs[chunkID]; !ok {
					return fmt.Errorf("chapter %d references unknown chunk %q", chapter.ChapterIndex, chunkID)
				}
			}
		}
	}
	if totalPoints > 16 {
		return fmt.Errorf("outline contains too many knowledge points: %d", totalPoints)
	}
	if transcriptEndSeconds > 0 {
		first := document.Chapters[0]
		last := document.Chapters[len(document.Chapters)-1]
		if first.StartSeconds > 1 {
			return fmt.Errorf("outline starts after video beginning")
		}
		if last.EndSeconds < transcriptEndSeconds-1 {
			return fmt.Errorf("outline does not cover effective transcript end: transcript_end_seconds=%d outline_end_seconds=%d", transcriptEndSeconds, last.EndSeconds)
		}
	}
	return nil
}

func IsLegacyMarkdown(content string) bool {
	return legacyMarkdownHeading.MatchString(stripFrontmatter(content))
}

func RenderOrLegacy(content string) string {
	document, err := Parse(content)
	if err == nil {
		return RenderMarkdown(document)
	}
	return stripFrontmatter(content)
}

func RenderMarkdown(document Document) string {
	var builder strings.Builder
	for _, chapter := range document.Chapters {
		fmt.Fprintf(&builder, "## %s\n\n- 时间：`%s–%s`\n- 对齐状态：`%s`\n\n### 本章核心内容\n\n%s\n\n### 关键知识点\n\n", chapter.ChapterTitle, formatSeconds(chapter.StartSeconds), formatSeconds(chapter.EndSeconds), defaultAlignment(chapter.AlignmentStatus), chapter.ChapterSummary)
		for _, point := range chapter.KnowledgePoints {
			fmt.Fprintf(&builder, "- %s（%s）\n", point.Title, formatSeconds(point.Seconds))
		}
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

func formatSeconds(seconds int) string {
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	remainder := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, remainder)
	}
	return fmt.Sprintf("%02d:%02d", minutes, remainder)
}

func defaultAlignment(status string) string {
	if strings.TrimSpace(status) == "" {
		return "verified"
	}
	return status
}

func validateShortTitle(label, value string) error {
	title := strings.TrimSpace(value)
	if title == "" || isPlaceholder(title) {
		return fmt.Errorf("%s title is empty or placeholder", label)
	}
	if utf8.RuneCountInString(title) > 20 {
		return fmt.Errorf("%s title is too long", label)
	}
	if strings.ContainsAny(title, "。！？；;\n") {
		return fmt.Errorf("%s title must be a short phrase", label)
	}
	return nil
}

func isPlaceholder(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "..." || trimmed == "…" || trimmed == "省略"
}

func stripFrontmatter(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---") {
		return trimmed
	}
	if end := strings.Index(trimmed[3:], "\n---"); end >= 0 {
		return strings.TrimSpace(trimmed[end+7:])
	}
	return trimmed
}
