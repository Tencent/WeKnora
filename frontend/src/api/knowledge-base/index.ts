import { get, post, put, del, postUpload, getDown } from "../../utils/request";
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess';
import type { AuditLog, AuditOutcome, ListAuditLogResponse } from '@/api/tenant/audit-log';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

export type KnowledgeBaseActivity = AuditLog;

export interface ListKnowledgeBaseActivityParams {
  after_id?: number;
  limit?: number;
  action?: string;
  outcome?: AuditOutcome;
  actor?: string;
}

export async function listKnowledgeBaseActivity(
  id: string,
  params: ListKnowledgeBaseActivityParams = {},
): Promise<ListAuditLogResponse> {
  const query = new URLSearchParams();
  if (params.after_id) query.set('after_id', String(params.after_id));
  if (params.limit) query.set('limit', String(params.limit));
  if (params.action) query.set('action', params.action);
  if (params.outcome) query.set('outcome', params.outcome);
  if (params.actor) query.set('actor', params.actor);
  const qs = query.toString();
  return (await get(`/api/v1/knowledge-bases/${id}/activity${qs ? `?${qs}` : ''}`)) as unknown as ListAuditLogResponse;
}

// 知识库管理 API（列表、创建、获取、更新、删除、复制）
export function listKnowledgeBases(params?: {
  agent_id?: string;
  /**
   * Optional creator filter. Server-side semantics:
   *   - "mine"   → only KBs whose creator_id matches the caller
   *   - "others" → only KBs created by someone else in this tenant
   *   - omitted/"all" → no filter
   * KBs predating the RBAC backfill (creator_id="") never match
   * mine/others — they fall out of both views by design.
   */
  creator?: 'all' | 'mine' | 'others';
}) {
  const query = new URLSearchParams();
  if (params?.agent_id) query.set('agent_id', params.agent_id);
  if (params?.creator && params.creator !== 'all') query.set('creator', params.creator);
  const qs = query.toString();
  return get(qs ? `/api/v1/knowledge-bases?${qs}` : '/api/v1/knowledge-bases');
}

// Read-only vector-store binding metadata enriched onto every KB
// response (list, create, get, update, pin). Source carries where the
// binding points; status reports whether that target is currently
// reachable by the server.
//
//   - source 'env'    → KB uses the tenant's env-configured store
//                       (RETRIEVE_DRIVER). vector_store_id is null and
//                       vector_store_name is the localized "System
//                       default" label; vector_store_engine_type still
//                       reports the underlying engine (e.g. "postgres").
//   - source 'user'   → KB is bound to a tenant-owned VectorStore.
//                       vector_store_id / name / engine_type are real.
//   - source 'shared' → KB belongs to a different tenant and is
//                       readable via cross-organization sharing. The
//                       server strips vector_store_id and engine_type
//                       to avoid leaking the owner tenant's store
//                       inventory; only this source marker arrives.
//   - status 'unavailable' → the binding cannot be reached right now
//                       (deleted row, registry miss, transient infra
//                       failure). Operators recover via the global
//                       Vector Stores settings page.
export type VectorStoreSource = 'env' | 'user' | 'shared' | 'unavailable';
export type VectorStoreStatus = 'available' | 'unavailable';

export interface KnowledgeBaseStoreView {
  vector_store_id?: string | null;
  vector_store_name?: string;
  vector_store_engine_type?: string;
  vector_store_source?: VectorStoreSource;
  vector_store_status?: VectorStoreStatus;
}

