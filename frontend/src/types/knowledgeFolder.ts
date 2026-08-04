export interface KnowledgeFolder {
  id: string
  knowledge_base_id: string
  parent_id: string | null
  name: string
  path: string
  depth: number
  created_at: string
  updated_at: string
}

export interface KnowledgeFolderRow {
  folder: KnowledgeFolder
  depth: number
}
