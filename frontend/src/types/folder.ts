export interface Folder {
  id: string
  knowledge_base_id: string
  parent_id: string | null
  name: string
  created_at: string
  updated_at: string
}

export interface FolderTreeNode {
  folder: Folder
  children: FolderTreeNode[]
}

export interface FolderTreeRow {
  folder: Folder
  depth: number
  hasChildren: boolean
}
