package youtube

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Segment is one timed piece of transcript text.
type Segment struct {
	// Start is the offset from the beginning of the video, in seconds.
	Start float64
	Text  string
}

type json3Captions struct {
	Events []struct {
		TStartMs int64 `json:"tStartMs"`
		AAppend  int   `json:"aAppend"`
		Segs     []struct {
			UTF8 string `json:"utf8"`
		} `json:"segs"`
	} `json:"events"`
}

// ParseJSON3 parses YouTube's json3 caption format. Auto-generated captions
// interleave "append" events that only carry a line break; those are skipped
// so each spoken phrase appears exactly once.
func ParseJSON3(data []byte) ([]Segment, error) {
	var captions json3Captions
	if err := json.Unmarshal(data, &captions); err != nil {
		return nil, fmt.Errorf("parse json3 captions: %w", err)
	}
	segments := make([]Segment, 0, len(captions.Events))
	for _, event := range captions.Events {
		if event.AAppend != 0 || len(event.Segs) == 0 {
			continue
		}
		var text strings.Builder
		for _, seg := range event.Segs {
			text.WriteString(seg.UTF8)
		}
		if cleaned := normalizeCaptionText(text.String()); cleaned != "" {
			segments = append(segments, Segment{Start: float64(event.TStartMs) / 1000, Text: cleaned})
		}
	}
	return segments, nil
}

var (
	vttTimingPattern = regexp.MustCompile(`^(\d{1,2}:)?(\d{2}):(\d{2})[.,](\d{3})\s+-->`)
	vttTagPattern    = regexp.MustCompile(`<[^>]*>`)
)

// ParseVTT parses WebVTT captions. YouTube's auto-generated VTT repeats the
// previous line at the top of each cue ("rolling" captions), so lines equal
// to the last emitted line are dropped.
func ParseVTT(data []byte) ([]Segment, error) {
	var segments []Segment
	lastLine := ""
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inCue := false
	var cueStart float64
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if match := vttTimingPattern.FindStringSubmatch(line); match != nil {
			cueStart = vttSeconds(match)
			inCue = true
			continue
		}
		if line == "" {
			inCue = false
			continue
		}
		if !inCue {
			continue
		}
		text := normalizeCaptionText(vttTagPattern.ReplaceAllString(line, ""))
		if text == "" || text == lastLine {
			continue
		}
		lastLine = text
		segments = append(segments, Segment{Start: cueStart, Text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse vtt captions: %w", err)
	}
	return segments, nil
}

func vttSeconds(match []string) float64 {
	hours := 0
	if match[1] != "" {
		hours, _ = strconv.Atoi(strings.TrimSuffix(match[1], ":"))
	}
	minutes, _ := strconv.Atoi(match[2])
	seconds, _ := strconv.Atoi(match[3])
	millis, _ := strconv.Atoi(match[4])
	return float64(hours*3600+minutes*60+seconds) + float64(millis)/1000
}

func normalizeCaptionText(text string) string {
	return strings.Join(strings.Fields(html.UnescapeString(text)), " ")
}

// FormatTranscript groups segments into paragraphs of roughly
// paragraphSeconds each, prefixing every paragraph with its start timestamp
// so retrieved chunks can be traced back to a moment in the video.
func FormatTranscript(segments []Segment, paragraphSeconds float64) string {
	if len(segments) == 0 {
		return ""
	}
	if paragraphSeconds <= 0 {
		paragraphSeconds = 60
	}
	var out strings.Builder
	var paragraph []string
	paragraphStart := segments[0].Start
	flush := func() {
		if len(paragraph) == 0 {
			return
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString("[" + FormatTimestamp(paragraphStart) + "] ")
		out.WriteString(strings.Join(paragraph, " "))
		paragraph = paragraph[:0]
	}
	for _, seg := range segments {
		if len(paragraph) > 0 && seg.Start-paragraphStart >= paragraphSeconds {
			flush()
		}
		if len(paragraph) == 0 {
			paragraphStart = seg.Start
		}
		paragraph = append(paragraph, seg.Text)
	}
	flush()
	return out.String()
}

// FormatTimestamp renders seconds as m:ss, or h:mm:ss for an hour or more.
func FormatTimestamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds)
	h, m, s := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
