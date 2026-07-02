import { ref, computed, unref, type MaybeRef } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  getFolderTree,
  listFolders,
  getBreadcrumb,
  createFolder,
  updateFolder,
  deleteFolder,
  moveFolder,
  batchMoveKnowledgeToFolder,
} from '@/api/knowledge-folder';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

/**
 * Knowledge Folder Management Composable
 * 文件夹管理组合式函数
 */
export function useKnowledgeFolder(kbId: MaybeRef<string>) {
  const resolvedKbId = computed(() => unref(kbId));

  // 当前文件夹 ID (null = 根目录)
  const currentFolderId = ref<string | null>(null);

  // 当前文件夹下的文件夹列表
  const folders = ref<KnowledgeFolder[]>([]);

  // 面包屑路径
  const breadcrumbPath = ref<KnowledgeFolder[]>([]);

  // 加载状态
  const foldersLoading = ref(false);
  const breadcrumbLoading = ref(false);

  // 完整的文件夹树（用于选择目标文件夹）
  const folderTree = ref<KnowledgeFolder[]>([]);
  const treeLoading = ref(false);

  /**
   * Load folders in current directory
   * 加载当前目录下的文件夹
   */
  const loadFolders = async (parentId: string | null = null) => {
    if (!resolvedKbId.value) return;

    foldersLoading.value = true;
    try {
      const res = await listFolders(resolvedKbId.value, parentId);
      folders.value = res || [];
    } catch (error: any) {
      console.error('Failed to load folders:', error);
      folders.value = [];
    } finally {
      foldersLoading.value = false;
    }
  };

  /**
   * Load breadcrumb path
   * 加载面包屑路径
   */
  const loadBreadcrumb = async (folderId: string | null) => {
    if (!folderId) {
      breadcrumbPath.value = [];
      return;
    }

    breadcrumbLoading.value = true;
    try {
      const res = await getBreadcrumb(resolvedKbId.value, folderId);
      breadcrumbPath.value = res || [];
    } catch (error: any) {
      console.error('Failed to load breadcrumb:', error);
      breadcrumbPath.value = [];
    } finally {
      breadcrumbLoading.value = false;
    }
  };

  /**
   * Load full folder tree
   * 加载完整的文件夹树
   */
  const loadFolderTree = async () => {
    if (!resolvedKbId.value) return;

    treeLoading.value = true;
    try {
      const res = await getFolderTree(resolvedKbId.value);
      folderTree.value = res || [];
    } catch (error: any) {
      console.error('Failed to load folder tree:', error);
      folderTree.value = [];
    } finally {
      treeLoading.value = false;
    }
  };

  /**
   * Navigate to a folder
   * 导航到指定文件夹
   */
  const navigateToFolder = async (folderId: string | null) => {
    currentFolderId.value = folderId;
    await Promise.all([
      loadFolders(folderId),
      loadBreadcrumb(folderId),
    ]);
  };

  /**
   * Create a new folder
   * 创建新文件夹
   */
  const handleCreateFolder = async (data: {
    name: string;
    color?: string;
    description?: string;
  }) => {
    try {
      await createFolder(resolvedKbId.value, {
        ...data,
        parent_folder_id: currentFolderId.value,
      });
      MessagePlugin.success('文件夹创建成功');
      await loadFolders(currentFolderId.value);
      return true;
    } catch (error: any) {
      MessagePlugin.error(error.message || '文件夹创建失败');
      return false;
    }
  };

  /**
   * Update folder properties
   * 更新文件夹属性
   */
  const handleUpdateFolder = async (
    folderId: string,
    data: { name?: string; color?: string; description?: string }
  ) => {
    try {
      await updateFolder(resolvedKbId.value, folderId, data);
      MessagePlugin.success('文件夹更新成功');
      await loadFolders(currentFolderId.value);
      return true;
    } catch (error: any) {
      MessagePlugin.error(error.message || '文件夹更新失败');
      return false;
    }
  };

  /**
   * Delete a folder
   * 删除文件夹
   */
  const handleDeleteFolder = async (folderId: string, force = false) => {
    try {
      await deleteFolder(resolvedKbId.value, folderId, force);
      MessagePlugin.success('文件夹已删除');
      await loadFolders(currentFolderId.value);
      return true;
    } catch (error: any) {
      MessagePlugin.error(error.message || '文件夹删除失败');
      return false;
    }
  };

  /**
   * Move folder to new parent
   * 移动文件夹
   */
  const handleMoveFolder = async (folderId: string, targetParentId: string | null) => {
    try {
      await moveFolder(resolvedKbId.value, folderId, { target_parent_folder_id: targetParentId });
      MessagePlugin.success('文件夹已移动');
      await loadFolders(currentFolderId.value);
      return true;
    } catch (error: any) {
      MessagePlugin.error(error.message || '文件夹移动失败');
      return false;
    }
  };

  /**
   * Batch move knowledge to folder
   * 批量移动知识到文件夹
   */
  const handleBatchMoveKnowledge = async (
    knowledgeIds: string[],
    targetFolderId: string | null
  ) => {
    try {
      await batchMoveKnowledgeToFolder({
        knowledge_ids: knowledgeIds,
        folder_id: targetFolderId,
      });
      MessagePlugin.success(`已移动 ${knowledgeIds.length} 个文件`);
      return true;
    } catch (error: any) {
      MessagePlugin.error(error.message || '批量移动失败');
      return false;
    }
  };

  /**
   * Get current folder name for display
   * 获取当前文件夹名称用于显示
   */
  const currentFolderName = computed(() => {
    if (!currentFolderId.value) return '根目录';
    if (breadcrumbPath.value.length > 0) {
      return breadcrumbPath.value[breadcrumbPath.value.length - 1]?.name || '';
    }
    return '';
  });

  /**
   * Get full path string
   * 获取完整路径字符串
   */
  const currentFolderPath = computed(() => {
    if (!currentFolderId.value) return '根目录';
    return breadcrumbPath.value.map((f) => f.name).join(' / ');
  });

  return {
    // State
    currentFolderId,
    folders,
    breadcrumbPath,
    folderTree,
    foldersLoading,
    breadcrumbLoading,
    treeLoading,

    // Computed
    currentFolderName,
    currentFolderPath,

    // Actions
    loadFolders,
    loadBreadcrumb,
    loadFolderTree,
    navigateToFolder,
    handleCreateFolder,
    handleUpdateFolder,
    handleDeleteFolder,
    handleMoveFolder,
    handleBatchMoveKnowledge,
  };
}
