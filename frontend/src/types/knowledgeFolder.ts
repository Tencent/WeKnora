import type { KnowledgeProcessOverrides } from './knowledgeProcess'

export const ROOT_FOLDER_ID = ''
export const ROOT_FOLDER_FILTER = '__root__'
export const MAX_FOLDER_DEPTH = 10

export interface KnowledgeFolder {
  id: string
  tenant_id?: number
  knowledge_base_id: string
  parent_id: string
  name: string
  path: string
  depth: number
  sort_order: number
  knowledge_count?: number | null
  children?: KnowledgeFolder[]
  created_at: string
  updated_at: string
}

export interface FolderBreadcrumbItem {
  id: string
  name: string
  isRoot: boolean
}

export interface FileSystemSelection {
  knowledgeIds: Set<string>
  folderIds: Set<string>
}

export interface BatchFolderScopeRequest {
  kb_id: string
  knowledge_ids: string[]
  folder_ids: string[]
}

export interface BatchMoveFolderRequest extends BatchFolderScopeRequest {
  target_folder_id: string
}

export interface BatchReparseFolderRequest {
  kb_id: string
  knowledge_ids: string[]
  process_config?: KnowledgeProcessOverrides
}

export interface ResolveFolderPathsRequest {
  current_folder_id: string
  paths: string[]
}

export interface ResolvedFolderPath {
  relative_path: string
  folder_id: string
}

export interface ResolveFolderPathsResponse {
  paths: ResolvedFolderPath[]
}

export interface KnowledgeListQuery {
  page: number
  page_size: number
  tag_ids?: string
  keyword?: string
  file_type?: string
  parse_status?: string
  source?: string
  start_time?: string
  end_time?: string
  folder_id?: string
}

export function normalizeFolderListParentId(parentId: string): string {
  return parentId === ROOT_FOLDER_FILTER ? ROOT_FOLDER_ID : parentId
}

export function serializeFolderForBrowse(
  folderId: string,
  searchActive: boolean,
): string | undefined {
  if (searchActive) return undefined
  return serializeFolderForUpload(folderId)
}

export function serializeFolderForUpload(folderId: string): string {
  return folderId || ROOT_FOLDER_FILTER
}
