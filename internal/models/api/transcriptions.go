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
	// TranscriptionChatAudio is a dedicated speech-recognition model behind
	// POST {base}/chat/completions: the audio goes in as a base64 data URI in
	// an input_audio content part and the transcript comes back as the
	// assistant message. Alibaba's qwen3-asr-flash and Xiaomi's mimo-v2.5-asr
	// take no instruction; the model only transcribes.
	TranscriptionChatAudio TranscriptionAPI = "openai-chat-audio"
)

// Known reports whether the value names a protocol this build implements.
func (a TranscriptionAPI) Known() bool {
	return a == TranscriptionOpenAI || a == TranscriptionChatAudio
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
	// Duration is the audio length in seconds when the reply states it —
	// OpenAI-shaped duration or usage.seconds — and 0 otherwise.
	Duration float64
}

// TranscriptionRequest is one file to transcribe.
type TranscriptionRequest struct {
	Audio    []byte
	FileName string
	// Language is the operator's hint, empty for auto-detection. Where it is
	// sent, and whether at all, is the vendor's declaration.
	Language string
}

// Transcriber turns one audio file into text.
type Transcriber interface {
	Transcribe(ctx context.Context, req TranscriptionRequest) (*Transcription, error)
}
