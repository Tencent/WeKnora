import { get, post, put, del, postUpload, getDown } from "../../utils/request";
import { buildKnowledgeChunksPath } from "./chunkPath";
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess';
import type { AuditLog, AuditOutcome, ListAuditLogResponse } from '@/api/tenant/audit-log';
import { buildListKnowledgeFilesQuery } from './knowledgeFileListQuery';

export { buildListKnowledgeFilesQuery } from './knowledgeFileListQuery';

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
  agent_source_tenant_id?: string;
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
  if (params?.agent_source_tenant_id) query.set('agent_source_tenant_id', params.agent_source_tenant_id);
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
// ---------------------------------------------------------------------------
// Image attribute observation (route 3). Mirrors backend types in
// internal/types/image_attrs.go. The backend registry is the single source of
// truth; the frontend fetches it from GET /image-attrs/schema and renders the
// attribute panel dynamically. The types/constants below mirror the v1 shape
// so the UI still compiles and can render a fallback before the schema loads.
// ---------------------------------------------------------------------------

// One allowed value of an image attribute.
//
// Display text is split for the same reasons the gallery splits it: `label` is
// what fits on a checkbox or a table cell, `description` is the sentence that
// explains the value where there is room. `value` stays the raw machine value.
export interface ImageAttrValue {
  value: string;
  label: string;
  description?: string;
}

// One observable image attribute, as returned by the schema endpoint. The
// registry is authoritative for both the behaviour and the wording: label,
// description and the per-value texts are what the settings panel shows to an
// operator who does not read identifiers like "contain.text".
export interface ImageAttrSpec {
  name: string;
  type: 'extent' | 'presence';
  values?: ImageAttrValue[];
  question: string;
  label: string;
  description?: string;
  consumers?: string[];
}

// One attribute match: attribute `prop` equals `is`.
export interface ImageAttrCondition {
  prop: string;
  is: string;
}

// The OCR clause of the attribute -> work policy.
export interface ImageOCRAction {
  on: ImageAttrCondition[];
  on_unobserved: boolean;
}

// The attribute -> work policy sent back to the API (snake_case JSON).
export interface ImageActionsConfig {
  ocr: ImageOCRAction;
}

// Canonical registry + built-in actions returned by GET /image-attrs/schema.
export interface ImageAttrSchema {
  version: string;
  prompt: string;
  attributes: ImageAttrSpec[];
  default_actions: ImageActionsConfig;
}

// Mirrors backend types.DefaultImageActions. The schema endpoint returns the
// authoritative copy; this is the fallback used before the schema loads (e.g.
// in create mode, where no knowledge base id exists yet to query).
export const DEFAULT_IMAGE_ACTIONS: ImageActionsConfig = {
  ocr: {
    on: [
      { prop: 'contain.text', is: 'block' },
      { prop: 'contain.data_visual', is: 'true' },
    ],
    on_unobserved: true,
  },
};

// Display-only fallback used before a KB exists (create mode) so the panel can
// render without a network call. It mirrors the registry for layout and wording
// only; the endpoint response is authoritative whenever it loads.
export const FALLBACK_IMAGE_ATTR_SCHEMA: ImageAttrSchema = {
  version: 'attrs/2',
  prompt: 'observe/1',
  attributes: [
    {
      name: 'contain.text',
      type: 'extent',
      values: [
        { value: 'none', label: 'None', description: 'no text at all' },
        { value: 'sparse', label: 'Sparse', description: 'a few words — a logo, a road sign, a single label' },
        { value: 'block', label: 'Block', description: 'a block of body text — a screenshot, a table, a document page' },
      ],
      question: '',
      label: 'Text in the image',
      description: 'How much body text the picture itself carries.',
      consumers: ['ocr'],
    },
    {
      name: 'contain.data_visual',
      type: 'presence',
      values: [
        { value: 'true', label: 'Yes', description: 'a chart, graph or diagram with plotted values' },
        { value: 'false', label: 'No', description: 'a photo, drawing, icon or decoration' },
      ],
      question: '',
      label: 'Data visual',
      description: 'Whether the picture conveys data as a chart, graph, diagram or infographic.',
      consumers: ['ocr'],
    },
  ],
  default_actions: DEFAULT_IMAGE_ACTIONS,
};

