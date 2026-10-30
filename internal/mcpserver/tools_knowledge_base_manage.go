package mcpserver

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
)

// updateKnowledgeBaseTool exposes a deliberately small part of the settings
// surface. Model and storage bindings have additional owner-only rules and
// are left to the existing HTTP settings editor.
func updateKnowledgeBaseTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolUpdateKnowledgeBase,
		mcp.WithDescription("Update the name, description, Wiki/graph indexing switches, or FAQ index mode of one knowledge base. Omitted fields stay unchanged. Use list_knowledge_bases to find an allowed knowledge base first."),
		mcp.WithString("knowledge_base_id", mcp.Required(), mcp.Description("Knowledge base id or exact name")),
		mcp.WithString("name", mcp.Description("New name; omit to keep the current name")),
		mcp.WithString("description", mcp.Description("New description; an empty string clears it")),
		mcp.WithBoolean("wiki_enabled", mcp.Description("Enable or disable automatic Wiki indexing")),
		mcp.WithBoolean("graph_enabled", mcp.Description("Enable or disable knowledge-graph extraction")),
		mcp.WithString("faq_index_mode", mcp.Description("For FAQ knowledge bases only"),
			mcp.Enum(string(types.FAQIndexModeQuestionOnly), string(types.FAQIndexModeQuestionAnswer))),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
	)
}

func (s *Server) handleUpdateKnowledgeBase(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	selector, err := req.RequireString("knowledge_base_id")
	if err != nil || strings.TrimSpace(selector) == "" {
		return mcp.NewToolResultError("knowledge_base_id is required"), nil
	}
	kbs, err := s.selectKnowledgeBases(ctx, ep, []string{selector})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	kb := kbs[0]
	ctx, err = s.scopedKBContext(ctx, kb, types.OrgRoleEditor)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if scope, ok := types.TenantAPIKeyScopeFromContext(ctx); !ok ||
		!scope.HasCapability(types.APIKeyCapabilityManageKnowledgeBases) || !scope.AllowsKnowledgeBase(kb.ID) {
		return mcp.NewToolResultError("this endpoint is not allowed to manage knowledge base " + kb.ID), nil
	}

	args := req.GetArguments()
	changed := false
	name := kb.Name
	if raw, present := args["name"]; present {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return mcp.NewToolResultError("name must be a non-empty string"), nil
		}
		name = strings.TrimSpace(value)
		changed = true
	}
	description := kb.Description
	if raw, present := args["description"]; present {
		value, ok := raw.(string)
		if !ok {
			return mcp.NewToolResultError("description must be a string"), nil
		}
		description = value
		changed = true
	}
	config := &types.KnowledgeBaseConfig{ChunkingConfig: kb.ChunkingConfig}
	changedConfig := false
	strategy := kb.IndexingStrategy
	for _, field := range []struct {
		name   string
		target *bool
	}{
		{"wiki_enabled", &strategy.WikiEnabled},
		{"graph_enabled", &strategy.GraphEnabled},
	} {
		if raw, present := args[field.name]; present {
			if kb.Type != types.KnowledgeBaseTypeDocument {
				return mcp.NewToolResultError(field.name + " is only supported on a document knowledge base"), nil
			}
			value, ok := raw.(bool)
			if !ok {
				return mcp.NewToolResultError(field.name + " must be a boolean"), nil
			}
			*field.target = value
			changedConfig = true
			changed = true
		}
	}
	if changedConfig {
		config.IndexingStrategy = &strategy
	}
	if raw, present := args["faq_index_mode"]; present {
		mode, ok := raw.(string)
		if !ok || kb.Type != types.KnowledgeBaseTypeFAQ ||
			(mode != string(types.FAQIndexModeQuestionOnly) && mode != string(types.FAQIndexModeQuestionAnswer)) {
			return mcp.NewToolResultError("faq_index_mode must be question_only or question_answer on an FAQ knowledge base"), nil
		}
		faq := types.FAQConfig{}
		if kb.FAQConfig != nil {
			faq = *kb.FAQConfig
		}
		faq.IndexMode = types.FAQIndexMode(mode)
		config.FAQConfig = &faq
		changedConfig = true
		changed = true
	}
	if !changedConfig {
		config = nil
	}
	if !changed {
		return mcp.NewToolResultError("provide at least one setting to update"), nil
	}
	updated, err := s.kbService.UpdateKnowledgeBase(ctx, kb.ID, name, description, config, nil)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to update knowledge base", err), nil
	}
	return jsonResult(map[string]any{
		"knowledge_base":   summarizeKnowledgeBase(updated),
		"indexing_strategy": updated.IndexingStrategy,
		"faq_config":        updated.FAQConfig,
	})
}
