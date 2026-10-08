package tools

// maxFunctionNameLength is the maximum length for a tool/function name
// imposed by the OpenAI API.
const maxFunctionNameLength = 64

// Tool names constants
const (
	// Capability-scoped MCP discovery and invocation; not tenant-selectable builtins.
	ToolDiscoverMCPTools = "discover_mcp_tools"
	ToolCallMCPTool      = "call_mcp_tool"
	ToolThinking         = "thinking"
	ToolTodoWrite        = "todo_write"
	// Knowledge retrieval surface. search_knowledge covers semantic, keyword
	// and hybrid retrieval over chunk-indexed knowledge bases; read_document
	// reads a document's metadata and chunks (by page, by chunk handle, or by
	// an in-document text search); list_documents browses one knowledge base.
	ToolSearchKnowledge     = "search_knowledge"
	ToolReadDocument        = "read_document"
	ToolListDocuments       = "list_documents"
	ToolQueryKnowledgeGraph = "query_knowledge_graph"
	ToolSearchConversations = "search_conversations"
	ToolSearchMemory        = "search_memory"
	ToolDatabaseQuery       = "database_query"
	ToolDataAnalysis        = "data_analysis"
	ToolDataSchema          = "data_schema"
	ToolWebSearch           = "web_search"
	ToolWebFetch            = "web_fetch"
	// Unified reading: workspace access follows sandbox file capability;
	// skill resources follow SkillsEnabled and do not require a sandbox.
	ToolReadFile = "read_file"
	// Sandbox filesystem tools (only available when the sandbox backend
	// supports per-session files — Cube, E2B, Docker). list/read inspect
	// the session workspace; write creates text files so generated
	// scripts do not have to travel through a shell_exec heredoc; edit
	// patches an existing file without regenerating it.
	//
	// Deliberately absent from AvailableToolDefinitions and
	// DefaultAllowedTools, like search_memory and web_search: the sandbox
	// switch already decides whether a run has a workspace at all, and
	// registerSandboxFileTools registers these from that capability rather
	// than from the allowlist. A checkbox would have been a lie — clearing
	// it changed nothing.
	ToolListSandboxFiles = "list_sandbox_files"
	ToolWriteSandboxFile = "write_sandbox_file"
	ToolEditSandboxFile  = "edit_sandbox_file"
	// ToolWriteSkillFile / ToolEditSkillFile write the skill tree under
	// /opt/weknora/tenant/skills rather than /workspace, and exist only for
	// the built-in skill installer. They are scoped to the one skill being
	// installed; see internal/agent/tools/skill_file.go.
	//
	// Deliberately absent from AvailableToolDefinitions: these write the
	// shared snapshot image, so they are granted by install mode alone and
	// must not become selectable on a tenant-editable agent config.
	ToolWriteSkillFile = "write_skill_file"
	ToolEditSkillFile  = "edit_skill_file"
	// ToolShellExec lets the LLM execute ad-hoc shell commands inside the
	// current session's sandbox (dependency installs, environment probing).
	// Registered only when the resolved backend advertises the session shell
	// capability (Cube, E2B, Docker). The command never runs on the WeKnora host.
	//
	// Also absent from AvailableToolDefinitions: registerSandboxShellIfAllowed
	// keys it on SkillsEnabled (or install mode), so the shell follows the
	// skills switch and not a per-agent tool checkbox.
	ToolShellExec = "shell_exec"
	// Wiki-related tools (only available when wiki KBs are in scope)
	ToolWikiReadPage    = "wiki_read_page"
	ToolWikiWritePage   = "wiki_write_page"
	ToolWikiReplaceText = "wiki_replace_text"
	ToolWikiRenamePage  = "wiki_rename_page"
	ToolWikiDeletePage  = "wiki_delete_page"
	ToolWikiSearch      = "wiki_search"
	ToolWikiFlagIssue   = "wiki_flag_issue"
	ToolWikiReadIssue   = "wiki_read_issue"
	ToolWikiUpdateIssue = "wiki_update_issue"
)

// AvailableTool defines a simple tool metadata used by settings APIs.
type AvailableTool struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// AvailableToolDefinitions returns the list of tools exposed to the UI.
// Keep this in sync with registered tools in this package.
func AvailableToolDefinitions() []AvailableTool {
	return []AvailableTool{
		{Name: ToolThinking, Label: "思考", Description: "动态和反思性的问题解决思考工具"},
		{Name: ToolTodoWrite, Label: "制定计划", Description: "创建结构化的研究计划"},
		{Name: ToolSearchKnowledge, Label: "检索知识库", Description: "语义、关键词或混合检索知识库分块"},
		{Name: ToolReadDocument, Label: "阅读文档", Description: "读取文档元数据与分块内容，支持分页和文内查找"},
		{Name: ToolListDocuments, Label: "浏览文档列表", Description: "分页列出知识库中的文档"},
		{Name: ToolQueryKnowledgeGraph, Label: "查询知识图谱", Description: "从知识图谱中查询关系"},
		{
			Name:        ToolSearchConversations,
			Label:       "回顾历史对话",
			Description: "在用户自己的历史会话中查找之前聊过的内容",
		},
		{Name: ToolDatabaseQuery, Label: "查询数据库", Description: "查询数据库中的信息"},
		{Name: ToolDataAnalysis, Label: "数据分析", Description: "理解数据文件并进行数据分析"},
		{Name: ToolDataSchema, Label: "查看数据元信息", Description: "获取表格文件的元信息"},
		{Name: ToolWikiReadPage, Label: "读取Wiki页面", Description: "读取指定的Wiki页面内容"},
		{Name: ToolWikiSearch, Label: "搜索Wiki", Description: "在Wiki中搜索页面"},
		{Name: ToolWikiFlagIssue, Label: "标记Wiki问题", Description: "标记页面中存在的事实错误或合并冲突问题"},
		{Name: ToolWikiWritePage, Label: "创建/覆盖Wiki", Description: "创建新页面或完全覆盖已有页面"},
		{Name: ToolWikiReplaceText, Label: "局部替换Wiki", Description: "替换Wiki页面中的特定文本"},
		{Name: ToolWikiRenamePage, Label: "重命名Wiki", Description: "重命名Wiki页面并自动更新关联链接"},
		{Name: ToolWikiDeletePage, Label: "删除Wiki", Description: "删除Wiki页面并自动清理关联死链"},
		{Name: ToolWikiReadIssue, Label: "查看Wiki问题", Description: "查看特定的Wiki页面问题详情"},
		{Name: ToolWikiUpdateIssue, Label: "更新Wiki问题状态", Description: "更新特定的Wiki页面问题状态"},
	}
}

