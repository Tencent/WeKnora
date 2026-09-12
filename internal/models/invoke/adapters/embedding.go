package adapters

// embedding.go — P2 strangler: the embedding facet (design §6.2). This file
// holds the family machinery: the OpenAI-compatible fallback shape
// (openai/zhipu/nvidia + every dual-facet family vendor), its per-vendor spec
// table, and the composites. Bespoke vendor wire lives in each vendor's file
// — azure.go (deployment path), jina.go (truncate boolean), aliyun.go
// (DashScope native multimodal), volcengine.go (Ark native multimodal),
// ollama.go (/api/embed), gemini.go (batchEmbedContents), weknoracloud.go
// (HMAC).
//
// Cross-vendor v1 conventions preserved here:
//   - timeout: every v1 embedding client used a fixed 60s http.Client.Timeout,
//     so every Build sets Request.Timeout=60s (executor default is the chat
//     timeout — NOT the v1 embedding posture);
//   - dimensions wire param gated by SupportsDimensionOverride && Dimensions>0;
//   - truncate_prompt_tokens defaults to 511 at the v1 CONSTRUCTOR for the
//     vendors carrying the param, so the wire always shows 511 when the model
//     record left it zero;
//   - non-2xx never reaches Parse* (the executor classifies status+body into
//     the unified error model first).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

// embeddingRequestTimeout mirrors the v1 fixed per-client timeout.
const embeddingRequestTimeout = 60 * time.Second

// openAIEmbedRequest is the merged openai-family wire shape. Field order
// tracks the v1 per-vendor structs so golden request bodies stay
// byte-identical: openai {model,input,encoding_format,truncate_prompt_tokens,
// dimensions} ≡ zhipu (no encoding_format) ≡ nvidia (+input_type) ≡ azure
// (encoding_format+dimensions only). jina's {model,input,truncate,dimensions}
// order differs — its struct lives in jina.go.
type openAIEmbedRequest struct {
	Model                string   `json:"model"`
	Input                []string `json:"input"`
	EncodingFormat       string   `json:"encoding_format,omitempty"`
	Dimensions           int      `json:"dimensions,omitempty"`
	TruncatePromptTokens int      `json:"truncate_prompt_tokens,omitempty"`
	InputType            string   `json:"input_type,omitempty"`
}

// openAIEmbedResponse is the shared data[]-array response shape (openai,
// zhipu, nvidia, jina, azure).
type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// openaiEmbedSpec carries the openai-shape per-vendor deltas (v1 constructor
// differences): empty fields mean the base shape sends nothing.
type openaiEmbedSpec struct {
	// defaultBaseURL applies when the model record has no base_url (v1
	// constructor fallbacks).
	defaultBaseURL string
	// encodingFormat is set on the wire when non-empty (openai/nvidia/azure
	// send "float"; zhipu sends nothing).
	encodingFormat string
	// truncate511: the vendor carries truncate_prompt_tokens with the v1
	// constructor default of 511 (openai, zhipu).
	truncate511 bool
	// inputType is the nvidia passage/query tag (v1 read a ctx flag no caller
	// ever set, so the wire always said "passage" — kept as a constant).
	inputType string
}

// embedSpecFor ports the v1 per-vendor constructor defaults. Native-shape
// vendors (aliyun multimodal branch, volcengine, gemini) bypass this spec
// entirely with their own build/parse.
func embedSpecFor(name invoke.ProviderName) openaiEmbedSpec {
	switch name {
	case invoke.ProviderZhipu:
		return openaiEmbedSpec{
			defaultBaseURL: invoke.ZhipuEmbeddingBaseURL,
			truncate511:    true,
		}
	case invoke.ProviderNvidia:
		return openaiEmbedSpec{
			defaultBaseURL: invoke.NvidiaChatBaseURL,
			encodingFormat: "float",
			inputType:      "passage",
		}
	case invoke.ProviderAzureOpenAI:
		// azure's URL/auth differ (deployment path + api-key header); the
		// defaultBaseURL stays empty — v1 REQUIRED a base URL there.
		return openaiEmbedSpec{encodingFormat: "float"}
	default:
		return openaiEmbedSpec{
			defaultBaseURL: "https://api.openai.com/v1",
			encodingFormat: "float",
			truncate511:    true,
		}
	}
}

