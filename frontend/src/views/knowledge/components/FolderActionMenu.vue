<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';

interface FolderItem {
  id: string;
  name?: string;
  file_name?: string;
  knowledge_count?: number;
}

const props = defineProps<{
  folder: FolderItem;
  canEdit?: boolean;
}>();

const emit = defineEmits<{
  (e: 'rename'): void;
  (e: 'move'): void;
  (e: 'delete'): void;
}>();

const { t } = useI18n();

const folderName = computed(() => props.folder.name || props.folder.file_name || props.folder.id);
const hasContent = computed(() => (props.folder.knowledge_count || 0) > 0);

const deleteConfirmContent = computed(() =>
  hasContent.value
    ? t('knowledgeFolder.confirmDeleteFolderWithContent', { name: folderName.value, count: props.folder.knowledge_count || 0 })
    : t('knowledgeFolder.confirmDeleteFolder', { name: folderName.value })
);
</script>

<template>
  <!-- Rename -->
  <div class="doc-action-menu-item" @click.stop="emit('rename')">
    <t-icon class="icon" name="edit" />
    <span>{{ $t('knowledgeFolder.editFolder') }}</span>
  </div>

  <!-- Move to... -->
  <div v-if="canEdit" class="doc-action-menu-item" @click.stop="emit('move')">
    <t-icon class="icon" name="swap" />
    <span>{{ $t('knowledgeFolder.moveFolder') }}</span>
  </div>

  <!-- Delete -->
  <t-popconfirm
    theme="warning"
    :content="deleteConfirmContent"
    :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
    :cancel-btn="{ content: $t('common.cancel') }"
    placement="left"
    @confirm="emit('delete')"
  >
    <div class="doc-action-menu-item danger" @click.stop>
      <t-icon class="icon" name="delete" />
      <span>{{ $t('knowledgeFolder.deleteFolder') }}</span>
    </div>
  </t-popconfirm>
</template>

<style scoped lang="less">
// Reuses .doc-action-menu-item styles from DocumentActionMenu.vue.
// If this component is used standalone (not inside a card-menu container),
// ensure styles are loaded. The parent .card-menu in dropdown-menu.less
// provides the container styles; .doc-action-menu-item is scoped here.

.doc-action-menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  font-size: 14px;
  line-height: 20px;
  color: var(--td-text-color-primary);
  cursor: pointer;
  border-radius: 6px;
  transition: background-color 0.15s cubic-bezier(0.2, 0, 0, 1), transform 0.12s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:active {
    background: var(--td-bg-color-container-active);
    transform: scale(0.98);
  }

  .icon {
    font-size: 16px;
    color: var(--td-text-color-secondary);
    transition: color 0.15s ease;
  }

  &:hover .icon {
    color: var(--td-text-color-primary);
  }

  &.danger {
    color: var(--td-error-color-6);
    margin-top: 4px;
    position: relative;

    &::before {
      content: '';
      position: absolute;
      top: -3px;
      left: 8px;
      right: 8px;
      height: 1px;
      background: var(--td-component-stroke);
    }

    .icon {
      color: var(--td-error-color-6);
    }

    &:hover {
      background: var(--td-error-color-1);
      color: var(--td-error-color-6);

      .icon {
        color: var(--td-error-color-6);
      }
    }

    &:active {
      background: var(--td-error-color-2);
    }
  }
}
</style>
