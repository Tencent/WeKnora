package api

// RerankCompat is the overlay form (every field optional) of the rerank
// settings. JSON keys are the documented names used in models.json and
// config/models.json.
type RerankCompat struct {
	Path             *string     `json:"path,omitempty"`
	SendTopN         *bool       `json:"send_top_n,omitempty"`
	SendReturnDocs   *bool       `json:"send_return_documents,omitempty"`
	ScoreScale       *ScoreScale `json:"score_scale,omitempty"`
	Truncate         *string     `json:"truncate,omitempty"`
	MaxDocuments     *int        `json:"max_documents,omitempty"`
	MaxQueryChars    *int        `json:"max_query_chars,omitempty"`
	MaxDocumentChars *int        `json:"max_document_chars,omitempty"`
	MaxRequestChars  *int        `json:"max_request_chars,omitempty"`
	MaxConcurrency   *int        `json:"max_concurrency,omitempty"`
	RequestTimeout   *int        `json:"request_timeout_seconds,omitempty"`
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
	// ScoreScale says what the returned numbers mean. See ScoreScale:
	// getting this wrong makes a relevance threshold meaningless.
	ScoreScale ScoreScale
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
	// RequestTimeout caps one request, in seconds. 0 uses the rerank
	// client's default (see rerank.newRerankHTTPClient).
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

// DefaultRerankMaxDocuments bounds one rerank request for a vendor that
// documents no per-request ceiling of its own.
//
// Splitting only on what the catalog states is not enough: most rerank vendors
// state nothing, and "no ceiling declared" used to mean "put the whole
// candidate set in one request", which is how an undocumented per-request
// limit becomes an HTTP 400 and a silently unranked retrieval (#3559).
//
// The vendors in that state publish no number to declare here. SiliconFlow's
// rate-limit page documents RPM/TPM for its rerankers, not a request size
// (https://docs.siliconflow.com/cn/userguide/rate-limits/rate-limit-and-upgradation),
// and its request schema states only minimums; Jina's contract bounds a single
// document by the model's token limit; Novita's schema has no such field;
// OpenRouter, LiteLLM, GPUStack and generic pass a request through to whatever
// serves it. Speed belongs to internal/models/limiter, and a token budget
// cannot be declared here because these ceilings are counted in runes.
//
// 60 is the smallest ceiling this catalog does document (lkeap's), so a vendor
// whose ceiling is unknown is never asked for more documents in one request
// than a vendor we do have documentation for accepts. It bounds the request,
// it does not claim anything about the vendor: a vendor whose real ceiling is
// higher says so with max_documents, and the batches grow back to it. Rerank
// only — the embedding path has its own batch size (BATCH_EMBED_SIZE) and is
// left alone.
const DefaultRerankMaxDocuments = 60

// BatchLimits renders the ceilings for SplitBatches. A vendor that documents
// nothing still gets a bounded request instead of an unbounded one.
func (s RerankSettings) BatchLimits() BatchLimits {
	maxItems := s.MaxDocuments
	if maxItems <= 0 && s.MaxRequestChars <= 0 {
		// Nothing documented at all, so bound the request: one request
		// carrying the entire candidate set is the failure this guards. A
		// vendor that documented a request budget instead keeps it — the
		// default is not a second ceiling on top of theirs.
		maxItems = DefaultRerankMaxDocuments
	}
	return BatchLimits{
		MaxItems:      maxItems,
		MaxItemRunes:  s.MaxDocumentChars,
		MaxTotalRunes: s.MaxRequestChars,
	}
}

// DefaultRerank is the protocol baseline: ask for every document, expect a
// 0..1 relevance score, enforce no ceiling the vendor did not state
// (BatchLimits still bounds the request itself, see DefaultRerankMaxDocuments).
func DefaultRerank() RerankSettings {
	return RerankSettings{
		ScoreScale: ScoreProbability,
	}
}
