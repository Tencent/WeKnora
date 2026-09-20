package rerank

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/cohererank"
	"github.com/Tencent/WeKnora/internal/models/api/dashscoperank"
	"github.com/Tencent/WeKnora/internal/models/api/nimrerank"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	// catalog.Resolve answers from the vendor catalog, which is empty until
	// the vendor packages have run their init. Without this import every row
	// resolves to the generic vendor: LKEAP and Volcengine would lose their
	// signed clients and Aliyun its native protocol.
	_ "github.com/Tencent/WeKnora/internal/models/vendors"
	"github.com/Tencent/WeKnora/internal/types"
)

// Reranker defines the interface for document reranking
type Reranker interface {
	// Rerank reranks documents based on relevance to the query
	Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error)

	// GetModelName returns the model name
	GetModelName() string

	// GetModelID returns the model ID
	GetModelID() string
}

type RankResult struct {
	Index          int          `json:"index"`
	Document       DocumentInfo `json:"document"`
	RelevanceScore float64      `json:"relevance_score"`
}

// Handles the RelevanceScore field by checking if RelevanceScore exists first, otherwise falls back to Score field
func (r *RankResult) UnmarshalJSON(data []byte) error {
	var temp struct {
		Index          int          `json:"index"`
		Document       DocumentInfo `json:"document"`
		RelevanceScore *float64     `json:"relevance_score"`
		Score          *float64     `json:"score"`
	}

	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal rank result: %w", err)
	}

	r.Index = temp.Index
	r.Document = temp.Document

	if temp.RelevanceScore != nil {
		r.RelevanceScore = *temp.RelevanceScore
	} else if temp.Score != nil {
		r.RelevanceScore = *temp.Score
	}

	return nil
}

type DocumentInfo struct {
	Text string `json:"text"`
}

// UnmarshalJSON handles both string and object formats for DocumentInfo
func (d *DocumentInfo) UnmarshalJSON(data []byte) error {
	// First try to unmarshal as a string
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		d.Text = text
		return nil
	}

	// If that fails, try to unmarshal as an object with text field
	var temp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal DocumentInfo: %w", err)
	}

	d.Text = temp.Text
	return nil
}

type RerankerConfig struct {
	APIKey      string
	BaseURL     string
	ModelName   string
	Source      types.ModelSource
	ModelID     string
	Provider    string // Provider identifier: openai, aliyun, zhipu, siliconflow, jina, generic
	ExtraConfig map[string]string
	// CustomHeaders 允许在调用远程 API 时附加自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
	CustomHeaders map[string]string
	AppID         string
	AppSecret     string // 加密值，工厂函数调用方传入，使用前已解密
}

// ConfigFromModel 根据 types.Model 构造 RerankerConfig。
// 生产路径（从 DB 拉起）和测试连接路径（临时表单）共享这份映射。
// appID / appSecret 是已解密的 WeKnoraCloud 凭证，调用方负责传入。
func ConfigFromModel(m *types.Model, appID, appSecret string) *RerankerConfig {
	if m == nil {
		return nil
	}
	return &RerankerConfig{
		ModelID:       m.ID,
		APIKey:        m.Parameters.APIKey,
		BaseURL:       m.Parameters.BaseURL,
		ModelName:     m.Name,
		Source:        m.Source,
		Provider:      m.Parameters.Provider,
		ExtraConfig:   m.Parameters.ExtraConfig,
		CustomHeaders: m.Parameters.CustomHeaders,
		AppID:         appID,
		AppSecret:     appSecret,
	}
}

// NewReranker creates a reranker based on the configuration
func NewReranker(config *RerankerConfig) (Reranker, error) {
	r, err := newReranker(config)
	if err != nil {
		return r, err
	}
	if logger.LLMDebugEnabled() {
		r = &debugReranker{inner: r}
	}
	return wrapRerankerLangfuse(r, nil)
}

