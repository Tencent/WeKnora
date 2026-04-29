package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
)

// This file is the declarative registry of HTTP-transport embedders. Each
// variable below replaces an entire ~200-line provider file that existed
// before the refactor. The heavy lifting (HTTP retry, SSRF-safe client,
// custom-header injection, error shaping) is in runner.go.
//
// To add a new provider: add an embedderSpec, then a case in newEmbedder()
// in embedder.go. That's all.

// openaiSpec covers vanilla OpenAI-compatible /embeddings endpoints.
// Aliyun's text-only compatible mode also uses this spec (see newEmbedder).
var openaiSpec = embedderSpec{
	providerName:   "OpenAI",
	defaultBaseURL: "https://api.openai.com/v1",
	batchMode:      batchAll,
	buildURL: func(e *httpEmbedder) string {
		return e.baseURL + "/embeddings"
	},
	buildBody: func(ctx context.Context, e *httpEmbedder, texts []string) ([]byte, error) {
		logInputLengths(ctx, "OpenAI", texts)
		return marshalJSON(map[string]any{
			"model":                  e.modelName,
			"input":                  texts,
			"encoding_format":        "float",
			"truncate_prompt_tokens": e.truncatePromptTokens,
		})
	},
	parseBody: parseOpenAIResponse,
}

// jinaSpec targets api.jina.ai. Differences from OpenAI: no
// truncate_prompt_tokens (replaced by boolean truncate), optional dimensions.
var jinaSpec = embedderSpec{
	providerName:   "Jina",
	defaultBaseURL: "https://api.jina.ai/v1",
	batchMode:      batchAll,
	buildURL: func(e *httpEmbedder) string {
		return e.baseURL + "/embeddings"
	},
	buildBody: func(_ context.Context, e *httpEmbedder, texts []string) ([]byte, error) {
		body := map[string]any{
			"model":    e.modelName,
			"input":    texts,
			"truncate": true,
		}
		if e.dimensions > 0 {
			body["dimensions"] = e.dimensions
		}
		return marshalJSON(body)
	},
	parseBody: parseOpenAIResponse,
}

// nvidiaSpec targets integrate.api.nvidia.com. NVIDIA embedders accept an
// input_type parameter (query/passage) which the retriever signals via
// ctx[types.EmbedQueryContextKey].
var nvidiaSpec = embedderSpec{
	providerName:   "Nvidia",
	defaultBaseURL: "https://integrate.api.nvidia.com/v1",
	batchMode:      batchAll,
	buildURL: func(e *httpEmbedder) string {
		return e.baseURL + "/embeddings"
	},
	buildBody: func(ctx context.Context, e *httpEmbedder, texts []string) ([]byte, error) {
		inputType := "passage"
		if isQuery, _ := ctx.Value(types.EmbedQueryContextKey).(bool); isQuery {
			inputType = "query"
		}
		return marshalJSON(map[string]any{
			"model":                  e.modelName,
			"input":                  texts,
			"encoding_format":        "float",
			"truncate_prompt_tokens": e.truncatePromptTokens,
			"input_type":             inputType,
		})
	},
	parseBody: parseOpenAIResponse,
}

// azureOpenAISpec targets Azure OpenAI. URL is built from the deployment name
// + api-version; auth is via the api-key header rather than Bearer.
var azureOpenAISpec = embedderSpec{
	providerName: "AzureOpenAI",
	// No defaultBaseURL — Azure always requires a resource endpoint.
	defaultBaseURL: "",
	batchMode:      batchAll,
	validate: func(e *httpEmbedder) error {
		if e.baseURL == "" {
			return fmt.Errorf("Azure resource endpoint (base URL) is required")
		}
		if e.apiVersion == "" {
			e.apiVersion = "2024-10-21"
		}
		return nil
	},
	buildURL: func(e *httpEmbedder) string {
		return fmt.Sprintf("%s/openai/deployments/%s/embeddings?api-version=%s",
			e.baseURL, e.modelName, e.apiVersion)
	},
	buildAuthHeaders: func(e *httpEmbedder) map[string]string {
		return map[string]string{"api-key": e.apiKey}
	},
	buildBody: func(_ context.Context, e *httpEmbedder, texts []string) ([]byte, error) {
		body := map[string]any{
			"model":           e.modelName,
			"input":           texts,
			"encoding_format": "float",
		}
		if azureSupportsDimensions(e) {
			body["dimensions"] = e.dimensions
		}
		return marshalJSON(body)
	},
	parseBody: parseOpenAIResponse,
}

// azureSupportsDimensions captures the compatibility matrix: Azure only
// honors the dimensions parameter on 2024-10-21+ with text-embedding-3-*
// deployments. ada-002 rejects the field.
func azureSupportsDimensions(e *httpEmbedder) bool {
	if e.dimensions <= 0 {
		return false
	}
	if strings.TrimSpace(e.apiVersion) < "2024-10-21" {
		return false
	}
	modelRef := strings.ToLower(strings.TrimSpace(e.modelID))
	if modelRef == "" {
		modelRef = strings.ToLower(strings.TrimSpace(e.modelName))
	}
	if strings.Contains(modelRef, "ada-002") {
		return false
	}
	return strings.Contains(modelRef, "text-embedding-3-small") ||
		strings.Contains(modelRef, "text-embedding-3-large")
}

