package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

func scopeFailure(ctx context.Context, operation string, scope operations.Scope) *types.ToolResult {
	logger.Infof(ctx, "[Tool][Scope] Refused operation=%s scope=%s", operation, scope.Level)
	object := "no verified page context"
	if scope.KnowledgeBaseID != "" {
		object = "knowledge base " + scope.KnowledgeBaseID
	}
	if scope.Level == types.AssistantScopePage {
		switch {
		case scope.AgentID != "":
			object = "agent " + scope.AgentID
		case scope.KnowledgeID != "":
			object = "document " + scope.KnowledgeID
		case scope.WikiSlug != "":
			object = "Wiki page " + scope.WikiSlug
		}
	}
	return operationFailure(fmt.Sprintf(
		"%s is refused by assistant scope %s (limited to %s). The user can widen the scope in the assistant panel.",
		operation, scope.Level, object))
}

func (t *RunOperationTool) checkScope(
	ctx context.Context, op operations.Operation, params map[string]json.RawMessage, identity operations.Identity,
) *types.ToolResult {
	scope := t.rt.Scope
	scope.Level = scope.Level.WithDefault()
	if scope.Level == types.AssistantScopeWorkspace {
		return nil
	}
	if !scope.Level.Valid() || !t.inScope(ctx, op, params, identity, scope) {
		return scopeFailure(ctx, op.ID, scope)
	}
	return nil
}

func (t *RunOperationTool) inScope(
	ctx context.Context, op operations.Operation, params map[string]json.RawMessage,
	identity operations.Identity, scope operations.Scope,
) bool {
	var docs []string
	var agentID, slug string
	resource := false
	kbParam := false
	for _, p := range op.Params {
		kind, known := op.ScopeParam(p.Name)
		if !known {
			if operations.IsScopeIdentifier(p.Name) {
				return false
			}
			continue
		}
		if kind == operations.ScopeParamReference {
			continue
		}
		resource = true
		value, _ := rawString(params, p.Name)
		value = strings.TrimSpace(value)
		switch kind {
		case operations.ScopeParamKnowledgeBase:
			kbParam = true
			if value == "" || value != scope.KnowledgeBaseID {
				return false
			}
		case operations.ScopeParamKnowledge:
			if value == "" {
				return false
			}
			docs = append(docs, value)
		case operations.ScopeParamKnowledgeList:
			ids, _ := rawStringList(params, p.Name)
			if len(ids) == 0 || len(ids) > p.ListLimit() {
				return false
			}
			for _, id := range ids {
				docs = append(docs, strings.TrimSpace(id))
			}
		case operations.ScopeParamAgent:
			agentID = value
		case operations.ScopeParamWiki:
			slug = value
		case operations.ScopeParamChild:
			if _, ok := op.Param("knowledge_base_id"); !ok {
				return false
			}
		}
	}
	if !resource {
		return !op.Risk.Mutates()
	}
	if agentID != "" {
		return scope.Level == types.AssistantScopePage && agentID == scope.AgentID && !kbParam && len(docs) == 0
	}
	if scope.KnowledgeBaseID == "" || (scope.Level == types.AssistantScopePage && scope.AgentID != "") {
		return false
	}
	if op.Risk.Mutates() && scope.Level == types.AssistantScopePage {
		switch {
		case scope.KnowledgeID != "":
			if len(docs) == 0 || slug != "" {
				return false
			}
			for _, id := range docs {
				if id != scope.KnowledgeID {
					return false
				}
			}
		case scope.WikiSlug != "":
			if slug != scope.WikiSlug || len(docs) > 0 {
				return false
			}
		}
	}
	for _, id := range docs {
		if !t.documentInScope(ctx, identity, id, scope.KnowledgeBaseID) {
			return false
		}
	}
	return kbParam || len(docs) > 0
}

func (t *RunOperationTool) documentInScope(
	ctx context.Context, identity operations.Identity, id, kbID string,
) bool {
	get, ok := t.rt.Catalog.Get("knowledge.get")
	if !ok {
		return false
	}
	raw, _ := json.Marshal(id)
	call, err := get.Build(map[string]json.RawMessage{"knowledge_id": raw})
	if err != nil {
		return false
	}
	identity.Agent.Operation = get.ID
	resp, err := t.rt.Dispatcher.Dispatch(ctx, identity, call)
	if err != nil || resp.Status < 200 || resp.Status >= 300 {
		return false
	}
	projected, err := get.Project(resp.Body)
	return err == nil && projected.Label("knowledge_base_id") == kbID
}