export function createKnowledgeBase(data: {
  name: string;
  description?: string;
  type?: 'document' | 'faq';
  chunking_config?: any;
  embedding_model_id?: string;
  summary_model_id?: string;
  // Opt-in binding to a specific tenant-owned VectorStore. Omit (or
  // send undefined / empty string) to fall back to the env-configured
  // store. Immutable after creation — UpdateKnowledgeBase intentionally
  // does not accept this field.
  vector_store_id?: string;
  // Concrete tenant-owned storage instance. When omitted, the tenant default
  // backend is bound by the server at creation time.
  storage_backend_id?: string;
  vlm_config?: {
    enabled: boolean;
    model_id?: string;
    description_language?: string;
    custom_instructions?: string;
  };
  storage_provider_config?: { provider: string };
  storage_config?: any; // legacy, kept for backward compat (dual-write)
  asr_config?: {
    enabled: boolean;
    model_id?: string;
    language?: string;
  };
  extract_config?: any;
  faq_config?: { index_mode: string; question_index_mode?: string };
  wiki_config?: {
    synthesis_model_id?: string;
    max_pages_per_ingest?: number;
    extraction_granularity?: 'focused' | 'standard' | 'exhaustive';
    content_instructions?: string;
    extraction_instructions?: string;
  };
  indexing_strategy?: {
    vector_enabled: boolean;
    keyword_enabled: boolean;
    wiki_enabled: boolean;
    graph_enabled: boolean;
  };
}) {
  return post(`/api/v1/knowledge-bases`, data);
}

export function getKnowledgeBaseById(id: string, options?: { agent_id?: string }) {
  const query = new URLSearchParams();
  if (options?.agent_id) query.set('agent_id', options.agent_id);
  const qs = query.toString();
  return get(qs ? `/api/v1/knowledge-bases/${id}?${qs}` : `/api/v1/knowledge-bases/${id}`);
}

export function updateKnowledgeBase(id: string, data: {
  name: string;
  description?: string;
  config?: {
    chunking_config?: any;
    image_processing_config?: any;
    faq_config?: any;
    wiki_config?: {
      synthesis_model_id?: string;
      max_pages_per_ingest?: number;
      extraction_granularity?: 'focused' | 'standard' | 'exhaustive';
      content_instructions?: string;
      extraction_instructions?: string;
    };
    indexing_strategy?: {
      vector_enabled: boolean;
      keyword_enabled: boolean;
      wiki_enabled: boolean;
      graph_enabled: boolean;
    };
  }
}) {
  return put(`/api/v1/knowledge-bases/${id}`, data);
}

export function rebuildKBIndex(kbId: string) {
  return post(`/api/v1/knowledge-bases/${kbId}/rebuild-index`, {});
}

export function deleteKnowledgeBase(id: string) {
  return del(`/api/v1/knowledge-bases/${id}`);
}

export function copyKnowledgeBase(data: { source_id: string; target_id?: string }) {
  return post(`/api/v1/knowledge-bases/copy`, data);
}

export function duplicateKnowledgeBase(id: string) {
  return post(`/api/v1/knowledge-bases/${id}/duplicate`);
}

// 获取可移动目标知识库列表（同类型、同Embedding模型）
export function listMoveTargets(sourceKbId: string) {
  return get(`/api/v1/knowledge-bases/${sourceKbId}/move-targets`);
}

// 移动知识到其他知识库
export function moveKnowledge(data: {
  knowledge_ids: string[];
  source_kb_id: string;
  target_kb_id: string;
  mode: 'reuse_vectors' | 'reparse';
}) {
  return post('/api/v1/knowledge/move', data);
}

// 获取知识移动进度
export function getKnowledgeMoveProgress(taskId: string) {
  return get(`/api/v1/knowledge/move/progress/${taskId}`);
}

export function togglePinKnowledgeBase(id: string) {
  return put(`/api/v1/knowledge-bases/${id}/pin`);
}

// 知识文件 API（基于具体知识库）
// data.tag_ids: 可选，指定知识所属的多个标签 ID
export function uploadKnowledgeFile(
  kbId: string,
  data: {
    file: File
    tag_ids?: string[]
    fileName?: string
    process_config?: KnowledgeProcessOverrides | string
    [key: string]: any
  } = { file: new File([], '') },
  onProgress?: (progressEvent: any) => void,
) {
  const formData = new FormData();
  Object.keys(data).forEach(key => {
    const value = data[key];
    if (value === undefined) return;
    if (key === 'tag_ids' && Array.isArray(value)) {
      formData.append(key, value.join(','));
    } else if (key === 'process_config' && value && typeof value !== 'string') {
      formData.append(key, JSON.stringify(value));
    } else {
      formData.append(key, value);
    }
  });
  return postUpload(`/api/v1/knowledge-bases/${kbId}/knowledge/file`, formData, onProgress);
}