// volcengineSpec targets Volcengine Ark's multimodal embedding endpoint.
// The endpoint returns a single embedding per request, so batchMode=batchOne.
var volcengineSpec = embedderSpec{
	providerName:   "Volcengine",
	defaultBaseURL: "https://ark.cn-beijing.volces.com",
	batchMode:      batchOne,
	normalizeBaseURL: func(baseURL string) string {
		baseURL = strings.TrimRight(baseURL, "/")
		// Accept URLs that already include the full multimodal path or the
		// /api/v3 suffix; strip back to the host.
		if strings.Contains(baseURL, "/embeddings/multimodal") {
			if idx := strings.Index(baseURL, "/api/"); idx != -1 {
				baseURL = baseURL[:idx]
			}
		} else if strings.HasSuffix(baseURL, "/api/v3") {
			baseURL = strings.TrimSuffix(baseURL, "/api/v3")
		}
		return baseURL
	},
	buildURL: func(e *httpEmbedder) string {
		return e.baseURL + "/api/v3/embeddings/multimodal"
	},
	buildBody: func(_ context.Context, e *httpEmbedder, texts []string) ([]byte, error) {
		// batchOne guarantees len(texts)==1.
		return marshalJSON(map[string]any{
			"model": e.modelName,
			"input": []map[string]any{
				{"type": "text", "text": texts[0]},
			},
		})
	},
	parseBody: func(body []byte, _ []string) ([][]float32, error) {
		var resp struct {
			Data struct {
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		return [][]float32{resp.Data.Embedding}, nil
	},
	formatError: func(body []byte, status string) error {
		var errResp struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			return fmt.Errorf("API error: %s - %s", errResp.Error.Code, errResp.Error.Message)
		}
		return fmt.Errorf("BatchEmbed API error: Http Status %s", status)
	},
}

// aliyunMultimodalSpec targets Aliyun DashScope's multimodal embedding API.
// (The text-only variant uses openaiSpec — see newEmbedder's routing.)
var aliyunMultimodalSpec = embedderSpec{
	providerName:   "Aliyun",
	defaultBaseURL: "https://dashscope.aliyuncs.com",
	batchMode:      batchAll,
	normalizeBaseURL: func(baseURL string) string {
		baseURL = strings.TrimRight(baseURL, "/")
		// Users sometimes paste the OpenAI-compatible-mode URL; strip it so
		// the multimodal endpoint path works.
		return strings.Replace(baseURL, "/compatible-mode/v1", "", 1)
	},
	buildURL: func(e *httpEmbedder) string {
		return e.baseURL + "/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding"
	},
	buildBody: func(_ context.Context, e *httpEmbedder, texts []string) ([]byte, error) {
		contents := make([]map[string]any, 0, len(texts))
		for _, t := range texts {
			contents = append(contents, map[string]any{"text": t})
		}
		return marshalJSON(map[string]any{
			"model": e.modelName,
			"input": map[string]any{"contents": contents},
		})
	},
	parseBody: func(body []byte, texts []string) ([][]float32, error) {
		var resp struct {
			Output struct {
				Embeddings []struct {
					Embedding []float32 `json:"embedding"`
					TextIndex int       `json:"text_index"`
				} `json:"embeddings"`
			} `json:"output"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		out := make([][]float32, len(texts))
		for _, emb := range resp.Output.Embeddings {
			if emb.TextIndex >= 0 && emb.TextIndex < len(out) {
				out[emb.TextIndex] = emb.Embedding
			}
		}
		return out, nil
	},
	formatError: func(body []byte, status string) error {
		var errResp struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Message != "" {
			return fmt.Errorf("API error: %s - %s", errResp.Code, errResp.Message)
		}
		return fmt.Errorf("BatchEmbed API error: Http Status %s", status)
	},
}

// logInputLengths mirrors the detailed [0] (len=N) diagnostic that
// OpenAIEmbedder produced before the refactor. Keeping it behind
// logger.Debugf/Errorf matters because one WeKnora deployment uses this to
// trace empty-input bugs in the chunker. Only OpenAI has full parity; other
// providers' buildBody skipped this logging, so we scope it to openaiSpec.
func logInputLengths(ctx context.Context, providerName string, texts []string) {
	hasInvalid := false
	for i, t := range texts {
		n := len(t)
		preview := t
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		if n == 0 || n > 8192 {
			hasInvalid = true
			logger.GetLogger(ctx).Errorf("%sEmbedder BatchEmbed input[%d]: INVALID length=%d (must be [1, 8192]), preview=%s",
				providerName, i, n, preview)
		} else {
			logger.GetLogger(ctx).Debugf("%sEmbedder BatchEmbed input[%d]: length=%d, preview=%s",
				providerName, i, n, preview)
		}
	}
	if hasInvalid {
		logger.GetLogger(ctx).Errorf("%sEmbedder BatchEmbed: Found invalid input lengths, this will likely cause API error", providerName)
	}
}

// specForProvider picks the embedder spec for the given provider + model.
// Returns (spec, ok). The caller (newEmbedder) handles the fallback path
// (Ollama, WeKnoraCloud, or the OpenAI-compatible generic case).
func specForProvider(name provider.ProviderName, modelName string) (embedderSpec, bool) {
	switch name {
	case provider.ProviderJina:
		return jinaSpec, true
	case provider.ProviderNvidia:
		return nvidiaSpec, true
	case provider.ProviderAzureOpenAI:
		return azureOpenAISpec, true
	case provider.ProviderVolcengine:
		return volcengineSpec, true
	case provider.ProviderAliyun:
		lower := strings.ToLower(modelName)
		if strings.Contains(lower, "vision") || strings.Contains(lower, "multimodal") {
			return aliyunMultimodalSpec, true
		}
		// text-only Qwen embeddings use the OpenAI-compatible endpoint.
		return openaiSpec, true
	case provider.ProviderOpenAI:
		return openaiSpec, true
	}
	return embedderSpec{}, false
}
