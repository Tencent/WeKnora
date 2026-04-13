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

	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/google/uuid"
)

const weKnoraCloudEmbedPath = "/api/v1/embeddings"

// WeKnoraCloudEmbedder 实现 embedding.Embedder 接口，对接 WeKnoraCloud /api/v1/embeddings
type WeKnoraCloudEmbedder struct {
	modelName       string
	remoteModelName string
	modelID         string
	appID           string
	apiKey          string
	baseURL         string
	dimensions      int
	client          *http.Client
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
	return &WeKnoraCloudEmbedder{
		modelName:       config.ModelName,
		remoteModelName: remoteModelName,
		modelID:         config.ModelID,
		appID:           config.AppID,
		apiKey:          config.AppSecret,
		baseURL:         strings.TrimRight(config.BaseURL, "/"),
		dimensions:      config.Dimensions,
		client:          &http.Client{Timeout: 60 * time.Second},
	}, nil
}

type weKnoraCloudEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
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
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("weknoracloud embedder: marshal: %w", err)
	}

	requestID := uuid.New().String()
	headers := utils.Sign(e.appID, e.apiKey, requestID, string(bodyBytes))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+weKnoraCloudEmbedPath, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("weknoracloud embedder: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := e.client.Do(req)
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
	for _, item := range embedResp.Data {
		if item.Index < len(result) {
			result[item.Index] = item.Embedding
		}
	}
	return result, nil
}

func (e *WeKnoraCloudEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
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
