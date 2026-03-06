/**
 * Tool Icons Utility
 * Maps tool names and match types to icons for better UI display
 */
import i18n from '@/i18n'

const t = (key: string) => i18n.global.t(key)

// Tool name to icon mapping
export const toolIcons: Record<string, string> = {
    multi_kb_search: '🔍',
    knowledge_search: '📚',
    grep_chunks: '🔎',
    get_chunk_detail: '📄',
    list_knowledge_bases: '📂',
    list_knowledge_chunks: '🧩',
    get_document_info: 'ℹ️',
    query_knowledge_graph: '🕸️',
    think: '💭',
    todo_write: '📋',
};

// Match type internal keys for icon mapping
const matchTypeIconKeys: Record<string, string> = {
    vector: '🎯',
    keyword: '🔤',
    adjacent: '📌',
    history: '📜',
    parent: '⬆️',
    relation: '🔗',
    graph: '🕸️',
};

// Match type to icon mapping (keys match backend API response)
export const matchTypeIcons: Record<string, string> = {
    'Vector Match': '🎯',
    'Keyword Match': '🔤',
    'Adjacent Chunk Match': '📌',
    'History Match': '📜',
    'Parent Chunk Match': '⬆️',
    'Relation Chunk Match': '🔗',
    'Graph Match': '🕸️',
};

// Get icon for a tool name
export function getToolIcon(toolName: string): string {
    return toolIcons[toolName] || '🛠️';
}

// Get icon for a match type
export function getMatchTypeIcon(matchType: string): string {
    return matchTypeIcons[matchType] || matchTypeIconKeys[matchType] || '📍';
}

// Tool name to i18n key mapping
const toolDisplayNameKeys: Record<string, string> = {
    multi_kb_search: 'tools.multiKbSearch',
    knowledge_search: 'tools.knowledgeSearch',
    grep_chunks: 'tools.grepChunks',
    get_chunk_detail: 'tools.getChunkDetail',
    list_knowledge_chunks: 'tools.listKnowledgeChunks',
    list_knowledge_bases: 'tools.listKnowledgeBases',
    get_document_info: 'tools.getDocumentInfo',
    query_knowledge_graph: 'tools.queryKnowledgeGraph',
    think: 'tools.think',
    todo_write: 'tools.todoWrite',
};

// Get tool display name (user-friendly, localized)
export function getToolDisplayName(toolName: string): string {
    const key = toolDisplayNameKeys[toolName];
    return key ? t(key) : toolName;
}

