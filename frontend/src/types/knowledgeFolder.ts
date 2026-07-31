/**
 * Knowledge Folder Types
 * 知识库文件夹类型定义
 */

export interface KnowledgeFolder {
  id: string;
  tenant_id: number;
  knowledge_base_id: string;
  name: string;
  parent_folder_id?: string | null;
  path: string;
  depth: number;
  sort_order: number;
  color?: string;
  description?: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
  // 查询时填充的字段
  children?: KnowledgeFolder[];
  knowledge_count?: number;
}

export interface CreateFolderRequest {
  name: string;
  parent_folder_id?: string | null;
  color?: string;
  description?: string;
}

export interface UpdateFolderRequest {
  name?: string;
  color?: string;
  description?: string;
  sort_order?: number;
}

export interface MoveFolderRequest {
  target_parent_folder_id?: string | null;
}

export interface MoveKnowledgeToFolderRequest {
  folder_id?: string | null;
}

export interface BatchMoveKnowledgeToFolderRequest {
  knowledge_ids: string[];
  folder_id?: string | null;
}

export interface FolderTreeNode extends KnowledgeFolder {
  children: FolderTreeNode[];
}

/**
 * View mode for displaying folders and files
 */
export type ViewMode = 'list' | 'grid';

/**
 * Breadcrumb item for navigation
 */
export interface BreadcrumbItem {
  id: string | null;
  name: string;
  path: string;
}
