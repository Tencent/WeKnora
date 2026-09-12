// Package client provides the implementation for interacting with the WeKnora API
// The Model related interfaces are used to manage models for different tasks
// Models can be created, retrieved, updated, deleted, and queried
package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ModelType represents the type of AI model
type ModelType string

// Supported model types identify each model's application role.
const (
	ModelTypeEmbedding   ModelType = "Embedding"   // Embedding model
	ModelTypeRerank      ModelType = "Rerank"      // Rerank model
	ModelTypeKnowledgeQA ModelType = "KnowledgeQA" // KnowledgeQA model
	ModelTypeVLLM        ModelType = "VLLM"        // VLLM model
	ModelTypeASR         ModelType = "ASR"         // ASR (Automatic Speech Recognition) model
)

// AllModelTypes returns every model type the server recognises, in a stable
// order. Callers (CLI flag validation, docs) should use this instead of
// re-typing the string set, so they can't drift from the SDK.
func AllModelTypes() []ModelType {
	return []ModelType{
		ModelTypeEmbedding, ModelTypeRerank, ModelTypeKnowledgeQA, ModelTypeVLLM, ModelTypeASR,
	}
}

// ModelSource represents the source of the model
type ModelSource string

// Supported model sources identify local runtimes and remote providers.
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
	ModelSourceLiteLLM     ModelSource = "litellm"      // LiteLLM proxy model
	ModelSourceRequesty    ModelSource = "requesty"     // Requesty model
	ModelSourceNvidia      ModelSource = "nvidia"       // NVIDIA model
	ModelSourceNovita      ModelSource = "novita"       // Novita AI model
	ModelSourceAzureOpenAI ModelSource = "azure_openai" // Azure OpenAI model
)

// AllModelSources returns every model source the server recognises, in a stable
// order. This is the broad set used for FILTERING existing records (model
// list --source); creating a model only supports local/remote (the provider
// identity goes in ModelParameters.provider). Use this instead of re-typing
// the set so callers can't drift from the SDK.
func AllModelSources() []ModelSource {
	return []ModelSource{
		ModelSourceLocal, ModelSourceRemote, ModelSourceAliyun, ModelSourceZhipu,
		ModelSourceVolcengine, ModelSourceDeepseek, ModelSourceHunyuan, ModelSourceMinimax,
		ModelSourceOpenAI, ModelSourceGemini, ModelSourceMimo, ModelSourceSiliconFlow,
		ModelSourceJina, ModelSourceOpenRouter, ModelSourceLiteLLM, ModelSourceRequesty,
		ModelSourceNvidia, ModelSourceNovita,
		ModelSourceAzureOpenAI,
	}
}

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

// ModelUsageOptions selects a half-open UTC reporting interval and optional models.
type ModelUsageOptions struct {
	From     *time.Time
	To       *time.Time
	ModelIDs []string
}

// ModelCostTotal sums integer microcurrency amounts within one currency.
type ModelCostTotal struct {
	Currency       string `json:"currency"`
	CostMicrounits int64  `json:"cost_microunits"`
}

// ProviderCacheStatistics reports cache tokens observed in provider responses.
type ProviderCacheStatistics struct {
	ReadTokens     int64    `json:"read_tokens"`
	WriteTokens    int64    `json:"write_tokens"`
	MissTokens     int64    `json:"miss_tokens"`
	ObservedTokens int64    `json:"observed_tokens"`
	HitRate        *float64 `json:"hit_rate"`
}

// ApplicationCacheStatistics reports persistent embedding-cache lookups and item outcomes.
type ApplicationCacheStatistics struct {
	LookupCount             int64    `json:"lookup_count"`
	BypassLookupCount       int64    `json:"bypass_lookup_count"`
	RequestedItems          int64    `json:"requested_items"`
	UniqueItems             int64    `json:"unique_items"`
	HitItems                int64    `json:"hit_items"`
	MissItems               int64    `json:"miss_items"`
	BypassItems             int64    `json:"bypass_items"`
	ObservedItems           int64    `json:"observed_items"`
	HitRate                 *float64 `json:"hit_rate"`
	AverageLookupDurationMs float64  `json:"average_lookup_duration_ms"`
}

// ModelLatencyStatistics contains physical-call latency quantiles in milliseconds.
type ModelLatencyStatistics struct {
	P50Ms         *float64 `json:"p50_ms"`
	P95Ms         *float64 `json:"p95_ms"`
	P99Ms         *float64 `json:"p99_ms"`
	ReportedCalls int64    `json:"reported_calls"`
}

