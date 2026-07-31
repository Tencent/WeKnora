<template>
  <div class="knowledge-folder-view">
    <!-- Breadcrumb navigation -->
    <div v-if="showBreadcrumb" class="folder-navigation">
      <FolderBreadcrumb
        :kb-id="kbId"
        :current-folder-id="currentFolderId"
        @navigate="handleNavigateToFolder"
      />
    </div>

    <!-- Toolbar -->
    <div class="folder-toolbar">
      <div class="toolbar-left">
        <!-- Create folder button -->
        <t-button v-if="canEdit" theme="default" @click="handleCreateFolderClick">
          <template #icon>
            <t-icon name="folder-add" />
          </template>
          {{ $t('knowledgeFolder.createFolder') }}
        </t-button>

        <!-- Current path hint -->
        <div class="current-path-hint">
          <t-icon name="folder-open" />
          <span>{{ currentFolderPath }}</span>
        </div>

        <!-- Current folder quick actions -->
        <div v-if="canEdit && currentFolderId" class="current-folder-actions">
          <t-button variant="text" size="small" @click="handleRenameCurrentFolder">
            <t-icon name="edit" />
            {{ $t('knowledgeFolder.editFolder') }}
          </t-button>
          <t-button variant="text" size="small" @click="handleMoveCurrentFolder">
            <t-icon name="swap" />
            {{ $t('knowledgeFolder.moveFolder') }}
          </t-button>
          <t-button variant="text" size="small" theme="danger" @click="handleDeleteCurrentFolder">
            <t-icon name="delete" />
            {{ $t('knowledgeFolder.deleteFolder') }}
          </t-button>
        </div>
      </div>

      <div class="toolbar-right">
        <!-- View mode toggle -->
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

    <!-- List view -->
    <FolderListView
      v-if="viewMode === 'list'"
      :folders="folders"
      :files="files"
      :loading="loading"
      :can-edit="canEdit"
      @enter-folder="handleEnterFolder"
      @batch-move="handleBatchMove"
      @batch-delete="handleBatchDelete"
      @folder-rename="handleRenameFolder"
      @folder-move="handleMoveFolderIndividual"
      @folder-delete="handleDeleteFolderConfirm"
    />

    <!-- Card view -->
    <FolderCardView
      v-else
      :folders="folders"
      :files="files"
      :loading="loading"
      :can-edit="canEdit"
      @enter-folder="handleEnterFolder"
      @batch-move="handleBatchMove"
      @batch-delete="handleBatchDelete"
      @folder-rename="handleRenameFolder"
      @folder-move="handleMoveFolderIndividual"
      @folder-delete="handleDeleteFolderConfirm"
    />

    <!-- Folder management dialog (create / rename) -->
    <FolderManageDialog
      v-model:visible="folderDialogVisible"
      :kb-id="kbId"
      :mode="folderDialogMode"
      :folder="currentEditFolder"
      :parent-folder-id="currentFolderId"
      @success="handleFolderDialogSuccess"
    />

    <!-- Folder selector (for moving individual folders) -->
    <FolderSelector
      v-model:visible="folderMoveSelectorVisible"
      :title="$t('knowledgeFolder.moveFolder')"
      :folder-tree="folderTree"
      :tree-loading="treeLoading"
      :current-folder-id="currentFolderId"
      @confirm="handleConfirmMoveFolder"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import FolderBreadcrumb from '@/views/knowledge/components/FolderBreadcrumb.vue';
import FolderListView from '@/views/knowledge/components/FolderListView.vue';
import FolderCardView from '@/views/knowledge/components/FolderCardView.vue';
import FolderManageDialog from '@/views/knowledge/components/FolderManageDialog.vue';
import FolderSelector from '@/views/knowledge/components/FolderSelector.vue';
import { useKnowledgeFolder } from '@/composables/useKnowledgeFolder';
import { moveFolder as moveFolderApi } from '@/api/knowledge-folder';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

const { t } = useI18n();

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
  loadFolders,
  loadFolderTree,
  handleDeleteFolder,
} = useKnowledgeFolder(props.kbId);

// Folder dialog for create / rename
const folderDialogVisible = ref(false);
const folderDialogMode = ref<'create' | 'edit'>('create');
const currentEditFolder = ref<KnowledgeFolder | null>(null);

