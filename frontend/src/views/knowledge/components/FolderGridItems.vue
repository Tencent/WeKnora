<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';
import FolderActionMenu from './FolderActionMenu.vue';

const props = withDefaults(defineProps<{
  folders: KnowledgeFolder[];
  selectedFolderIds: Set<string>;
  editable: boolean;
  batchMode: boolean;
  // Parent-controlled inline rename mode. When this equals a folder's id, the
  // name label is replaced by an inline <input> (Enter/Esc/blur).
  renamingFolderId?: string | null;
  // Localized error text for the current rename, surfaced via aria-describedby.
  renameError?: string;
}>(), {
  renamingFolderId: null,
  renameError: '',
});

const emit = defineEmits<{
  (e: 'open', folderId: string): void;
  (e: 'toggle-selection', folderId: string, checked: boolean, shiftKey: boolean): void;
  (e: 'create', parentId: string): void;
  (e: 'rename', folderId: string): void;
  (e: 'rename-commit', folderId: string, name: string): void;
  (e: 'rename-cancel', folderId: string): void;
  (e: 'move-folder', folderId: string): void;
  (e: 'batch-manage', folderId: string): void;
  (e: 'delete', folderId: string): void;
}>();

const { t } = useI18n();

const formatFolderTime = (time?: string) => {
  if (!time) return '--';
  const d = new Date(time);
  if (Number.isNaN(d.getTime())) return '--';
  const yy = String(d.getFullYear()).slice(2);
  const MM = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  const hh = String(d.getHours()).padStart(2, '0');
  const mm = String(d.getMinutes()).padStart(2, '0');
  return `${yy}-${MM}-${dd} ${hh}:${mm}`;
};

// --- Action menu open state (one open at a time) ---
const openMenuFolderId = ref<string | null>(null);
const onMenuVisibleChange = (visible: boolean, folder: KnowledgeFolder) => {
  openMenuFolderId.value = visible ? folder.id : null;
};

// --- Inline rename ---
const draftName = ref('');
const renameInputEl = ref<HTMLInputElement | null>(null);

const setRenameInput = (el: any) => {
  renameInputEl.value = (el as HTMLInputElement | null) || null;
};

// When the parent puts a folder into rename mode, seed the draft and focus.
watch(
  () => props.renamingFolderId,
  (id) => {
    if (id) {
      const folder = props.folders.find((f) => f.id === id);
      draftName.value = folder?.name ?? '';
      nextTick(() => {
        renameInputEl.value?.focus();
        renameInputEl.value?.select();
      });
    }
  },
);

const isRenaming = (folder: KnowledgeFolder) => props.renamingFolderId === folder.id;

const commitRename = (folder: KnowledgeFolder) => {
  // Only commit if this folder is still in rename mode.
  if (!isRenaming(folder)) return;
  const trimmed = draftName.value.trim();
  if (!trimmed) {
    // Empty: cancel rather than emit an invalid name. The parent owns the
    // backend validation error via `renameError`; basic emptiness is handled
    // here so we never send a blank name.
    emit('rename-cancel', folder.id);
    return;
  }
  // Unchanged name: cancel (no-op rename).
  if (trimmed === folder.name) {
    emit('rename-cancel', folder.id);
    return;
  }
  emit('rename-commit', folder.id, trimmed);
};

const cancelRename = (folder: KnowledgeFolder) => {
  if (!isRenaming(folder)) return;
  emit('rename-cancel', folder.id);
};

// Checkbox: selection ONLY from here, never from body click.
const onCheckboxChange = (folder: KnowledgeFolder, checked: boolean, ctx?: { e?: Event }) => {
  const me = ctx?.e as MouseEvent | undefined;
  emit('toggle-selection', folder.id, checked, !!me?.shiftKey);
};

const onCardClick = (folder: KnowledgeFolder) => {
  // Body click = open. Selection is checkbox-only (hard rule).
  emit('open', folder.id);
};

// Keyboard reachable: the folder card body is a button-like
// target. Enter / Space trigger the same `open` as a click. Checkbox, menu
// and rename input already call @click.stop, so they never reach here.
const onCardKeydown = (folder: KnowledgeFolder, e: KeyboardEvent) => {
  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault();
    emit('open', folder.id);
  }
};

const folderCountLabel = (folder: KnowledgeFolder) => {
  if (folder.knowledge_count == null) return '';
  return t('knowledgeBase.folderDocumentCount', { count: folder.knowledge_count });
};

const renameErrorId = (folder: KnowledgeFolder) => `folder-grid-rename-err-${folder.id}`;
const describedBy = (folder: KnowledgeFolder) =>
  props.renameError && isRenaming(folder) ? renameErrorId(folder) : undefined;
</script>

