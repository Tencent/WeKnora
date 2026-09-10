package adapters

// embedding.go — P2 strangler: the embedding facet (design §6.2). Behavior
// ports of v1 internal/models/embedding/*: the OpenAI-compatible fallback
// (openai/zhipu + every dual-facet family vendor), the per-vendor wire deltas
// (azure deployment URL + api-key, nvidia input_type, jina truncate boolean),
// and the native shapes (aliyun DashScope multimodal, volcengine Ark
// multimodal, gemini batchEmbedContents). ollama (/api/embed) and weknoracloud
// (HMAC) live in their own adapter files.
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
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/provider"
)

// embeddingRequestTimeout mirrors the v1 fixed per-client timeout.
const embeddingRequestTimeout = 60 * time.Second

// openAIEmbedRequest is the merged openai-family wire shape. Field order
// tracks the v1 per-vendor structs so golden request bodies stay
// byte-identical: openai {model,input,encoding_format,truncate_prompt_tokens,
// dimensions} ≡ zhipu (no encoding_format) ≡ nvidia (+input_type) ≡ azure
// (encoding_format+dimensions only). jina's {model,input,truncate,dimensions}
// order differs, so it gets its own struct below.
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
func embedSpecFor(name provider.ProviderName) openaiEmbedSpec {
	switch name {
	case provider.ProviderZhipu:
		return openaiEmbedSpec{
			defaultBaseURL: provider.ZhipuEmbeddingBaseURL,
			truncate511:    true,
		}
	case provider.ProviderNvidia:
		return openaiEmbedSpec{
			defaultBaseURL: "https://integrate.api.nvidia.com/v1",
			encodingFormat: "float",
			inputType:      "passage",
		}
	case provider.ProviderAzureOpenAI:
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

// buildAzureEmbedding ports v1 azure_openai.go: deployment-path URL,
// api-version query (Endpoint.APIVersion, default 2024-10-21), api-key header.
func buildAzureEmbedding(ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions) (*invoke.Request, error) {
	if ep.BaseURL == "" {
		return nil, fmt.Errorf("azure resource endpoint (base URL) is required")
	}
	if model == "" {
		return nil, fmt.Errorf("deployment name (model name) is required")
	}
	apiVersion := ep.APIVersion
	if apiVersion == "" {
		apiVersion = "2024-10-21"
	}
	body := openAIEmbedRequest{
		Model:          model,
		Input:          opts.Inputs,
		EncodingFormat: "float",
	}
	if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
		body.Dimensions = opts.Dimensions
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Api-Key", ep.Credentials.APIKey)
	return &invoke.Request{
		Method: http.MethodPost,
		URL: fmt.Sprintf("%s/openai/deployments/%s/embeddings?api-version=%s",
			ep.BaseURL, model, apiVersion),
		Header:  header,
		Body:    data,
		Timeout: embeddingRequestTimeout,
	}, nil
}

// jinaEmbedRequest: v1 jina.go field order {model, input, truncate, dimensions}
// — different from the merged openai struct, hence its own type.
type jinaEmbedRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Truncate   bool     `json:"truncate,omitempty"`
	Dimensions int      `json:"dimensions,omitempty"`
}

func buildJinaEmbedding(ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions) (*invoke.Request, error) {
	if model == "" {
		return nil, fmt.Errorf("model name is required")
	}
	base := ep.BaseURL
	if base == "" {
		base = "https://api.jina.ai/v1"
	}
	body := jinaEmbedRequest{Model: model, Input: opts.Inputs, Truncate: true}
	if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
		body.Dimensions = opts.Dimensions
	}
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

// --- aliyun: text models ride the openai shape via compatible-mode; vision /
// multimodal models ride the DashScope multimodal endpoint (v1 routed at the
// factory from the model name — the branch lives in the adapter now). ---

const aliyunMultimodalEmbeddingPath = "/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding"

type aliyunEmbedRequest struct {
	Model      string             `json:"model"`
	Input      aliyunEmbedInput   `json:"input"`
	Parameters *aliyunEmbedParams `json:"parameters,omitempty"`
}

type aliyunEmbedInput struct {
	Contents []aliyunEmbedContent `json:"contents"`
}

type aliyunEmbedContent struct {
	Text string `json:"text,omitempty"`
}

type aliyunEmbedParams struct {
	Dimension int `json:"dimension,omitempty"`
}

type aliyunEmbedResponse struct {
	Output struct {
		Embeddings []struct {
			Embedding []float32 `json:"embedding"`
			TextIndex int       `json:"text_index"`
		} `json:"embeddings"`
	} `json:"output"`
}

func isMultimodalEmbeddingModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "vision") || strings.Contains(lower, "multimodal")
}

