package catalog

import "github.com/Tencent/WeKnora/internal/models/api"

// TranscriptionsCompat is the overlay form (every field optional) of the
// speech-to-text settings. JSON keys are the names used in models.json.
type TranscriptionsCompat struct {
	API  *api.TranscriptionAPI `json:"api,omitempty"`
	Path *string               `json:"path,omitempty"`
	// ResponseFormat is sent as response_format when non-empty. OpenAI's
	// gpt-4o-transcribe and gpt-4o-mini-transcribe accept only json, which is
	// also the default, so most models leave it unset.
	ResponseFormat *string `json:"response_format,omitempty"`
	// MaxFileBytes is the documented upload ceiling.
	MaxFileBytes   *int `json:"max_file_bytes,omitempty"`
	RequestTimeout *int `json:"request_timeout_seconds,omitempty"`
}

// TranscriptionsSettings is the resolved (fully defaulted) form.
type TranscriptionsSettings struct {
	API            api.TranscriptionAPI
	Path           string
	ResponseFormat string
	// MaxFileBytes refuses an upload the vendor would reject anyway, before
	// sending it; 0 leaves the check to the vendor.
	MaxFileBytes int
	// RequestTimeout caps one request, in seconds.
	RequestTimeout int
}

// DefaultTranscriptions is the protocol baseline: a form with file and model
// and nothing else, which every speech-to-text endpoint in this catalog
// documents. The deadline is the one the pre-catalog client carried; audio
// transcription is slow.
func DefaultTranscriptions() TranscriptionsSettings {
	return TranscriptionsSettings{Path: "/audio/transcriptions", RequestTimeout: 300}
}