// ModelUsageStatistics aggregates the call ledger for one model.
type ModelUsageStatistics struct {
	ModelID                 string                     `json:"model_id"`
	CallCount               int64                      `json:"call_count"`
	SuccessCalls            int64                      `json:"success_calls"`
	ErrorCalls              int64                      `json:"error_calls"`
	CanceledCalls           int64                      `json:"canceled_calls"`
	UsageReportedCalls      int64                      `json:"usage_reported_calls"`
	UsageUnreportedCalls    int64                      `json:"usage_unreported_calls"`
	AccountingCompleteCalls int64                      `json:"accounting_complete_calls"`
	UnpricedCalls           int64                      `json:"unpriced_calls"`
	PromptTokens            int64                      `json:"prompt_tokens"`
	CompletionTokens        int64                      `json:"completion_tokens"`
	TotalTokens             int64                      `json:"total_tokens"`
	AverageDurationMs       float64                    `json:"average_duration_ms"`
	Latency                 ModelLatencyStatistics     `json:"latency"`
	Costs                   []ModelCostTotal           `json:"costs"`
	ProviderCache           ProviderCacheStatistics    `json:"provider_cache"`
	ApplicationCache        ApplicationCacheStatistics `json:"application_cache"`
}

// ModelUsageReport contains model aggregates over a bounded time range.
type ModelUsageReport struct {
	From  time.Time              `json:"from"`
	To    time.Time              `json:"to"`
	Items []ModelUsageStatistics `json:"items"`
}

// ModelPriceVersion freezes rates and their effective interval for one model.
type ModelPriceVersion struct {
	CachePricing               *ModelCachePricing `json:"cache_pricing,omitempty"`
	ID                         string             `json:"id"`
	TenantID                   uint64             `json:"tenant_id"`
	ModelID                    string             `json:"model_id"`
	ValidFrom                  time.Time          `json:"valid_from"`
	ValidTo                    *time.Time         `json:"valid_to,omitempty"`
	InputMicrounitsPerMillion  int64              `json:"input_microunits_per_million"`
	OutputMicrounitsPerMillion int64              `json:"output_microunits_per_million"`
	Currency                   string             `json:"currency"`
	CreatedAt                  time.Time          `json:"created_at"`
}

// PutModelPriceRequest appends an effective-dated model price.
type PutModelPriceRequest struct {
	CachePricing               *ModelCachePricing `json:"cache_pricing,omitempty"`
	ValidFrom                  time.Time          `json:"valid_from"`
	ValidTo                    *time.Time         `json:"valid_to,omitempty"`
	InputMicrounitsPerMillion  int64              `json:"input_microunits_per_million"`
	OutputMicrounitsPerMillion int64              `json:"output_microunits_per_million"`
	Currency                   string             `json:"currency"`
}

// ModelCachePricing distinguishes unknown cache rates from an explicit zero rate.
type ModelCachePricing struct {
	Version                     int    `json:"version"`
	ReadMicrounitsPerMillion    *int64 `json:"read_microunits_per_million"`
	Write5mMicrounitsPerMillion *int64 `json:"write_5m_microunits_per_million"`
	Write1hMicrounitsPerMillion *int64 `json:"write_1h_microunits_per_million"`
}

// ListModelUsage retrieves ledger aggregates for the selected time range.
func (c *Client) ListModelUsage(ctx context.Context, options ModelUsageOptions) (*ModelUsageReport, error) {
	return c.modelUsage(ctx, "/api/v1/models/usage", options)
}

// GetModelUsage retrieves ledger aggregates for one model.
func (c *Client) GetModelUsage(
	ctx context.Context,
	modelID string,
	options ModelUsageOptions,
) (*ModelUsageReport, error) {
	return c.modelUsage(ctx, fmt.Sprintf("/api/v1/models/%s/usage", url.PathEscape(modelID)), options)
}

func (c *Client) modelUsage(ctx context.Context, path string, options ModelUsageOptions) (*ModelUsageReport, error) {
	query := url.Values{}
	if options.From != nil {
		query.Set("from", options.From.UTC().Format(time.RFC3339))
	}
	if options.To != nil {
		query.Set("to", options.To.UTC().Format(time.RFC3339))
	}
	if len(options.ModelIDs) > 0 {
		query.Set("model_ids", strings.Join(options.ModelIDs, ","))
	}
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, query)
	if err != nil {
		return nil, err
	}
	var response struct {
		Success bool             `json:"success"`
		Data    ModelUsageReport `json:"data"`
	}
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}

// ListModelPrices retrieves the effective-dated price history of a model.
func (c *Client) ListModelPrices(ctx context.Context, modelID string) ([]ModelPriceVersion, error) {
	path := fmt.Sprintf("/api/v1/models/%s/pricing", url.PathEscape(modelID))
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Success bool                `json:"success"`
		Data    []ModelPriceVersion `json:"data"`
	}
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// PutModelPrice appends a price version through the application API.
func (c *Client) PutModelPrice(
	ctx context.Context,
	modelID string,
	request PutModelPriceRequest,
) (*ModelPriceVersion, error) {
	path := fmt.Sprintf("/api/v1/models/%s/pricing", url.PathEscape(modelID))
	resp, err := c.doRequest(ctx, http.MethodPut, path, request, nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Success bool              `json:"success"`
		Data    ModelPriceVersion `json:"data"`
	}
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}