<template>
  <div
    v-for="folder in folders"
    :key="folder.id"
    class="folder-card"
    :class="{ 'is-selected': selectedFolderIds.has(folder.id) }"
    :data-select-id="folder.id"
    :data-select-key="`folder:${folder.id}`"
    tabindex="0"
    role="button"
    :aria-label="folder.name"
    @click="onCardClick(folder)"
    @keydown="onCardKeydown(folder, $event)"
  >
    <div class="folder-card-content">
      <div class="folder-card-nav">
        <div v-if="editable && batchMode" class="folder-card-check" @click.stop>
          <t-checkbox
            class="folder-card-checkbox"
            size="small"
            :checked="selectedFolderIds.has(folder.id)"
            :title="folder.name"
            @change="(checked: boolean, ctx?: { e?: Event }) => onCheckboxChange(folder, checked, ctx)"
          />
        </div>

        <span class="folder-card-icon-wrap" aria-hidden="true">
          <t-icon name="folder" />
        </span>

        <!-- Inline rename input OR folder name label -->
        <input
          v-if="isRenaming(folder)"
          :ref="setRenameInput"
          v-model="draftName"
          class="folder-card-rename-input"
          type="text"
          :placeholder="t('knowledgeBase.folderActionRename')"
          :aria-label="folder.name"
          :aria-describedby="describedBy(folder)"
          :aria-invalid="!!props.renameError && isRenaming(folder)"
          @click.stop
          @keydown.enter.prevent="commitRename(folder)"
          @keydown.esc.prevent="cancelRename(folder)"
          @blur="commitRename(folder)"
        />
        <span v-else class="folder-card-name" :title="folder.name">{{ folder.name }}</span>

        <t-popup
          v-if="editable"
          overlay-class-name="card-more"
          trigger="click"
          destroy-on-close
          placement="bottom-right"
          :on-visible-change="(v: boolean) => onMenuVisibleChange(v, folder)"
        >
          <div
            class="folder-card-more"
            :class="{ active: openMenuFolderId === folder.id }"
            @click.stop
          >
            <t-icon name="more" size="16px" />
          </div>
          <template #content>
            <div class="card-menu">
              <FolderActionMenu
                :folder="folder"
                @create="emit('create', folder.id)"
                @rename="emit('rename', folder.id)"
                @move-folder="emit('move-folder', folder.id)"
                @batch-manage="emit('batch-manage', folder.id)"
                @delete="emit('delete', folder.id)"
              />
            </div>
          </template>
        </t-popup>
      </div>

      <!-- Body: document count subtitle (folders never have tags). -->
      <div class="folder-card-body">
        <span v-if="folderCountLabel(folder)" class="folder-card-count">{{ folderCountLabel(folder) }}</span>
      </div>

      <!-- Inline rename error (aria-describedby target). -->
      <div
        v-if="isRenaming(folder) && props.renameError"
        :id="renameErrorId(folder)"
        class="folder-card-rename-error"
        role="alert"
      >
        {{ props.renameError }}
      </div>
    </div>

    <div class="folder-card-bottom">
      <span class="folder-card-time">{{ formatFolderTime(folder.updated_at) }}</span>
      <div class="folder-card-bottom-right">
        <span class="folder-card-type">{{ t('knowledgeBase.folderCardType') }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped lang="less">
// Matches `.knowledge-card` footprint: 240px min width, 136px height, 8px
// radius. Lives inside the same CSS grid as document cards.
.folder-card {
  min-width: 240px;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-border);
  height: 136px;
  border-radius: 8px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
  background: var(--td-bg-color-container);
  position: relative;
  cursor: pointer;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background-color 0.2s ease;

  &:hover {
    border-color: color-mix(in srgb, var(--td-component-stroke) 55%, var(--td-brand-color));
    box-shadow: 0 4px 14px rgba(0, 0, 0, 0.07);
  }

  // 选中态 = 浅品牌背景 + 品牌边框 (shallow brand).
  &.is-selected {
    background: var(--td-brand-color-light);
    border-color: var(--td-brand-color);
  }
}

.folder-card-content {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 10px 14px 8px;
}

.folder-card-nav {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.folder-card-check {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 24px;
  cursor: pointer;

  .folder-card-checkbox {
    margin: 0;
    line-height: 0;

    :deep(.t-checkbox) { align-items: center; }
    :deep(.t-checkbox__label) { display: none !important; width: 0 !important; min-width: 0 !important; margin: 0 !important; padding: 0 !important; }
    :deep(.t-checkbox__input) { margin: 0; }
    :deep(.t-checkbox__input-wrapper) { margin: 0; }
  }
}

// 文件夹图标品牌色 (16-18px). 28x28 底座 matches the list row icon wrap
// (文件图标底座 28×28px, 圆角 6px, 浅灰背景).
.folder-card-icon-wrap {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  border-radius: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-brand-color);
}

.folder-card-name {
  flex: 1;
  min-width: 0;
  height: 24px;
  line-height: 24px;
  display: inline-block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0.01em;
}

.folder-card-rename-input {
  flex: 1;
  min-width: 0;
  height: 24px;
  padding: 0 6px;
  border: 1px solid var(--td-brand-color);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 600;
  line-height: 22px;
  outline: none;
  box-sizing: border-box;

  &:focus {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 2px var(--td-brand-color-light);
  }
}

.folder-card-rename-error {
  margin-top: 4px;
  font-size: 11px;
  line-height: 16px;
  color: var(--td-error-color-6);
}

.folder-card-more {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 25px;
  height: 25px;
  border-radius: 5px;
  cursor: pointer;
  color: var(--td-text-color-secondary);

  &:hover {
    background: var(--td-component-stroke);
    color: var(--td-text-color-primary);
  }

  &.active {
    background: var(--td-component-stroke);
    color: var(--td-text-color-primary);
  }
}

.folder-card-body {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: flex-start;
}

.folder-card-count {
  font-family: var(--app-font-family);
  font-size: 12px;
  font-weight: 400;
  line-height: 19px;
  color: var(--td-text-color-secondary);
}

// Bottom bar matches `.card-bottom` rhythm (底栏高度 32px).
.folder-card-bottom {
  flex-shrink: 0;
  margin-top: auto;
  padding: 0 14px;
  box-sizing: border-box;
  height: 32px;
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: var(--td-bg-color-container);
  border-top: 1px solid var(--td-component-stroke);
}

.folder-card.is-selected .folder-card-bottom {
  background: var(--td-brand-color-light);
}

.folder-card-time {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: 12px;
  font-weight: 400;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.folder-card-bottom-right {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  overflow: hidden;
}

.folder-card-type {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.02em;
}
</style>
