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
		return "高相关"
	case score >= 0.6:
		return "中相关"
	case score >= 0.4:
		return "低相关"
	default:
		return "弱相关"
	}
}

// FormatMatchType converts MatchType to a human-readable string
func FormatMatchType(mt types.MatchType) string {
	switch mt {
	case types.MatchTypeEmbedding:
		return "向量匹配"
	case types.MatchTypeKeywords:
		return "关键词匹配"
	case types.MatchTypeNearByChunk:
		return "相邻块匹配"
	case types.MatchTypeHistory:
		return "历史匹配"
	case types.MatchTypeParentChunk:
		return "父块匹配"
	case types.MatchTypeRelationChunk:
		return "关系块匹配"
	case types.MatchTypeGraph:
		return "图谱匹配"
	default:
		return fmt.Sprintf("未知类型(%d)", mt)
	}
}
