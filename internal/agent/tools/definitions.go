package tools

// maxFunctionNameLength is the maximum length for a tool/function name
// imposed by the OpenAI API.
const maxFunctionNameLength = 64

// Tool names constants
const (
	ToolThinking            = "thinking"
	ToolTodoWrite           = "todo_write"
	ToolGrepChunks          = "grep_chunks"
	ToolKnowledgeSearch     = "knowledge_search"
	ToolListKnowledgeChunks = "list_knowledge_chunks"
	ToolQueryKnowledgeGraph = "query_knowledge_graph"
	ToolGetDocumentInfo     = "get_document_info"
	ToolDatabaseQuery       = "database_query"
	ToolDataAnalysis        = "data_analysis"
	ToolDataSchema          = "data_schema"
	ToolWebSearch           = "web_search"
	ToolWebFetch            = "web_fetch"
	ToolFinalAnswer         = "final_answer"
	// Skills-related tools (only available when skills are enabled)
	ToolExecuteSkillScript = "execute_skill_script"
	ToolReadSkill          = "read_skill"
	// Wiki-related tools (only available when wiki KBs are in scope)
	ToolWikiReadPage      = "wiki_read_page"
	ToolWikiWritePage     = "wiki_write_page"
	ToolWikiReplaceText   = "wiki_replace_text"
	ToolWikiRenamePage    = "wiki_rename_page"
	ToolWikiDeletePage    = "wiki_delete_page"
	ToolWikiSearch        = "wiki_search"
	ToolWikiReadSourceDoc = "wiki_read_source_doc"
	ToolWikiFlagIssue     = "wiki_flag_issue"
	ToolWikiReadIssue     = "wiki_read_issue"
	ToolWikiUpdateIssue   = "wiki_update_issue"
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
		{Name: ToolGrepChunks, Label: "关键词搜索", Description: "快速定位包含特定关键词的文档和分块"},
		{Name: ToolKnowledgeSearch, Label: "语义搜索", Description: "理解问题并查找语义相关内容"},
		{Name: ToolListKnowledgeChunks, Label: "查看文档分块", Description: "获取文档完整分块内容"},
		{Name: ToolQueryKnowledgeGraph, Label: "查询知识图谱", Description: "从知识图谱中查询关系"},
		{Name: ToolGetDocumentInfo, Label: "获取文档信息", Description: "查看文档元数据"},
		{Name: ToolDatabaseQuery, Label: "查询数据库", Description: "查询数据库中的信息"},
		{Name: ToolDataAnalysis, Label: "数据分析", Description: "理解数据文件并进行数据分析"},
		{Name: ToolDataSchema, Label: "查看数据元信息", Description: "获取表格文件的元信息"},
		{Name: ToolReadSkill, Label: "读取技能", Description: "按需读取技能内容以学习专业能力"},
		{Name: ToolExecuteSkillScript, Label: "执行技能脚本", Description: "在沙箱环境中执行技能脚本"},
		{Name: ToolFinalAnswer, Label: "提交最终回答", Description: "提交最终回答给用户"},
		{Name: ToolWikiReadPage, Label: "读取Wiki页面", Description: "读取指定的Wiki页面内容"},
		{Name: ToolWikiSearch, Label: "搜索Wiki", Description: "在Wiki中搜索页面"},
		{Name: ToolWikiReadSourceDoc, Label: "精读源文档", Description: "使用知识点深入阅读特定原始文档"},
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
		ToolThinking,
		ToolTodoWrite,
		ToolKnowledgeSearch,
		ToolGrepChunks,
		ToolListKnowledgeChunks,
		ToolQueryKnowledgeGraph,
		ToolGetDocumentInfo,
		ToolDatabaseQuery,
		ToolDataAnalysis,
		ToolDataSchema,
		ToolFinalAnswer,
	}
}
