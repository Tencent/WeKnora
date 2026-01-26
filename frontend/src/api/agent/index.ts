import { get, post, put, del } from "../../utils/request";

// 智能体配置
export interface CustomAgentConfig {
  // ===== 基础设置 =====
  agent_mode?: 'quick-answer' | 'smart-reasoning';  // 运行模式：quick-answer=RAG模式, smart-reasoning=ReAct Agent模式
  system_prompt?: string;           // 统一系统提示词（使用 {{web_search_status}} 占位符动态控制行为）
  context_template?: string;        // 上下文模板（普通模式）

  // ===== 模型设置 =====
  model_id?: string;
  rerank_model_id?: string;         // ReRank 模型 ID
  temperature?: number;
  max_completion_tokens?: number;   // 最大生成token数（普通模式）

  // ===== Agent模式设置 =====
  max_iterations?: number;          // 最大迭代次数
  allowed_tools?: string[];         // 允许的工具
  reflection_enabled?: boolean;     // 是否启用反思
  // MCP服务选择模式：all=全部启用的MCP服务, selected=指定服务, none=不使用MCP
  mcp_selection_mode?: 'all' | 'selected' | 'none';
  mcp_services?: string[];          // 选择的MCP服务ID列表

  // ===== 知识库设置 =====
  // 知识库选择模式：all=全部知识库, selected=指定知识库, none=不使用知识库
  kb_selection_mode?: 'all' | 'selected' | 'none';
  knowledge_bases?: string[];
  // 是否仅在显式 @ 提及时检索知识库（默认: false）
  // true: 只有用户通过 @ 明确提及知识库/文档时才检索
  // false: 根据 kb_selection_mode 自动检索知识库
  retrieve_kb_only_when_mentioned?: boolean;

  // ===== 文件类型限制 =====
  // 支持的文件类型（如 ["csv", "xlsx", "xls"]）
  // 为空表示支持所有文件类型
  supported_file_types?: string[];

  // ===== 网络搜索设置 =====
  web_search_enabled?: boolean;
  web_search_max_results?: number;

  // ===== 多轮对话设置 =====
  multi_turn_enabled?: boolean;     // 是否启用多轮对话
  history_turns?: number;           // 保留历史轮数

  // ===== 检索策略设置 =====
  embedding_top_k?: number;         // 向量召回TopK
  keyword_threshold?: number;       // 关键词召回阈值
  vector_threshold?: number;        // 向量召回阈值
  rerank_top_k?: number;            // 重排TopK
  rerank_threshold?: number;        // 重排阈值

  // ===== 高级设置（主要用于普通模式）=====
  enable_query_expansion?: boolean; // 是否启用查询扩展
  enable_rewrite?: boolean;         // 是否启用问题改写
  rewrite_prompt_system?: string;   // 改写系统提示词
  rewrite_prompt_user?: string;     // 改写用户提示词模板
  fallback_strategy?: 'fixed' | 'model'; // 兜底策略
  fallback_response?: string;       // 固定兜底回复
  fallback_prompt?: string;         // 兜底提示词（模型生成时）

  // ===== 已废弃字段（保留兼容）=====
  welcome_message?: string;
  suggested_prompts?: string[];
}

// 智能体
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

// 创建智能体请求
export interface CreateAgentRequest {
  name: string;
  description?: string;
  avatar?: string;
  config?: CustomAgentConfig;
}

// 更新智能体请求
export interface UpdateAgentRequest {
  name: string;
  description?: string;
  avatar?: string;
  config?: CustomAgentConfig;
}

// 内置智能体 ID（常用的保留常量，便于代码引用）
export const BUILTIN_QUICK_ANSWER_ID = 'builtin-quick-answer';
export const BUILTIN_SMART_REASONING_ID = 'builtin-smart-reasoning';

// AgentMode 常量
export const AGENT_MODE_QUICK_ANSWER = 'quick-answer';
export const AGENT_MODE_SMART_REASONING = 'smart-reasoning';

// Deprecated: Use BUILTIN_QUICK_ANSWER_ID instead
export const BUILTIN_AGENT_NORMAL_ID = BUILTIN_QUICK_ANSWER_ID;
// Deprecated: Use BUILTIN_SMART_REASONING_ID instead
export const BUILTIN_AGENT_AGENT_ID = BUILTIN_SMART_REASONING_ID;

// 获取智能体列表（包括内置智能体）
export function listAgents() {
  return get<{ data: CustomAgent[] }>('/api/v1/agents');
}

// 获取智能体详情
export function getAgentById(id: string) {
  return get<{ data: CustomAgent }>(`/api/v1/agents/${id}`);
}

// 创建智能体
export function createAgent(data: CreateAgentRequest) {
  return post<{ data: CustomAgent }>('/api/v1/agents', data);
}

// 更新智能体
export function updateAgent(id: string, data: UpdateAgentRequest) {
  return put<{ data: CustomAgent }>(`/api/v1/agents/${id}`, data);
}

// 删除智能体
export function deleteAgent(id: string) {
  return del<{ success: boolean }>(`/api/v1/agents/${id}`);
}

// 复制智能体
export function copyAgent(id: string) {
  return post<{ data: CustomAgent }>(`/api/v1/agents/${id}/copy`);
}

// 判断是否为内置智能体（通过 agent.is_builtin 字段或 ID 前缀判断）
export function isBuiltinAgent(agentId: string): boolean {
  return agentId.startsWith('builtin-');
}

// 占位符定义
export interface PlaceholderDefinition {
  name: string;
  label: string;
  description: string;
}

// 占位符响应
export interface PlaceholdersResponse {
  all: PlaceholderDefinition[];
  system_prompt: PlaceholderDefinition[];
  agent_system_prompt: PlaceholderDefinition[];
  context_template: PlaceholderDefinition[];
  rewrite_system_prompt: PlaceholderDefinition[];
  rewrite_prompt: PlaceholderDefinition[];
  fallback_prompt: PlaceholderDefinition[];
}

// 获取占位符定义
export function getPlaceholders() {
  return get<{ data: PlaceholdersResponse }>('/api/v1/agents/placeholders');
}