// Mirrors backend types.MergeImageActions: a custom OCR.On replaces the default
// OCR.On wholesale (the "on" list is a unit); a config that only sets
// on_unobserved keeps the default On. The result is what the form seeds with
// and what makes an empty custom table safe to open.
export function mergeImageActions(
  custom?: ImageActionsConfig | null,
): ImageActionsConfig {
  if (custom && custom.ocr && custom.ocr.on && custom.ocr.on.length > 0) {
    return { ocr: { on: custom.ocr.on, on_unobserved: custom.ocr.on_unobserved } };
  }
  return {
    ocr: {
      on: DEFAULT_IMAGE_ACTIONS.ocr.on.map((c) => ({ ...c })),
      on_unobserved: DEFAULT_IMAGE_ACTIONS.ocr.on_unobserved,
    },
  };
}

// Fetch the canonical image-attribute registry + default actions from the
// backend. The registry drives the attribute panel; the default actions seed
// the OCR-condition display.
export async function fetchImageAttrSchema(kbId: string): Promise<ImageAttrSchema> {
  // The registry is global, not per-KB. The backend exposes it at a top-level
  // route, the same shape as the other read-only KB-editor helpers
  // (GET /api/v1/chunker/preview, GET /api/v1/system/parser-engines). kbId is
  // kept in the signature for callers that pass KB context.
  void kbId;
  const res = await get<{ success: boolean; data: ImageAttrSchema }>(
    `/api/v1/image-attrs/schema`,
  );
  return res.data;
}

// Mirrors backend types.ImageProcessingConfig (snake_case JSON). The UI edits
// the attribute-observation switch and the on_unobserved toggle; saving sends
// the snapshot back with those fields updated so API-side settings survive a
// UI edit.
export interface ImageProcessingConfig {
  model_id?: string;
  image_actions?: ImageActionsConfig;
  image_attrs_enabled?: boolean;
}

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
  image_processing_config?: ImageProcessingConfig;
  embedding_model_id?: string;
  summary_model_id?: string;
  auto_tag_config?: { enabled: boolean; model_id?: string; max_tags?: number; skip_if_tagged?: boolean };
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

export function getKnowledgeBaseById(id: string, options?: { agent_id?: string; agent_source_tenant_id?: string }) {
  const query = new URLSearchParams();
  if (options?.agent_id) query.set('agent_id', options.agent_id);
  if (options?.agent_source_tenant_id) query.set('agent_source_tenant_id', options.agent_source_tenant_id);
  const qs = query.toString();
  return get(qs ? `/api/v1/knowledge-bases/${id}?${qs}` : `/api/v1/knowledge-bases/${id}`);
}

