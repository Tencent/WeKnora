<template>
  <t-dialog
    v-model:visible="dialogVisible"
    :header="title"
    :on-confirm="handleConfirm"
    width="500px"
  >
    <div class="folder-selector">
      <div class="folder-selector-hint">
        <t-icon name="info-circle" />
        <span>选择目标文件夹，或选择"根目录"移动到顶层</span>
      </div>

      <div class="folder-tree-container">
        <!-- 根目录选项 -->
        <div
          class="folder-tree-item root-folder"
          :class="{ selected: selectedFolderId === null }"
          @click="handleSelectFolder(null)"
        >
          <home-icon />
          <span class="folder-name">根目录</span>
        </div>

        <!-- 文件夹树 -->
        <div v-if="treeLoading" class="folder-tree-loading">
          <t-loading size="small" />
          <span>加载中...</span>
        </div>

        <div v-else-if="folderTree.length === 0" class="folder-tree-empty">
          <t-icon name="folder" />
          <span>暂无文件夹</span>
        </div>

        <div v-else class="folder-tree">
          <FolderTreeNode
            v-for="folder in folderTree"
            :key="folder.id"
            :folder="folder"
            :selected-id="selectedFolderId"
            :disabled-ids="disabledFolderIds"
            @select="handleSelectFolder"
          />
        </div>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { HomeIcon } from 'tdesign-icons-vue-next';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';
import FolderTreeNode from './FolderTreeNode.vue';

interface Props {
  visible: boolean;
  title?: string;
  folderTree: KnowledgeFolder[];
  treeLoading?: boolean;
  currentFolderId?: string | null;
  disabledFolderIds?: string[];
}

const props = withDefaults(defineProps<Props>(), {
  title: '选择文件夹',
  treeLoading: false,
  currentFolderId: null,
  disabledFolderIds: () => [],
});

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void;
  (e: 'confirm', folderId: string | null): void;
}>();

const selectedFolderId = ref<string | null>(null);

const dialogVisible = computed({
  get: () => props.visible,
  set: (value) => emit('update:visible', value),
});

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      selectedFolderId.value = props.currentFolderId || null;
    }
  }
);

const handleSelectFolder = (folderId: string | null) => {
  selectedFolderId.value = folderId;
};

const handleConfirm = () => {
  emit('confirm', selectedFolderId.value);
  dialogVisible.value = false;
};
</script>

<style scoped lang="less">
.folder-selector {
  .folder-selector-hint {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 6px;
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin-bottom: 16px;

    .t-icon {
      color: var(--td-brand-color);
    }
  }

  .folder-tree-container {
    max-height: 400px;
    overflow-y: auto;
    border: 1px solid var(--td-component-stroke);
    border-radius: 6px;
    padding: 8px;
  }

  .folder-tree-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    border-radius: 4px;
    cursor: pointer;
    transition: background-color 0.2s;
    font-size: 14px;

    &:hover {
      background: var(--td-bg-color-container-hover);
    }

    &.selected {
      background: var(--td-brand-color-light);
      color: var(--td-brand-color);
      font-weight: 500;
    }

    &.disabled {
      cursor: not-allowed;
      opacity: 0.5;

      &:hover {
        background: transparent;
      }
    }

    .folder-name {
      flex: 1;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }

  .root-folder {
    margin-bottom: 8px;
    border-bottom: 1px solid var(--td-component-stroke);
    padding-bottom: 12px;
  }

  .folder-tree-loading,
  .folder-tree-empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    padding: 40px 20px;
    color: var(--td-text-color-placeholder);
    font-size: 14px;
  }
}
</style>
