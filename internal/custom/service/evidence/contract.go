// Package evidence defines the immutable sentence-level evidence contract.
// A sentence belongs to exactly one video transcript generation. Its ID is a
// deterministic digest of the source mapping, so retries can rebuild it while
// a re-transcription can never silently reuse the previous generation's ID.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const contractVersion = "v1"

// Input is the minimum source mapping needed to freeze one evidence sentence.
type Input struct {
	VideoID              string
	TranscriptGeneration string
	Ordinal              int
	SourceSentenceID     string
	Text                 string
	SpeakerID            string
	StartMs              int
	EndMs                int
}

// Sentence is the immutable mapping used by chapters, summaries and Wiki
// source references. Text is retained here as an audit value; canonical video
// content remains stored in WeKnora.
type Sentence struct {
	ID                   string `json:"evidence_sentence_id"`
	VideoID              string `json:"video_id"`
	TranscriptGeneration string `json:"transcript_generation"`
	Ordinal              int    `json:"ordinal"`
	SourceSentenceID     string `json:"source_sentence_id"`
	Text                 string `json:"text"`
	SpeakerID            string `json:"speaker_id,omitempty"`
	StartMs              int    `json:"start_ms"`
	EndMs                int    `json:"end_ms"`
	Digest               string `json:"-"`
}

// BuildSentence creates the stable ID and validates the source mapping.
func BuildSentence(input Input) (Sentence, error) {
	input.VideoID = strings.TrimSpace(input.VideoID)
	input.TranscriptGeneration = strings.TrimSpace(input.TranscriptGeneration)
	input.SourceSentenceID = strings.TrimSpace(input.SourceSentenceID)
	input.Text = strings.TrimSpace(input.Text)
	input.SpeakerID = strings.TrimSpace(input.SpeakerID)
	if input.VideoID == "" {
		return Sentence{}, fmt.Errorf("video id is required")
	}
	if input.TranscriptGeneration == "" {
		return Sentence{}, fmt.Errorf("transcript generation is required")
	}
	if input.Ordinal < 0 {
		return Sentence{}, fmt.Errorf("sentence ordinal must not be negative")
	}
	if input.Text == "" {
		return Sentence{}, fmt.Errorf("sentence text is required")
	}
	if input.StartMs < 0 || input.EndMs <= input.StartMs {
		return Sentence{}, fmt.Errorf("sentence time range is invalid: start_ms=%d end_ms=%d", input.StartMs, input.EndMs)
	}

	// The ordinal disambiguates repeated provider IDs and identical utterances.
	// Length-prefixed components avoid collisions caused by separators in text.
	canonical := strings.Join([]string{
		contractVersion,
		lengthPrefixed(input.VideoID),
		lengthPrefixed(input.TranscriptGeneration),
		strconv.Itoa(input.Ordinal),
		lengthPrefixed(input.SourceSentenceID),
		lengthPrefixed(input.Text),
		lengthPrefixed(input.SpeakerID),
		strconv.Itoa(input.StartMs),
		strconv.Itoa(input.EndMs),
	}, "|")
	digestBytes := sha256.Sum256([]byte(canonical))
	digest := hex.EncodeToString(digestBytes[:])
	return Sentence{
		ID:                   "evs:" + contractVersion + ":" + digest,
		VideoID:              input.VideoID,
		TranscriptGeneration: input.TranscriptGeneration,
		Ordinal:              input.Ordinal,
		SourceSentenceID:     input.SourceSentenceID,
		Text:                 input.Text,
		SpeakerID:            input.SpeakerID,
		StartMs:              input.StartMs,
		EndMs:                input.EndMs,
		Digest:               digest,
	}, nil
}

// BuildEvidenceSentenceID is the scalar form used by callers that only need
// the immutable reference and not the complete audit mapping.
func BuildEvidenceSentenceID(input Input) (string, error) {
	sentence, err := BuildSentence(input)
	if err != nil {
		return "", err
	}
	return sentence.ID, nil
}

func lengthPrefixed(value string) string {
	return strconv.Itoa(len(value)) + ":" + value
}

// ValidateSentence verifies both the source fields and the derived ID.
func ValidateSentence(sentence Sentence) error {
	expected, err := BuildSentence(Input{
		VideoID:              sentence.VideoID,
		TranscriptGeneration: sentence.TranscriptGeneration,
		Ordinal:              sentence.Ordinal,
		SourceSentenceID:     sentence.SourceSentenceID,
		Text:                 sentence.Text,
		SpeakerID:            sentence.SpeakerID,
		StartMs:              sentence.StartMs,
		EndMs:                sentence.EndMs,
	})
	if err != nil {
		return err
	}
	if strings.TrimSpace(sentence.ID) == "" || sentence.ID != expected.ID {
		return fmt.Errorf("evidence sentence ID does not match its immutable mapping")
	}
	return nil
}

// ValidateManifest checks that all sentences belong to one current generation
// and form an ordered, one-to-one mapping with no duplicate IDs or source IDs.
func ValidateManifest(sentences []Sentence, videoID, generation string) error {
	videoID = strings.TrimSpace(videoID)
	generation = strings.TrimSpace(generation)
	if videoID == "" || generation == "" {
		return fmt.Errorf("video id and transcript generation are required")
	}
	if len(sentences) == 0 {
		return fmt.Errorf("evidence sentence manifest is empty")
	}
	seenIDs := make(map[string]struct{}, len(sentences))
	seenSourceIDs := make(map[string]struct{}, len(sentences))
	for index, sentence := range sentences {
		if sentence.Ordinal != index {
			return fmt.Errorf("evidence sentence ordinal is not contiguous: expected=%d actual=%d", index, sentence.Ordinal)
		}
		if sentence.VideoID != videoID || sentence.TranscriptGeneration != generation {
			return fmt.Errorf("evidence sentence %d belongs to another video or transcript generation", index)
		}
		if err := ValidateSentence(sentence); err != nil {
			return fmt.Errorf("evidence sentence %d is invalid: %w", index, err)
		}
		if _, exists := seenIDs[sentence.ID]; exists {
			return fmt.Errorf("duplicate evidence sentence ID %q", sentence.ID)
		}
		seenIDs[sentence.ID] = struct{}{}
		if sourceID := strings.TrimSpace(sentence.SourceSentenceID); sourceID != "" {
			if _, exists := seenSourceIDs[sourceID]; exists {
				return fmt.Errorf("duplicate source sentence ID %q", sourceID)
			}
			seenSourceIDs[sourceID] = struct{}{}
		}
	}
	return nil
}
