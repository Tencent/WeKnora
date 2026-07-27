<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';
import {
  type FolderIndex,
  folderPathLabel,
  isMoveTargetDisabled,
} from '@/views/knowledge/folderModel';

// FolderPickerDialog is the move-to-folder target picker (same-KB move). It is
// a modal: render the folder tree with a root row, let the user pick a
// destination, show a path preview, and confirm/cancel. It is DISTINCT from
// the cross-KB transfer flow (which is document-only and async).
//
// Reuses FolderTree's ARIA treeview + roving-tabindex keyboard model, but
// adapted for *selection* (Enter/Space picks the target) rather than
// navigation. There is NO drag-and-drop here - selection is click / keyboard
// only. Tree node height 32px, indent 16px per level, icon-name gap 8px;
// disabled targets use the disabled-text color and are not selectable.
//
// The root is a LOCAL VIRTUAL NODE (id ""). Root is always a selectable
// target (unless it equals currentParentId, in which case isMoveTargetDisabled
// disables it). The API-only `__root__` sentinel never appears in this
// component - the page converts '' to the sentinel at the API boundary.

const props = withDefaults(defineProps<{
  visible: boolean;
  tree: KnowledgeFolder[];
  index: FolderIndex;
  // Folder ids being moved (to disable self + descendants as targets).
  selectedFolderIds: Set<string>;
  // Current parent of the moved items (to disable "move to same parent").
  // For a document move this is the folder the user is currently browsing.
  currentParentId: string;
  loading?: boolean;
  submitting?: boolean;
  rootLabel?: string;
}>(), {
  loading: false,
  submitting: false,
  rootLabel: '',
});

const emit = defineEmits<{
  (e: 'update:visible', visible: boolean): void;
  (e: 'confirm', targetFolderId: string): void;
  (e: 'cancel'): void;
}>();

const { t, te } = useI18n();

const resolvedRootLabel = computed(() => {
  if (props.rootLabel) return props.rootLabel;
  if (te('knowledgeBase.rootFolder')) return t('knowledgeBase.rootFolder');
  return '根目录';
});

// --- Selection + expand state ---
// selectedTargetId is the picked destination ('' = root). null = nothing
// picked yet (confirm disabled). On open we default to root when root is a
// valid target, else null - this saves a click in the common case (moving
// items up to root) while avoiding a "disabled row looks selected" state.
const selectedTargetId = ref<string | null>(null);
const expandedIds = ref<Set<string>>(new Set());

function isTargetDisabled(targetId: string): boolean {
  return isMoveTargetDisabled(
    props.index,
    props.selectedFolderIds,
    targetId,
    props.currentParentId,
  );
}

function toggleExpand(id: string): void {
  if (expandedIds.value.has(id)) {
    expandedIds.value.delete(id);
  } else {
    expandedIds.value.add(id);
  }
}

// Reset selection/expand state every time the dialog opens so a previous
// session never leaks in.
watch(
  () => props.visible,
  (open) => {
    if (open) {
      expandedIds.value = new Set();
      selectedTargetId.value = isTargetDisabled('') ? null : '';
      focusedId.value = null;
    }
  },
);

// --- Visible-node flattening (mirrors FolderTree, adds disabled/selected) ---
interface PickerNode {
  id: string;
  name: string;
  level: number;
  hasChildren: boolean;
  isExpanded: boolean;
  isDisabled: boolean;
  isSelected: boolean;
  isRoot: boolean;
  parentId: string;
}

const visibleNodes = computed<PickerNode[]>(() => {
  const nodes: PickerNode[] = [];
  const rootDisabled = isTargetDisabled('');
  nodes.push({
    id: '',
    name: resolvedRootLabel.value,
    level: 1,
    hasChildren: props.tree.length > 0,
    isExpanded: true,
    isDisabled: rootDisabled,
    isSelected: selectedTargetId.value === '' && !rootDisabled,
    isRoot: true,
    parentId: '',
  });
  const visit = (folders: KnowledgeFolder[], level: number, parentId: string) => {
    for (const folder of folders) {
      const hasChildren = (folder.children?.length ?? 0) > 0;
      const isExpanded = expandedIds.value.has(folder.id);
      const disabled = isTargetDisabled(folder.id);
      nodes.push({
        id: folder.id,
        name: folder.name,
        level,
        hasChildren,
        isExpanded,
        isDisabled: disabled,
        isSelected: selectedTargetId.value === folder.id && !disabled,
        isRoot: false,
        parentId,
      });
      if (hasChildren && isExpanded) {
        visit(folder.children!, level + 1, folder.id);
      }
    }
  };
  visit(props.tree, 2, '');
  return nodes;
});

const isEmpty = computed(() => props.tree.length === 0);

const canConfirm = computed(() => {
  const id = selectedTargetId.value;
  if (id === null) return false;
  return !isTargetDisabled(id);
});

const targetPathPreview = computed(() => {
  const id = selectedTargetId.value;
  if (id === null) return '';
  return folderPathLabel(props.index, id, resolvedRootLabel.value);
});