export function updateKnowledgeBase(id: string, data: {
  name: string;
  description?: string;
  config?: {
    chunking_config?: any;
    image_processing_config?: ImageProcessingConfig;
    faq_config?: any;
    wiki_config?: {
      synthesis_model_id?: string;
      max_pages_per_ingest?: number;
      extraction_granularity?: 'focused' | 'standard' | 'exhaustive';
      content_instructions?: string;
      extraction_instructions?: string;
    };
    auto_tag_config?: { enabled: boolean; model_id?: string; max_tags?: number; skip_if_tagged?: boolean };
    profile_config?: KnowledgeBaseProfileConfig;
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

/** Opt-in automatic generation of the knowledge-base description. */
export interface KnowledgeBaseProfileConfig {
  enabled: boolean;
  model_id?: string;
  custom_instructions?: string;
}

export interface KnowledgeBaseProfileNamedCount {
  name: string;
  count: number;
}

/**
 * Machine-generated knowledge-base description. Derived from per-document
 * profiles; never overwrites the user-authored description.
 */
export interface KnowledgeBaseProfile {
  gist?: string;
  topics?: string[];
  typical_questions?: string[];
  stats?: {
    document_count: number;
    profiled_count: number;
    file_types?: KnowledgeBaseProfileNamedCount[];
    tags?: KnowledgeBaseProfileNamedCount[];
    raw_topics?: KnowledgeBaseProfileNamedCount[];
    doc_types?: KnowledgeBaseProfileNamedCount[];
    folders?: string[];
    earliest_at?: string;
    latest_at?: string;
  };
  aggregate_hash?: string;
  status?: 'ready' | 'empty' | 'failed' | string;
  error?: string;
  model_id?: string;
  generated_at?: string;
}

/** Regenerates the AI description of a knowledge base synchronously. */
export function generateKnowledgeBaseProfile(id: string) {
  return post(`/api/v1/knowledge-bases/${id}/profile/generate`, {});
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
  config?: { signal?: AbortSignal },
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
  return postUpload(`/api/v1/knowledge-bases/${kbId}/knowledge/file`, formData, onProgress, config);
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

export type KnowledgeListSortField = 'updated_at' | 'created_at' | 'file_name';
export type KnowledgeListSortOrder = 'asc' | 'desc';

export interface ListKnowledgeFilesParams {
  page: number;
  page_size: number;
  tag_ids?: string;
  keyword?: string;
  file_type?: string;
  parse_status?: string;
  source?: string;
  start_time?: string;
  end_time?: string;
  sort_by?: KnowledgeListSortField;
  sort_order?: KnowledgeListSortOrder;
  /**
   * 当前浏览的目录。空字符串表示知识库根目录；未定义时不按目录筛选。
   */
  folder_path?: string;
  /** 是否同时包含 folder_path 下所有子目录中的文档。 */
  folder_recursive?: boolean;
}

export function listKnowledgeFiles(kbId: string, params: ListKnowledgeFilesParams) {
  return get(`/api/v1/knowledge-bases/${kbId}/knowledge?${buildListKnowledgeFilesQuery(params)}`);
}

/** One node of the knowledge base folder tree. */
export interface KnowledgeFolderNode {
  /** Canonical folder path, e.g. "docs/spec". */
  path: string;
  /** Last segment of the path, used as the row label. */
  name: string;
  /** Documents stored directly in this folder. */
  document_count: number;
  /** Documents in this folder plus every descendant folder. */
  total_count: number;
  children?: KnowledgeFolderNode[];
}

export interface KnowledgeFolderTree {
  /** Documents that are not part of any uploaded folder. */
  root_document_count: number;
  /** Documents in the whole knowledge base. */
  total_document_count: number;
  folders: KnowledgeFolderNode[];
}

export function listKnowledgeFolders(kbId: string) {
  return get(`/api/v1/knowledge-bases/${kbId}/knowledge/folders`);
}

/**
 * Re-file documents under `folderPath` ('' = knowledge base top level). Folders
 * are derived from the stored paths, so a path that does not exist yet is
 * created by this call. Only the grouping changes; documents are not re-parsed.
 */
export function moveKnowledgeToFolder(kbId: string, ids: string[], folderPath: string) {
  return post('/api/v1/knowledge/folder', {
    kb_id: kbId,
    knowledge_ids: ids,
    folder_path: folderPath,
  });
}

/** Rename or move a folder together with everything below it. */
export function renameKnowledgeFolder(kbId: string, from: string, to: string) {
  return put(`/api/v1/knowledge-bases/${kbId}/knowledge/folders`, { from, to });
}

export function getKnowledgeDetails(id: string, options?: { agent_id?: string; agent_source_tenant_id?: string }) {
  const query = new URLSearchParams();
  if (options?.agent_id) query.set('agent_id', options.agent_id);
  if (options?.agent_source_tenant_id) query.set('agent_source_tenant_id', options.agent_source_tenant_id);
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

// 使用已有登录和租户请求头下载 ZIP，不将凭据放入下载链接。
export function batchDownloadKnowledge(kbId: string, ids: string[], signal?: AbortSignal): Promise<Blob> {
  return post<Blob>(`/api/v1/knowledge-bases/${encodeURIComponent(kbId)}/knowledge/batch-download`, { ids }, {
    responseType: 'blob',
    timeout: 300000,
    signal,
  });
}

export function previewKnowledgeFile(id: string) {
  return getDown(`/api/v1/knowledge/${id}/preview`);
}

/** @param idsQueryString - query string with ids (e.g. ids=xxx&ids=yyy) */
export function batchQueryKnowledge(idsQueryString: string, kbId?: string, agentId?: string, agentSourceTenantId?: string) {
  let qs = idsQueryString;
  if (kbId) qs += `&kb_id=${encodeURIComponent(kbId)}`;
  if (agentId) qs += `&agent_id=${encodeURIComponent(agentId)}`;
  if (agentSourceTenantId) qs += `&agent_source_tenant_id=${encodeURIComponent(agentSourceTenantId)}`;
  return get(`/api/v1/knowledge/batch?${qs}`);
}

export const KNOWLEDGE_CHUNK_PAGE_SIZE = 25;

export function getKnowledgeDetailsCon(id: string, page: number, chunkType?: string) {
  return get(buildKnowledgeChunksPath(id, page, KNOWLEDGE_CHUNK_PAGE_SIZE, chunkType));
}

export interface ChunkEditPayload {
  content?: string;
  is_enabled?: boolean;
  expected_revision?: number;
}

export function updateDocumentChunk(knowledgeId: string, chunkId: string, data: ChunkEditPayload) {
  return put(`/api/v1/chunks/${knowledgeId}/${chunkId}`, data);
}

export function listChunkRevisions(knowledgeId: string, chunkId: string) {
  return get(`/api/v1/chunks/${knowledgeId}/${chunkId}/revisions`);
}

export function revertDocumentChunk(knowledgeId: string, chunkId: string, revision: number, expectedRevision: number) {
  return post(`/api/v1/chunks/${knowledgeId}/${chunkId}/revert`, {
    revision,
    expected_revision: expectedRevision,
  });
}

export function updateKnowledgeMetadata(knowledgeId: string, customMetadata: Record<string, unknown>) {
  return put(`/api/v1/knowledge/${knowledgeId}`, { custom_metadata: customMetadata });
}

export function updateKnowledgeSummary(knowledgeId: string, description: string) {
  return put(`/api/v1/knowledge/${knowledgeId}`, { description });
}

export function regenerateKnowledgeSummary(knowledgeId: string) {
  return post(`/api/v1/knowledge/${knowledgeId}/regenerate-summary`, {});
}

// Get chunk by chunk_id only (new endpoint - to be added to backend)
export function getChunkByIdOnly(chunkId: string) {
  return get(`/api/v1/chunks/by-id/${chunkId}`);
}

// Delete a single generated question from a chunk by question ID
export function deleteGeneratedQuestion(chunkId: string, questionId: string) {
  return del(`/api/v1/chunks/by-id/${chunkId}/questions`, { question_id: questionId });
}

export function upsertGeneratedQuestion(chunkId: string, question: string, questionId?: string) {
  return put(`/api/v1/chunks/by-id/${chunkId}/questions`, {
    question_id: questionId || '',
    question,
  });
}

export function regenerateGeneratedQuestions(chunkId: string) {
  return post(`/api/v1/chunks/by-id/${chunkId}/questions/regenerate`, {});
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
  params?: {
    page?: number
    page_size?: number
    tag_id?: number
    tag_ids?: string
    keyword?: string
    is_enabled?: boolean
  },
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
  options?: { agent_id?: string; agent_source_tenant_id?: string; recent?: boolean }
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
  if (options?.agent_source_tenant_id) query.set('agent_source_tenant_id', options.agent_source_tenant_id);
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
