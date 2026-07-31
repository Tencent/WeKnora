export interface FolderScopeSelection {
  knowledgeBaseId: string
  knowledgeBaseName: string
  folderId: string
  folderName: string
  folderPath: string[]
  ancestorFolderIds: string[]
  includeDescendants: true
}

export interface FolderScopeRequest {
  knowledge_base_id: string
  folder_ids: string[]
  include_descendants?: boolean
}

export interface TagScopeRequest {
  knowledge_base_id: string
  tag_ids: string[]
}

export interface KnowledgeScopeRequest {
  knowledge_base_ids?: string[]
  knowledge_ids?: string[]
  tag_scopes?: TagScopeRequest[]
  folder_scopes?: FolderScopeRequest[]
}

export interface KnowledgeScopeTagSelection {
  id: string
  kbId: string
}

export interface KnowledgeScopeKnowledgeSelection {
  id: string
  kbId?: string
}

export interface KnowledgeScopeBuildInput {
  knowledgeBaseIds: readonly string[]
  knowledgeIds: readonly string[]
  knowledgeSelections?: readonly KnowledgeScopeKnowledgeSelection[]
  tags: readonly KnowledgeScopeTagSelection[]
  folderSelections: readonly FolderScopeSelection[]
}

export interface KnowledgeScopeProjection {
  knowledge_base_ids: string[]
  knowledge_scope?: KnowledgeScopeRequest
}
