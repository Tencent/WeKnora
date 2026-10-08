package api

import (
	"fmt"
	"slices"
)

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
	AcceptsTruncatePromptTokens *bool          `json:"accepts_truncate_prompt_tokens,omitempty"`
	RequestTimeout              *int           `json:"request_timeout_seconds,omitempty"`
	ExtraBody                   map[string]any `json:"extra_body,omitempty"`
	ImageField                  *string        `json:"image_field,omitempty"`
	MaxImageBatchSize           *int           `json:"max_image_batch_size,omitempty"`
	MaxImageBytes               *int           `json:"max_image_bytes,omitempty"`
	ImageMIMETypes              []string       `json:"image_mime_types,omitempty"`
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
	// ImageField is the key of the input object that carries an image on
	// the OpenAI shape — "image" on Jina. That shape has no standard image
	// input, so an endpoint that does not name one takes no images. The
	// multimodal protocols have their image part fixed by their own schema.
	ImageField string `json:",omitempty"`
	// MaxImageBatchSize is the documented number of images per request.
	// 0 means one: a vendor that fuses its inputs, or does not say, must not
	// be sent several.
	MaxImageBatchSize int `json:",omitempty"`
	// MaxImageBytes is the documented size limit of one image, 0 when the
	// vendor states none. The caller shrinks an image to fit; the client
	// refuses one that does not rather than spend a request on it.
	MaxImageBytes int `json:",omitempty"`
	// ImageMIMETypes lists the formats the vendor documents, empty when it
	// takes any common one.
	ImageMIMETypes []string `json:",omitempty"`
}

// BatchLimits renders the documented ceilings for SplitBatches.
func (s EmbeddingsSettings) BatchLimits() BatchLimits {
	return BatchLimits{MaxItems: s.MaxBatchSize, MaxItemRunes: s.MaxInputChars}
}

// ImageBatchLimit is the number of images one request may carry.
func (s EmbeddingsSettings) ImageBatchLimit() int {
	if s.MaxImageBatchSize > 0 {
		return s.MaxImageBatchSize
	}
	return 1
}

// CheckImage reports an image the vendor documents it will not take.
func (s EmbeddingsSettings) CheckImage(img EmbedImage) error {
	if len(img.Data) == 0 {
		return fmt.Errorf("image is empty")
	}
	if s.MaxImageBytes > 0 && len(img.Data) > s.MaxImageBytes {
		return fmt.Errorf("image is %d bytes; the limit is %d", len(img.Data), s.MaxImageBytes)
	}
	if len(s.ImageMIMETypes) > 0 && !slices.Contains(s.ImageMIMETypes, img.MIMEType) {
		return fmt.Errorf("image type %q is not one of %v", img.MIMEType, s.ImageMIMETypes)
	}
	return nil
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