// --- Roving tabindex keyboard model (ARIA APG treeview, single select) ---
const focusedId = ref<string | null>(null);
const treeRef = ref<HTMLElement | null>(null);

const focusedIndex = computed(() => {
  const nodes = visibleNodes.value;
  if (nodes.length === 0) return -1;
  const id = focusedId.value;
  if (id !== null) {
    const idx = nodes.findIndex((n) => n.id === id);
    if (idx >= 0) return idx;
  }
  // Default to the selected node, else root.
  const selIdx = nodes.findIndex((n) => n.isSelected);
  return selIdx >= 0 ? selIdx : 0;
});

function focusNodeById(id: string) {
  focusedId.value = id;
  nextTick(() => {
    const idx = visibleNodes.value.findIndex((n) => n.id === id);
    if (idx < 0 || !treeRef.value) return;
    const el = treeRef.value.querySelector<HTMLElement>(`[data-picker-idx="${idx}"]`);
    el?.focus();
  });
}

function selectTarget(id: string) {
  if (isTargetDisabled(id)) return;
  selectedTargetId.value = id;
}

function onTreeKeydown(e: KeyboardEvent) {
  const nodes = visibleNodes.value;
  if (nodes.length === 0) return;
  const idx = focusedIndex.value;
  if (idx < 0) return;
  const node = nodes[idx];

  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault();
      if (idx < nodes.length - 1) focusNodeById(nodes[idx + 1].id);
      break;
    case 'ArrowUp':
      e.preventDefault();
      if (idx > 0) focusNodeById(nodes[idx - 1].id);
      break;
    case 'ArrowRight':
      e.preventDefault();
      if (node.hasChildren && !node.isExpanded) {
        toggleExpand(node.id);
      } else if (node.hasChildren && node.isExpanded && idx < nodes.length - 1) {
        focusNodeById(nodes[idx + 1].id);
      }
      break;
    case 'ArrowLeft':
      e.preventDefault();
      if (!node.isRoot && node.hasChildren && node.isExpanded) {
        toggleExpand(node.id);
      } else if (!node.isRoot) {
        focusNodeById(node.parentId);
      }
      break;
    case 'Enter':
    case ' ':
      e.preventDefault();
      selectTarget(node.id);
      break;
    case 'Home':
      e.preventDefault();
      focusNodeById(nodes[0].id);
      break;
    case 'End':
      e.preventDefault();
      focusNodeById(nodes[nodes.length - 1].id);
      break;
    default:
      break;
  }
}

function onNodeClick(node: PickerNode) {
  focusNodeById(node.id);
  if (node.hasChildren && !node.isRoot) {
    // Clicking a folder row toggles expand AND selects it (if enabled). This
    // matches the "click a folder to move into it" intuition: the user can
    // expand to see children, then pick a child. Root has no expand toggle
    // (always expanded) so clicking root only selects.
    toggleExpand(node.id);
  }
  selectTarget(node.id);
}

function onChevronClick(node: PickerNode, e: MouseEvent) {
  e.stopPropagation();
  focusNodeById(node.id);
  toggleExpand(node.id);
}

function onConfirm() {
  if (!canConfirm.value || props.submitting) return;
  const id = selectedTargetId.value;
  if (id === null) return;
  emit('confirm', id);
}

// t-dialog fires `update:visible(false)` for any dismiss (cancel button,
// overlay, esc). We forward it and also emit `cancel` so parents that key off
// the cancel signal still work; the page treats both the same (clear flow
// state). `confirm` is NOT auto-closed - the parent closes the dialog after
// the async batchMove succeeds, so the picker stays open on error.
function onUpdateVisible(val: boolean) {
  emit('update:visible', val);
  if (!val) emit('cancel');
}
</script>

