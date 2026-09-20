package catalog

import "github.com/Tencent/WeKnora/internal/models/api"

// RerankCompat is the overlay form (every field optional) of the rerank
// settings. JSON keys are the documented names used in models.json and
// config/models.json.
type RerankCompat struct {
	Path             *string         `json:"path,omitempty"`
	SendTopN         *bool           `json:"send_top_n,omitempty"`
	SendReturnDocs   *bool           `json:"send_return_documents,omitempty"`
	ScoreScale       *api.ScoreScale `json:"score_scale,omitempty"`
	Truncate         *string         `json:"truncate,omitempty"`
	MaxDocuments     *int            `json:"max_documents,omitempty"`
	MaxQueryChars    *int            `json:"max_query_chars,omitempty"`
	MaxDocumentChars *int            `json:"max_document_chars,omitempty"`
	MaxRequestChars  *int            `json:"max_request_chars,omitempty"`
	MaxConcurrency   *int            `json:"max_concurrency,omitempty"`
	RequestTimeout   *int            `json:"request_timeout_seconds,omitempty"`
	// AcceptsTruncatePromptTokens marks a vLLM-class runtime.
	AcceptsTruncatePromptTokens *bool `json:"accepts_truncate_prompt_tokens,omitempty"`
	// UnsupportedReason marks a model this build cannot call.
	UnsupportedReason *string        `json:"unsupported_reason,omitempty"`
	ExtraBody         map[string]any `json:"extra_body,omitempty"`
}

// RerankSettings is the resolved (fully defaulted) form.
type RerankSettings struct {
	// Path is appended to the base URL by protocols that use one. Vendors
	// whose default base URL already names the full endpoint leave it empty.
	Path string
	// SendTopN asks for every document back by naming their count. Omitting
	// it means "all", which is what every protocol here does by default.
	SendTopN bool
	// SendReturnDocs asks the vendor to echo the document text. Callers index
	// into their own slice, so this is off unless a vendor needs it.
	SendReturnDocs bool
	// ScoreScale says what the returned numbers mean. See api.ScoreScale:
	// getting this wrong makes a relevance threshold meaningless.
	ScoreScale api.ScoreScale
	// Truncate is the vendor's server-side truncation setting for
	// over-long input ("END" on NIM). Empty sends nothing.
	Truncate string
	// Limits carry the documented per-request ceilings. MaxQueryChars is
	// checked on its own rather than through BatchLimits: the query is not an
	// item to be split, so an over-long one cannot be made to fit by batching.
	MaxDocuments     int
	MaxQueryChars    int
	MaxDocumentChars int
	MaxRequestChars  int
	// MaxConcurrency bounds in-flight batches when the input is split. 0
	// means the caller's default.
	MaxConcurrency int
	// RequestTimeout caps one request, in seconds. 0 leaves the HTTP client
	// without its own deadline and lets the caller's context govern, which is
	// what every vendor but WeKnora Cloud has always done here.
	RequestTimeout int
	// AcceptsTruncatePromptTokens reports that this vendor is a vLLM-class
	// runtime, which is the only kind that implements the
	// `truncate_prompt_tokens` extension. It is not part of the Cohere shape
	// and appears in no managed vendor's schema, so it must never be sent to
	// one: that is how an undocumented field ends up on every request.
	AcceptsTruncatePromptTokens bool
	// UnsupportedReason is set on a catalog entry naming a rerank dialect no
	// protocol package implements. Resolve refuses it rather than letting a
	// request go out shaped for a different protocol, which surfaces as a
	// decode error far from its cause.
	UnsupportedReason string
	// TruncatePromptTokens is the budget the operator opted into on this row,
	// honoured only where AcceptsTruncatePromptTokens is set. It is a row
	// setting rather than a vendor fact because one runtime serves models
	// with different context windows. Zero sends nothing.
	TruncatePromptTokens int
	ExtraBody            map[string]any
}

// BatchLimits renders the documented ceilings for api.SplitBatches.
func (s RerankSettings) BatchLimits() api.BatchLimits {
	return api.BatchLimits{
		MaxItems:      s.MaxDocuments,
		MaxItemRunes:  s.MaxDocumentChars,
		MaxTotalRunes: s.MaxRequestChars,
	}
}

// DefaultRerank is the protocol baseline: ask for every document, expect a
// 0..1 relevance score, enforce no ceiling the vendor did not state.
func DefaultRerank() RerankSettings {
	return RerankSettings{
		ScoreScale: api.ScoreProbability,
	}
}
