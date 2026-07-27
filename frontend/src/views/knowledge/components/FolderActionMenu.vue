<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

// FolderActionMenu is the per-folder action dropdown content. It is rendered
// inside the same `.card-more` t-popup overlay the document cards use, so it
// shares the document action-menu visual rhythm (复用现有视图语言). It is DISTINCT
// from DocumentActionMenu: folders get create-subfolder, rename, move-to-folder
// (same-KB) and delete. The document cross-KB transfer (`moveToKnowledgeBase`)
// is intentionally NOT present here - that action only applies to documents.
// Delete shows a cascading-effect popconfirm (folder + all subfolders +
// documents).

const props = defineProps<{
  folder: KnowledgeFolder;
}>();

const emit = defineEmits<{
  (e: 'create', folderId: string): void;
  (e: 'rename', folderId: string): void;
  (e: 'move-folder', folderId: string): void;
  (e: 'delete', folderId: string): void;
}>();

const { t } = useI18n();

const folderName = computed(() => props.folder.name);
</script>

<template>
  <!-- 新建子文件夹 -->
  <div class="folder-action-menu-item" @click.stop="emit('create', folder.id)">
    <t-icon class="icon" name="folder-add" />
    <span>{{ t('knowledgeBase.folderActionAddSubfolder') }}</span>
  </div>

  <!-- 重命名 -->
  <div class="folder-action-menu-item" @click.stop="emit('rename', folder.id)">
    <t-icon class="icon" name="edit" />
    <span>{{ t('knowledgeBase.folderActionRename') }}</span>
  </div>

  <!-- 移动到文件夹… (same-KB folder move; distinct from document cross-KB transfer) -->
  <div class="folder-action-menu-item" @click.stop="emit('move-folder', folder.id)">
    <t-icon class="icon" name="swap" />
    <span>{{ t('knowledgeBase.folderActionMoveTo') }}</span>
  </div>

  <!-- 删除文件夹 -->
  <t-popconfirm
    theme="warning"
    :content="t('knowledgeBase.folderActionDeleteConfirm', { name: folderName })"
    :confirm-btn="{ content: t('knowledgeBase.folderActionDelete'), theme: 'danger' }"
    :cancel-btn="{ content: t('common.cancel') }"
    placement="left"
    @confirm="emit('delete', folder.id)"
  >
    <div class="folder-action-menu-item danger" @click.stop>
      <t-icon class="icon" name="delete" />
      <span>{{ t('knowledgeBase.folderActionDelete') }}</span>
    </div>
  </t-popconfirm>
</template>

<style scoped lang="less">
// Mirrors `.doc-action-menu-item` (DocumentActionMenu.vue) so folder and
// document menus share the same item rhythm inside `.card-more` overlays.
.folder-action-menu-item {
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

  // 删除项前应有弱分割线, 文字和图标使用错误色.
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
