package catalog

import "github.com/Tencent/WeKnora/internal/models/api"

// Where a transcription protocol puts the operator's language hint. Each
// vendor documents one place, or none.
const (
	// LanguageForm is a multipart field named language (OpenAI's shape,
	// ISO-639-1).
	LanguageForm = "form"
	// LanguageHeader is an HTTP request header named language (MiniMax,
	// BCP-47).
	LanguageHeader = "header"
	// LanguageASROptions is asr_options.language in a chat-served request
	// body (Alibaba, Xiaomi).
	LanguageASROptions = "asr_options"
)

// TranscriptionsCompat is the overlay form (every field optional) of the
// speech-to-text settings. JSON keys are the names used in models.json.
type TranscriptionsCompat struct {
	API  *api.TranscriptionAPI `json:"api,omitempty"`
	Path *string               `json:"path,omitempty"`
	// ResponseFormat is sent as response_format when non-empty. OpenAI's
	// gpt-4o-transcribe and gpt-4o-mini-transcribe accept only json, which is
	// also the default, so most models leave it unset.
	ResponseFormat *string `json:"response_format,omitempty"`
	// LanguageParam names where the language hint goes: form, header or
	// asr_options; empty sends none.
	LanguageParam *string `json:"language_param,omitempty"`
	// MaxFileBytes is the documented upload ceiling for the audio itself.
	MaxFileBytes *int `json:"max_file_bytes,omitempty"`
	// MaxEncodedBytes is the documented ceiling on the base64 data URI, for
	// the protocols that send the audio that way.
	MaxEncodedBytes *int `json:"max_encoded_bytes,omitempty"`
	// Formats lists the file extensions the vendor documents, without the
	// dot. Empty means the vendor documents no closed list.
	Formats        []string `json:"formats,omitempty"`
	RequestTimeout *int     `json:"request_timeout_seconds,omitempty"`
	// UnsupportedReason marks a model this build cannot call.
	UnsupportedReason *string `json:"unsupported_reason,omitempty"`
}

// TranscriptionsSettings is the resolved (fully defaulted) form.
type TranscriptionsSettings struct {
	API            api.TranscriptionAPI
	Path           string
	ResponseFormat string
	LanguageParam  string
	// MaxFileBytes and MaxEncodedBytes refuse an upload the vendor would
	// reject anyway, before sending it; 0 leaves the check to the vendor.
	MaxFileBytes    int
	MaxEncodedBytes int
	Formats         []string
	// RequestTimeout caps one request, in seconds.
	RequestTimeout int
	// UnsupportedReason is set where a vendor's recognition models, or some
	// of them, cannot be called with the audio in the request — they take a
	// public URL or run as asynchronous tasks. Resolve refuses such a model
	// rather than sending it a shape it does not accept.
	UnsupportedReason string
}

// DefaultTranscriptions is the protocol baseline: a form with file and model
// and nothing else, which every speech-to-text endpoint in this catalog
// documents. The deadline is the one the pre-catalog client carried; audio
// transcription is slow.
func DefaultTranscriptions() TranscriptionsSettings {
	return TranscriptionsSettings{Path: "/audio/transcriptions", RequestTimeout: 300}
}
