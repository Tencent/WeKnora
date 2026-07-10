package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// InitializationConfig is the WRITE payload for InitializeByKB / UpdateKBConfig
// (the server's write endpoint accepts these flat model ids). It is NOT the
// shape the read endpoint returns — see KBModelConfigView / GetInitializationConfig.
type InitializationConfig struct {
	ChatModelID      string `json:"chat_model_id,omitempty"`
	EmbeddingModelID string `json:"embedding_model_id,omitempty"`
	RerankModelID    string `json:"rerank_model_id,omitempty"`
	MultimodalID     string `json:"multimodal_id,omitempty"`
}

// KBModelConfigView is the secret-free, read-only model configuration of a
// knowledge base, returned by GetInitializationConfig. The server's read
// response nests config under embedding/llm/rerank/multimodal and INCLUDES
// provider apiKey/baseUrl (for the web config form); this view intentionally
// parses only the non-secret fields, so credentials can never leak through the
// CLI. Field tags are snake_case (the CLI envelope convention), remapped from
// the server's camelCase.
type KBModelConfigView struct {
	RetrievalReady bool                  `json:"retrieval_ready"` // embedding model bound → KB can embed/retrieve
	Embedding      ModelSlotView         `json:"embedding"`
	LLM            ModelSlotView         `json:"llm"`
	Rerank         RerankSlotView        `json:"rerank"`
	Multimodal     MultimodalSlotView    `json:"multimodal"`
}

// ModelSlotView is one non-secret model slot (embedding / llm).
type ModelSlotView struct {
	Configured bool   `json:"configured"`
	ModelName  string `json:"model_name,omitempty"`
	Source     string `json:"source,omitempty"`
	Dimension  int    `json:"dimension,omitempty"`
}

// RerankSlotView is the rerank slot (may be disabled).
type RerankSlotView struct {
	Enabled   bool   `json:"enabled"`
	ModelName string `json:"model_name,omitempty"`
}

// MultimodalSlotView reports whether multimodal processing is enabled.
type MultimodalSlotView struct {
	Enabled bool `json:"enabled"`
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

// GetInitializationConfig returns a knowledge base's model configuration as a
// secret-free KBModelConfigView. The server response nests config under
// embedding/llm/rerank/multimodal and includes provider apiKey/baseUrl; this
// parses ONLY the non-secret fields (apiKey/baseUrl are never read into the
// struct, so they cannot leak through the CLI) and remaps to snake_case.
func (c *Client) GetInitializationConfig(ctx context.Context, kbID string) (*KBModelConfigView, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/initialization/config/%s", kbID), nil, nil)
	if err != nil {
		return nil, err
	}
	// Deliberately model only non-secret fields; apiKey / baseUrl in the server
	// payload are ignored by omission.
	var result struct {
		Data struct {
			Embedding struct {
				Source    string `json:"source"`
				ModelName string `json:"modelName"`
				Dimension int    `json:"dimension"`
			} `json:"embedding"`
			LLM struct {
				Source    string `json:"source"`
				ModelName string `json:"modelName"`
			} `json:"llm"`
			Rerank struct {
				Enabled   bool   `json:"enabled"`
				ModelName string `json:"modelName"`
			} `json:"rerank"`
			Multimodal struct {
				Enabled bool `json:"enabled"`
			} `json:"multimodal"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	d := result.Data
	view := &KBModelConfigView{
		RetrievalReady: d.Embedding.ModelName != "",
		Embedding:      ModelSlotView{Configured: d.Embedding.ModelName != "", ModelName: d.Embedding.ModelName, Source: d.Embedding.Source, Dimension: d.Embedding.Dimension},
		LLM:            ModelSlotView{Configured: d.LLM.ModelName != "", ModelName: d.LLM.ModelName, Source: d.LLM.Source},
		Rerank:         RerankSlotView{Enabled: d.Rerank.Enabled, ModelName: d.Rerank.ModelName},
		Multimodal:     MultimodalSlotView{Enabled: d.Multimodal.Enabled},
	}
	return view, nil
}

// InitializeByKB initializes a knowledge base with model configuration
func (c *Client) InitializeByKB(ctx context.Context, kbID string, config *InitializationConfig) error {
	resp, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/initialization/initialize/%s", kbID), config, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// UpdateKBConfig updates the model configuration for a knowledge base.
//
// Deprecated: the PUT /initialization/config endpoint binds KBModelConfigRequest
// (fields llmModelId / embeddingModelId), not InitializationConfig, so this
// method sends a shape the server rejects. Use SetKBModelConfig instead.
func (c *Client) UpdateKBConfig(ctx context.Context, kbID string, config *InitializationConfig) error {
	resp, err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/api/v1/initialization/config/%s", kbID), config, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// KBModelConfig points a knowledge base at already-registered models. Field
// names match the server's KBModelConfigRequest (PUT
// /initialization/config/:kbId). LLMModelID is required server-side;
// EmbeddingModelID is optional (omitted when RAG indexing is disabled).
type KBModelConfig struct {
	LLMModelID       string `json:"llmModelId"`
	EmbeddingModelID string `json:"embeddingModelId,omitempty"`
}

// SetKBModelConfig binds a knowledge base to already-registered models via PUT
// /initialization/config/:kbId. Register models first with CreateModel; the
// server rejects unknown model ids and refuses to change the embedding model of
// a KB that already has documents.
func (c *Client) SetKBModelConfig(ctx context.Context, kbID string, cfg *KBModelConfig) error {
	resp, err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/api/v1/initialization/config/%s", kbID), cfg, nil)
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
