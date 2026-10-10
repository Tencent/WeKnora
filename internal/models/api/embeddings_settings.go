package api

// EmbeddingsCompat is the overlay form (every field optional) of the
// embedding settings. JSON keys are the documented names used in models.json
// and config/models.json.
type EmbeddingsCompat struct {
	// API overrides the vendor's embedding protocol for one model. Aliyun and
	// Volcengine each serve two: a text endpoint in the OpenAI shape and a
	// multimodal one of their own, chosen by which model the row names.
	API                *EmbeddingAPI     `json:"api,omitempty"`
	Path               *string           `json:"path,omitempty"`
	SendEncodingFormat *bool             `json:"send_encoding_format,omitempty"`
	DimensionsField    *string           `json:"dimensions_field,omitempty"`
	TruncateField      *string           `json:"truncate_field,omitempty"`
	TruncateValue      *string           `json:"truncate_value,omitempty"`
	InputTypeField     *string           `json:"input_type_field,omitempty"`
	InputTypeValues    map[string]string `json:"input_type_values,omitempty"`
	MaxBatchSize       *int              `json:"max_batch_size,omitempty"`
	MaxInputChars      *int              `json:"max_input_chars,omitempty"`
	// AcceptsTruncatePromptTokens marks a vLLM-class runtime, the only kind
	// that implements the `truncate_prompt_tokens` extension.
	AcceptsTruncatePromptTokens *bool                 `json:"accepts_truncate_prompt_tokens,omitempty"`
	RequestTimeout              *int                  `json:"request_timeout_seconds,omitempty"`
	ExtraBody                   map[string]any        `json:"extra_body,omitempty"`
	ImageField                  *string               `json:"image_field,omitempty"`
	MaxImageBatchSize           *int                  `json:"max_image_batch_size,omitempty"`
	MaxImageBytes               *int                  `json:"max_image_bytes,omitempty"`
	ImageMIMETypes              []string              `json:"image_mime_types,omitempty"`
	ImageFormat                 *EmbeddingImageFormat `json:"image_format,omitempty"`
	ImagePrompt                 *string               `json:"image_prompt,omitempty"`
	TextAsMessages              *bool                 `json:"text_as_messages,omitempty"`
}

// EmbeddingImageFormat is how an image travels on the OpenAI embedding
// shape, which has no standard image input of its own.
type EmbeddingImageFormat string

const (
	// EmbeddingImageObject puts {ImageField: data URI} in `input`, as Jina
	// and SGLang document.
	EmbeddingImageObject EmbeddingImageFormat = "object"
	// EmbeddingImageMessages sends a chat conversation in `messages` instead
	// of `input`, which is how vLLM serves multimodal embedding models
	// (https://docs.vllm.ai/en/latest/models/pooling_models.html). One
	// conversation yields one vector, so every image is its own request.
	EmbeddingImageMessages EmbeddingImageFormat = "messages"
)

// Known reports whether f is a format this build can send; empty is known.
func (f EmbeddingImageFormat) Known() bool {
	return f == "" || f == EmbeddingImageObject || f == EmbeddingImageMessages
}

