import { get, post, put, del } from "../../utils/request";

// Agent Configuration
export interface CustomAgentConfig {
  // ===== Basic Settings =====
  agent_mode?: 'quick-answer' | 'smart-reasoning';  // Running mode: quick-answer=RAG mode, smart-reasoning=ReAct Agent mode
  system_prompt?: string;           // Unified system prompt (use {{web_search_status}} placeholder to dynamically control behavior)
  context_template?: string;        // Context template (normal mode)

  // ===== Model Settings =====
  model_id?: string;
  rerank_model_id?: string;         // ReRank Model ID
  temperature?: number;
  max_completion_tokens?: number;   // Max completion tokens (normal mode)

  // ===== Agent Mode Settings =====
  max_iterations?: number;          // Max iterations
  allowed_tools?: string[];         // Allowed tools
  reflection_enabled?: boolean;     // Whether to enable reflection
  // MCP service selection mode: all=all enabled MCP services, selected=selected services, none=no MCP
  mcp_selection_mode?: 'all' | 'selected' | 'none';
  mcp_services?: string[];          // List of selected MCP service IDs

  // ===== Knowledge Base Settings =====
  // Knowledge base selection mode: all=all knowledge bases, selected=selected knowledge bases, none=no knowledge base
  kb_selection_mode?: 'all' | 'selected' | 'none';
  knowledge_bases?: string[];

  // ===== File Type Restrictions =====
  // Supported file types (e.g. ["csv", "xlsx", "xls"])
  // Empty means all file types are supported
  supported_file_types?: string[];

  // ===== Web Search Settings =====
  web_search_enabled?: boolean;
  web_search_max_results?: number;

  // ===== Multi-turn Conversation Settings =====
  multi_turn_enabled?: boolean;     // Whether to enable multi-turn conversation
  history_turns?: number;           // History turns to keep

  // ===== Retrieval Strategy Settings =====
  embedding_top_k?: number;         // Embedding Recall TopK
  keyword_threshold?: number;       // Keyword Recall Threshold
  vector_threshold?: number;        // Vector Recall Threshold
  rerank_top_k?: number;            // Rerank TopK
  rerank_threshold?: number;        // Rerank Threshold

  // ===== Advanced Settings (Mainly for Normal Mode) =====
  enable_query_expansion?: boolean; // Whether to enable query expansion
  enable_rewrite?: boolean;         // Whether to enable query rewrite
  rewrite_prompt_system?: string;   // Rewrite system prompt
  rewrite_prompt_user?: string;     // Rewrite user prompt template
  fallback_strategy?: 'fixed' | 'model'; // Fallback strategy
  fallback_response?: string;       // Fixed fallback response
  fallback_prompt?: string;         // Fallback prompt (when generating with model)

  // ===== Deprecated Fields (Retained for Compatibility) =====
  welcome_message?: string;
  suggested_prompts?: string[];
}

// Agent
export interface CustomAgent {
  id: string;
  name: string;
  description?: string;
  avatar?: string;
  is_builtin: boolean;
  tenant_id?: number;
  created_by?: string;
  config: CustomAgentConfig;
  created_at?: string;
  updated_at?: string;
}

// Create Agent Request
export interface CreateAgentRequest {
  name: string;
  description?: string;
  avatar?: string;
  config?: CustomAgentConfig;
}

// Update Agent Request
export interface UpdateAgentRequest {
  name: string;
  description?: string;
  avatar?: string;
  config?: CustomAgentConfig;
}

// Built-in Agent ID (Commonly used reserved constants for code reference)
export const BUILTIN_QUICK_ANSWER_ID = 'builtin-quick-answer';
export const BUILTIN_SMART_REASONING_ID = 'builtin-smart-reasoning';

// AgentMode Constants
export const AGENT_MODE_QUICK_ANSWER = 'quick-answer';
export const AGENT_MODE_SMART_REASONING = 'smart-reasoning';

// Deprecated: Use BUILTIN_QUICK_ANSWER_ID instead
export const BUILTIN_AGENT_NORMAL_ID = BUILTIN_QUICK_ANSWER_ID;
// Deprecated: Use BUILTIN_SMART_REASONING_ID instead
export const BUILTIN_AGENT_AGENT_ID = BUILTIN_SMART_REASONING_ID;

// Get agent list (including built-in agents)
export function listAgents() {
  return get<{ data: CustomAgent[] }>('/api/v1/agents');
}

// Get agent details
export function getAgentById(id: string) {
  return get<{ data: CustomAgent }>(`/api/v1/agents/${id}`);
}

// Create agent
export function createAgent(data: CreateAgentRequest) {
  return post<{ data: CustomAgent }>('/api/v1/agents', data);
}

// Update agent
export function updateAgent(id: string, data: UpdateAgentRequest) {
  return put<{ data: CustomAgent }>(`/api/v1/agents/${id}`, data);
}

// Delete agent
export function deleteAgent(id: string) {
  return del<{ success: boolean }>(`/api/v1/agents/${id}`);
}

// Copy agent
export function copyAgent(id: string) {
  return post<{ data: CustomAgent }>(`/api/v1/agents/${id}/copy`);
}

// Check if it is a built-in agent (determined by agent.is_builtin field or ID prefix)
export function isBuiltinAgent(agentId: string): boolean {
  return agentId.startsWith('builtin-');
}

// Placeholder Definition
export interface PlaceholderDefinition {
  name: string;
  label: string;
  description: string;
}

// Placeholder Response
export interface PlaceholdersResponse {
  all: PlaceholderDefinition[];
  system_prompt: PlaceholderDefinition[];
  agent_system_prompt: PlaceholderDefinition[];
  context_template: PlaceholderDefinition[];
  rewrite_system_prompt: PlaceholderDefinition[];
  rewrite_prompt: PlaceholderDefinition[];
  fallback_prompt: PlaceholderDefinition[];
}

// Get placeholder definitions
export function getPlaceholders() {
  return get<{ data: PlaceholdersResponse }>('/api/v1/agents/placeholders');
}
