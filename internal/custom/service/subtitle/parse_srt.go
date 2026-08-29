package subtitle

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var srtTimestampPattern = regexp.MustCompile(`^(\d{1,2}):(\d{2}):(\d{2})[,.](\d{3})\s+-->\s+(\d{1,2}):(\d{2}):(\d{2})[,.](\d{3})`)
var speakerPattern = regexp.MustCompile(`^\[说话人\s+([^\]]+)\]\s*`)

func ParseSRT(reader io.Reader) ([]TranscriptParagraph, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	lines := make([]string, 0, 256)
	for scanner.Scan() {
		lines = append(lines, strings.TrimPrefix(scanner.Text(), "\ufeff"))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read srt: %w", err)
	}

	paragraphs := make([]TranscriptParagraph, 0, len(lines)/4)
	for index := 0; index < len(lines); {
		for index < len(lines) && strings.TrimSpace(lines[index]) == "" {
			index++
		}
		if index >= len(lines) {
			break
		}
		if _, err := strconv.Atoi(strings.TrimSpace(lines[index])); err != nil {
			return nil, fmt.Errorf("invalid srt cue number at line %d", index+1)
		}
		index++
		if index >= len(lines) {
			return nil, fmt.Errorf("missing srt timestamp after cue at line %d", index)
		}
		startMs, endMs, err := parseSRTTimestampLine(lines[index])
		if err != nil {
			return nil, fmt.Errorf("invalid srt timestamp at line %d: %w", index+1, err)
		}
		index++
		textLines := make([]string, 0, 2)
		for index < len(lines) && strings.TrimSpace(lines[index]) != "" {
			textLines = append(textLines, strings.TrimSpace(lines[index]))
			index++
		}
		text := strings.TrimSpace(strings.Join(textLines, "\n"))
		if text == "" {
			return nil, fmt.Errorf("empty srt cue at line %d", index+1)
		}
		speakerID := "0"
		if match := speakerPattern.FindStringSubmatch(text); len(match) == 2 {
			speakerID = strings.TrimSpace(match[1])
			text = strings.TrimSpace(speakerPattern.ReplaceAllString(text, ""))
		}
		if text == "" {
			return nil, fmt.Errorf("empty srt cue after speaker marker at line %d", index+1)
		}
		cueID := fmt.Sprintf("srt-%06d", len(paragraphs)+1)
		paragraphs = append(paragraphs, TranscriptParagraph{
			ParagraphID: cueID,
			SpeakerID:   speakerID,
			StartMs:     startMs,
			EndMs:       endMs,
			Sentences: []TranscriptSentence{{
				SentenceID: cueID,
				Text:       text,
				StartMs:    startMs,
				EndMs:      endMs,
			}},
		})
	}
	if err := ValidateParagraphs(paragraphs); err != nil {
		return nil, err
	}
	return paragraphs, nil
}

func parseSRTTimestampLine(line string) (int, int, error) {
	match := srtTimestampPattern.FindStringSubmatch(strings.TrimSpace(line))
	if len(match) != 9 {
		return 0, 0, fmt.Errorf("expected HH:MM:SS,mmm --> HH:MM:SS,mmm")
	}
	startMs, err := parseSRTTimestamp(match[1:5])
	if err != nil {
		return 0, 0, fmt.Errorf("parse start: %w", err)
	}
	endMs, err := parseSRTTimestamp(match[5:9])
	if err != nil {
		return 0, 0, fmt.Errorf("parse end: %w", err)
	}
	if endMs <= startMs {
		return 0, 0, fmt.Errorf("end must be after start")
	}
	return startMs, endMs, nil
}

func parseSRTTimestamp(parts []string) (int, error) {
	values := make([]int, 4)
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return 0, err
		}
		values[index] = value
	}
	if values[2] > 59 || values[1] > 59 || values[3] > 999 {
		return 0, fmt.Errorf("timestamp component out of range")
	}
	return ((values[0]*60+values[1])*60+values[2])*1000 + values[3], nil
}
