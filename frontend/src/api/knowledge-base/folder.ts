import { del, get, patch, post } from '@/utils/request'
import type { Folder } from '@/types/folder'
import { buildFolderListSearchParams, type FolderListQueryOptions } from './query'
import { buildFolderMovePayload } from './move'

export interface FolderResponse {
  success: boolean
  data: Folder
}

export interface FolderListResponse {
  success: boolean
  data: Folder[]
}

export function listFolders(kbId: string, options: FolderListQueryOptions = {}) {
  const query = buildFolderListSearchParams(options).toString()
  const path = `/api/v1/knowledge-bases/${kbId}/folders`
  return get<FolderListResponse>(query ? `${path}?${query}` : path)
}

export function getFolder(kbId: string, folderId: string) {
  return get<FolderResponse>(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}`)
}

export function createFolder(kbId: string, name: string, parentId: string | null) {
  return post<FolderResponse>(`/api/v1/knowledge-bases/${kbId}/folders`, {
    name,
    parent_id: parentId,
  })
}

export function renameFolder(kbId: string, folderId: string, name: string) {
  return patch<FolderResponse>(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}/name`, {
    name,
  })
}

export function moveFolder(kbId: string, folderId: string, parentId: string | null) {
  return patch<FolderResponse>(
    `/api/v1/knowledge-bases/${kbId}/folders/${folderId}/parent`,
    buildFolderMovePayload(parentId),
  )
}

export function deleteFolder(kbId: string, folderId: string) {
  return del<void>(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}`)
}