// newReranker resolves the catalog and returns the protocol client for the
// configured model, wrapped in the shared batching and score-scaling layer.
// It mirrors chat.NewRemoteChat: the vendor's facts decide the protocol, the
// URL and the credential, and this function knows no vendor names.
func newReranker(config *RerankerConfig) (Reranker, error) {
	if config == nil {
		return nil, fmt.Errorf("rerank config is nil")
	}
	resolved, err := catalog.Resolve(catalog.Ref{
		Provider:  config.Provider,
		Model:     config.ModelName,
		BaseURL:   config.BaseURL,
		ModelType: types.ModelTypeRerank,
		Extra:     config.ExtraConfig,
	})
	if err != nil {
		return nil, err
	}
	if err := validateRerankBaseURL(resolved.BaseURL); err != nil {
		return nil, err
	}

	vendor := resolved.Vendor
	creds := catalog.Credentials{APIKey: config.APIKey, AppID: config.AppID, AppSecret: config.AppSecret}
	if creds.APIKey == "" {
		creds.APIKey = vendor.DefaultAPIKey
	}
	// A signing vendor with no identity pair would otherwise send unsigned
	// requests and fail at the far end, which is a worse error than this one.
	if vendor.Auth == catalog.AuthSigned {
		if creds.AppID == "" {
			return nil, fmt.Errorf("%s rerank: AppID is required", vendor.Name)
		}
		if creds.AppSecret == "" {
			return nil, fmt.Errorf("%s rerank: AppSecret is required", vendor.Name)
		}
	}
	// Vendor headers first so a user header cannot silently replace a vendor
	// beta flag, matching chat.NewRemoteChat.
	headers := make(map[string]string, len(vendor.Headers)+len(config.CustomHeaders))
	for k, v := range vendor.Headers {
		headers[k] = v
	}
	for k, v := range config.CustomHeaders {
		headers[k] = v
	}
	endpoint := api.Endpoint{
		BaseURL: resolved.BaseURL,
		Model:   resolved.RemoteModel,
		ModelID: config.ModelID,
		Auth:    vendor.AuthFunc(vendor.API, creds),
		Headers: headers,
		// Most vendors have always run without a client deadline here and let
		// the caller's context govern; the ones that declare a timeout get it.
		Client: newRerankHTTPClient(
			time.Duration(resolved.Rerank.RequestTimeout) * time.Second,
		),
	}
	if vendor.Endpoint != nil {
		if url, query := vendor.Endpoint(catalog.EndpointRequest{
			BaseURL:   resolved.BaseURL,
			Model:     resolved.RemoteModel,
			ModelType: types.ModelTypeRerank,
			Extra:     config.ExtraConfig,
		}); url != "" {
			endpoint.URL, endpoint.Query = url, query
		}
	}

	var client api.Reranker
	switch resolved.RerankAPI {
	case api.RerankCohere:
		client = cohererank.New(cohererank.Config{Endpoint: endpoint, Settings: resolved.Rerank})
	case api.RerankDashScope:
		client = dashscoperank.New(dashscoperank.Config{Endpoint: endpoint, Settings: resolved.Rerank})
	case api.RerankNIM:
		client = nimrerank.New(nimrerank.Config{Endpoint: endpoint, Settings: resolved.Rerank})
	case api.RerankTencentLKEAP:
		client, err = newLKEAPClient(config, resolved)
	case api.RerankVolcengineKnowledge:
		client, err = newVolcengineClient(config, resolved)
	default:
		return nil, fmt.Errorf("unsupported rerank api %q for provider %s", resolved.RerankAPI, vendor.ID)
	}
	if err != nil {
		return nil, err
	}

	return &protocolReranker{
		inner:     client,
		settings:  resolved.Rerank,
		endpoint:  resolved.BaseURL,
		modelName: config.ModelName,
		modelID:   config.ModelID,
	}, nil
}
