package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/custom/service/evidence"
)

// RawInput is the provider-neutral boundary for the Wiki source adapter.
// Payload is the successful transcription result already persisted by the
// worker; the adapter never performs network calls or reads subtitle chunks.
type RawInput struct {
	VideoID              string
	TranscriptGeneration string
	Title                string
	DurationSeconds      int
	Provider             string
	Payload              []byte
	Merge                MergeConfig
}

// MergeConfig controls the deterministic short-subtitle policy applied before
// the stable document is built. Zero values use the production defaults.
type MergeConfig struct {
	MaxGapMs          int
	ShortMaxRunes     int
	MaxUnitRunes      int
	MaxUnitDurationMs int
}

const (
	defaultMergeMaxGapMs          = 1200
	defaultMergeShortMaxRunes     = 12
	defaultMergeMaxUnitRunes      = 160
	defaultMergeMaxUnitDurationMs = 15_000
)

// BuildFromJSON maps a successful MPS or Tingwu result to the stable document
// contract. Adjacent short subtitles are merged into speaker-consistent
// utterance paragraphs while each source sentence remains a separate time mark.
func BuildFromJSON(input RawInput) (FullVideoDocument, error) {
	input.VideoID = strings.TrimSpace(input.VideoID)
	input.TranscriptGeneration = strings.TrimSpace(input.TranscriptGeneration)
	input.Title = strings.TrimSpace(input.Title)
	if input.VideoID == "" || input.TranscriptGeneration == "" || input.Title == "" {
		return FullVideoDocument{}, fmt.Errorf("video id, transcript generation and title are required")
	}
	if len(bytes.TrimSpace(input.Payload)) == 0 {
		return FullVideoDocument{}, fmt.Errorf("transcript payload is empty")
	}

	provider := normalizeTranscriptProvider(input.Provider)
	var paragraphs []rawParagraph
	var err error
	switch provider {
	case "mps":
		paragraphs, err = parseMPS(input.Payload)
	case "tingwu":
		paragraphs, err = parseTingwu(input.Payload)
	default:
		return FullVideoDocument{}, fmt.Errorf("unsupported transcript provider %q", input.Provider)
	}
	if err != nil {
		return FullVideoDocument{}, err
	}
	if len(paragraphs) == 0 {
		return FullVideoDocument{}, fmt.Errorf("transcript payload contains no paragraphs")
	}
	paragraphs = mergeShortParagraphs(paragraphs, input.Merge)

	chapters, err := buildInputChapters(input, provider, paragraphs)
	if err != nil {
		return FullVideoDocument{}, err
	}
	return Build(Input{
		VideoID: input.VideoID, TranscriptGeneration: input.TranscriptGeneration,
		Title: input.Title, DurationSeconds: input.DurationSeconds, Chapters: chapters,
	})
}

func (config MergeConfig) withDefaults() MergeConfig {
	if config.MaxGapMs <= 0 || config.MaxGapMs > defaultMergeMaxGapMs {
		config.MaxGapMs = defaultMergeMaxGapMs
	}
	if config.ShortMaxRunes <= 0 || config.ShortMaxRunes > defaultMergeShortMaxRunes {
		config.ShortMaxRunes = defaultMergeShortMaxRunes
	}
	if config.MaxUnitRunes <= 0 || config.MaxUnitRunes > defaultMergeMaxUnitRunes {
		config.MaxUnitRunes = defaultMergeMaxUnitRunes
	}
	if config.MaxUnitDurationMs <= 0 || config.MaxUnitDurationMs > defaultMergeMaxUnitDurationMs {
		config.MaxUnitDurationMs = defaultMergeMaxUnitDurationMs
	}
	return config
}

type rawParagraph struct {
	ID        string
	SpeakerID string
	Sentences []rawSentence
}

type rawSentence struct {
	ID        string
	Text      string
	SpeakerID string
	StartMs   int
	EndMs     int
}

type mpsEnvelope struct {
	Segments []struct {
		SourceSegmentID string  `json:"source_segment_id"`
		Text            string  `json:"text"`
		StartMs         int     `json:"start_ms"`
		EndMs           int     `json:"end_ms"`
		SpeakerID       string  `json:"speaker_id"`
		StartSec        float64 `json:"start_sec"`
		EndSec          float64 `json:"end_sec"`
		Speaker         string  `json:"speaker"`
	} `json:"segments"`
	MPSResult *struct {
		Segments []struct {
			SourceSegmentID string  `json:"source_segment_id"`
			Text            string  `json:"text"`
			StartMs         int     `json:"start_ms"`
			EndMs           int     `json:"end_ms"`
			SpeakerID       string  `json:"speaker_id"`
			StartSec        float64 `json:"start_sec"`
			EndSec          float64 `json:"end_sec"`
			Speaker         string  `json:"speaker"`
		} `json:"segments"`
	} `json:"mps_result"`
}

