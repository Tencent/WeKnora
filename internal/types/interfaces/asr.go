package interfaces

import "context"

// ASR defines the interface for Automatic Speech Recognition model
// operations (P3 port of the v1 internal/models/asr.ASR — every vendor rides
// the OpenAI-compatible transcriptions shape through invoke.Transcribe).
type ASR interface {
	// Transcribe sends audio bytes to the ASR model and returns the
	// transcribed text and segments.
	Transcribe(ctx context.Context, audioBytes []byte, fileName string) (*TranscriptionResult, error)

	GetModelName() string
	GetModelID() string
}

// Segment represents a transcribed segment with timestamps.
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// TranscriptionResult holds the full text and its segments.
type TranscriptionResult struct {
	Text     string    `json:"text"`
	Segments []Segment `json:"segments,omitempty"`
}
