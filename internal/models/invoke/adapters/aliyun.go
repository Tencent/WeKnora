package adapters

// aliyun.go — the aliyun DashScope vendor deltas. Chat rides the openai
// family funnel; text embedding rides the shared openai shape through the
// OFFICIAL compatible-mode endpoint; multimodal embedding and rerank are
// native DashScope shapes. (v1 routed text-vs-multimodal at the factory from
// the model name — the branch lives in buildAliyunEmbedding now.)

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

// --- embedding: dual-path (openai-shape text / DashScope-native multimodal) ---

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

// --- rerank (native DashScope; the v1 base URL default is the FULL endpoint,
// not a host prefix) ---

type aliyunRerankRequest struct {
	Model      string             `json:"model"`
	Input      aliyunRerankInput  `json:"input"`
	Parameters aliyunRerankParams `json:"parameters"`
}

type aliyunRerankInput struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type aliyunRerankParams struct {
	ReturnDocuments bool `json:"return_documents"`
	TopN            int  `json:"top_n"`
}

const aliyunRerankEndpoint = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"

func buildAliyunRerank(ep invoke.Endpoint, model string, opts *invoke.RerankOptions) (*invoke.Request, error) {
	base := ep.BaseURL
	if base == "" {
		base = aliyunRerankEndpoint
	}
	body := aliyunRerankRequest{
		Model: model,
		Input: aliyunRerankInput{
			Query:     opts.Query,
			Documents: opts.Documents,
		},
		Parameters: aliyunRerankParams{
			ReturnDocuments: true,
			TopN:            len(opts.Documents), // v1: return all documents
		},
	}
	return buildRerankRequestShared(base, ep, body, "")
}

// parseAliyunRerank unwraps the DashScope envelope: results live under
// output.results (v1 AliyunRerankResponse), NOT at the top level — a
// top-level parse silently returns zero results (P3 review finding 2).
func parseAliyunRerank(_ int, _ http.Header, body []byte) (*invoke.RerankResponse, error) {
	var resp struct {
		Output struct {
			Results []rankResultWire `json:"results"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	out := make([]invoke.RerankResult, 0, len(resp.Output.Results))
	for _, r := range resp.Output.Results {
		out = append(out, invoke.RerankResult{Index: r.Index, Score: r.Score})
	}
	return &invoke.RerankResponse{Results: out}, nil
}
