<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';
import FolderTree, { type FolderActionType } from './FolderTree.vue';


const props = withDefaults(defineProps<{
  tree: KnowledgeFolder[];
  currentFolderId: string;
  expandedFolderIds: Set<string>;
  editable: boolean;
  visible: boolean;
  loading?: boolean;
  rootLabel?: string;
  error?: string | null;
  creatingParentId?: string | null;
  createError?: string;
}>(), {
  loading: false,
  rootLabel: '',
  error: null,
  creatingParentId: null,
  createError: '',
});

const emit = defineEmits<{
  (e: 'navigate', folderId: string): void;
  (e: 'toggle-expand', folderId: string): void;
  (e: 'action', action: FolderActionType, folderId: string): void;
  (e: 'toggle'): void;
  (e: 'retry'): void;
  (e: 'create-commit', name: string): void;
  (e: 'create-cancel'): void;
}>();

const { t } = useI18n();
</script>

<template>
  <transition name="folder-panel-backdrop">
    <div
      v-if="visible"
      class="folder-panel-backdrop"
      @click="emit('toggle')"
    ></div>
  </transition>

  <aside
    class="folder-navigation-panel"
    :class="{
      'is-visible': visible,
      'is-collapsed': !visible,
    }"
    :aria-hidden="!visible"
    :inert="!visible ? true : undefined"
  >
    <header class="folder-panel-header">
      <span class="folder-panel-title">{{ t('knowledgeBase.folderTreeTitle') }}</span>
      <div class="folder-panel-header-actions">
        <!-- New root-level folder (emits action with empty folderId = root). -->
        <t-tooltip
          v-if="editable"
          :content="t('knowledgeBase.newFolder')"
          placement="bottom"
        >
          <button
            type="button"
            class="folder-panel-icon-btn"
            :aria-label="t('knowledgeBase.newFolder')"
            @click="emit('action', 'add-subfolder', '')"
          >
            <t-icon name="folder-add" size="16px" aria-hidden="true" />
          </button>
        </t-tooltip>

        <t-tooltip :content="t('knowledgeBase.hideFolderTree')" placement="bottom">
          <button
            type="button"
            class="folder-panel-icon-btn"
            :aria-label="t('knowledgeBase.hideFolderTree')"
            @click="emit('toggle')"
          >
            <t-icon name="close" size="16px" aria-hidden="true" />
          </button>
        </t-tooltip>
      </div>
    </header>

    <div class="folder-panel-body">
      <!-- Non-blocking tree error retry. The tree keeps its last stable state
           below; this banner just offers a force-reload. -->
      <div v-if="error" class="folder-panel-error" role="alert">
        <span class="folder-panel-error-text">{{ t('knowledgeBase.folderTreeLoadFailed') }}</span>
        <t-button theme="primary" variant="text" size="small" @click="emit('retry')">
          {{ t('knowledgeBase.folderTreeRetry') }}
        </t-button>
      </div>
      <FolderTree
        :tree="tree"
        :current-folder-id="currentFolderId"
        :expanded-folder-ids="expandedFolderIds"
        :editable="editable"
        :loading="loading"
        :root-label="rootLabel"
        :creating-parent-id="creatingParentId"
        :create-error="createError"
        @navigate="(folderId: string) => emit('navigate', folderId)"
        @toggle-expand="(folderId: string) => emit('toggle-expand', folderId)"
        @action="(action: FolderActionType, folderId: string) => emit('action', action, folderId)"
        @create-commit="(name: string) => emit('create-commit', name)"
        @create-cancel="emit('create-cancel')"
      />
    </div>
  </aside>
</template>

<style scoped lang="less">
.folder-navigation-panel {
  display: flex;
  flex-direction: column;
  width: 260px;
  flex-shrink: 0;
  height: 100%;
  background: var(--td-bg-color-sidebar);
  border-right: 1px solid var(--td-component-stroke);
  overflow: hidden;
  // width:0 (NOT display:none) so the collapse transition animates.
  transition: width 0.2s cubic-bezier(0.2, 0, 0, 1),
              border-right-color 0.2s ease;

  &.is-collapsed {
    width: 0;
    border-right-color: transparent;
  }
}

.folder-panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  height: 40px;
  flex-shrink: 0;
  padding: 0 8px 0 12px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.folder-panel-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.folder-panel-header-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.folder-panel-icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: none;
  background: transparent;
  border-radius: 6px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 1px;
  }
}

.folder-panel-body {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.folder-panel-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 12px;
  background: var(--td-error-color-1);
  border-bottom: 1px solid var(--td-error-color-2);
  font-size: 12px;
  color: var(--td-error-color-6);
}

.folder-panel-error-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.folder-panel-backdrop {
  display: none;
}

@media (max-width: 767px) {
  .folder-navigation-panel {
    position: fixed;
    top: 0;
    bottom: 0;
    left: 0;
    width: 260px;
    z-index: 1001;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.1);
    // Override desktop width:0 - on mobile we keep 260px and slide off-screen.
    transition: transform 0.2s cubic-bezier(0.2, 0, 0, 1);

    &.is-collapsed {
      width: 260px;
      border-right-color: var(--td-component-stroke);
      transform: translateX(-100%);
    }
  }

  .folder-panel-backdrop {
    display: block;
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.4);
    z-index: 1000;
  }
}

.folder-panel-backdrop-enter-active,
.folder-panel-backdrop-leave-active {
  transition: opacity 0.2s ease;
}

.folder-panel-backdrop-enter-from,
.folder-panel-backdrop-leave-to {
  opacity: 0;
}
</style>