// 从URL创建知识
// data.tag_ids: 可选，指定知识所属的多个标签 ID
export function createKnowledgeFromURL(
  kbId: string,
  data: { url: string; enable_multimodel?: boolean; tag_ids?: string[]; process_config?: KnowledgeProcessOverrides },
) {
  return post(`/api/v1/knowledge-bases/${kbId}/knowledge/url`, data);
}

// 手工创建知识
// data.tag_ids: 可选，指定知识所属的标签 ID
export function createManualKnowledge(
  kbId: string,
  data: {
    title: string
    content: string
    status: string
    tag_ids?: string[]
    process_config?: KnowledgeProcessOverrides
  },
) {
  return post(`/api/v1/knowledge-bases/${kbId}/knowledge/manual`, data);
}

export function listKnowledgeFiles(
  kbId: string,
  params: {
    page: number;
    page_size: number;
    tag_ids?: string;
    keyword?: string;
    file_type?: string;
    parse_status?: string;
    source?: string;
    start_time?: string;
    end_time?: string;
    folder_id?: string | null;
    include_descendants?: boolean;
  },
) {
  const query = new URLSearchParams();
  query.append('page', String(params.page));
  query.append('page_size', String(params.page_size));
  if (params.tag_ids) query.append('tag_ids', params.tag_ids);
  if (params.keyword) query.append('keyword', params.keyword);
  if (params.file_type) query.append('file_type', params.file_type);
  if (params.parse_status) query.append('parse_status', params.parse_status);
  if (params.source) query.append('source', params.source);
  if (params.start_time) query.append('start_time', params.start_time);
  if (params.end_time) query.append('end_time', params.end_time);
  if (params.folder_id === null) query.append('folder_id', '');
  if (typeof params.folder_id === 'string') query.append('folder_id', params.folder_id);
  if (params.include_descendants) query.append('include_descendants', 'true');
  const qs = query.toString();
  return get(`/api/v1/knowledge-bases/${kbId}/knowledge?${qs}`);
}

export function listKnowledgeFolders(kbId: string) {
  return get(`/api/v1/knowledge-bases/${kbId}/folders`) as Promise<{
    success: boolean
    data: KnowledgeFolder[]
  }>
}

export function createKnowledgeFolder(kbId: string, name: string, parentId: string | null) {
  return post(`/api/v1/knowledge-bases/${kbId}/folders`, {
    name,
    parent_id: parentId,
  })
}

export function updateKnowledgeFolder(
  kbId: string,
  folderId: string,
  data: { name?: string; parent_id?: string | null; move_parent?: boolean },
) {
  return put(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}`, data)
}

export function deleteKnowledgeFolder(kbId: string, folderId: string) {
  return del(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}`)
}

export function moveKnowledgeToFolder(
  kbId: string,
  knowledgeId: string,
  folderId: string | null,
) {
  return put(`/api/v1/knowledge-bases/${kbId}/knowledge/${knowledgeId}/folder`, {
    folder_id: folderId,
  })
}

export function batchMoveKnowledgeToFolder(
  kbId: string,
  knowledgeIds: string[],
  folderId: string | null,
) {
  return post(`/api/v1/knowledge-bases/${kbId}/knowledge/batch-folder`, {
    knowledge_ids: knowledgeIds,
    folder_id: folderId,
  })
}

export function getKnowledgeDetails(id: string, options?: { agent_id?: string }) {
  const query = new URLSearchParams();
  if (options?.agent_id) query.set('agent_id', options.agent_id);
  const qs = query.toString();
  return get(qs ? `/api/v1/knowledge/${id}?${qs}` : `/api/v1/knowledge/${id}`);
}

export function updateManualKnowledge(
  id: string,
  data: { title: string; content: string; status: string; process_config?: KnowledgeProcessOverrides },
) {
  return put(`/api/v1/knowledge/manual/${id}`, data);
}

export function reparseKnowledge(id: string, data?: { process_config?: KnowledgeProcessOverrides }) {
  return post(`/api/v1/knowledge/${id}/reparse`, data);
}

