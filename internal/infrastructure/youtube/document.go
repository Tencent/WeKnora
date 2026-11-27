package youtube

import (
	"strings"
	"unicode/utf8"
)

const maxDescriptionRunes = 2000

// SourceLabel describes where the transcript came from, for the document header.
func (t *Transcript) SourceLabel() string {
	switch t.Source {
	case SourceCaptions:
		return "YouTube captions (" + t.Language + ")"
	case SourceAutoCaptions:
		return "YouTube auto-generated captions (" + t.Language + ")"
	default:
		return "Speech recognition"
	}
}

// RenderMarkdown builds the knowledge document for a video: a metadata
// header, the generated documentation (when available) and the full
// timestamped transcript. The transcript is always kept so answers can quote
// the speaker verbatim and cite a timestamp, even if the documentation pass
// summarised something away.
func RenderMarkdown(t *Transcript, documentation string) string {
	video := t.Video
	var b strings.Builder
	title := strings.TrimSpace(video.Title)
	if title == "" {
		title = "YouTube video " + video.ID
	}
	b.WriteString("# " + title + "\n\n")
	b.WriteString("- Source: " + VideoURL(video.ID) + "\n")
	if channel := video.ChannelName(); channel != "" {
		b.WriteString("- Channel: " + channel + "\n")
	}
	if date := video.UploadDate; len(date) == 8 {
		b.WriteString("- Published: " + date[:4] + "-" + date[4:6] + "-" + date[6:] + "\n")
	}
	if video.Duration > 0 {
		b.WriteString("- Duration: " + FormatTimestamp(video.Duration) + "\n")
	}
	b.WriteString("- Transcript: " + t.SourceLabel() + "\n")

	if description := truncateRunes(strings.TrimSpace(video.Description), maxDescriptionRunes); description != "" {
		b.WriteString("\n## Description\n\n" + description + "\n")
	}
	if documentation = strings.TrimSpace(documentation); documentation != "" {
		b.WriteString("\n## Documentation\n\n" + documentation + "\n")
	}
	b.WriteString("\n## Transcript\n\n" + FormatTranscript(t.Segments, 60) + "\n")
	return b.String()
}

// SplitText splits text on paragraph boundaries into pieces of at most
// maxRunes runes. A single paragraph longer than maxRunes is hard-split.
func SplitText(text string, maxRunes int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return []string{text}
	}
	var pieces []string
	var current strings.Builder
	currentRunes := 0
	flush := func() {
		if currentRunes > 0 {
			pieces = append(pieces, current.String())
			current.Reset()
			currentRunes = 0
		}
	}
	for _, paragraph := range strings.Split(text, "\n\n") {
		runes := []rune(paragraph)
		for len(runes) > maxRunes {
			flush()
			pieces = append(pieces, string(runes[:maxRunes]))
			runes = runes[maxRunes:]
		}
		if currentRunes > 0 && currentRunes+2+len(runes) > maxRunes {
			flush()
		}
		if currentRunes > 0 {
			current.WriteString("\n\n")
			currentRunes += 2
		}
		current.WriteString(string(runes))
		currentRunes += len(runes)
	}
	flush()
	return pieces
}

func truncateRunes(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}
