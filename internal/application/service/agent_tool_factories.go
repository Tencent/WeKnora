package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// toolBuildContext is what building one agent tool may need from the run.
type toolBuildContext struct {
	ctx               context.Context
	config            *types.AgentConfig
	rerankModel       rerank.Reranker
	sessionID         string
	wikiScopes        []tools.WikiScope
	wikiRoutes        *tools.WikiRouteResolver
	wikiKBIDs         []string
	writableWikiKBIDs []string
}

// toolFactory builds one builtin tool for a run.
type toolFactory func(s *agentService, b *toolBuildContext) types.Tool

// builtinToolFactories builds each tool an agent allowlist may name. Adding a
// builtin tool means adding its factory here; tools from plugins arrive as MCP
// tools and are registered separately (registerMCPTools).
var builtinToolFactories = map[string]toolFactory{
	tools.ToolThinking:  func(*agentService, *toolBuildContext) types.Tool { return tools.NewSequentialThinkingTool() },
	tools.ToolTodoWrite: func(*agentService, *toolBuildContext) types.Tool { return tools.NewTodoWriteTool() },
	tools.ToolSearchKnowledge: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewSearchKnowledgeTool(
			s.knowledgeBaseService, s.knowledgeService, s.chunkService, b.config.SearchTargets, b.rerankModel, s.cfg,
		)
	},
	tools.ToolReadDocument: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewReadDocumentTool(s.knowledgeService, s.chunkService, b.config.SearchTargets)
	},
	tools.ToolListDocuments: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewListDocumentsTool(s.knowledgeService, b.config.SearchTargets)
	},
	tools.ToolQueryKnowledgeGraph: func(s *agentService, b *toolBuildContext) types.Tool {
		var chunkRepo interfaces.ChunkRepository
		if s.chunkService != nil {
			chunkRepo = s.chunkService.GetRepository()
		}
		return tools.NewQueryKnowledgeGraphTool(s.knowledgeBaseService, b.config.SearchTargets).
			WithKnowledgeScope(s.knowledgeService).
			WithGraph(s.graphRepo, chunkRepo)
	},
	// The owner is captured from the caller's identity here, not read from the
	// model's arguments, so no prompt can redirect the search at somebody
	// else's conversations.
	tools.ToolSearchConversations: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewSearchConversationsTool(s.messageService, types.SessionOwnerIDFromContext(b.ctx), b.sessionID)
	},
	// Reaching this factory means the memory switches were already checked
	// where the tool is injected. The memory space is resolved from the
	// request context inside the service, so the tool takes no owner.
	tools.ToolSearchMemory: func(s *agentService, _ *toolBuildContext) types.Tool {
		return tools.NewSearchMemoryTool(s.memoryService)
	},
	tools.ToolDatabaseQuery: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewDatabaseQueryTool(s.db, b.config.SearchTargets)
	},
	tools.ToolWebSearch: func(s *agentService, b *toolBuildContext) types.Tool {
		logger.Infof(b.ctx, "Registered web_search tool for session: %s, maxResults: %d, providerID: %s",
			b.sessionID, b.config.WebSearchMaxResults, b.config.WebSearchProviderID)
		return tools.NewWebSearchTool(s.webSearchService, b.config.WebSearchMaxResults, b.config.WebSearchProviderID)
	},
	tools.ToolWebFetch: func(_ *agentService, b *toolBuildContext) types.Tool {
		logger.Infof(b.ctx, "Registered web_fetch tool for session: %s", b.sessionID)
		return tools.NewWebFetchTool()
	},
	tools.ToolDataAnalysis: func(s *agentService, b *toolBuildContext) types.Tool {
		logger.Infof(b.ctx, "Registered data_analysis tool for session: %s", b.sessionID)
		return tools.NewDataAnalysisTool(
			s.knowledgeBaseService, s.knowledgeService, s.tenantService, s.fileService, s.duckdb,
			b.sessionID, s.storageResolver,
		).WithSearchTargets(b.config.SearchTargets)
	},
	tools.ToolDataSchema: func(s *agentService, b *toolBuildContext) types.Tool {
		logger.Infof(b.ctx, "Registered data_schema tool")
		return tools.NewDataSchemaTool(s.knowledgeService, s.chunkService.GetRepository()).
			WithSearchTargets(b.config.SearchTargets)
	},

	// Wiki tools only make it into the allowlist when wiki KBs are in scope.
	tools.ToolWikiReadPage: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewWikiReadPageTool(s.wikiPageService, s.knowledgeService, b.wikiScopes, b.wikiRoutes)
	},
	tools.ToolWikiSearch: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewWikiSearchTool(s.wikiPageService, s.knowledgeService, b.wikiScopes, b.wikiRoutes)
	},
	tools.ToolWikiFlagIssue: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewWikiFlagIssueTool(s.wikiPageService, b.writableWikiKBIDs, b.wikiRoutes).
			WithKnowledgeScope(s.knowledgeService, b.config.SearchTargets)
	},
	tools.ToolWikiReadIssue: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewWikiReadIssueTool(s.wikiPageService, b.wikiKBIDs)
	},
	tools.ToolWikiUpdateIssue: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewWikiUpdateIssueTool(s.wikiPageService, b.writableWikiKBIDs)
	},
	tools.ToolWikiWritePage: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewWikiWritePageTool(s.wikiPageService, b.writableWikiKBIDs, s.knowledgeService, b.wikiRoutes).
			WithSearchTargets(b.config.SearchTargets)
	},
	tools.ToolWikiReplaceText: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewWikiReplaceTextTool(s.wikiPageService, b.writableWikiKBIDs, s.knowledgeService, b.wikiRoutes).
			WithSearchTargets(b.config.SearchTargets)
	},
	tools.ToolWikiRenamePage: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewWikiRenamePageTool(s.wikiPageService, b.writableWikiKBIDs, b.wikiRoutes)
	},
	tools.ToolWikiDeletePage: func(s *agentService, b *toolBuildContext) types.Tool {
		return tools.NewWikiDeletePageTool(s.wikiPageService, b.writableWikiKBIDs, b.wikiRoutes)
	},
}

// sandboxBoundTools are allowlist names bound to the resolved sandbox manager
// (registerSandboxFileTools / registerSandboxShellIfAllowed /
// initializeSkillsManager). They have no factory, and seeing them in the
// allowlist is expected rather than an unknown tool.
var sandboxBoundTools = map[string]bool{
	tools.ToolShellExec:                true,
	tools.ToolReadFile:                 true,
	tools.LegacyToolReadSkill:          true,
	tools.LegacyToolExecuteSkillScript: true,
	tools.ToolListSandboxFiles:         true,
	tools.LegacyToolReadSandboxFile:    true,
	tools.ToolWriteSandboxFile:         true,
	tools.ToolEditSandboxFile:          true,
}
