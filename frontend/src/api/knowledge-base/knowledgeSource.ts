import { get, put } from '@/utils/request'

export const MAX_SOURCE_BYTES = 2 * 1024 * 1024

export interface KnowledgeSource {
  content: string
  revision: string
  file_name: string
}

export function getKnowledgeSource(id: string) {
  return get(`/api/v1/knowledge/${encodeURIComponent(id)}/content`) as unknown as Promise<{
    success: boolean; data: KnowledgeSource
  }>
}

export function saveKnowledgeSource(id: string, content: string, revision: string) {
  return put(`/api/v1/knowledge/${encodeURIComponent(id)}/content`, { content, revision }) as unknown as Promise<{
    success: boolean; data: { id: string; parse_status: string; [key: string]: unknown }
  }>
}