export function cancelKnowledgeParse(id: string) {
  return post(`/api/v1/knowledge/${id}/cancel-parse`);
}

export function getKnowledgeSpans(id: string, attempt?: number) {
  const qs = attempt ? `?attempt=${attempt}` : '';
  return get(`/api/v1/knowledge/${id}/spans${qs}`);
}

export function delKnowledgeDetails(id: string) {
  return del(`/api/v1/knowledge/${id}`);
}

// 批量删除（同一知识库内）。后端会校验所有 id 隶属于 kb_id 且具有编辑权限。
export function batchDeleteKnowledge(kbId: string, ids: string[]) {
  return post(`/api/v1/knowledge/batch-delete`, { kb_id: kbId, ids });
}

export function downKnowledgeDetails(id: string) {
  return getDown(`/api/v1/knowledge/${id}/download`);
}

export function previewKnowledgeFile(id: string) {
  return getDown(`/api/v1/knowledge/${id}/preview`);
}

/** @param idsQueryString - query string with ids (e.g. ids=xxx&ids=yyy) */
export function batchQueryKnowledge(idsQueryString: string, kbId?: string, agentId?: string) {
  let qs = idsQueryString;
  if (kbId) qs += `&kb_id=${encodeURIComponent(kbId)}`;
  if (agentId) qs += `&agent_id=${encodeURIComponent(agentId)}`;
  return get(`/api/v1/knowledge/batch?${qs}`);
}

export function getKnowledgeDetailsCon(id: string, page: number) {
  return get(`/api/v1/chunks/${id}?page=${page}&page_size=25`);
}

// Get chunk by chunk_id only (new endpoint - to be added to backend)
export function getChunkByIdOnly(chunkId: string) {
  return get(`/api/v1/chunks/by-id/${chunkId}`);
}

// Delete a single generated question from a chunk by question ID
export function deleteGeneratedQuestion(chunkId: string, questionId: string) {
  return del(`/api/v1/chunks/by-id/${chunkId}/questions`, { question_id: questionId });
}

export function listKnowledgeTags(
  kbId: string,
  params?: { page?: number; page_size?: number; keyword?: string },
) {
  const query = buildQuery(params);
  return get(`/api/v1/knowledge-bases/${kbId}/tags${query}`);
}

export function createKnowledgeBaseTag(
  kbId: string,
  data: { name: string; color?: string; sort_order?: number },
) {
  return post(`/api/v1/knowledge-bases/${kbId}/tags`, data);
}

export function updateKnowledgeBaseTag(
  kbId: string,
  tagId: string,
  data: { name?: string; color?: string; sort_order?: number },
) {
  return put(`/api/v1/knowledge-bases/${kbId}/tags/${tagId}`, data);
}

export function deleteKnowledgeBaseTag(kbId: string, tagSeqId: number, params?: { force?: boolean }) {
  const forceQuery = params?.force ? '?force=true' : '';
  return del(`/api/v1/knowledge-bases/${kbId}/tags/${tagSeqId}${forceQuery}`);
}

export function updateKnowledgeTagBatch(data: { updates: Record<string, string[]> }) {
  return put(`/api/v1/knowledge/tags`, data);
}

export function updateFAQEntryTagBatch(kbId: string, data: { updates: Record<number, number | null> }) {
  return put(`/api/v1/knowledge-bases/${kbId}/faq/entries/tags`, data);
}

const buildQuery = (params?: Record<string, any>) => {
  if (!params) return '';
  const query = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value === undefined || value === null || value === '') return;
    query.append(key, String(value));
  });
  const queryString = query.toString();
  return queryString ? `?${queryString}` : '';
};

export function listFAQEntries(
  kbId: string,
  params?: { page?: number; page_size?: number; tag_id?: number; tag_ids?: string; keyword?: string },
) {
  const query = buildQuery(params);
  return get(`/api/v1/knowledge-bases/${kbId}/faq/entries${query}`);
}

export function upsertFAQEntries(kbId: string, data: { entries: any[]; mode: 'append' | 'replace' }) {
  return post(`/api/v1/knowledge-bases/${kbId}/faq/entries`, data);
}

