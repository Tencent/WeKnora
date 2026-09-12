// GeminiAdapter — Google Gemini over the OFFICIAL OpenAI-compatible layer
// (裁定 #31, 2026-09-11). 设计原则：服务商适配层本职是原生接口；厂商官方
// 提供 OpenAI 协议接口时走 OpenAI 协议——机制分歧（Gemini 三代模型的
// level/budget/地板差异，R1 教训）由 Google 服务端吸收，用户面对统一的
// reasoning_effort 词表。
//
// Wire: POST {compat-base}/chat/completions（Bearer 鉴权），embedding 仍走
// 原生 batchEmbedContents（无 OpenAI 协议面，P2 起 golden 钉住），模型列表
// 走兼容层 GET {compat-base}/models（OpenAI 形 data 信封）。
//
// 历史：v1 即走兼容层；P5-1 曾换原生 generateContent（R1 暴露三族三机制
// 分派代价）后按裁定 #31 切回兼容层。原生实现见 git 历史 223990d7。

package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/sashabaranov/go-openai"
)

// compile-time lock #1 (design §6.2).
var (
	_ invoke.ChatAdapter       = (*GeminiAdapter)(nil)
	_ invoke.EmbeddingAdapter  = (*GeminiAdapter)(nil)
	_ invoke.ListModelsAdapter = (*GeminiAdapter)(nil)
)

// GeminiAdapter serves gemini over the official OpenAI-compat wire. The chat
// facet (build/parse/stream) is the openai family's golden-pinned path via
// composition — only the gemini deltas are overridden here: default base URL,
// reasoning_effort thinking strategy, the compat-shaped model list, and the
// native embedding pair.
type GeminiAdapter struct {
	openaiAdapter
}

func init() {
	if err := invoke.Default.Register(newGeminiAdapter()); err != nil {
		panic(err) // three-lock #2 fail fast
	}
}

func newGeminiAdapter() *GeminiAdapter {
	return &GeminiAdapter{openaiAdapter: openaiAdapter{
		name: invoke.ProviderGemini,
		spec: openaiVendorSpec{thinking: geminiReasoningEffortApply},
		caps: chatCapsFor(invoke.ProviderGemini),
	}}
}

// BuildChatRequest delegates to the openai funnel after normalizing the base
// URL: records created in the native era (or users pasting the Gemini API
// root) carry a base WITHOUT the /openai segment — the compat endpoint lives
// one segment deeper.
func (a *GeminiAdapter) BuildChatRequest(
	ep invoke.Endpoint, model string, opts *invoke.ChatOptions,
) (*invoke.Request, error) {
	ep.BaseURL = geminiCompatBaseURL(ep.BaseURL)
	return a.openaiAdapter.BuildChatRequest(ep, model, opts)
}

// geminiCompatBaseURL normalizes any stored base onto the compat surface:
// empty → the official compat endpoint; a Google root or /v1beta (native-era
// base) gains the /openai segment; an explicit /openai base passes through.
func geminiCompatBaseURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return invoke.GeminiOpenAICompatBaseURL
	}
	if strings.Contains(base, "generativelanguage.googleapis.com") &&
		!strings.HasSuffix(base, "/openai") {
		base += "/openai"
	}
	return base
}

// Capabilities reports the served shards: chat from the provider knowledge
// table, embedding (native facet below), and the model-listing flag.
// Registration lock #2: shard set == facet set.
func (a *GeminiAdapter) Capabilities() invoke.Capabilities {
	caps := a.openaiAdapter.Capabilities()
	if info, ok := providerInfoFor(invoke.ProviderGemini); ok {
		caps.Embedding = info.EffectiveCapabilities().Embedding
	}
	return caps
}

// --- thinking: reasoning_effort（兼容层即 OpenAI 词表，裁定 #31） ---

// geminiReasoningEffortApply maps the platform decision onto reasoning_effort.
// The mechanism translation (thinkingLevel enum vs budget, per-model floors,
// disable-ability) is GOOGLE'S server-side job now — this function only
// speaks the standard vocabulary:
//
//	Thinking=nil    → 不发（模型默认）
//	Thinking=&false → "none"（OpenAI 词表的关闭语义；Google 按模型钳制——
//	                  2.5-pro 不可关闭为厂商事实）
//	Thinking=&true  → low/medium/high（平台档位经五档链解析后映射）
func geminiReasoningEffortApply(
	req *openai.ChatCompletionRequest, opts *invoke.ChatOptions, _ bool,
) (any, bool) {
	if opts == nil || opts.Thinking == nil {
		return nil, false
	}
	if !*opts.Thinking {
		req.ReasoningEffort = "none"
		return nil, false
	}
	var caps invoke.ThinkingCaps
	if cc := chatCapsFor(invoke.ProviderGemini); cc != nil {
		caps = cc.Thinking
	}
	level := invoke.ResolveThinkingLevel(opts.ThinkingLevel, "", nil, caps)
	req.ReasoningEffort = geminiEffortVocab(level)
	return nil, false
}

// geminiEffortVocab maps the platform five-level vocabulary onto the compat
// reasoning_effort vocabulary (minimal/low/medium/high); xhigh/max cap at
// high. No default arm — a new platform Level must be mapped explicitly
// (negative-verified: add a constant and lint fails the build).
//
//exhaustive:enforce
func geminiEffortVocab(level string) string {
	switch invoke.Level(level) {
	case invoke.LevelLow:
		return "low"
	case invoke.LevelMedium:
		return "medium"
	case invoke.LevelHigh, invoke.LevelXHigh, invoke.LevelMax:
		return "high"
	}
	return "medium"
}

// --- embedding facet: native batchEmbedContents (P2 port, homed here since
// the compat rewrite gave gemini a dedicated file) — NO OpenAI-protocol
// surface; the wire is unchanged since P2 (golden embedding_gemini pins it) ---

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
	apiKey := strings.TrimSpace(ep.Credentials.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("gemini provider: API key is required")
	}
	model = strings.TrimPrefix(model, "models/")
	base := ep.BaseURL
	if base == "" {
		base = invoke.GeminiBaseURL
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
	header.Set("X-Goog-Api-Key", apiKey)
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

// BuildEmbeddingRequest serves the native pair above.
func (a *GeminiAdapter) BuildEmbeddingRequest(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	return buildGeminiEmbedding(ep, model, opts)
}

// ParseEmbeddingResponse serves the native pair above.
func (a *GeminiAdapter) ParseEmbeddingResponse(
	status int, header http.Header, body []byte,
) (*invoke.EmbeddingResponse, error) {
	return parseGeminiEmbedding(status, header, body)
}

// --- model listing facet: the compat layer exposes OpenAI-shaped /models ---

// BuildListRequest targets the compat list endpoint directly: the family's
// openAIListURL derives .../openai/v1/models for this base (wrong — the
// compat layer serves /models at the root of the /openai segment).
func (a *GeminiAdapter) BuildListRequest(ep invoke.Endpoint, _ invoke.ListOptions) (*invoke.Request, error) {
	base := geminiCompatBaseURL(ep.BaseURL)
	header := http.Header{}
	if key := strings.TrimSpace(ep.Credentials.APIKey); key != "" {
		header.Set("Authorization", "Bearer "+key)
	}
	return &invoke.Request{
		Method: http.MethodGet,
		URL:    base + "/models",
		Header: header,
	}, nil
}

// ParseListResponse decodes the OpenAI-shaped listing envelope shared with
// the other compat providers.
func (a *GeminiAdapter) ParseListResponse(_ int, _ http.Header, body []byte) ([]invoke.RemoteModel, error) {
	return parseModelListEnvelope(body)
}