// buildOpenAIShapeEmbedding is the shared openai-family builder. ep.APIVersion
// is unused here; azure overrides the URL/auth in its own builder.
func buildOpenAIShapeEmbedding(
	spec openaiEmbedSpec, ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	if model == "" {
		return nil, fmt.Errorf("model name is required")
	}
	base := ep.BaseURL
	if base == "" {
		base = spec.defaultBaseURL
	}
	body := openAIEmbedRequest{
		Model:          model,
		Input:          opts.Inputs,
		EncodingFormat: spec.encodingFormat,
		InputType:      spec.inputType,
	}
	if spec.truncate511 {
		body.TruncatePromptTokens = opts.TruncatePromptTokens
		if body.TruncatePromptTokens == 0 {
			body.TruncatePromptTokens = 511
		}
	}
	if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
		body.Dimensions = opts.Dimensions
	}
	// v1 jina carried truncate:true — expressed via jinaEmbedRequest, not here.
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	return &invoke.Request{
		Method:  http.MethodPost,
		URL:     base + "/embeddings",
		Header:  header,
		Body:    data,
		Timeout: embeddingRequestTimeout,
	}, nil
}

// parseOpenAIEmbeddingResponse maps the data[] array in wire order (v1 never
// re-ordered by index on this shape).
func parseOpenAIEmbeddingResponse(_ int, _ http.Header, body []byte) (*invoke.EmbeddingResponse, error) {
	var resp openAIEmbedResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	embeddings := make([][]float32, 0, len(resp.Data))
	for _, d := range resp.Data {
		embeddings = append(embeddings, d.Embedding)
	}
	return &invoke.EmbeddingResponse{Vectors: embeddings}, nil
}

// --- composites ---

// embeddingCapsFor resolves the provider-level Embedding shard from the v1
// capability registry (nil when the catalog does not serve embedding).
func embeddingCapsFor(name invoke.ProviderName) *invoke.EmbeddingCaps {
	info, ok := providerInfoFor(name)
	if !ok {
		return nil
	}
	return info.EffectiveCapabilities().Embedding
}

// openaiEmbeddingAdapter adds the embedding facet to an openai-family chat
// adapter (dual-facet providers: one registry entry, both shards).
type openaiEmbeddingAdapter struct {
	openaiAdapter
	espec openaiEmbedSpec
}

var _ invoke.EmbeddingAdapter = (*openaiEmbeddingAdapter)(nil)

// Capabilities overrides the embedded adapter's declaration: both served
// shards (registration lock #2 requires shard set == facet set).
func (a *openaiEmbeddingAdapter) Capabilities() invoke.Capabilities {
	caps := a.openaiAdapter.Capabilities()
	caps.Embedding = embeddingCapsFor(a.name)
	return caps
}

// embedBuildParse resolves this vendor's build/parse pair: the native shapes
// (azure URL/auth, aliyun dual-path, volcengine multimodal — the builders
// live in the vendors' own files) take over completely; everything else
// rides the shared openai shape.
func (a *openaiEmbeddingAdapter) embedBuildParse() (
	func(ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions) (*invoke.Request, error),
	func(status int, header http.Header, body []byte) (*invoke.EmbeddingResponse, error),
) {
	switch a.name {
	case invoke.ProviderAzureOpenAI:
		return buildAzureEmbedding, parseOpenAIEmbeddingResponse
	case invoke.ProviderAliyun:
		return buildAliyunEmbedding, parseAliyunEmbedding
	case invoke.ProviderVolcengine:
		return buildVolcengineEmbedding, parseVolcengineEmbedding
	// gemini left this dispatch in P5-1: GeminiAdapter owns the native pair.
	default:
		spec := a.espec
		return func(ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions) (*invoke.Request, error) {
			return buildOpenAIShapeEmbedding(spec, ep, model, opts)
		}, parseOpenAIEmbeddingResponse
	}
}

// BuildEmbeddingRequest dispatches to the vendor build/parse pair.
func (a *openaiEmbeddingAdapter) BuildEmbeddingRequest(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	build, _ := a.embedBuildParse()
	return build(ep, model, opts)
}

// ParseEmbeddingResponse dispatches to the vendor build/parse pair.
func (a *openaiEmbeddingAdapter) ParseEmbeddingResponse(
	status int, header http.Header, body []byte,
) (*invoke.EmbeddingResponse, error) {
	_, parse := a.embedBuildParse()
	return parse(status, header, body)
}
