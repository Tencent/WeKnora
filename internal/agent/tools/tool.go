package tools

import (
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// BaseTool provides common functionality for tools
type BaseTool struct {
	name        string
	description string
	schema      json.RawMessage
}

// NewBaseTool creates a new base tool
func NewBaseTool(name, description string, schema json.RawMessage) BaseTool {
	return BaseTool{
		name:        name,
		description: description,
		schema:      schema,
	}
}

// Name returns the tool name
func (t *BaseTool) Name() string {
	return t.name
}

// Description returns the tool description
func (t *BaseTool) Description() string {
	return t.description
}

// Parameters returns the tool parameters schema
func (t *BaseTool) Parameters() json.RawMessage {
	return t.schema
}

// ToolExecutor is a helper interface for executing tools
type ToolExecutor interface {
	types.Tool

	// GetContext returns any context-specific data needed for tool execution
	GetContext() map[string]interface{}
}

// Shared helper functions for tool output formatting

// GetRelevanceLevel converts a score to a human-readable relevance level
func GetRelevanceLevel(score float64) string {
	switch {
	case score >= 0.8:
		return "High Relevance"
	case score >= 0.6:
		return "Medium Relevance"
	case score >= 0.4:
		return "Low Relevance"
	default:
		return "Weak Relevance"
	}
}

// FormatMatchType converts MatchType to a human-readable string
func FormatMatchType(mt types.MatchType) string {
	switch mt {
	case types.MatchTypeEmbedding:
		return "Vector Match"
	case types.MatchTypeKeywords:
		return "Keyword Match"
	case types.MatchTypeNearByChunk:
		return "Adjacent Chunk Match"
	case types.MatchTypeHistory:
		return "History Match"
	case types.MatchTypeParentChunk:
		return "Parent Chunk Match"
	case types.MatchTypeRelationChunk:
		return "Relation Chunk Match"
	case types.MatchTypeGraph:
		return "Graph Match"
	default:
		return fmt.Sprintf("Unknown Type(%d)", mt)
	}
}
