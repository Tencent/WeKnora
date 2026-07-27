<script setup lang="ts">
import { nextTick, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';
import FolderActionMenu from './FolderActionMenu.vue';


const props = withDefaults(defineProps<{
  folders: KnowledgeFolder[];
  selectedFolderIds: Set<string>;
  editable: boolean;
  renamingFolderId?: string | null;
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

// --- Action menu open state ---
const openMenuFolderId = ref<string | null>(null);
const onMenuVisibleChange = (visible: boolean, folder: KnowledgeFolder) => {
  openMenuFolderId.value = visible ? folder.id : null;
};

// --- Inline rename (same mechanic as FolderGridItems) ---
const draftName = ref('');
const renameInputEl = ref<HTMLInputElement | null>(null);
const setRenameInput = (el: any) => {
  renameInputEl.value = (el as HTMLInputElement | null) || null;
};

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
  if (!isRenaming(folder)) return;
  const trimmed = draftName.value.trim();
  if (!trimmed || trimmed === folder.name) {
    emit('rename-cancel', folder.id);
    return;
  }
  emit('rename-commit', folder.id, trimmed);
};

const cancelRename = (folder: KnowledgeFolder) => {
  if (!isRenaming(folder)) return;
  emit('rename-cancel', folder.id);
};

const onCheckboxChange = (folder: KnowledgeFolder, checked: boolean, ctx?: { e?: Event }) => {
  const me = ctx?.e as MouseEvent | undefined;
  emit('toggle-selection', folder.id, checked, !!me?.shiftKey);
};

const onRowClick = (folder: KnowledgeFolder) => {
  emit('open', folder.id);
};

// Keyboard reachable: the folder row body is a button-like
// target. Enter / Space trigger the same `open` as a click. Checkbox, menu
// and rename input already call @click.stop, so they never reach here.
const onRowKeydown = (folder: KnowledgeFolder, e: KeyboardEvent) => {
  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault();
    emit('open', folder.id);
  }
};

const renameErrorId = (folder: KnowledgeFolder) => `folder-list-rename-err-${folder.id}`;
const describedBy = (folder: KnowledgeFolder) =>
  props.renameError && isRenaming(folder) ? renameErrorId(folder) : undefined;
</script>

<template>
  <div
    v-for="folder in folders"
    :key="folder.id"
    class="folder-list-row"
    :class="{ 'selected': selectedFolderIds.has(folder.id), 'menu-open': openMenuFolderId === folder.id }"
    :data-select-id="folder.id"
    :data-select-key="`folder:${folder.id}`"
    role="row"
    tabindex="0"
    :aria-label="folder.name"
    @click="onRowClick(folder)"
    @keydown="onRowKeydown(folder, $event)"
  >
    <!-- Checkbox (editable only). Selection ONLY from here, never body. -->
    <div class="cell cell-check" @click.stop>
      <t-checkbox
        v-if="editable"
        class="folder-list-check"
        size="small"
        :checked="selectedFolderIds.has(folder.id)"
        :title="folder.name"
        @change="(checked: boolean, ctx?: { e?: Event }) => onCheckboxChange(folder, checked, ctx)"
      />
    </div>

    <!-- Name: folder icon + name (or inline rename input) -->
    <div class="cell cell-name">
      <span class="row-folder-icon-wrap" aria-hidden="true">
        <t-icon name="folder" />
      </span>
      <div class="row-folder-text">
        <input
          v-if="isRenaming(folder)"
          :ref="setRenameInput"
          v-model="draftName"
          class="folder-list-rename-input"
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
        <span v-else class="row-folder-name" :title="folder.name">{{ folder.name }}</span>
        <span
          v-if="isRenaming(folder) && props.renameError"
          :id="renameErrorId(folder)"
          class="folder-list-rename-error"
          role="alert"
        >{{ props.renameError }}</span>
      </div>
    </div>

    <!-- Tag: folders never have tags (物理层级与标签分离). -->
    <div class="cell cell-tag"><span class="row-muted">--</span></div>
    <!-- Source: not applicable to folders. -->
    <div class="cell cell-source"><span class="row-muted">--</span></div>
    <!-- Size: not applicable to folders. -->
    <div class="cell cell-size"><span class="row-muted">--</span></div>
    <!-- Status: not applicable to folders. -->
    <div class="cell cell-status"><span class="row-muted">--</span></div>

    <!-- Updated time -->
    <div class="cell cell-time">
      <span class="row-mono">{{ formatFolderTime(folder.updated_at) }}</span>
    </div>

    <!-- Actions (editable only) -->
    <div class="cell cell-actions" v-if="editable" @click.stop>
      <t-popup
        placement="bottom-right"
        trigger="click"
        destroy-on-close
        overlay-class-name="card-more"
        :on-visible-change="(v: boolean) => onMenuVisibleChange(v, folder)"
      >
        <button
          class="row-more-btn"
          :class="{ active: openMenuFolderId === folder.id }"
          type="button"
          :aria-label="t('knowledgeBase.folderActions')"
        >
          <t-icon name="more" size="16px" />
        </button>
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
    <!-- Spacer cell when not editable, so the grid columns stay aligned. -->
    <div class="cell cell-actions" v-else></div>
  </div>
</template>

<style scoped lang="less">
// Same grid as `.doc-list-header` / `.doc-list-row` (DocumentListView) so
// folder rows align column-for-column with document rows.
.folder-list-row {
  display: grid;
  grid-template-columns:
    44px // checkbox
    minmax(260px, 2.6fr) // name
    minmax(100px, 0.9fr) // tag
    minmax(96px, 0.8fr) // source
    96px // size
    minmax(96px, 0.7fr) // status
    140px // updated_at
    48px; // actions
  align-items: center;
  column-gap: 0;
  padding: 0 16px;
  position: relative;
  min-height: 60px;
  font-size: 13px;
  color: var(--td-text-color-primary);
  border-bottom: 1px solid var(--td-component-stroke);
  cursor: pointer;
  transition: background-color 0.2s ease, border-color 0.2s ease;

  // 选中态 = 浅品牌背景.
  &.selected {
    background: var(--td-brand-color-light);
  }

  &:hover:not(.selected),
  &.menu-open:not(.selected) {
    background: var(--td-bg-color-secondarycontainer);
  }

  &:hover .row-more-btn,
  &.menu-open .row-more-btn,
  &.selected .row-more-btn {
    opacity: 1;
  }
}

.cell {
  display: flex;
  align-items: center;
  min-width: 0;
  padding: 0 8px;

  &:first-child {
    padding-left: 0;
  }

  &:last-child {
    padding-right: 0;
  }
}

.cell-check {
  justify-content: center;
  padding: 0;
}

.cell-name {
  gap: 10px;
  font-family: var(--app-font-family);
}

.cell-size,
.cell-time {
  justify-content: flex-end;
}

.cell-actions {
  justify-content: flex-end;
}

// TDesign checkbox: hide the empty label (matches `.doc-list-check`).
.folder-list-check {
  margin: 0;

  :deep(.t-checkbox) { align-items: center; }
  :deep(.t-checkbox__label) { display: none !important; width: 0 !important; min-width: 0 !important; margin: 0 !important; padding: 0 !important; }
  :deep(.t-checkbox__input) { margin: 0; }
  :deep(.t-checkbox__input-wrapper) { margin: 0; }
}

// 28x28 icon 底座, 圆角 6px, 浅灰背景; folder icon 品牌色.
.row-folder-icon-wrap {
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

.row-folder-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.row-folder-name {
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0.01em;
  color: var(--td-text-color-primary);
}

.folder-list-rename-input {
  width: 100%;
  min-width: 0;
  height: 26px;
  padding: 0 6px;
  border: 1px solid var(--td-brand-color);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 600;
  line-height: 24px;
  outline: none;
  box-sizing: border-box;

  &:focus {
    box-shadow: 0 0 0 2px var(--td-brand-color-light);
  }
}

.folder-list-rename-error {
  font-size: 11px;
  line-height: 16px;
  color: var(--td-error-color-6);
}

.row-muted {
  color: var(--td-text-color-disabled, #bbb);
  font-size: 12px;
}

.row-mono {
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  font-family: var(--app-font-family);
  color: var(--td-text-color-secondary);
}

// Matches `.row-more-btn` (DocumentListView).
.row-more-btn {
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  border-radius: 5px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s ease, background-color 0.15s ease, color 0.15s ease;

  &:hover {
    background: var(--td-component-stroke);
    color: var(--td-text-color-primary);
  }

  &.active {
    opacity: 1;
    background: var(--td-component-stroke);
    color: var(--td-text-color-primary);
  }
}
</style>

