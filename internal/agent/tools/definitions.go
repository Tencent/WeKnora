package tools

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
		{Name: ToolThinking, Label: "생각", Description: "동적이고 반성적인 문제 해결 사고 도구"},
		{Name: ToolTodoWrite, Label: "계획 수립", Description: "구조화된 연구 계획 생성"},
		{Name: ToolGrepChunks, Label: "키워드 검색", Description: "특정 키워드가 포함된 문서 및 청크를 빠르게 찾기"},
		{Name: ToolKnowledgeSearch, Label: "의미 검색", Description: "문제를 이해하고 의미론적으로 관련된 내용 찾기"},
		{Name: ToolListKnowledgeChunks, Label: "문서 청크 보기", Description: "문서의 전체 청크 내용 가져오기"},
		{Name: ToolQueryKnowledgeGraph, Label: "지식 그래프 쿼리", Description: "지식 그래프에서 관계 쿼리"},
		{Name: ToolGetDocumentInfo, Label: "문서 정보 가져오기", Description: "문서 메타데이터 보기"},
		{Name: ToolDatabaseQuery, Label: "데이터베이스 쿼리", Description: "데이터베이스에서 정보 쿼리"},
		{Name: ToolDataAnalysis, Label: "데이터 분석", Description: "데이터 파일을 이해하고 데이터 분석 수행"},
		{Name: ToolDataSchema, Label: "데이터 메타 정보 보기", Description: "테이블 파일의 메타 정보 가져오기"},
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
	}
}
