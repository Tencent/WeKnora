export interface KnowledgeFolder {
  id: string
  parent_id: string
  name: string
  depth: number
  sort_order: number
  created_at: string
  updated_at: string
}

export interface KnowledgeFolderWithStats extends KnowledgeFolder {
  knowledge_count: number
  has_children: boolean
}

export interface KnowledgeFolderCreateRequest {
  parent_id: string
  name: string
  sort_order?: number
}

export interface KnowledgeFolderUpdateRequest {
  parent_id?: string
  name?: string
  sort_order?: number
}

export interface KnowledgeFolderBreadcrumb {
  id: string
  parent_id: string
  name: string
  depth: number
}

export type KnowledgeFolderBreadcrumbItem = KnowledgeFolderBreadcrumb

export interface KnowledgeFolderListParams {
  parent_id: string
  page?: number
  page_size?: number
}

export interface KnowledgeFolderListPage {
  total: number
  page: number
  page_size: number
  data: KnowledgeFolderWithStats[]
}

export interface KnowledgeFolderApiEnvelope<T> {
  success: boolean
  data: T
}

export type KnowledgeFolderListEnvelope = KnowledgeFolderApiEnvelope<KnowledgeFolderListPage>
export type KnowledgeFolderDetailEnvelope = KnowledgeFolderApiEnvelope<KnowledgeFolderWithStats>
export type KnowledgeFolderBreadcrumbEnvelope = KnowledgeFolderApiEnvelope<KnowledgeFolderBreadcrumb[]>
export type KnowledgeFolderMutationEnvelope = KnowledgeFolderApiEnvelope<KnowledgeFolder>

export interface KnowledgeFolderEnsurePathInput {
  client_key: string
  segments: string[]
}

export interface KnowledgeFolderEnsurePathsRequest {
  parent_id: string
  paths: KnowledgeFolderEnsurePathInput[]
}

export interface KnowledgeFolderEnsurePathResult {
  client_key: string
  folder_id: string
}

export interface KnowledgeFolderEnsurePathsData {
  items: KnowledgeFolderEnsurePathResult[]
}

export type KnowledgeFolderEnsurePathsResponse =
  KnowledgeFolderApiEnvelope<KnowledgeFolderEnsurePathsData>
