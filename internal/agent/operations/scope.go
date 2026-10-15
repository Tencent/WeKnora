package operations

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// Scope contains only identifiers resolved from the server-verified page context.
type Scope struct {
	Level           types.AssistantScope
	KnowledgeBaseID string
	KnowledgeID     string
	WikiSlug        string
	AgentID         string
}

// ScopeParam describes how a parameter participates in the resource boundary.
type ScopeParam string

// Parameter roles distinguish mutation targets from configuration references.
const (
	ScopeParamKnowledgeBase ScopeParam = "knowledge_base"
	ScopeParamKnowledge     ScopeParam = "knowledge"
	ScopeParamKnowledgeList ScopeParam = "knowledge_list"
	ScopeParamAgent         ScopeParam = "agent"
	ScopeParamWiki          ScopeParam = "wiki"
	ScopeParamChild         ScopeParam = "kb_child"
	ScopeParamReference     ScopeParam = "reference"
)

var scopeParams = map[string]ScopeParam{
	"knowledge_base_id":        ScopeParamKnowledgeBase,
	"source_knowledge_base_id": ScopeParamKnowledgeBase,
	"knowledge_id":             ScopeParamKnowledge,
	"knowledge_ids":            ScopeParamKnowledgeList,
	"agent_id":                 ScopeParamAgent,
	"slug":                     ScopeParamWiki,
	"entry_id":                 ScopeParamChild,
	"entry_ids":                ScopeParamChild,
	"tag_ids":                  ScopeParamChild,
	"tag_seq_id":               ScopeParamChild,
	"folder_id":                ScopeParamChild,
	"parent_id":                ScopeParamChild,
}

// Agent bindings change the agent configuration, not the referenced resources.
// Model/MCP lookups and task progress reads have no page resource boundary.
var scopeParamExceptions = map[string]map[string]ScopeParam{
	"agent.update": {
		"knowledge_base_ids": ScopeParamReference,
		"model_id":           ScopeParamReference,
		"mcp_service_ids":    ScopeParamReference,
	},
	"model.get":                   {"model_id": ScopeParamReference},
	"mcp_service.get":             {"mcp_service_id": ScopeParamReference},
	"skill.list":                  {"sandbox_config_id": ScopeParamReference},
	"knowledge.get_move_progress": {"task_id": ScopeParamReference},
}

// ScopeParam recognizes declared resource roles; unknown identifiers must be refused.
func (op Operation) ScopeParam(name string) (ScopeParam, bool) {
	if kind, ok := scopeParamExceptions[op.ID][name]; ok {
		return kind, true
	}
	kind, ok := scopeParams[name]
	return kind, ok
}

// ListLimit is shared by request validation and bounded ownership lookups.
func (p Param) ListLimit() int {
	if p.MaxItems > 0 {
		return p.MaxItems
	}
	return defaultMaxListItems
}

// IsScopeIdentifier also covers identifiers not yet present in the catalog.
func IsScopeIdentifier(name string) bool {
	return name == "id" || name == "ids" || name == "slug" || strings.HasSuffix(name, "_id") ||
		strings.HasSuffix(name, "_ids") || strings.HasSuffix(name, "_slug")
}