func buildAliyunEmbedding(ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions) (*invoke.Request, error) {
	if model == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if isMultimodalEmbeddingModel(model) {
		return buildAliyunMultimodalEmbedding(ep, model, opts)
	}
	// v1 text path: force the compatible-mode endpoint unless the record URL
	// already points at it.
	base := ep.BaseURL
	if base == "" || !strings.Contains(base, "/compatible-mode/") {
		base = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	spec := openaiEmbedSpec{defaultBaseURL: base, encodingFormat: "float", truncate511: true}
	return buildOpenAIShapeEmbedding(spec, ep, model, opts)
}

func buildAliyunMultimodalEmbedding(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	// v1 multimodal path: default to the DashScope root and strip any
	// compatible-mode suffix the user pointed at the text endpoint with
	// (trailing slash trimmed first — v1 aliyun.go constructor; P2 review
	// finding 6).
	base := ep.BaseURL
	if base == "" {
		base = "https://dashscope.aliyuncs.com"
	} else {
		base = strings.TrimRight(base, "/")
		if strings.Contains(base, "/compatible-mode/") {
			base = strings.Replace(base, "/compatible-mode/v1", "", 1)
			base = strings.Replace(base, "/compatible-mode", "", 1)
		}
	}
	contents := make([]aliyunEmbedContent, 0, len(opts.Inputs))
	for _, text := range opts.Inputs {
		contents = append(contents, aliyunEmbedContent{Text: text})
	}
	reqBody := aliyunEmbedRequest{
		Model: model,
		Input: aliyunEmbedInput{Contents: contents},
	}
	if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
		reqBody.Parameters = &aliyunEmbedParams{Dimension: opts.Dimensions}
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	return &invoke.Request{
		Method:  http.MethodPost,
		URL:     base + aliyunMultimodalEmbeddingPath,
		Header:  header,
		Body:    data,
		Timeout: embeddingRequestTimeout,
	}, nil
}

func parseAliyunEmbedding(_ int, _ http.Header, body []byte) (*invoke.EmbeddingResponse, error) {
	var resp aliyunEmbedResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	// v1: place by text_index (DashScope may return vectors out of order),
	// sized by the returned count — the caller-side pooler validates the
	// count against the inputs (v1 batchEmbedder did the same).
	embeddings := make([][]float32, len(resp.Output.Embeddings))
	for _, emb := range resp.Output.Embeddings {
		if emb.TextIndex >= 0 && emb.TextIndex < len(embeddings) {
			embeddings[emb.TextIndex] = emb.Embedding
		}
	}
	return &invoke.EmbeddingResponse{Vectors: embeddings}, nil
}

// --- volcengine: Ark multimodal API returns ONE combined vector per request,
// so multi-input batches fan out per input (v1 client loop) — declared via
// SingleInputPerRequest. ---

const volcengineMultimodalEmbeddingPath = "/api/v3/embeddings/multimodal"

type volcengineEmbedRequest struct {
	Model      string                   `json:"model"`
	Input      []volcengineInputContent `json:"input"`
	Dimensions int                      `json:"dimensions,omitempty"`
}

type volcengineInputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type volcengineEmbedResponse struct {
	Data struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// volcengineEmbeddingBase normalizes the record base URL the way the v1
// constructor did (strip a full multimodal path or an /api/v3 suffix).
func volcengineEmbeddingBase(baseURL string) string {
	if baseURL == "" {
		return "https://ark.cn-beijing.volces.com"
	}
	base := strings.TrimRight(baseURL, "/")
	if strings.Contains(base, "/embeddings/multimodal") {
		if idx := strings.Index(base, "/api/"); idx != -1 {
			base = base[:idx]
		}
	} else if strings.HasSuffix(base, "/api/v3") {
		base = strings.TrimSuffix(base, "/api/v3")
	}
	return base
}

func buildVolcengineEmbedding(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	if model == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if len(opts.Inputs) != 1 {
		// The entry fans single-input vendors out per text; a multi-input
		// build here would silently collapse the batch into one vector.
		return nil, fmt.Errorf("volcengine multimodal embedding: one input per request (got %d)",
			len(opts.Inputs))
	}
	reqBody := volcengineEmbedRequest{
		Model: model,
		Input: []volcengineInputContent{{Type: "text", Text: opts.Inputs[0]}},
	}
	if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
		reqBody.Dimensions = opts.Dimensions
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	return &invoke.Request{
		Method:  http.MethodPost,
		URL:     volcengineEmbeddingBase(ep.BaseURL) + volcengineMultimodalEmbeddingPath,
		Header:  header,
		Body:    data,
		Timeout: embeddingRequestTimeout,
	}, nil
}

func parseVolcengineEmbedding(_ int, _ http.Header, body []byte) (*invoke.EmbeddingResponse, error) {
	var resp volcengineEmbedResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	return &invoke.EmbeddingResponse{Vectors: [][]float32{resp.Data.Embedding}}, nil
}

// --- gemini: native batchEmbedContents (the v1 embedder was already native;
// ported in P2, not deferred to the P5 generateContent work). ---

const geminiEmbeddingBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type geminiBatchEmbedRequest struct {
	Requests []geminiEmbedRequest `json:"requests"`
}

type geminiEmbedRequest struct {
	Model                string        `json:"model"`
	Content              geminiContent `json:"content"`
	TaskType             string        `json:"taskType,omitempty"`
	OutputDimensionality int           `json:"output_dimensionality,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiBatchEmbedResponse struct {
	Embeddings []struct {
		Values []float32 `json:"values"`
	} `json:"embeddings"`
}

func buildGeminiEmbedding(ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions) (*invoke.Request, error) {
	if model == "" {
		return nil, fmt.Errorf("model name is required")
	}
	model = strings.TrimPrefix(model, "models/")
	base := ep.BaseURL
	if base == "" {
		base = geminiEmbeddingBaseURL
	}
	base = strings.TrimRight(base, "/")
	base = strings.TrimSuffix(base, "/openai")

	requests := make([]geminiEmbedRequest, 0, len(opts.Inputs))
	for _, text := range opts.Inputs {
		req := geminiEmbedRequest{
			Model: "models/" + model,
			Content: geminiContent{Parts: []geminiPart{
				{Text: text},
			}},
		}
		if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
			req.OutputDimensionality = opts.Dimensions
		}
		requests = append(requests, req)
	}
	data, err := json.Marshal(geminiBatchEmbedRequest{Requests: requests})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("X-Goog-Api-Key", ep.Credentials.APIKey)
	return &invoke.Request{
		Method:  http.MethodPost,
		URL:     fmt.Sprintf("%s/models/%s:batchEmbedContents", base, model),
		Header:  header,
		Body:    data,
		Timeout: embeddingRequestTimeout,
	}, nil
}

func parseGeminiEmbedding(_ int, _ http.Header, body []byte) (*invoke.EmbeddingResponse, error) {
	var resp geminiBatchEmbedResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	// (v1 also failed fast on a count mismatch inside the client; the
	// caller-side pooler count check covers the same anomaly, so the adapter
	// maps whatever came back.)
	embeddings := make([][]float32, 0, len(resp.Embeddings))
	for _, emb := range resp.Embeddings {
		embeddings = append(embeddings, emb.Values)
	}
	return &invoke.EmbeddingResponse{Vectors: embeddings}, nil
}

// --- composites ---

// embeddingCapsFor resolves the provider-level Embedding shard from the v1
// capability registry (nil when the catalog does not serve embedding).
func embeddingCapsFor(name provider.ProviderName) *provider.EmbeddingCaps {
	p, ok := provider.Get(name)
	if !ok {
		return nil
	}
	caps := p.Info().EffectiveCapabilities()
	return caps.Embedding
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
func (a *openaiEmbeddingAdapter) Capabilities() provider.Capabilities {
	caps := a.openaiAdapter.Capabilities()
	caps.Embedding = embeddingCapsFor(a.name)
	return caps
}

// embedBuildParse resolves this vendor's build/parse pair: the native shapes
// (azure URL/auth, aliyun dual-path, volcengine multimodal, gemini native)
// take over completely; everything else rides the shared openai shape.
func (a *openaiEmbeddingAdapter) embedBuildParse() (
	func(ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions) (*invoke.Request, error),
	func(status int, header http.Header, body []byte) (*invoke.EmbeddingResponse, error),
) {
	switch a.name {
	case provider.ProviderAzureOpenAI:
		return buildAzureEmbedding, parseOpenAIEmbeddingResponse
	case provider.ProviderAliyun:
		return buildAliyunEmbedding, parseAliyunEmbedding
	case provider.ProviderVolcengine:
		return buildVolcengineEmbedding, parseVolcengineEmbedding
	case provider.ProviderGemini:
		return buildGeminiEmbedding, parseGeminiEmbedding
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

// volcengineEmbeddingAdapter marks the single-input vendor so the Embed entry
// fans multi-input batches out per request (v1 client loop semantics).
type volcengineEmbeddingAdapter struct {
	openaiEmbeddingAdapter
}

var _ invoke.SingleInputEmbedder = (*volcengineEmbeddingAdapter)(nil)

// SingleInputPerRequest reports the Ark multimodal API contract: one vector
// per request.
func (a *volcengineEmbeddingAdapter) SingleInputPerRequest() bool { return true }

// jinaEmbeddingAdapter serves the embedding-only vendor (no chat shard — v1
// routed jina to the embedding factory only).
type jinaEmbeddingAdapter struct {
	name provider.ProviderName
}

var _ invoke.EmbeddingAdapter = (*jinaEmbeddingAdapter)(nil)

// Provider returns the canonical provider name.
func (a *jinaEmbeddingAdapter) Provider() string { return string(a.name) }

// Capabilities reports the embedding-only shard.
func (a *jinaEmbeddingAdapter) Capabilities() provider.Capabilities {
	return provider.Capabilities{Embedding: embeddingCapsFor(a.name)}
}

// BuildEmbeddingRequest builds the jina wire request.
func (a *jinaEmbeddingAdapter) BuildEmbeddingRequest(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	return buildJinaEmbedding(ep, model, opts)
}

// ParseEmbeddingResponse parses the shared openai data[] shape.
func (a *jinaEmbeddingAdapter) ParseEmbeddingResponse(
	status int, header http.Header, body []byte,
) (*invoke.EmbeddingResponse, error) {
	return parseOpenAIEmbeddingResponse(status, header, body)
}
