/** TDesign icon names for agent / RAG pipeline tool steps. */
export function getAgentToolIconName(
  toolName: string,
  searchSource?: 'knowledge' | 'web' | 'mixed',
): string {
  if (toolName === 'thinking') {
    return 'ai-search'
  }
  if (toolName === 'search_knowledge' || toolName === 'knowledge_search') {
    if (searchSource === 'web') {
      return 'internet'
    }
    return 'data-search'
  }
  if (toolName === 'wiki_search') {
    return 'search'
  }
  if (toolName === 'grep_chunks') {
    return 'search'
  }
  if (toolName === 'web_search') {
    return 'internet'
  }
  if (toolName === 'get_document_info' || toolName === 'list_knowledge_chunks') {
    return 'file-search'
  }
  if (toolName === 'get_document_content' || toolName === 'wiki_read_page' || toolName === 'wiki_read_source_doc') {
    return 'file-search'
  }
  if (toolName === 'todo_write') {
    return 'task'
  }
  if (toolName === 'image_analysis' || toolName === 'query_understand') {
    return 'ai-search'
  }
  if (toolName === 'attachment_parsing') {
    return 'attach'
  }
  if (toolName.startsWith('mcp_')) {
    return 'terminal'
  }
  if (toolName === 'shell_exec') {
    return 'terminal'
  }
  if (toolName === 'list_sandbox_files') {
    return 'folder'
  }
  if (toolName === 'read_file' || toolName === 'read_sandbox_file' || toolName === 'read_skill') {
    return 'file'
  }
  if (toolName === 'write_sandbox_file' || toolName === 'edit_sandbox_file') {
    return 'edit'
  }
  if (toolName === 'execute_skill_script') {
    return 'code'
  }
  return 'file-paste'
}
