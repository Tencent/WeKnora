import { get, post, put, del } from '@/utils/request';

export interface KnowledgeFolder {
  id: string;
  tenant_id: number;
  knowledge_base_id: string;
  parent_id: string;
  name: string;
  path: string;
  depth: number;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

export interface KnowledgeFolderWithStats extends KnowledgeFolder {
  knowledge_count: number;
  child_count: number;
}

export interface FolderTreeNode {
  id: string;
  name: string;
  parent_id: string;
  path: string;
  depth: number;
  sort_order: number;
  knowledge_count: number;
  child_count: number;
  children: FolderTreeNode[];
  created_at: string;
  updated_at: string;
}

export function listFolders(kbId: string): Promise<{ data: KnowledgeFolderWithStats[] }> {
  return get(`/api/v1/knowledge-bases/${kbId}/folders`);
}

export function getFolderTree(kbId: string): Promise<{ data: FolderTreeNode[] }> {
  return get(`/api/v1/knowledge-bases/${kbId}/folders/tree`);
}

export function createFolder(kbId: string, name: string, parentId?: string): Promise<{ data: KnowledgeFolder }> {
  return post(`/api/v1/knowledge-bases/${kbId}/folders`, { name, parent_id: parentId || '' });
}

export function renameFolder(kbId: string, folderId: string, name: string): Promise<{ data: KnowledgeFolder }> {
  return put(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}`, { name });
}

export function deleteFolder(kbId: string, folderId: string): Promise<void> {
  return del(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}`);
}

export function moveKnowledgeToFolder(knowledgeIds: string[], folderId: string): Promise<void> {
  return post('/api/v1/knowledge/folders/move', { knowledge_ids: knowledgeIds, folder_id: folderId });
}