export function createFAQEntry(kbId: string, data: any) {
  return post(`/api/v1/knowledge-bases/${kbId}/faq/entry`, data);
}

export function updateFAQEntry(kbId: string, entryId: number, data: any) {
  return put(`/api/v1/knowledge-bases/${kbId}/faq/entries/${entryId}`, data);
}

// Unified batch update API - supports is_enabled, is_recommended, tag_id
// Supports two modes:
// 1. By entry ID: use by_id field
// 2. By Tag: use by_tag field to apply the same update to all entries under a tag
export interface FAQEntryFieldsUpdate {
  is_enabled?: boolean
  is_recommended?: boolean
  tag_id?: number | null
}

export interface FAQEntryFieldsBatchRequest {
  by_id?: Record<number, FAQEntryFieldsUpdate>
  by_tag?: Record<number, FAQEntryFieldsUpdate>
  exclude_ids?: number[]
}

export function updateFAQEntryFieldsBatch(kbId: string, data: FAQEntryFieldsBatchRequest) {
  return put(`/api/v1/knowledge-bases/${kbId}/faq/entries/fields`, data);
}

export function deleteFAQEntries(kbId: string, ids: number[]) {
  return del(`/api/v1/knowledge-bases/${kbId}/faq/entries`, { ids });
}

export function searchFAQEntries(
  kbId: string,
  data: {
    query_text: string
    vector_threshold?: number
    match_count?: number
  }
) {
  return post(`/api/v1/knowledge-bases/${kbId}/faq/search`, data);
}

// Export FAQ entries as CSV or JSON file
export async function exportFAQEntries(kbId: string, format: 'csv' | 'json' = 'csv'): Promise<Blob> {
  const suffix = format === 'json' ? '?format=json' : ''
  const response = await getDown(`/api/v1/knowledge-bases/${kbId}/faq/entries/export${suffix}`)
  return response as unknown as Blob
}

// FAQ Import Progress API
export interface FAQBlockedEntry {
  index: number
  standard_question: string
  reason: string
}

export interface FAQSuccessEntry {
  index: number
  seq_id: number
  tag_id?: number
  tag_name?: string
  standard_question: string
}

export interface FAQImportProgress {
  task_id: string
  kb_id: string
  knowledge_id: string
  status: 'pending' | 'processing' | 'completed' | 'failed'
  progress: number
  total: number
  processed: number
  blocked: number
  blocked_entries?: FAQBlockedEntry[]
  success_entries?: FAQSuccessEntry[]
  message: string
  error: string
  created_at: number
  updated_at: number
}

export function getFAQImportProgress(taskId: string) {
  return get(`/api/v1/faq/import/progress/${taskId}`);
}

export function updateFAQImportResultDisplayStatus(knowledgeBaseId: string, displayStatus: 'open' | 'close') {
  return put(`/api/v1/knowledge-bases/${knowledgeBaseId}/faq/import/last-result/display`, {
    display_status: displayStatus
  });
}

export function searchKnowledge(
  keyword: string,
  offset = 0,
  limit = 20,
  fileTypes?: string[],
  options?: { agent_id?: string; recent?: boolean }
) {
  const query = new URLSearchParams();
  if (keyword) {
    query.set('keyword', keyword);
  }
  query.set('offset', String(offset));
  query.set('limit', String(limit));
  if (fileTypes && fileTypes.length > 0) {
    query.set('file_types', fileTypes.join(','));
  }
  if (options?.agent_id) query.set('agent_id', options.agent_id);
  if (options?.recent) query.set('recent', 'true');
  return get(`/api/v1/knowledge/search?${query.toString()}`);
}

export function knowledgeSemanticSearch(data: {
  query: string;
  knowledge_base_ids?: string[];
  knowledge_ids?: string[];
}) {
  return post('/api/v1/knowledge-search', data);
}

export function batchReparseKnowledge(kbId: string, ids: string[], processConfig?: KnowledgeProcessOverrides) {
  return post(`/api/v1/knowledge/batch-reparse`, {
    kb_id: kbId,
    ids,
    process_config: processConfig,
  });
}
