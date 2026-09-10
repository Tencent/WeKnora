package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/google/uuid"
)

const weKnoraCloudEmbedPath = "/api/v1/embeddings"

// WeKnoraCloudEmbedder 实现 embedding.Embedder 接口，对接 WeKnoraCloud /api/v1/embeddings
type WeKnoraCloudEmbedder struct {
	modelName                 string
	remoteModelName           string
	modelID                   string
	appID                     string
	apiKey                    string
	baseURL                   string
	dimensions                int
	supportsDimensionOverride bool
	client                    *http.Client
	EmbedderPooler
}

// NewWeKnoraCloudEmbedder 构造 WeKnoraCloudEmbedder
func NewWeKnoraCloudEmbedder(config Config) (*WeKnoraCloudEmbedder, error) {
	if config.AppID == "" {
		return nil, fmt.Errorf("WeKnoraCloud embedder: AppID is required")
	}
	if config.AppSecret == "" {
		return nil, fmt.Errorf("WeKnoraCloud embedder: AppSecret is required")
	}
	remoteModelName := ""
	if config.ExtraConfig != nil {
		remoteModelName = strings.TrimSpace(config.ExtraConfig["remote_model_name"])
	}
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = provider.WeKnoraCloudBaseURL
	}
	if err := validateEmbeddingBaseURL(baseURL); err != nil {
		return nil, err
	}
	return &WeKnoraCloudEmbedder{
		modelName:                 config.ModelName,
		remoteModelName:           remoteModelName,
		modelID:                   config.ModelID,
		appID:                     config.AppID,
		apiKey:                    config.AppSecret,
		baseURL:                   baseURL,
		dimensions:                config.Dimensions,
		supportsDimensionOverride: config.SupportsDimensionOverride,
		client:                    newEmbeddingHTTPClient(60 * time.Second),
	}, nil
}

type weKnoraCloudEmbedRequest struct {
	Model                string   `json:"model"`
	Input                []string `json:"input"`
	Dimensions           int      `json:"dimensions,omitempty"`
	TruncatePromptTokens int      `json:"truncate_prompt_tokens,omitempty"`
}

type weKnoraCloudEmbedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func (e *WeKnoraCloudEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	results, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("weknoracloud embedder: empty response")
	}
	return results[0], nil
}

func (e *WeKnoraCloudEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := weKnoraCloudEmbedRequest{Model: e.effectiveModelName(), Input: texts}
	if e.supportsDimensionOverride && e.dimensions > 0 {
		reqBody.Dimensions = e.dimensions
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("weknoracloud embedder: marshal: %w", err)
	}

	requestID := uuid.New().String()
	headers := utils.Sign(e.appID, e.apiKey, requestID, string(bodyBytes))
	// Signed headers are deterministic for this body+requestID, so the shared
	// retry policy (retry_http.go) can safely resend the same request.
	resp, err := retryEmbeddingRequest(ctx, e.client, http.MethodPost,
		e.baseURL+weKnoraCloudEmbedPath, bodyBytes, func(req *http.Request) {
			req.Header.Set("Content-Type", "application/json")
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		})
	if err != nil {
		return nil, fmt.Errorf("weknoracloud embedder: do request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("weknoracloud embedder: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		message := fmt.Sprintf("weknoracloud embedder: status %d: %s", resp.StatusCode, string(respBytes))
		return nil, embedHTTPError(resp.StatusCode, message)
	}

	var embedResp weKnoraCloudEmbedResponse
	if err := json.Unmarshal(respBytes, &embedResp); err != nil {
		return nil, fmt.Errorf("weknoracloud embedder: unmarshal: %w", err)
	}

	result := make([][]float32, len(texts))
	seen := make([]bool, len(texts))
	for _, item := range embedResp.Data {
		if item.Index < 0 || item.Index >= len(result) {
			return nil, fmt.Errorf(
				"weknoracloud embedder: response index %d out of range for %d inputs",
				item.Index,
				len(texts),
			)
		}
		if seen[item.Index] {
			return nil, fmt.Errorf("weknoracloud embedder: duplicate response index %d", item.Index)
		}
		result[item.Index] = item.Embedding
		seen[item.Index] = true
	}
	for index, found := range seen {
		if !found {
			return nil, fmt.Errorf("weknoracloud embedder: missing embedding for input index %d", index)
		}
	}
	return result, nil
}

func (e *WeKnoraCloudEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func (e *WeKnoraCloudEmbedder) SetSupportsDimensionOverride(supported bool) {
	e.supportsDimensionOverride = supported
}

func (e *WeKnoraCloudEmbedder) effectiveModelName() string {
	if e.remoteModelName != "" {
		return e.remoteModelName
	}
	return e.modelName
}

func (e *WeKnoraCloudEmbedder) GetModelName() string { return e.modelName }
func (e *WeKnoraCloudEmbedder) GetModelID() string   { return e.modelID }
func (e *WeKnoraCloudEmbedder) GetDimensions() int   { return e.dimensions }
