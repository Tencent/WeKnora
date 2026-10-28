package types

import (
	"database/sql/driver"
	"encoding/json"
	"slices"
	"strings"
)

// DefaultSettings is the workspace's choice of default models, stored as
// JSONB on the tenant. An empty field leaves the slot to the deployment's
// default (the is_default flag a built-in model YAML sets).
//
// The default web search provider is not stored here: providers carry their
// own is_default flag, which the provider page already edits.
type DefaultSettings struct {
	// ChatModelID is the default KnowledgeQA model.
	ChatModelID string `json:"chat_model_id"`
	// UtilityModelID is the KnowledgeQA model for short generation tasks
	// such as writing MCP usage instructions. Empty follows the chat default.
	UtilityModelID string `json:"utility_model_id"`
	// EmbeddingModelID is the default Embedding model.
	EmbeddingModelID string `json:"embedding_model_id"`
	// RerankModelID is the default Rerank model.
	RerankModelID string `json:"rerank_model_id"`
	// VLMModelID is the default VLLM or vision-capable KnowledgeQA model.
	VLMModelID string `json:"vlm_model_id"`
	// ASRModelID is the default ASR model.
	ASRModelID string `json:"asr_model_id"`
}

// Normalize trims every ID.
func (c *DefaultSettings) Normalize() {
	c.ChatModelID = strings.TrimSpace(c.ChatModelID)
	c.UtilityModelID = strings.TrimSpace(c.UtilityModelID)
	c.EmbeddingModelID = strings.TrimSpace(c.EmbeddingModelID)
	c.RerankModelID = strings.TrimSpace(c.RerankModelID)
	c.VLMModelID = strings.TrimSpace(c.VLMModelID)
	c.ASRModelID = strings.TrimSpace(c.ASRModelID)
}

// ModelSlots maps each model-type slot to the capability its model must support. The
// utility slot is a KnowledgeQA model chosen for a purpose rather than a
// type, so it is listed separately by callers that validate it.
func (c *DefaultSettings) ModelSlots() map[ModelType]string {
	if c == nil {
		return nil
	}
	return map[ModelType]string{
		ModelTypeKnowledgeQA: c.ChatModelID,
		ModelTypeEmbedding:   c.EmbeddingModelID,
		ModelTypeRerank:      c.RerankModelID,
		ModelTypeVLLM:        c.VLMModelID,
		ModelTypeASR:         c.ASRModelID,
	}
}

// ApplyModelDefaults projects workspace defaults onto a fresh model list.
// IsDefault remains specific to the model's native type. DefaultFor also
// represents cross-type vision choices and the utility purpose. Missing or
// inactive choices leave deployment defaults in effect.
func (c *DefaultSettings) ApplyModelDefaults(models []*Model) {
	for modelType, id := range c.ModelSlots() {
		if id == "" || !hasActiveModel(models, id, modelType) {
			continue
		}
		for _, m := range models {
			if m == nil {
				continue
			}
			if m.ID == id {
				m.DefaultFor = append(m.DefaultFor, string(modelType))
			}
			if m.Type == modelType {
				m.IsDefault = m.ID == id
			}
		}
	}
	if c != nil && hasActiveModel(models, c.UtilityModelID, ModelTypeKnowledgeQA) {
		for _, m := range models {
			if m != nil && m.ID == c.UtilityModelID {
				m.DefaultFor = append(m.DefaultFor, "Utility")
			}
		}
	}
}

// SupportsType includes chat models that can also be used for vision.
func (m *Model) SupportsType(modelType ModelType) bool {
	return m.Type == modelType || (modelType == ModelTypeVLLM &&
		m.Type == ModelTypeKnowledgeQA && m.Parameters.SupportsVision)
}

// PickUtilityModel resolves background defaults from ListModels, including
// workers whose context carries only a tenant ID. An explicit task model is
// handled by the caller before falling back here.
func PickUtilityModel(models []*Model) *Model {
	for _, m := range models {
		if m != nil && m.Type == ModelTypeKnowledgeQA && m.Status == ModelStatusActive &&
			slices.Contains(m.DefaultFor, "Utility") {
			return m
		}
	}
	return PickDefaultModel(models, ModelTypeKnowledgeQA)
}

// PickDefaultModel prefers a workspace purpose default, then a deployment
// default of the native type, then the first usable model.
func PickDefaultModel(models []*Model, modelType ModelType) *Model {
	var first, deploymentDefault *Model
	for _, m := range models {
		if m == nil || !m.SupportsType(modelType) || m.Status != ModelStatusActive {
			continue
		}
		if slices.Contains(m.DefaultFor, string(modelType)) {
			return m
		}
		if deploymentDefault == nil && m.Type == modelType && m.IsDefault {
			deploymentDefault = m
		}
		if first == nil {
			first = m
		}
	}
	if deploymentDefault != nil {
		return deploymentDefault
	}
	return first
}

// PickChatModel returns the active KnowledgeQA model to use for a background
// task: preferredID when it is usable, else the default, else the first
// active one. Nil means the workspace has no active chat model.
func PickChatModel(models []*Model, preferredID string) *Model {
	if preferredID != "" && hasActiveModel(models, preferredID, ModelTypeKnowledgeQA) {
		for _, m := range models {
			if m != nil && m.ID == preferredID {
				return m
			}
		}
	}
	return PickDefaultModel(models, ModelTypeKnowledgeQA)
}

func hasActiveModel(models []*Model, id string, modelType ModelType) bool {
	for _, m := range models {
		if m != nil && m.ID == id && m.SupportsType(modelType) && m.Status == ModelStatusActive {
			return true
		}
	}
	return false
}

// Value implements driver.Valuer for JSON storage.
func (c DefaultSettings) Value() (driver.Value, error) { return json.Marshal(c) }

// Scan implements sql.Scanner for JSON storage.
func (c *DefaultSettings) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return nil
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, c)
}