// DefaultAllowedTools returns the default allowed tools list.
func DefaultAllowedTools() []string {
	return []string{
		ToolSearchKnowledge,
		ToolReadDocument,
		ToolListDocuments,
		// Looking up what this user asked before is only ever a read of their
		// own history, and it is what lets "上次你给我的那个配置" resolve at all
		// without stuffing every past conversation into the context window.
		ToolSearchConversations,
		// ToolSearchMemory is deliberately absent here and from
		// AvailableToolDefinitions. Like web_search it is not chosen from this
		// list at all: registerTools injects it whenever the workspace, the
		// user and the agent all allow memory, and strips it whenever they do
		// not. Adding it here would let a stale allowlist decide something the
		// memory switches already decide.
		// Graph, SQL, data analysis and explicit planning are opt-in. Existing
		// agents keep their explicit allowlists; domain presets select extras.
	}
}

// Retired tool identifiers are retained only for decoding existing histories
// and ignoring obsolete allowlist entries. They have no implementation or
// registration path and are never offered in model tool schemas.
const (
	LegacyToolExecuteSkillScript = "execute_skill_script"
	LegacyToolReadSkill          = "read_skill"
	LegacyToolReadSandboxFile    = "read_sandbox_file"
	// The pre-consolidation knowledge retrieval surface. knowledge_search and
	// grep_chunks were folded into search_knowledge (mode=semantic|keyword|
	// hybrid); list_knowledge_chunks, get_document_info and
	// wiki_read_source_doc were folded into read_document.
	LegacyToolKnowledgeSearch     = "knowledge_search"
	LegacyToolGrepChunks          = "grep_chunks"
	LegacyToolListKnowledgeChunks = "list_knowledge_chunks"
	LegacyToolGetDocumentInfo     = "get_document_info"
	LegacyToolWikiReadSourceDoc   = "wiki_read_source_doc"
)

// legacyToolSuccessors maps retired allowlist entries to the tool that now
// provides the capability. Stored agent configurations, presets and API
// callers keep working without a data migration: NormalizeAllowedTools maps
// them at registration time.
var legacyToolSuccessors = map[string]string{
	LegacyToolKnowledgeSearch:     ToolSearchKnowledge,
	LegacyToolGrepChunks:          ToolSearchKnowledge,
	LegacyToolListKnowledgeChunks: ToolReadDocument,
	LegacyToolGetDocumentInfo:     ToolReadDocument,
	LegacyToolWikiReadSourceDoc:   ToolReadDocument,
}

// SuccessorToolName returns the current tool that replaces a retired
// allowlist entry, or name itself when it is not retired.
func SuccessorToolName(name string) string {
	if successor, ok := legacyToolSuccessors[name]; ok {
		return successor
	}
	return name
}

// IsLegacyRetrievalTool reports whether name is a retired knowledge retrieval
// tool that NormalizeAllowedTools rewrites to its successor.
func IsLegacyRetrievalTool(name string) bool {
	_, ok := legacyToolSuccessors[name]
	return ok
}

// NormalizeAllowedTools rewrites retired tool names in an allowlist to their
// successors and drops duplicates while preserving first-seen order.
func NormalizeAllowedTools(allowed []string) []string {
	if len(allowed) == 0 {
		return allowed
	}
	seen := make(map[string]struct{}, len(allowed))
	out := make([]string, 0, len(allowed))
	for _, name := range allowed {
		name = SuccessorToolName(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// RetiredToolReplacement tells the model how to replace a removed tool.
// Empty when name was never a WeKnora tool.
func RetiredToolReplacement(name string) string {
	switch name {
	case LegacyToolExecuteSkillScript:
		return "execute_skill_script is no longer available; use shell_exec(skill_name=..., command=...) to run skill scripts"
	case LegacyToolReadSkill:
		return `read_skill is no longer available; use read_file(path="skill://<name>/<file_path or SKILL.md>")`
	case LegacyToolReadSandboxFile:
		return "read_sandbox_file is no longer available; use read_file(path=...)"
	case LegacyToolKnowledgeSearch:
		return "knowledge_search is no longer available; use search_knowledge(query=..., mode=\"semantic\"|\"hybrid\")"
	case LegacyToolGrepChunks:
		return "grep_chunks is no longer available; use search_knowledge(query=..., mode=\"keyword\") for exact " +
			"terms, or read_document(id=dN, query=...) to search inside one document"
	case LegacyToolListKnowledgeChunks:
		return "list_knowledge_chunks is no longer available; use read_document(id=dN or cN, offset=..., limit=...)"
	case LegacyToolGetDocumentInfo:
		return "get_document_info is no longer available; read_document(id=dN) returns the document metadata " +
			"with its first page"
	case LegacyToolWikiReadSourceDoc:
		return "wiki_read_source_doc is no longer available; use read_document(id=dN, query=... or offset=...)"
	default:
		return ""
	}
}
