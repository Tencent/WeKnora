package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// WebSearchConfig represents the web search configuration for a tenant
type WebSearchConfig struct {
	// Deprecated: Use WebSearchProviderEntity.Parameters.APIKey instead.
	Provider string `json:"provider,omitempty"`
	// Deprecated: Use WebSearchProviderEntity.Parameters.APIKey instead.
	APIKey string `json:"api_key,omitempty"`

	MaxResults        int      `json:"max_results"`        // 最大搜索结果数
	IncludeDate       bool     `json:"include_date"`       // 是否包含日期
	CompressionMethod string   `json:"compression_method"` // 压缩方法：none, summary, extract, rag
	Blacklist         []string `json:"blacklist"`          // 黑名单规则列表
	// RAG压缩相关配置
	EmbeddingModelID   string `json:"embedding_model_id,omitempty"`  // 嵌入模型ID（用于RAG压缩）
	EmbeddingDimension int    `json:"embedding_dimension,omitempty"` // 嵌入维度（用于RAG压缩）
	RerankModelID      string `json:"rerank_model_id,omitempty"`     // 重排模型ID（用于RAG压缩）
	DocumentFragments  int    `json:"document_fragments,omitempty"`  // 文档片段数量（用于RAG压缩）
}

// Value implements driver.Valuer interface for WebSearchConfig
func (c WebSearchConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements sql.Scanner interface for WebSearchConfig
func (c *WebSearchConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// WebSearchResult represents a single web search result
type WebSearchResult struct {
	Title       string     `json:"title"`                  // 搜索结果标题
	URL         string     `json:"url"`                    // 结果URL
	Snippet     string     `json:"snippet"`                // 摘要片段
	Content     string     `json:"content"`                // 完整内容（可选，需要额外抓取）
	Source      string     `json:"source"`                 // 来源（如：duckduckgo等）
	PublishedAt *time.Time `json:"published_at,omitempty"` // 发布时间（如果有）
}

// WebSearchProviderInfo represents information about a web search provider
type WebSearchProviderInfo struct {
	ID             string `json:"id"`                // 提供商ID
	Name           string `json:"name"`              // 提供商名称
	Free           bool   `json:"free"`              // 是否免费
	RequiresAPIKey bool   `json:"requires_api_key"`  // 是否需要API密钥
	Description    string `json:"description"`       // 描述
	APIURL         string `json:"api_url,omitempty"` // API地址（可选）
}
