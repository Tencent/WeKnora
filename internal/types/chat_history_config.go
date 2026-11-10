package types

import (
	"database/sql/driver"
	"encoding/json"
)

// ChatHistoryConfig represents the chat history knowledge base configuration for a tenant.
// This config is managed via the settings UI and controls how chat messages are indexed
// and searched using a knowledge base for vector search.
//
// The KnowledgeBaseID is auto-managed: when the user enables the feature and picks an
// embedding model, the backend automatically creates (or reuses) a hidden KB.
// Users do NOT pick a KB themselves.
type ChatHistoryConfig struct {
	// Enabled controls whether chat history indexing is active
	Enabled bool `json:"enabled"`
	// EmbeddingModelID is the ID of the embedding model used for vectorizing chat messages.
	// Once messages have been indexed, the model cannot be changed (requires re-indexing).
	EmbeddingModelID string `json:"embedding_model_id"`
	// KnowledgeBaseID is the auto-managed hidden knowledge base for chat history.
	// This is set internally when the feature is first enabled; users should not set this directly.
	KnowledgeBaseID string `json:"knowledge_base_id"`
}

// Value implements the driver.Valuer interface for database serialization
func (c ChatHistoryConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for database deserialization
func (c *ChatHistoryConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// IsConfigured returns true if the chat history KB is properly configured and ready to use.
// Requires: enabled + embedding model selected + KB auto-created.
func (c *ChatHistoryConfig) IsConfigured() bool {
	return c != nil && c.Enabled && c.EmbeddingModelID != "" && c.KnowledgeBaseID != ""
}