func parseMPS(payload []byte) ([]rawParagraph, error) {
	var envelope mpsEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode MPS transcript: %w", err)
	}
	segments := envelope.Segments
	if len(segments) == 0 && envelope.MPSResult != nil {
		segments = envelope.MPSResult.Segments
	}
	paragraphs := make([]rawParagraph, 0, len(segments))
	for index, segment := range segments {
		id := strings.TrimSpace(segment.SourceSegmentID)
		if id == "" {
			id = fmt.Sprintf("mps-segment-%06d", index+1)
		}
		startMs, endMs := segment.StartMs, segment.EndMs
		if endMs <= startMs && segment.EndSec > segment.StartSec {
			startMs = secondsToMilliseconds(segment.StartSec)
			endMs = secondsToMilliseconds(segment.EndSec)
		}
		speaker := adapterFirstNonEmpty(segment.SpeakerID, segment.Speaker)
		paragraphs = append(paragraphs, rawParagraph{ID: id, SpeakerID: speaker, Sentences: []rawSentence{{
			ID: id, Text: segment.Text, SpeakerID: speaker, StartMs: startMs, EndMs: endMs,
		}}})
	}
	return paragraphs, nil
}

func secondsToMilliseconds(seconds float64) int {
	if seconds <= 0 {
		return 0
	}
	return int(math.Round(seconds * 1000))
}

type normalizedTranscript struct {
	Transcripts []struct {
		Paragraphs []normalizedParagraph `json:"paragraphs"`
	} `json:"transcripts"`
	Paragraphs []normalizedParagraph `json:"paragraphs"`
}

type normalizedParagraph struct {
	ID        string               `json:"paragraph_id"`
	SpeakerID string               `json:"speaker_id"`
	Sentences []normalizedSentence `json:"sentences"`
}

type normalizedSentence struct {
	ID        string `json:"sentence_id"`
	Text      string `json:"text"`
	StartMs   int    `json:"start_ms"`
	EndMs     int    `json:"end_ms"`
	SpeakerID string `json:"speaker_id"`
}

// rawTingwu supports the downloaded result returned by Tongyi as well as the
// already-normalized payload used by subtitle_generate.
type rawTingwu struct {
	Transcription struct {
		Paragraphs []struct {
			ID        string `json:"ParagraphId"`
			SpeakerID string `json:"SpeakerId"`
			Words     []struct {
				SentenceID json.Number `json:"SentenceId"`
				Start      int         `json:"Start"`
				End        int         `json:"End"`
				Text       string      `json:"Text"`
			} `json:"Words"`
		} `json:"Paragraphs"`
	} `json:"Transcription"`
}

func parseTingwu(payload []byte) ([]rawParagraph, error) {
	var normalized normalizedTranscript
	if err := json.Unmarshal(payload, &normalized); err == nil && (len(normalized.Transcripts) > 0 || len(normalized.Paragraphs) > 0) {
		paragraphs := make([]rawParagraph, 0)
		for _, file := range normalized.Transcripts {
			for _, paragraph := range file.Paragraphs {
				converted := rawParagraph{ID: paragraph.ID, SpeakerID: paragraph.SpeakerID}
				for _, sentence := range paragraph.Sentences {
					converted.Sentences = append(converted.Sentences, rawSentence{ID: sentence.ID, Text: sentence.Text, SpeakerID: sentence.SpeakerID, StartMs: sentence.StartMs, EndMs: sentence.EndMs})
				}
				paragraphs = append(paragraphs, converted)
			}
		}
		for _, paragraph := range normalized.Paragraphs {
			converted := rawParagraph{ID: paragraph.ID, SpeakerID: paragraph.SpeakerID}
			for _, sentence := range paragraph.Sentences {
				converted.Sentences = append(converted.Sentences, rawSentence{ID: sentence.ID, Text: sentence.Text, SpeakerID: sentence.SpeakerID, StartMs: sentence.StartMs, EndMs: sentence.EndMs})
			}
			paragraphs = append(paragraphs, converted)
		}
		return paragraphs, nil
	}

	var raw rawTingwu
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("decode Tingwu transcript: %w", err)
	}
	paragraphs := make([]rawParagraph, 0, len(raw.Transcription.Paragraphs))
	for index, paragraph := range raw.Transcription.Paragraphs {
		id := strings.TrimSpace(paragraph.ID)
		if id == "" {
			id = fmt.Sprintf("tingwu-paragraph-%06d", index+1)
		}
		converted := rawParagraph{ID: id, SpeakerID: paragraph.SpeakerID}
		for _, word := range paragraph.Words {
			sentenceID := word.SentenceID.String()
			if strings.TrimSpace(sentenceID) == "" {
				sentenceID = fmt.Sprintf("%s-word-%06d", id, len(converted.Sentences)+1)
			}
			if len(converted.Sentences) > 0 && converted.Sentences[len(converted.Sentences)-1].ID == sentenceID {
				last := &converted.Sentences[len(converted.Sentences)-1]
				last.Text += word.Text
				last.EndMs = word.End
				continue
			}
			converted.Sentences = append(converted.Sentences, rawSentence{ID: sentenceID, Text: word.Text, SpeakerID: paragraph.SpeakerID, StartMs: word.Start, EndMs: word.End})
		}
		paragraphs = append(paragraphs, converted)
	}
	return paragraphs, nil
}