// EmbeddingsSettings is the resolved (fully defaulted) form.
type EmbeddingsSettings struct {
	// API is the protocol this model speaks, which may differ from the
	// vendor's default.
	API EmbeddingAPI
	// Path is appended to the base URL; vendors whose default base URL names
	// the full endpoint leave it empty.
	Path string
	// SendEncodingFormat sends encoding_format: "float". OpenAI documents it;
	// several compatible gateways do not list it at all.
	SendEncodingFormat bool
	// DimensionsField carries the requested vector width: "dimensions" on the
	// OpenAI shape, "dimension" on DashScope (nested under parameters),
	// "outputDimensionality" on Gemini, and empty where the vendor does not
	// let a request choose. It is sent only when the row opted in with
	// supports_dimension_override; the field name is the vendor's fact, the
	// decision to narrow the vector is the operator's.
	DimensionsField string
	// TruncateField and TruncateValue are the vendor's server-side truncation
	// switch for over-long input: a boolean on Jina, an enum on NIM. Empty
	// sends nothing.
	TruncateField string
	TruncateValue string
	// InputTypeField names the parameter that distinguishes an indexed
	// document from a search query, and InputTypeValues maps the neutral
	// EmbedInputType values onto the vendor's vocabulary. Asymmetric
	// models score the two sides differently and are measurably worse when
	// both are embedded the same way.
	InputTypeField  string
	InputTypeValues map[string]string
	// MaxBatchSize and MaxInputChars are the documented per-request ceilings.
	MaxBatchSize  int
	MaxInputChars int
	// AcceptsTruncatePromptTokens reports a vLLM-class runtime. The extension
	// appears in no managed vendor's schema, so it must never be sent to one.
	AcceptsTruncatePromptTokens bool
	// TruncatePromptTokens is the budget the operator opted into on this row,
	// honoured only where the vendor accepts the extension.
	TruncatePromptTokens int
	// RequestTimeout caps one request, in seconds. 0 leaves the client
	// without its own deadline and lets the caller's context govern.
	RequestTimeout int
	ExtraBody      map[string]any
	// ImageInput is how this endpoint takes images: on the OpenAI shape, the
	// key of the input object that carries one ("image" on Jina; that shape
	// has no standard image input, so an endpoint that names none takes no
	// images), plus the documented per-request and per-image limits.
	ImageInput
	// ImageFormat chooses between ImageField's object in `input` and a chat
	// conversation in `messages`. Empty means object when ImageField is set.
	ImageFormat EmbeddingImageFormat `json:",omitempty"`
	// ImagePrompt is the text sent beside each image in the messages format;
	// some chat templates (VLM2Vec's) expect an instruction there. Empty
	// sends the image alone.
	ImagePrompt string `json:",omitempty"`
	// TextAsMessages sends texts in the messages format too, one per
	// request. A model whose chat template frames its inputs only places a
	// text and an image in the same space when both pass through it; a
	// plain `input` string skips the template.
	TextAsMessages bool `json:",omitempty"`
}

// OpenAIImageFormat is the effective image format on the OpenAI shape, empty
// when the endpoint takes no images there.
func (s EmbeddingsSettings) OpenAIImageFormat() EmbeddingImageFormat {
	switch {
	case s.ImageFormat == EmbeddingImageMessages:
		return EmbeddingImageMessages
	case s.ImageField != "":
		return EmbeddingImageObject
	}
	return ""
}

// BatchLimits renders the documented ceilings for SplitBatches.
func (s EmbeddingsSettings) BatchLimits() BatchLimits {
	return BatchLimits{MaxItems: s.MaxBatchSize, MaxItemRunes: s.MaxInputChars}
}

// InputTypeValue maps a neutral input kind onto the vendor's vocabulary,
// returning "" when this vendor does not distinguish the two sides.
func (s EmbeddingsSettings) InputTypeValue(kind EmbedInputType) string {
	if s.InputTypeField == "" {
		return ""
	}
	if v, ok := s.InputTypeValues[string(kind)]; ok {
		return v
	}
	return string(kind)
}

// DefaultEmbeddings is the protocol baseline, and it is deliberately bare:
// `model` and `input` are the only two fields every vendor in this catalog
// documents. The rest disagree — NVIDIA NIM and Volcengine's text endpoint
// have no `dimensions` at all, Zhipu has no `encoding_format`, and the
// parameter that separates a query from a document is spelled three
// different ways. A vendor that documents one declares it; nothing is sent
// on the assumption that an OpenAI-shaped endpoint accepts every OpenAI
// field.
//
// The one non-wire default is the request deadline: every pre-catalog
// embedding client carried 60 seconds.
func DefaultEmbeddings() EmbeddingsSettings {
	return EmbeddingsSettings{RequestTimeout: 60}
}
