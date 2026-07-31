import { del, get, patch, post } from '@/utils/request'
import type {
  KnowledgeFolderBreadcrumbEnvelope,
  KnowledgeFolderCreateRequest,
  KnowledgeFolderDetailEnvelope,
  KnowledgeFolderEnsurePathsRequest,
  KnowledgeFolderEnsurePathsResponse,
  KnowledgeFolderListEnvelope,
  KnowledgeFolderListParams,
  KnowledgeFolderMutationEnvelope,
  KnowledgeFolderUpdateRequest,
} from '@/types/knowledgeFolder'

const folderBasePath = (kbId: string) =>
  `/api/v1/knowledge-bases/${encodeURIComponent(kbId)}/folders`

export function listKnowledgeFolders(
  kbId: string,
  params: KnowledgeFolderListParams,
): Promise<KnowledgeFolderListEnvelope> {
  const query = new URLSearchParams()
  query.set('parent_id', params.parent_id)
  if (params.page !== undefined) query.set('page', String(params.page))
  if (params.page_size !== undefined) query.set('page_size', String(params.page_size))
  return get<KnowledgeFolderListEnvelope>(`${folderBasePath(kbId)}?${query.toString()}`)
}

export function getKnowledgeFolder(
  kbId: string,
  folderId: string,
): Promise<KnowledgeFolderDetailEnvelope> {
  return get<KnowledgeFolderDetailEnvelope>(
    `${folderBasePath(kbId)}/${encodeURIComponent(folderId)}`,
  )
}

export function getKnowledgeFolderBreadcrumb(
  kbId: string,
  folderId: string,
): Promise<KnowledgeFolderBreadcrumbEnvelope> {
  return get<KnowledgeFolderBreadcrumbEnvelope>(
    `${folderBasePath(kbId)}/${encodeURIComponent(folderId)}/breadcrumb`,
  )
}

export function createKnowledgeFolder(
  kbId: string,
  request: KnowledgeFolderCreateRequest,
): Promise<KnowledgeFolderMutationEnvelope> {
  return post<KnowledgeFolderMutationEnvelope>(folderBasePath(kbId), request)
}

export function updateKnowledgeFolder(
  kbId: string,
  folderId: string,
  request: KnowledgeFolderUpdateRequest,
): Promise<KnowledgeFolderMutationEnvelope> {
  return patch<KnowledgeFolderMutationEnvelope>(
    `${folderBasePath(kbId)}/${encodeURIComponent(folderId)}`,
    request,
  )
}

export function deleteKnowledgeFolder(
  kbId: string,
  folderId: string,
): Promise<void> {
  return del<void>(`${folderBasePath(kbId)}/${encodeURIComponent(folderId)}`)
}

export function ensureKnowledgeFolderPaths(
  kbId: string,
  request: KnowledgeFolderEnsurePathsRequest,
): Promise<KnowledgeFolderEnsurePathsResponse> {
  return post<KnowledgeFolderEnsurePathsResponse>(
    `${folderBasePath(kbId)}/ensure-paths`,
    request,
  )
}
