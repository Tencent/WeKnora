import { get, post, put, del } from '@/utils/request';
import type {
  KnowledgeFolder,
  CreateFolderRequest,
  UpdateFolderRequest,
  MoveFolderRequest,
  MoveKnowledgeToFolderRequest,
  BatchMoveKnowledgeToFolderRequest,
} from '@/types/knowledgeFolder';

/**
 * Knowledge Folder API
 * 知识库文件夹 API
 */

/**
 * Create a new folder
 * 创建文件夹
 */
export function createFolder(kbId: string, data: CreateFolderRequest) {
  return post(`/api/v1/knowledge-bases/${kbId}/folders`, data);
}

/**
 * List folders under a parent (or root if parent_id not provided)
 * 列出文件夹
 */
export function listFolders(kbId: string, parentId?: string | null) {
  const params = parentId ? { parent_id: parentId } : {};
  return get(`/api/v1/knowledge-bases/${kbId}/folders`, { params });
}

/**
 * Get full folder tree
 * 获取文件夹树
 */
export function getFolderTree(kbId: string) {
  return get(`/api/v1/knowledge-bases/${kbId}/folders/tree`);
}

/**
 * Get folder details
 * 获取文件夹详情
 */
export function getFolder(kbId: string, folderId: string) {
  return get(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}`);
}

/**
 * Update folder properties
 * 更新文件夹
 */
export function updateFolder(kbId: string, folderId: string, data: UpdateFolderRequest) {
  return put(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}`, data);
}

/**
 * Delete folder
 * 删除文件夹
 */
export function deleteFolder(kbId: string, folderId: string, force = false) {
  const url = force
    ? `/api/v1/knowledge-bases/${kbId}/folders/${folderId}?force=true`
    : `/api/v1/knowledge-bases/${kbId}/folders/${folderId}`;
  return del(url);
}

/**
 * Move folder to new parent
 * 移动文件夹
 */
export function moveFolder(kbId: string, folderId: string, data: MoveFolderRequest) {
  return post(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}/move`, data);
}

/**
 * Get breadcrumb path
 * 获取面包屑路径
 */
export function getBreadcrumb(kbId: string, folderId: string) {
  return get(`/api/v1/knowledge-bases/${kbId}/folders/${folderId}/breadcrumb`);
}

/**
 * Move knowledge to folder
 * 移动知识到文件夹
 */
export function moveKnowledgeToFolder(knowledgeId: string, data: MoveKnowledgeToFolderRequest) {
  return put(`/api/v1/knowledges/${knowledgeId}/folder`, data);
}

/**
 * Batch move knowledge to folder
 * 批量移动知识到文件夹
 */
export function batchMoveKnowledgeToFolder(data: BatchMoveKnowledgeToFolderRequest) {
  return post('/api/v1/knowledges/batch-move-folder', data);
}
