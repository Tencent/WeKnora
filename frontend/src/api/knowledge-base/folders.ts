import { get, post, put } from '@/utils/request'
import { normalizeFolderListParentId } from '@/types/knowledgeFolder'
import type {
  BatchFolderScopeRequest,
  BatchMoveFolderRequest,
  KnowledgeFolder,
  ResolveFolderPathsRequest,
  ResolveFolderPathsResponse,
} from '@/types/knowledgeFolder'

export const knowledgeFolderApi = {
  create(kbId: string, input: { parent_id: string; name: string }) {
    return post<KnowledgeFolder>(`/api/v1/knowledge-bases/${kbId}/folders`, input)
  },
  list(kbId: string, parentId: string) {
    const query = new URLSearchParams({
      parent_id: normalizeFolderListParentId(parentId),
    })
    return get<KnowledgeFolder[]>(`/api/v1/knowledge-bases/${kbId}/folders?${query}`)
  },
  tree(kbId: string) {
    return get<KnowledgeFolder[]>(`/api/v1/knowledge-bases/${kbId}/folders/tree`)
  },
  resolvePaths(kbId: string, input: ResolveFolderPathsRequest) {
    return post<ResolveFolderPathsResponse>(
      `/api/v1/knowledge-bases/${kbId}/folders/resolve-paths`,
      input,
    )
  },
  get(kbId: string, folderId: string) {
    return get<KnowledgeFolder>(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}`)
  },
  rename(kbId: string, folderId: string, name: string) {
    return put<KnowledgeFolder>(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}`, { name })
  },
  move(kbId: string, folderId: string, parentId: string) {
    return post<KnowledgeFolder>(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}/move`, {
      parent_id: parentId,
    })
  },
  breadcrumb(kbId: string, folderId: string) {
    return get<KnowledgeFolder[]>(
      `/api/v1/knowledge-bases/${kbId}/folders/${folderId}/breadcrumb`,
    )
  },
  moveKnowledge(knowledgeId: string, folderId: string) {
    return put<void>(`/api/v1/knowledges/${knowledgeId}/folder`, { folder_id: folderId })
  },
  batchMove(input: BatchMoveFolderRequest) {
    return post<{ success: boolean; moved_count: number }>(
      '/api/v1/knowledges/batch-move-folder',
      input,
    )
  },
  batchDelete(input: BatchFolderScopeRequest) {
    return post<{ success: boolean; data: { task_id: string; deleted_count: number } }>(
      '/api/v1/knowledge/batch-delete',
      input,
    )
  },
}
