// Package client provides the implementation for interacting with the WeKnora API
// The Model related interfaces are used to manage models for different tasks
// Models can be created, retrieved, updated, deleted, and queried
package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// ModelType represents the type of AI model
type ModelType string

const (
	ModelTypeEmbedding   ModelType = "Embedding"   // Embedding model
	ModelTypeRerank      ModelType = "Rerank"      // Rerank model
	ModelTypeKnowledgeQA ModelType = "KnowledgeQA" // KnowledgeQA model
	ModelTypeVLLM        ModelType = "VLLM"        // VLLM model
	ModelTypeASR         ModelType = "ASR"         // ASR (Automatic Speech Recognition) model
)

// ModelSource represents the source of the model
type ModelSource string

const (
	ModelSourceLocal       ModelSource = "local"        // Local model
	ModelSourceRemote      ModelSource = "remote"       // Remote model
	ModelSourceAliyun      ModelSource = "aliyun"       // Aliyun DashScope model
	ModelSourceZhipu       ModelSource = "zhipu"        // Zhipu model
	ModelSourceVolcengine  ModelSource = "volcengine"   // Volcengine model
	ModelSourceDeepseek    ModelSource = "deepseek"     // Deepseek model
	ModelSourceHunyuan     ModelSource = "hunyuan"      // Hunyuan model
	ModelSourceMinimax     ModelSource = "minimax"      // Minimax mode
	ModelSourceOpenAI      ModelSource = "openai"       // OpenAI model
	ModelSourceGemini      ModelSource = "gemini"       // Gemini model
	ModelSourceMimo        ModelSource = "mimo"         // Mimo model
	ModelSourceSiliconFlow ModelSource = "siliconflow"  // SiliconFlow model
	ModelSourceJina        ModelSource = "jina"         // Jina AI model
	ModelSourceOpenRouter  ModelSource = "openrouter"   // OpenRouter model
	ModelSourceNvidia      ModelSource = "nvidia"       // NVIDIA model
	ModelSourceNovita      ModelSource = "novita"       // Novita AI model
	ModelSourceAzureOpenAI ModelSource = "azure_openai" // Azure OpenAI model
)

// ModelParameters model parameters
type ModelParameters map[string]interface{}

// Model model information
type Model struct {
	ID          string          `json:"id"`
	TenantID    uint            `json:"tenant_id"`
	Name        string          `json:"name"`
	DisplayName string          `json:"display_name"`
	Type        ModelType       `json:"type"`
	Source      ModelSource     `json:"source"`
	Description string          `json:"description"`
	Parameters  ModelParameters `json:"parameters"`
	IsDefault   bool            `json:"is_default"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// CreateModelRequest model creation request
type CreateModelRequest struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"display_name"`
	Type        ModelType       `json:"type"`
	Source      ModelSource     `json:"source"`
	Description string          `json:"description"`
	Parameters  ModelParameters `json:"parameters"`
	IsDefault   bool            `json:"is_default"`
}

// UpdateModelRequest model update request
type UpdateModelRequest struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"display_name"`
	Description string          `json:"description"`
	Parameters  ModelParameters `json:"parameters"`
	IsDefault   bool            `json:"is_default"`
}

// ModelResponse model response
type ModelResponse struct {
	Success bool  `json:"success"`
	Data    Model `json:"data"`
}

// ModelListResponse model list response
type ModelListResponse struct {
	Success bool    `json:"success"`
	Data    []Model `json:"data"`
}

// CreateModel creates a model
func (c *Client) CreateModel(ctx context.Context, request *CreateModelRequest) (*Model, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/models", request, nil)
	if err != nil {
		return nil, err
	}

	var response ModelResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// GetModel gets a model
func (c *Client) GetModel(ctx context.Context, modelID string) (*Model, error) {
	path := fmt.Sprintf("/api/v1/models/%s", modelID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response ModelResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// ListModels lists all models
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/models", nil, nil)
	if err != nil {
		return nil, err
	}

	var response ModelListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

// UpdateModel updates a model
func (c *Client) UpdateModel(ctx context.Context, modelID string, request *UpdateModelRequest) (*Model, error) {
	path := fmt.Sprintf("/api/v1/models/%s", modelID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, request, nil)
	if err != nil {
		return nil, err
	}

	var response ModelResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// DeleteModel deletes a model
func (c *Client) DeleteModel(ctx context.Context, modelID string) error {
	path := fmt.Sprintf("/api/v1/models/%s", modelID)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}

	return parseResponse(resp, &response)
}

// ModelProvider represents a model provider with its supported types and default URLs
type ModelProvider struct {
	Value       string            `json:"value"`
	Label       string            `json:"label"`
	Description string            `json:"description"`
	DefaultURLs map[string]string `json:"defaultUrls"`
	ModelTypes  []string          `json:"modelTypes"`
}

// ModelProviderListResponse represents the API response for listing model providers
type ModelProviderListResponse struct {
	Success bool            `json:"success"`
	Data    []ModelProvider `json:"data"`
}

// ListModelProviders retrieves the list of supported model providers.
// modelType is optional and can be used to filter by type: "chat", "embedding", "rerank", "vllm".
func (c *Client) ListModelProviders(ctx context.Context, modelType string) ([]ModelProvider, error) {
	var queryParams url.Values
	if modelType != "" {
		queryParams = url.Values{}
		queryParams.Add("model_type", modelType)
	}

	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/models/providers", nil, queryParams)
	if err != nil {
		return nil, err
	}

	var response ModelProviderListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}
