/**
 * Tool Results Type Definitions
 * TypeScript interfaces for all tool result types
 */

// Relevance levels — values match the backend API response.
// Display labels are resolved via i18n in SearchResults.vue and GraphQueryResults.vue.
export type RelevanceLevel = 'High Relevance' | 'Medium Relevance' | 'Low Relevance' | 'Weak Relevance';

// Display types
export type DisplayType =
    | 'search_results'
    | 'chunk_detail'
    | 'related_chunks'
    | 'knowledge_base_list'
    | 'document_info'
    | 'graph_query_results'
    | 'thinking'
    | 'plan'
    | 'database_query'
    | 'web_search_results'
    | 'web_fetch_results'
    | 'grep_results'
    | 'wiki_write_page'
    | 'wiki_replace_text'
    | 'wiki_rename_page'
    | 'wiki_delete_page';

// Search result item
export interface SearchResultItem {
    result_index: number;
    chunk_id: string;
    content: string;
    score: number;
    relevance_level: RelevanceLevel;
    knowledge_id: string;
    knowledge_title: string;
    match_type: string;
}

// Chunk item
export interface ChunkItem {
    index: number;
    chunk_id: string;
    chunk_index: number;
    content: string;
    knowledge_id: string;
}

// Knowledge base item
export interface KnowledgeBaseItem {
    index: number;
    id: string;
    name: string;
    description: string;
}

// Graph config
export interface GraphConfig {
    nodes: string[];
    relations: string[];
}

// Search results data
export interface SearchResultsData {
    display_type: 'search_results';
    results?: SearchResultItem[];
    count?: number;
    kb_counts?: Record<string, number>;
    query?: string;
    knowledge_base_id?: string;
}

// Chunk detail data
export interface ChunkDetailData {
    display_type: 'chunk_detail';
    chunk_id: string;
    content: string;
    chunk_index: number;
    knowledge_id: string;
    content_length?: number;
}

// Related chunks data
export interface RelatedChunksData {
    display_type: 'related_chunks';
    chunk_id: string;
    relation_type: string;
    count: number;
    chunks: ChunkItem[];
}

// Knowledge base list data
export interface KnowledgeBaseListData {
    display_type: 'knowledge_base_list';
    knowledge_bases: KnowledgeBaseItem[];
    count: number;
}

// Document info data
export interface DocumentInfoDocument {
    knowledge_id: string;
    title: string;
    description?: string;
    type?: string;
    source?: string;
    channel?: string;
    file_name?: string;
    file_type?: string;
    file_size?: number;
    parse_status?: string;
    chunk_count?: number;
    metadata?: Record<string, any>;
    type_icon?: string;
}

export interface DocumentInfoData {
    display_type: 'document_info';
    documents?: DocumentInfoDocument[];
    total_docs: number;
    requested: number;
    errors?: string[];
    title?: string;
}

// Graph query results data
export interface GraphQueryResultsData {
    display_type: 'graph_query_results';
    results: SearchResultItem[];
    count: number;
    graph_config: GraphConfig;
}

// Thinking data
export interface ThinkingData {
    display_type: 'thinking';
    thought: string;
}

// Plan step
export interface PlanStep {
    id: string;
    description: string;
    tools_to_use?: string[]; // Changed from string to array
    status: 'pending' | 'in_progress' | 'completed' | 'skipped';
}

// Plan data
export interface PlanData {
    display_type: 'plan';
    task: string;
    steps: PlanStep[];
    total_steps: number;
}

// Database query data
export interface DatabaseQueryData {
    display_type: 'database_query';
    columns: string[];
    rows: Array<Record<string, any>>;
    row_count: number;
}

// Web search result item
export interface WebSearchResultItem {
    result_index: number;
    title: string;
    url: string;
    snippet?: string;
    content?: string;
    source?: string;
    published_at?: string;
}

// Web search results data
export interface WebSearchResultsData {
    display_type: 'web_search_results';
    query: string;
    results: WebSearchResultItem[];
    count: number;
}

// Web fetch result item
export interface WebFetchResultItem {
    url: string;
    prompt?: string;
    summary?: string;
    raw_content?: string;
    content_length?: number;
    method?: string;
    error?: string;
}

// Web fetch results data
export interface WebFetchResultsData {
    display_type: 'web_fetch_results';
    results: WebFetchResultItem[];
    count?: number;
}

// Grep knowledge aggregation item
export interface GrepKnowledgeResult {
    knowledge_id: string;
    knowledge_base_id: string;
    knowledge_title: string;
    chunk_hit_count: number;
    pattern_counts: Record<string, number>;
    total_pattern_hits: number;
    distinct_patterns: number;
}

// Grep results data
export interface GrepResultsData {
    display_type: 'grep_results';
    patterns: string[];
    knowledge_results: GrepKnowledgeResult[];
    result_count: number;
    total_matches: number;
    knowledge_base_ids?: string[];
    max_results: number;
}

// Wiki write page data
export interface WikiWritePageData {
    display_type: 'wiki_write_page';
    action: 'created' | 'updated';
    slug: string;
    title: string;
    page_type: string;
    summary: string;
}

// Wiki replace text data
export interface WikiReplaceTextData {
    display_type: 'wiki_replace_text';
    slug: string;
    title: string;
    old_text: string;
    new_text: string;
}

// Wiki rename page data
export interface WikiRenamePageData {
    display_type: 'wiki_rename_page';
    old_slug: string;
    new_slug: string;
    title: string;
    updated_count: number;
    affected_pages?: string[];
}

// Wiki delete page data
export interface WikiDeletePageData {
    display_type: 'wiki_delete_page';
    slug: string;
    title: string;
    updated_count: number;
    affected_pages?: string[];
}

// Union type for all wiki edit data
export type WikiEditData = WikiWritePageData | WikiReplaceTextData | WikiRenamePageData | WikiDeletePageData;

// Union type for all tool result data
export type ToolResultData =
    | SearchResultsData
    | ChunkDetailData
    | RelatedChunksData
    | KnowledgeBaseListData
    | DocumentInfoData
    | GraphQueryResultsData
    | ThinkingData
    | PlanData
    | DatabaseQueryData
    | WebSearchResultsData
    | WebFetchResultsData
    | GrepResultsData
    | WikiWritePageData
    | WikiReplaceTextData
    | WikiRenamePageData
    | WikiDeletePageData;

// Action data (from index.vue)
export interface ActionData {
    description: string;
    success: boolean;
    tool_name?: string;
    arguments?: any;
    output?: string;
    error?: string;
    details?: boolean;
    display_type?: DisplayType;
    tool_data?: Record<string, any>;
}

