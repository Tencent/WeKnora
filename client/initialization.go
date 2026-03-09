package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// InitializationConfig represents the initialization configuration for a knowledge base
type InitializationConfig struct {
	ChatModelID      string `json:"chat_model_id,omitempty"`
	EmbeddingModelID string `json:"embedding_model_id,omitempty"`
	RerankModelID    string `json:"rerank_model_id,omitempty"`
	MultimodalID     string `json:"multimodal_id,omitempty"`
}

// OllamaModelInfo represents info about an Ollama model
type OllamaModelInfo struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

// DownloadTask represents an Ollama model download task
type DownloadTask struct {
	ID        string     `json:"id"`
	ModelName string     `json:"modelName"`
	Status    string     `json:"status"`
	Progress  float64    `json:"progress"`
	Message   string     `json:"message"`
	StartTime time.Time  `json:"startTime"`
	EndTime   *time.Time `json:"endTime,omitempty"`
}

// ModelCheckResult represents the result of checking a remote model
type ModelCheckResult struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// GetInitializationConfig gets the current initialization config for a knowledge base
func (c *Client) GetInitializationConfig(ctx context.Context, kbID string) (*InitializationConfig, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/initialization/config/%s", kbID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                  `json:"success"`
		Data    *InitializationConfig `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// InitializeByKB initializes a knowledge base with model configuration
func (c *Client) InitializeByKB(ctx context.Context, kbID string, config *InitializationConfig) error {
	resp, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/initialization/initialize/%s", kbID), config, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// UpdateKBConfig updates the model configuration for a knowledge base
func (c *Client) UpdateKBConfig(ctx context.Context, kbID string, config *InitializationConfig) error {
	resp, err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/api/v1/initialization/config/%s", kbID), config, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// CheckOllamaStatus checks if Ollama is running and accessible
func (c *Client) CheckOllamaStatus(ctx context.Context) (bool, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/initialization/ollama/status", nil, nil)
	if err != nil {
		return false, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Available bool `json:"available"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return false, err
	}
	return result.Data.Available, nil
}

// ListOllamaModels lists all locally available Ollama models
func (c *Client) ListOllamaModels(ctx context.Context) ([]OllamaModelInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/initialization/ollama/models", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    []OllamaModelInfo `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// CheckOllamaModels checks if specific Ollama models are available
func (c *Client) CheckOllamaModels(ctx context.Context, models []string) (map[string]bool, error) {
	req := map[string][]string{"models": models}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/ollama/models/check", req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool            `json:"success"`
		Data    map[string]bool `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// DownloadOllamaModel starts downloading an Ollama model
func (c *Client) DownloadOllamaModel(ctx context.Context, modelName string) (*DownloadTask, error) {
	req := map[string]string{"model": modelName}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/ollama/models/download", req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool          `json:"success"`
		Data    *DownloadTask `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// GetOllamaDownloadProgress gets the download progress of an Ollama model
func (c *Client) GetOllamaDownloadProgress(ctx context.Context, taskID string) (*DownloadTask, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/initialization/ollama/download/progress/%s", taskID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool          `json:"success"`
		Data    *DownloadTask `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListOllamaDownloadTasks lists all Ollama download tasks
func (c *Client) ListOllamaDownloadTasks(ctx context.Context) ([]*DownloadTask, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/initialization/ollama/download/tasks", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool            `json:"success"`
		Data    []*DownloadTask `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// CheckRemoteModel checks if a remote model API is accessible
func (c *Client) CheckRemoteModel(ctx context.Context, params map[string]string) (*ModelCheckResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/remote/check", params, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    *ModelCheckResult `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// TestEmbeddingModel tests an embedding model
func (c *Client) TestEmbeddingModel(ctx context.Context, params map[string]string) (*ModelCheckResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/embedding/test", params, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    *ModelCheckResult `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// CheckRerankModel checks if a rerank model is accessible
func (c *Client) CheckRerankModel(ctx context.Context, params map[string]string) (*ModelCheckResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/rerank/check", params, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    *ModelCheckResult `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// TestMultimodalFunction tests multimodal model functionality
func (c *Client) TestMultimodalFunction(ctx context.Context, params map[string]string) (*ModelCheckResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/multimodal/test", params, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    *ModelCheckResult `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ExtractTextRelations extracts text relations for knowledge graph
func (c *Client) ExtractTextRelations(ctx context.Context, params any) (json.RawMessage, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/initialization/extract/text-relation", params, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}
