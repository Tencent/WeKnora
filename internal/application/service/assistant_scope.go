package service

import (
	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
)

func assistantOperationScope(level types.AssistantScope, page *agent.PageContextInfo) operations.Scope {
	scope := operations.Scope{Level: level.WithDefault()}
	if page == nil {
		return scope
	}
	scope.KnowledgeBaseID = page.KnowledgeBaseID
	if page.Document != nil {
		scope.KnowledgeID = page.Document.KnowledgeID
	}
	if page.WikiPage != nil {
		scope.WikiSlug = page.WikiPage.Slug
	}
	if page.Agent != nil {
		scope.AgentID = page.Agent.ID
	}
	return scope
}

// Direct retrieval and Wiki tools must not bypass the operation boundary.
func assistantToolConfig(
	config *types.AgentConfig, page *agent.PageContextInfo,
) (*types.AgentConfig, operations.Scope) {
	scope := operations.Scope{Level: config.AssistantScope.WithDefault()}
	if config.OperationsAgentID == "" || scope.Level == types.AssistantScopeWorkspace {
		return config, scope
	}
	scope = assistantOperationScope(scope.Level, page)
	narrowed := *config
	narrowed.SearchTargets = nil
	narrowed.KnowledgeBases = nil
	narrowed.KnowledgeIDs = nil
	narrowed.WritableKBIDs = nil
	kbID := scope.KnowledgeBaseID
	if scope.Level == types.AssistantScopePage && scope.AgentID != "" {
		kbID = ""
	}
	if kbID != "" {
		for _, target := range config.SearchTargets {
			if target != nil && target.KnowledgeBaseID == kbID {
				narrowed.SearchTargets = append(narrowed.SearchTargets, target)
				narrowed.KnowledgeIDs = append(narrowed.KnowledgeIDs, target.KnowledgeIDs...)
			}
		}
		narrowed.KnowledgeBases = []string{kbID}
		narrowed.WritableKBIDs = intersectStrings(config.WritableKBIDs, narrowed.KnowledgeBases)
	}
	return &narrowed, scope
}
