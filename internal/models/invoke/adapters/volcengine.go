package adapters

// volcengine.go — the volcengine Ark vendor deltas. Chat rides the openai
// family funnel (Ark's official OpenAI-compatible face); embedding is the
// NATIVE Ark multimodal API, which returns ONE combined vector per request —
// the entry fans multi-input batches out per input (SingleInputEmbedder
// marker below). Rerank stays on the v1 IAM-signed SDK client until P5-3.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

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

// volcengineEmbeddingAdapter marks the single-input vendor so the Embed entry
// fans multi-input batches out per request (v1 client loop semantics).
type volcengineEmbeddingAdapter struct {
	openaiEmbeddingAdapter
}

var _ invoke.SingleInputEmbedder = (*volcengineEmbeddingAdapter)(nil)

// SingleInputPerRequest reports the Ark multimodal API contract: one vector
// per request.
func (a *volcengineEmbeddingAdapter) SingleInputPerRequest() bool { return true }