func buildInputChapters(input RawInput, provider string, paragraphs []rawParagraph) ([]InputChapter, error) {
	chapter := InputChapter{Index: 0, Title: input.Title}
	seenSource := make(map[string]int)
	seenParagraph := make(map[string]int)
	usedParagraph := make(map[string]struct{})
	usedSource := make(map[string]struct{})
	ordinal := 0
	for paragraphIndex, source := range paragraphs {
		paragraphID := strings.TrimSpace(source.ID)
		if paragraphID == "" {
			paragraphID = fmt.Sprintf("%s-paragraph-%06d", provider, paragraphIndex+1)
		}
		if _, exists := usedParagraph[paragraphID]; exists {
			for occurrence := seenParagraph[paragraphID] + 1; ; occurrence++ {
				candidate := fmt.Sprintf("%s:%d", paragraphID, occurrence)
				if _, candidateExists := usedParagraph[candidate]; !candidateExists {
					paragraphID = candidate
					break
				}
			}
		}
		seenParagraph[strings.TrimSpace(source.ID)]++
		usedParagraph[paragraphID] = struct{}{}
		converted := InputParagraph{ParagraphID: paragraphID, Index: len(chapter.Paragraphs), SpeakerID: strings.TrimSpace(source.SpeakerID)}
		for sentenceIndex, sourceSentence := range source.Sentences {
			text := strings.TrimSpace(sourceSentence.Text)
			if text == "" {
				continue
			}
			sourceID := strings.TrimSpace(sourceSentence.ID)
			if sourceID == "" {
				sourceID = fmt.Sprintf("%s-sentence-%06d", provider, ordinal+1)
			}
			// Tingwu sentence numbers can restart in every paragraph. Keep the
			// provider ID visible while making the document mapping one-to-one.
			if _, exists := usedSource[sourceID]; exists {
				base := sourceID
				prefix := paragraphID
				for occurrence := seenSource[base] + 1; ; occurrence++ {
					candidate := fmt.Sprintf("%s:%s:%d", prefix, base, occurrence)
					if _, candidateExists := usedSource[candidate]; !candidateExists {
						sourceID = candidate
						break
					}
				}
			}
			seenSource[strings.TrimSpace(sourceSentence.ID)]++
			usedSource[sourceID] = struct{}{}
			evidenceSentence, err := evidence.BuildSentence(evidence.Input{
				VideoID: input.VideoID, TranscriptGeneration: input.TranscriptGeneration,
				Ordinal: ordinal, SourceSentenceID: sourceID, Text: text,
				SpeakerID: adapterFirstNonEmpty(sourceSentence.SpeakerID, source.SpeakerID),
				StartMs:   sourceSentence.StartMs, EndMs: sourceSentence.EndMs,
			})
			if err != nil {
				return nil, fmt.Errorf("map %s paragraph %d sentence %d: %w", provider, paragraphIndex, sentenceIndex, err)
			}
			converted.Sentences = append(converted.Sentences, InputSentence{
				SourceSentenceID: sourceID, EvidenceSentenceID: evidenceSentence.ID, Text: text,
				SpeakerID: evidenceSentence.SpeakerID, StartMs: sourceSentence.StartMs, EndMs: sourceSentence.EndMs,
			})
			ordinal++
		}
		if len(converted.Sentences) > 0 {
			chapter.Paragraphs = append(chapter.Paragraphs, converted)
		}
	}
	if len(chapter.Paragraphs) == 0 {
		return nil, fmt.Errorf("transcript payload contains no non-empty timed sentences")
	}
	return []InputChapter{chapter}, nil
}

