package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
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
	maxRetries                int
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
	runtimeConfig := loadEmbeddingRuntimeConfig()
	return &WeKnoraCloudEmbedder{
		modelName:                 config.ModelName,
		remoteModelName:           remoteModelName,
		modelID:                   config.ModelID,
		appID:                     config.AppID,
		apiKey:                    config.AppSecret,
		baseURL:                   baseURL,
		dimensions:                config.Dimensions,
		supportsDimensionOverride: config.SupportsDimensionOverride,
		client:                    newEmbeddingHTTPClient(runtimeConfig.Timeout),
		maxRetries:                runtimeConfig.MaxRetries,
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

	resp, err := e.doRequestWithRetry(ctx, bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("weknoracloud embedder: do request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("weknoracloud embedder: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weknoracloud embedder: status %d: %s", resp.StatusCode, string(respBytes))
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

func (e *WeKnoraCloudEmbedder) doRequestWithRetry(ctx context.Context, bodyBytes []byte) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		if attempt > 0 {
			backoffTime := time.Duration(1<<uint(attempt-1)) * time.Second
			if backoffTime > 10*time.Second {
				backoffTime = 10 * time.Second
			}
			logger.GetLogger(ctx).Infof(
				"WeKnoraCloudEmbedder retrying request (%d/%d), waiting %v",
				attempt,
				e.maxRetries,
				backoffTime,
			)
			select {
			case <-time.After(backoffTime):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// The signature contains the request ID, so each retry must create a
		// fresh ID and signature even though the request body is unchanged.
		requestID := uuid.New().String()
		headers := utils.Sign(e.appID, e.apiKey, requestID, string(bodyBytes))
		req, requestErr := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			e.baseURL+weKnoraCloudEmbedPath,
			bytes.NewReader(bodyBytes),
		)
		if requestErr != nil {
			err = requestErr
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			req.Header.Set(key, value)
		}

		resp, err = e.client.Do(req)
		if err == nil {
			return resp, nil
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		logger.GetLogger(ctx).Errorf(
			"WeKnoraCloudEmbedder request failed (attempt %d/%d): %v",
			attempt+1,
			e.maxRetries+1,
			err,
		)
	}

	return nil, err
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
