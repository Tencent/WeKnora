<template>
  <div class="knowledge-folder-view">
    <!-- 面包屑导航 -->
    <div v-if="showBreadcrumb" class="folder-navigation">
      <FolderBreadcrumb
        :kb-id="kbId"
        :current-folder-id="currentFolderId"
        @navigate="handleNavigateToFolder"
      />
    </div>

    <!-- 工具栏 -->
    <div class="folder-toolbar">
      <div class="toolbar-left">
        <!-- 新建文件夹按钮 -->
        <t-button v-if="canEdit" theme="default" @click="handleCreateFolder">
          <template #icon>
            <t-icon name="folder-add" />
          </template>
          新建文件夹
        </t-button>

        <!-- 当前路径提示 -->
        <div class="current-path-hint">
          <t-icon name="folder-open" />
          <span>{{ currentFolderPath }}</span>
        </div>
      </div>

      <div class="toolbar-right">
        <!-- 视图切换 -->
        <t-radio-group v-model="viewMode" variant="default-filled" size="small">
          <t-radio-button value="list">
            <t-icon name="view-list" />
          </t-radio-button>
          <t-radio-button value="grid">
            <t-icon name="view-module" />
          </t-radio-button>
        </t-radio-group>
      </div>
    </div>

    <!-- 列表视图 -->
    <FolderListView
      v-if="viewMode === 'list'"
      :folders="folders"
      :files="files"
      :loading="loading"
      @enter-folder="handleEnterFolder"
      @batch-move="handleBatchMove"
      @batch-delete="handleBatchDelete"
    />

    <!-- 宫格视图 -->
    <FolderGridView
      v-else
      :folders="folders"
      :files="files"
      :loading="loading"
      @enter-folder="handleEnterFolder"
      @batch-move="handleBatchMove"
      @batch-delete="handleBatchDelete"
    />

    <!-- 文件夹管理对话框 -->
    <FolderManageDialog
      v-model:visible="folderDialogVisible"
      :kb-id="kbId"
      :mode="folderDialogMode"
      :folder="currentEditFolder"
      :parent-folder-id="currentFolderId"
      @success="handleFolderDialogSuccess"
    />

    <!-- 文件夹选择器（用于批量移动） -->
    <FolderSelector
      v-model:visible="folderSelectorVisible"
      title="移动到文件夹"
      :folder-tree="folderTree"
      :tree-loading="treeLoading"
      :current-folder-id="currentFolderId"
      @confirm="handleConfirmMove"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import FolderBreadcrumb from '@/views/knowledge/components/FolderBreadcrumb.vue';
import FolderListView from '@/views/knowledge/components/FolderListView.vue';
import FolderGridView from '@/views/knowledge/components/FolderGridView.vue';
import FolderManageDialog from '@/views/knowledge/components/FolderManageDialog.vue';
import FolderSelector from '@/views/knowledge/components/FolderSelector.vue';
import { useKnowledgeFolder } from '@/composables/useKnowledgeFolder';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

interface Props {
  kbId: string;
  files: any[];
  loading?: boolean;
  canEdit?: boolean;
  showBreadcrumb?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
  canEdit: true,
  showBreadcrumb: true,
});

const emit = defineEmits<{
  (e: 'folder-change', folderId: string | null): void;
  (e: 'batch-move', items: any[]): void;
  (e: 'batch-delete', items: any[]): void;
  (e: 'refresh'): void;
}>();

// View mode (list / grid)
const viewMode = ref<'list' | 'grid'>('list');

// Folder management
const {
  currentFolderId,
  folders,
  breadcrumbPath,
  folderTree,
  foldersLoading,
  treeLoading,
  currentFolderPath,
  navigateToFolder,
  loadFolderTree,
} = useKnowledgeFolder(props.kbId);

// Folder dialog
const folderDialogVisible = ref(false);
const folderDialogMode = ref<'create' | 'edit'>('create');
const currentEditFolder = ref<KnowledgeFolder | null>(null);

// Folder selector for batch move
const folderSelectorVisible = ref(false);
const pendingMoveItems = ref<any[]>([]);

// Navigate to folder
const handleNavigateToFolder = async (folderId: string | null) => {
  await navigateToFolder(folderId);
  emit('folder-change', folderId);
  emit('refresh');
};

// Enter folder (double click)
const handleEnterFolder = (folderId: string) => {
  handleNavigateToFolder(folderId);
};

// Create folder
const handleCreateFolder = () => {
  folderDialogMode.value = 'create';
  currentEditFolder.value = null;
  folderDialogVisible.value = true;
};

// Folder dialog success
const handleFolderDialogSuccess = () => {
  emit('refresh');
};

// Batch move
const handleBatchMove = async (items: any[]) => {
  pendingMoveItems.value = items;
  await loadFolderTree();
  folderSelectorVisible.value = true;
};

// Confirm move
const handleConfirmMove = (targetFolderId: string | null) => {
  emit('batch-move', pendingMoveItems.value);
  pendingMoveItems.value = [];
};

// Batch delete
const handleBatchDelete = (items: any[]) => {
  emit('batch-delete', items);
};

// Watch kb change
watch(
  () => props.kbId,
  (newKbId) => {
    if (newKbId) {
      navigateToFolder(null);
    }
  },
  { immediate: true }
);
</script>

<style scoped lang="less">
.knowledge-folder-view {
  display: flex;
  flex-direction: column;
  gap: 16px;

  .folder-navigation {
    padding: 0 4px;
  }

  .folder-toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 12px 16px;
    background: var(--td-bg-color-container);
    border: 1px solid var(--td-component-stroke);
    border-radius: 6px;

    .toolbar-left {
      display: flex;
      align-items: center;
      gap: 16px;
      flex: 1;

      .current-path-hint {
        display: flex;
        align-items: center;
        gap: 6px;
        font-size: 13px;
        color: var(--td-text-color-secondary);
        padding: 4px 12px;
        background: var(--td-bg-color-secondarycontainer);
        border-radius: 4px;

        .t-icon {
          color: var(--td-warning-color);
        }
      }
    }

    .toolbar-right {
      display: flex;
      align-items: center;
      gap: 12px;
    }
  }
}

@media (max-width: 768px) {
  .knowledge-folder-view .folder-toolbar {
    flex-direction: column;
    align-items: stretch;

    .toolbar-left,
    .toolbar-right {
      justify-content: space-between;
    }
  }
}
</style>