<template>
  <t-dialog
    :visible="visible"
    :header="t('knowledgeBase.folderPickerTitle')"
    :confirm-btn="{
      content: t('knowledgeBase.folderPickerConfirm'),
      theme: 'primary',
      loading: submitting,
      disabled: !canConfirm,
    }"
    :cancel-btn="{ content: t('common.cancel') }"
    width="480px"
    destroy-on-close
    :close-on-overlay-click="!submitting"
    :close-on-esc-keydown="!submitting"
    @confirm="onConfirm"
    @update:visible="onUpdateVisible"
  >
    <div class="folder-picker">
      <!-- Target tree -->
      <div
        ref="treeRef"
        class="folder-picker-tree"
        role="tree"
        :aria-label="t('knowledgeBase.folderPickerTitle')"
        :aria-busy="loading"
        @keydown="onTreeKeydown"
      >
        <div v-if="loading" class="folder-picker-state folder-picker-loading" aria-busy="true">
          <t-icon name="loading" size="20px" class="folder-picker-loading-icon" />
        </div>
        <template v-else>
          <div
            v-for="(node, idx) in visibleNodes"
            :key="node.id || 'root'"
            class="folder-picker-node"
            :class="{
              'is-selected': node.isSelected,
              'is-disabled': node.isDisabled,
              'is-root': node.isRoot,
            }"
            role="treeitem"
            :data-picker-idx="idx"
            :aria-level="node.level"
            :aria-expanded="node.hasChildren ? node.isExpanded : undefined"
            :aria-selected="node.isSelected"
            :aria-disabled="node.isDisabled"
            :tabindex="focusedIndex === idx ? 0 : -1"
            :title="node.isDisabled ? t('knowledgeBase.folderPickerDisabledHint') : node.name"
            :style="{ paddingLeft: (node.level - 1) * 16 + 'px' }"
            @click="onNodeClick(node)"
          >
            <button
              v-if="node.hasChildren && !node.isRoot"
              type="button"
              class="folder-picker-chevron"
              tabindex="-1"
              :aria-label="node.isExpanded ? t('knowledgeBase.collapseFolder') : t('knowledgeBase.expandFolder')"
              @click="onChevronClick(node, $event)"
            >
              <t-icon
                name="chevron-right"
                size="14px"
                class="folder-picker-chevron-icon"
                aria-hidden="true"
              />
            </button>
            <span v-else class="folder-picker-chevron-placeholder" aria-hidden="true"></span>

            <t-icon
              :name="node.isRoot ? 'root-list' : (node.hasChildren && node.isExpanded ? 'folder-open' : 'folder')"
              size="16px"
              class="folder-picker-node-icon"
              aria-hidden="true"
            />

            <span class="folder-picker-node-label">{{ node.name }}</span>

            <t-icon
              v-if="node.isSelected"
              name="check"
              size="16px"
              class="folder-picker-check"
              aria-hidden="true"
            />
          </div>

          <!-- Empty hint: only root exists. Root is still shown above; this
               hint reassures the user that root is a valid destination. -->
          <div v-if="isEmpty && !loading" class="folder-picker-empty-hint">
            {{ t('knowledgeBase.folderPickerEmpty') }}
          </div>
        </template>
      </div>

      <!-- Path preview of the selected target -->
      <div class="folder-picker-preview">
        <span class="folder-picker-preview-label">{{ t('knowledgeBase.folderPickerTargetLabel') }}</span>
        <span
          class="folder-picker-preview-value"
          :class="{ 'is-placeholder': !targetPathPreview }"
        >
          {{ targetPathPreview || t('knowledgeBase.folderPickerSelectHint') }}
        </span>
      </div>
    </div>
  </t-dialog>
</template>

<style scoped lang="less">
.folder-picker {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.folder-picker-tree {
  display: flex;
  flex-direction: column;
  padding: 4px 0;
  max-height: 340px;
  min-height: 120px;
  overflow-y: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
}

.folder-picker-state {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 32px 16px;
}

.folder-picker-loading-icon {
  color: var(--td-text-color-placeholder);
  animation: folder-picker-spin 1s linear infinite;
}

@keyframes folder-picker-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

// tree node height 32px, indent 16px per level, icon-name gap 8px.
.folder-picker-node {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding-right: 8px;
  font-size: 13px;
  color: var(--td-text-color-primary);
  border-radius: 6px;
  cursor: pointer;
  outline: none;
  transition: background-color 0.15s ease;

  &:hover:not(.is-disabled) {
    background: var(--td-bg-color-container-hover);
  }

  &:focus-visible {
    box-shadow: inset 0 0 0 2px var(--td-brand-color);
  }

  // selected target - light brand background + brand icon/name.
  &.is-selected {
    background: var(--td-brand-color-light);

    .folder-picker-node-icon,
    .folder-picker-node-label {
      color: var(--td-brand-color);
      font-weight: 600;
    }

    .folder-picker-check {
      color: var(--td-brand-color);
    }
  }

  &.is-selected:hover {
    background: var(--td-brand-color-light);
  }

  // Disabled targets (self / descendant / current-parent): not selectable.
  &.is-disabled {
    color: var(--td-text-color-disabled);
    cursor: not-allowed;

    .folder-picker-node-icon,
    .folder-picker-chevron-icon {
      color: var(--td-text-color-disabled);
    }

    &:hover {
      background: transparent;
    }
  }
}

.folder-picker-chevron {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  padding: 0;
  border: none;
  background: transparent;
  border-radius: 4px;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: background-color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-component);
    color: var(--td-text-color-secondary);
  }

  .folder-picker-chevron-icon {
    transition: transform 0.18s cubic-bezier(0.2, 0, 0, 1);
  }
}

.folder-picker-node[aria-expanded='true'] .folder-picker-chevron-icon {
  transform: rotate(90deg);
}

.folder-picker-chevron-placeholder {
  display: inline-block;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
}

.folder-picker-node-icon {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.folder-picker-node-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-primary);
}

.folder-picker-check {
  flex-shrink: 0;
  color: var(--td-brand-color);
}

.folder-picker-empty-hint {
  padding: 8px 12px 12px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.folder-picker-preview {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 8px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  font-size: 12px;
}

.folder-picker-preview-label {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
}

.folder-picker-preview-value {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-primary);
  font-weight: 500;

  &.is-placeholder {
    color: var(--td-text-color-placeholder);
    font-weight: 400;
  }
}
</style>
