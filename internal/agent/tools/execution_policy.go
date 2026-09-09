package tools

// CanRunConcurrently is an explicit allowlist of built-in reads. Mutations,
// arbitrary code, and unknown/MCP tools form execution barriers. In particular,
// a file write must finish before the next shell or skill call starts.
func CanRunConcurrently(name string) bool {
	switch name {
	case ToolKnowledgeSearch, ToolGrepChunks, ToolListKnowledgeChunks,
		ToolQueryKnowledgeGraph, ToolGetDocumentInfo, ToolSearchConversations,
		ToolSearchMemory, ToolDataSchema, ToolWebSearch, ToolWebFetch,
		ToolReadFile, ToolListSandboxFiles,
		ToolWikiSearch, ToolWikiReadPage, ToolWikiReadSourceDoc, ToolWikiReadIssue:
		return true
	default:
		return false
	}
}