// Folder move selector (individual folder)
const folderMoveSelectorVisible = ref(false);
const pendingMoveFolder = ref<KnowledgeFolder | null>(null);

// ----- Navigation -----
const handleNavigateToFolder = async (folderId: string | null) => {
  await navigateToFolder(folderId);
  emit('folder-change', folderId);
  emit('refresh');
};

const handleEnterFolder = (folderId: string) => {
  handleNavigateToFolder(folderId);
};

// ----- Create folder -----
const handleCreateFolderClick = () => {
  folderDialogMode.value = 'create';
  currentEditFolder.value = null;
  folderDialogVisible.value = true;
};

const handleFolderDialogSuccess = () => {
  loadFolders(currentFolderId.value);
  loadFolderTree();
  emit('refresh');
};

// ----- Rename folder -----
const handleRenameFolder = (folder: KnowledgeFolder) => {
  folderDialogMode.value = 'edit';
  currentEditFolder.value = folder;
  folderDialogVisible.value = true;
};

const handleRenameCurrentFolder = () => {
  if (!currentFolderId.value) return;
  const currentFolder = folders.value.find((f) => f.id === currentFolderId.value);
  if (currentFolder) {
    handleRenameFolder(currentFolder);
  }
};

// ----- Move folder (individual) -----
const handleMoveFolderIndividual = (folder: KnowledgeFolder) => {
  pendingMoveFolder.value = folder;
  folderMoveSelectorVisible.value = true;
  loadFolderTree();
};

const handleMoveCurrentFolder = () => {
  if (!currentFolderId.value) return;
  const currentFolder = folders.value.find((f) => f.id === currentFolderId.value);
  if (currentFolder) {
    handleMoveFolderIndividual(currentFolder);
  }
};

const handleConfirmMoveFolder = async (targetFolderId: string | null) => {
  if (!pendingMoveFolder.value) return;
  const folder = pendingMoveFolder.value;

  if (targetFolderId === folder.id) {
    MessagePlugin.warning(t('knowledgeFolder.cannotMoveToSelf'));
    return;
  }

  try {
    await moveFolderApi(props.kbId, folder.id, { target_parent_folder_id: targetFolderId });
    MessagePlugin.success(t('knowledgeFolder.folderMovedSuccess'));
    loadFolders(currentFolderId.value);
    loadFolderTree();
    emit('refresh');
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeFolder.folderMovedFailed'));
  } finally {
    folderMoveSelectorVisible.value = false;
    pendingMoveFolder.value = null;
  }
};

// ----- Delete folder (individual) -----
const handleDeleteFolderConfirm = (folder: KnowledgeFolder) => {
  const name = folder.name || '';
  const count = folder.knowledge_count || 0;
  const msg = count > 0
    ? t('knowledgeFolder.confirmDeleteFolderWithContent', { name, count })
    : t('knowledgeFolder.confirmDeleteFolder', { name });

  const dialog = DialogPlugin.confirm({
    header: t('knowledgeFolder.deleteFolder'),
    body: msg,
    confirmBtn: { content: t('common.confirm'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      dialog.update({ confirmLoading: true });
      const ok = await handleDeleteFolder(folder.id, count > 0);
      dialog.destroy();
      if (ok) {
        loadFolders(currentFolderId.value);
        loadFolderTree();
        emit('refresh');
      }
    },
  });
};

const handleDeleteCurrentFolder = () => {
  if (!currentFolderId.value) return;
  const currentFolder = folders.value.find((f) => f.id === currentFolderId.value);
  if (currentFolder) {
    handleDeleteFolderConfirm(currentFolder);
  }
};

// ----- Batch operations (delegated to parent) -----
const handleBatchMove = (items: any[]) => {
  emit('batch-move', items);
};

const handleBatchDelete = (items: any[]) => {
  emit('batch-delete', items);
};

// Watch kbId change
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
      gap: 12px;
      flex: 1;
      flex-wrap: wrap;

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
          color: var(--td-brand-color);
        }
      }

      .current-folder-actions {
        display: flex;
        align-items: center;
        gap: 2px;
        padding-left: 8px;
        border-left: 1px solid var(--td-component-stroke);
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

    .current-folder-actions {
      border-left: none !important;
      padding-left: 0 !important;
    }
  }
}
</style>