// mergeShortParagraphs flattens provider paragraphs into an ordered stream,
// then rebuilds speaker-consistent paragraphs. Crossing a provider paragraph
// boundary is allowed only when the effective speaker and timing prove that
// the two pieces are one continuous utterance.
func mergeShortParagraphs(paragraphs []rawParagraph, config MergeConfig) []rawParagraph {
	config = config.withDefaults()
	merged := make([]rawParagraph, 0, len(paragraphs))
	for _, source := range paragraphs {
		paragraphID := strings.TrimSpace(source.ID)
		paragraphSpeaker := strings.TrimSpace(source.SpeakerID)
		for _, sourceSentence := range source.Sentences {
			if strings.TrimSpace(sourceSentence.Text) == "" {
				continue
			}
			sentence := sourceSentence
			sentence.Text = strings.TrimSpace(sentence.Text)
			sentence.SpeakerID = adapterFirstNonEmpty(sentence.SpeakerID, paragraphSpeaker)
			candidate := rawParagraph{
				ID:        paragraphID,
				SpeakerID: sentence.SpeakerID,
				Sentences: []rawSentence{sentence},
			}
			if len(merged) > 0 && canMergeShortSubtitle(merged[len(merged)-1], candidate, config) {
				last := &merged[len(merged)-1]
				last.Sentences = append(last.Sentences, sentence)
				continue
			}
			merged = append(merged, candidate)
		}
	}
	return merged
}

func canMergeShortSubtitle(current, next rawParagraph, config MergeConfig) bool {
	if len(current.Sentences) == 0 || len(next.Sentences) != 1 {
		return false
	}
	if strings.TrimSpace(current.SpeakerID) != strings.TrimSpace(next.SpeakerID) {
		return false
	}
	if strings.TrimSpace(current.SpeakerID) == "" {
		return false
	}
	last := current.Sentences[len(current.Sentences)-1]
	upcoming := next.Sentences[0]
	gapMs := upcoming.StartMs - last.EndMs
	if gapMs < 0 || gapMs > config.MaxGapMs {
		return false
	}
	if upcoming.EndMs <= current.Sentences[0].StartMs || upcoming.EndMs-current.Sentences[0].StartMs > config.MaxUnitDurationMs {
		return false
	}
	currentText := joinTranscriptText(rawSentenceTexts(current.Sentences))
	combinedText := joinTranscriptText(append(rawSentenceTexts(current.Sentences), upcoming.Text))
	if utf8.RuneCountInString(combinedText) > config.MaxUnitRunes {
		return false
	}
	if hasTerminalPunctuation(last.Text) {
		return false
	}
	if utf8.RuneCountInString(upcoming.Text) > config.ShortMaxRunes && !hasContinuationPunctuation(last.Text) {
		return false
	}
	if utf8.RuneCountInString(currentText) > config.ShortMaxRunes && !hasContinuationPunctuation(last.Text) {
		return false
	}
	return true
}

func rawSentenceTexts(sentences []rawSentence) []string {
	texts := make([]string, 0, len(sentences))
	for _, sentence := range sentences {
		texts = append(texts, sentence.Text)
	}
	return texts
}

func hasTerminalPunctuation(text string) bool {
	for _, character := range reverseMeaningfulRunes(text) {
		switch character {
		case '"', '\'', '\u201d', '\u2019', '\u300d', '\u300f', '\u300b', '\u3009', '\u3005', ')', ']', '}', '\uff09', '\u3011', '\u3015', '\u3017', '\u3019', '\u301b':
			continue
		case '.', '。', '!', '！', '?', '？', ';', '；', '…', '‥':
			return true
		default:
			return false
		}
	}
	return false
}

func hasContinuationPunctuation(text string) bool {
	for _, character := range reverseMeaningfulRunes(text) {
		switch character {
		case ',', '，', '\u3001', ':', '：', '-', '\uff0d', '\u2014':
			return true
		default:
			return false
		}
	}
	return false
}

func reverseMeaningfulRunes(text string) []rune {
	runes := []rune(strings.TrimSpace(text))
	for len(runes) > 0 {
		last := runes[len(runes)-1]
		switch last {
		case '"', '\'', '\u201d', '\u2019', '\u300d', '\u300f', '\u300b', '\u3009', ')', ']', '}', '\uff09', '\u3011', '\u3015', '\u3017', '\u3019', '\u301b':
			runes = runes[:len(runes)-1]
		default:
			return runes[len(runes)-1:]
		}
	}
	return nil
}

func normalizeTranscriptProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "mps", "tencent_mps", "tencent-mps":
		return "mps"
	case "tingwu", "aliyun_tingwu", "aliyun-tingwu", "aliyun":
		return "tingwu"
	default:
		return ""
	}
}

func adapterFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
