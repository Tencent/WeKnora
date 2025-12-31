import { get, post, put, del, postUpload, getDown } from "../../utils/request";

// 知识库管理 API（列表、创建、获取、更新、删除、复制）
export function listKnowledgeBases() {
  return get(`/api/v1/knowledge-bases`);
}

export function createKnowledgeBase(data: { 
  name: string; 
  description?: string; 
  type?: 'document' | 'faq';
  chunking_config?: any;
  embedding_model_id?: string;
  summary_model_id?: string;
  vlm_config?: {
    enabled: boolean;
    model_id?: string;
  };
  cos_config?: any;
  extract_config?: any;
  faq_config?: { index_mode: string; question_index_mode?: string };
}) {
  return post(`/api/v1/knowledge-bases`, data);
}

export function getKnowledgeBaseById(id: string) {
  return get(`/api/v1/knowledge-bases/${id}`);
}

export function updateKnowledgeBase(id: string, data: { name: string; description?: string; config: any }) {
  return put(`/api/v1/knowledge-bases/${id}` , data);
}

export function deleteKnowledgeBase(id: string) {
  return del(`/api/v1/knowledge-bases/${id}`);
}

export function copyKnowledgeBase(data: { source_id: string; target_id?: string }) {
  return post(`/api/v1/knowledge-bases/copy`, data);
}

// 知识文件 API（基于具体知识库）
export function uploadKnowledgeFile(kbId: string, data = {}, onProgress?: (progressEvent: any) => void) {
  return postUpload(`/api/v1/knowledge-bases/${kbId}/knowledge/file`, data, onProgress);
}

export function createKnowledgeFromURL(kbId: string, data: { url: string; enable_multimodel?: boolean }) {
  return post(`/api/v1/knowledge-bases/${kbId}/knowledge/url`, data);
}

export function createManualKnowledge(kbId: string, data: { title: string; content: string; status: string }) {
  return post(`/api/v1/knowledge-bases/${kbId}/knowledge/manual`, data);
}

export function listKnowledgeFiles(
  kbId: string,
  params: { page: number; page_size: number; tag_id?: string; keyword?: string; file_type?: string },
) {
  const query = new URLSearchParams();
  query.append('page', String(params.page));
  query.append('page_size', String(params.page_size));
  if (params.tag_id) {
    query.append('tag_id', params.tag_id);
  }
  if (params.keyword) {
    query.append('keyword', params.keyword);
  }
  if (params.file_type) {
    query.append('file_type', params.file_type);
  }
  const qs = query.toString();
  return get(`/api/v1/knowledge-bases/${kbId}/knowledge?${qs}`);
}

export function getKnowledgeDetails(id: string) {
  return get(`/api/v1/knowledge/${id}`);
}

export function updateManualKnowledge(id: string, data: { title: string; content: string; status: string }) {
  return put(`/api/v1/knowledge/manual/${id}`, data);
}

export function delKnowledgeDetails(id: string) {
  return del(`/api/v1/knowledge/${id}`);
}

export function downKnowledgeDetails(id: string) {
  return getDown(`/api/v1/knowledge/${id}/download`);
}

export function batchQueryKnowledge(idsQueryString: string) {
  return get(`/api/v1/knowledge/batch?${idsQueryString}`);
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

export function deleteKnowledgeBaseTag(kbId: string, tagId: string, params?: { force?: boolean }) {
  const forceQuery = params?.force ? '?force=true' : '';
  return del(`/api/v1/knowledge-bases/${kbId}/tags/${tagId}${forceQuery}`);
}

export function updateKnowledgeTagBatch(data: { updates: Record<string, string | null> }) {
  return put(`/api/v1/knowledge/tags`, data);
}

export function updateFAQEntryTagBatch(kbId: string, data: { updates: Record<string, string | null> }) {
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
  params?: { page?: number; page_size?: number; tag_id?: string; keyword?: string },
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

export function updateFAQEntry(kbId: string, entryId: string, data: any) {
  return put(`/api/v1/knowledge-bases/${kbId}/faq/entries/${entryId}`, data);
}

// Unified batch update API - supports is_enabled, is_recommended, tag_id
// Supports two modes:
// 1. By entry ID: use by_id field
// 2. By Tag: use by_tag field to apply the same update to all entries under a tag
export interface FAQEntryFieldsUpdate {
  is_enabled?: boolean
  is_recommended?: boolean
  tag_id?: string | null
}

export interface FAQEntryFieldsBatchRequest {
  by_id?: Record<string, FAQEntryFieldsUpdate>
  by_tag?: Record<string, FAQEntryFieldsUpdate>
}

export function updateFAQEntryFieldsBatch(kbId: string, data: FAQEntryFieldsBatchRequest) {
  return put(`/api/v1/knowledge-bases/${kbId}/faq/entries/fields`, data);
}

export function deleteFAQEntries(kbId: string, ids: string[]) {
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

// Export FAQ entries as CSV file
export async function exportFAQEntries(kbId: string): Promise<Blob> {
  const response = await getDown(`/api/v1/knowledge-bases/${kbId}/faq/entries/export`);
  return response as unknown as Blob;
}

// FAQ Import Progress API
export interface FAQImportProgress {
  task_id: string
  kb_id: string
  knowledge_id: string
  status: 'pending' | 'processing' | 'completed' | 'failed'
  progress: number
  total: number
  processed: number
  message: string
  error: string
  created_at: number
  updated_at: number
}

export function getFAQImportProgress(taskId: string) {
  return get(`/api/v1/faq/import/progress/${taskId}`);
}

export function searchKnowledge(keyword: string, offset = 0, limit = 20, fileTypes?: string[]) {
  let url = `/api/v1/knowledge/search?keyword=${encodeURIComponent(keyword)}&offset=${offset}&limit=${limit}`;
  if (fileTypes && fileTypes.length > 0) {
    url += `&file_types=${encodeURIComponent(fileTypes.join(','))}`;
  }
  return get(url);
}
