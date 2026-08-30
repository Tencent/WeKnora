package outline

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const SchemaVersion = 1

type Document struct {
	SchemaVersion int       `json:"schema_version"`
	Chapters      []Chapter `json:"chapters"`
}

type Chapter struct {
	ChapterIndex     int              `json:"chapter_index"`
	ChapterTitle     string           `json:"chapter_title"`
	StartSeconds     int              `json:"start_seconds"`
	EndSeconds       int              `json:"end_seconds"`
	ChapterSummary   string           `json:"chapter_summary"`
	KnowledgePoints  []KnowledgePoint `json:"knowledge_points"`
	AlignmentStatus  string           `json:"alignment_status,omitempty"`
	EvidenceChunkIDs []string         `json:"evidence_chunk_ids,omitempty"`
}

type KnowledgePoint struct {
	Title            string   `json:"title"`
	Seconds          int      `json:"seconds"`
	EvidenceChunkIDs []string `json:"evidence_chunk_ids,omitempty"`
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
		if durationSeconds > 0 && chapter.EndSeconds > durationSeconds {
			return fmt.Errorf("chapter %d exceeds video duration", chapter.ChapterIndex)
		}
		previousStart = chapter.StartSeconds

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
