package api

import "context"

// TranscriptionAPI names a speech-to-text wire protocol. Like RerankAPI and
// EmbeddingAPI it is its own type, so no other modality's value validates.
type TranscriptionAPI string

// The transcription protocols WeKnora speaks.
const (
	// TranscriptionOpenAI is POST {base}/audio/transcriptions as a multipart
	// form carrying file and model, answering {text} — or {text, segments}
	// when verbose_json is asked for.
	TranscriptionOpenAI TranscriptionAPI = "openai-transcriptions"
)

// Known reports whether the value names a protocol this build implements.
func (a TranscriptionAPI) Known() bool {
	return a == TranscriptionOpenAI
}

// TranscriptionSegment is one timed stretch of a transcript.
type TranscriptionSegment struct {
	Start float64
	End   float64
	Text  string
}

// Transcription is what a protocol client returns.
type Transcription struct {
	Text     string
	Segments []TranscriptionSegment
}

// Transcriber turns one audio file into text.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, fileName string) (*Transcription, error)
}
