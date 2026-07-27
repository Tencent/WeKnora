<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';
import { insertCreatePlaceholder } from '../folderModel';

export type FolderActionType = 'rename' | 'delete' | 'add-subfolder';

const props = withDefaults(defineProps<{
  tree: KnowledgeFolder[];
  currentFolderId: string;
  expandedFolderIds: Set<string>;
  editable: boolean;
  loading?: boolean;
  rootLabel?: string;
  // Tree-surface create: when non-null, an inline create-input row renders
  // immediately under the node whose id === creatingParentId ('' = root).
  // Null = no tree-surface create active.
  creatingParentId?: string | null;
  // Inline error message for the tree create input (empty when none).
  createError?: string;
}>(), {
  loading: false,
  rootLabel: '',
  creatingParentId: null,
  createError: '',
});

const emit = defineEmits<{
  (e: 'navigate', folderId: string): void;
  (e: 'toggle-expand', folderId: string): void;
  (e: 'action', action: FolderActionType, folderId: string): void;
  (e: 'create-commit', name: string): void;
  (e: 'create-cancel'): void;
}>();

const { t, te } = useI18n();

// Resolve the root label the same way the composable does: explicit prop wins,
// then the i18n key, then a final hardcoded fallback for safety.
const resolvedRootLabel = computed(() => {
  if (props.rootLabel) return props.rootLabel;
  if (te('knowledgeBase.rootFolder')) return t('knowledgeBase.rootFolder');
  return '根目录';
});

interface VisibleNode {
  id: string;
  name: string;
  // ARIA aria-level is 1-based: root = 1, top-level folders = 2, etc.
  level: number;
  hasChildren: boolean;
  isExpanded: boolean;
  isCurrent: boolean;
  isRoot: boolean;
  parentId: string;
}

// Flatten the nested tree + virtual root into the visible (expanded) node list.
// The root is always expanded (it cannot be collapsed - collapsing root would
// hide every folder, which is not useful).
const visibleNodes = computed<VisibleNode[]>(() => {
  const nodes: VisibleNode[] = [];
  const rootHasChildren = props.tree.length > 0;
  nodes.push({
    id: '',
    name: resolvedRootLabel.value,
    level: 1,
    hasChildren: rootHasChildren,
    isExpanded: true,
    isCurrent: props.currentFolderId === '',
    isRoot: true,
    parentId: '',
  });
  const visit = (folders: KnowledgeFolder[], level: number, parentId: string) => {
    for (const folder of folders) {
      const hasChildren = (folder.children?.length ?? 0) > 0;
      const isExpanded = props.expandedFolderIds.has(folder.id);
      nodes.push({
        id: folder.id,
        name: folder.name,
        level,
        hasChildren,
        isExpanded,
        isCurrent: props.currentFolderId === folder.id,
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

type RenderItem =
  | { kind: 'node'; node: VisibleNode; visibleIdx: number }
  | { kind: 'placeholder'; level: number };

const renderNodes = computed<RenderItem[]>(() => {
  const base = visibleNodes.value;
  const augmented = insertCreatePlaceholder(base, props.creatingParentId ?? null);
  let visibleIdx = 0;
  return augmented.map((item) => {
    if ('isPlaceholder' in item) {
      return { kind: 'placeholder' as const, level: item.level };
    }
    const node = item;
    const idx = visibleIdx++;
    return { kind: 'node' as const, node, visibleIdx: idx };
  });
});

// is required so '' is not treated as falsy/no-focus.
const focusedId = ref<string | null>(null);
const treeRef = ref<HTMLElement | null>(null);

const focusedIndex = computed(() => {
  const nodes = visibleNodes.value;
  if (nodes.length === 0) return -1;
  const id = focusedId.value;
  if (id !== null) {
    const idx = nodes.findIndex(n => n.id === id);
    if (idx >= 0) return idx;
  }
  // Default to the current node, else root.
  const curIdx = nodes.findIndex(n => n.isCurrent);
  return curIdx >= 0 ? curIdx : 0;
});

function focusNodeById(id: string) {
  focusedId.value = id;
  // Wait for tabindex to update in the DOM, then move focus.
  nextTick(() => {
    const idx = visibleNodes.value.findIndex(n => n.id === id);
    if (idx < 0 || !treeRef.value) return;
    const el = treeRef.value.querySelector<HTMLElement>(`[data-folder-idx="${idx}"]`);
    el?.focus();
  });
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
        emit('toggle-expand', node.id);
      } else if (node.hasChildren && node.isExpanded && idx < nodes.length - 1) {
        // Already expanded: move to first child.
        focusNodeById(nodes[idx + 1].id);
      }
      break;
    case 'ArrowLeft':
      e.preventDefault();
      if (!node.isRoot && node.hasChildren && node.isExpanded) {
        emit('toggle-expand', node.id);
      } else if (!node.isRoot) {
        // Collapsed or leaf: move to parent.
        focusNodeById(node.parentId);
      }
      break;
    case 'Enter':
    case ' ':
      e.preventDefault();
      emit('navigate', node.id);
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
      // No-op: let other keys bubble (e.g. Tab to leave the tree).
      break;
  }
}

function onNodeClick(node: VisibleNode) {
  focusNodeById(node.id);
  emit('navigate', node.id);
}

function onChevronClick(node: VisibleNode, e: MouseEvent) {
  e.stopPropagation();
  focusNodeById(node.id);
  emit('toggle-expand', node.id);
}

function onAction(action: FolderActionType, folderId: string) {
  emit('action', action, folderId);
}

// --- Inline create (tree surface) ---
// Local draft + input ref, mirroring the rename mechanic in FolderGridItems.
// The parent (page) owns the editing state machine; this component only owns
// the input element + draft, emitting create-commit(name) / create-cancel().
const treeCreateDraft = ref('');
const createInputEl = ref<HTMLInputElement | null>(null);
const setCreateInput = (el: any) => {
  createInputEl.value = (el as HTMLInputElement | null) || null;
};
// When the parent activates tree-surface create (creatingParentId turns
// non-null), seed an empty draft and focus the input. nextTick lets the
// placeholder row render before we query/focus the input.
watch(
  () => props.creatingParentId,
  (id) => {
    if (id !== null) {
      treeCreateDraft.value = '';
      nextTick(() => {
        createInputEl.value?.focus();
      });
    }
  },
);
const commitTreeCreate = () => {
  if (props.creatingParentId === null) return;
  const trimmed = treeCreateDraft.value.trim();
  if (!trimmed) {
    emit('create-cancel');
    return;
  }
  emit('create-commit', trimmed);
};
const cancelTreeCreate = () => {
  if (props.creatingParentId === null) return;
  emit('create-cancel');
};
</script>

<template>
  <div
    ref="treeRef"
    class="folder-tree"
    role="tree"
    :aria-label="t('knowledgeBase.folderTreeTitle')"
    @keydown="onTreeKeydown"
  >
    <!-- Loading state: 复用骨架屏 / 加载. -->
    <div v-if="loading" class="folder-tree-state folder-tree-loading" aria-busy="true">
      <t-icon name="loading" size="20px" class="folder-tree-loading-icon" />
    </div>

    <!-- Visible nodes (flat list, hierarchy expressed via aria-level + indent)
         with an optional tree-surface create-input placeholder interleaved.
         The root is ALWAYS rendered (even with zero folders) so the panel "+"
         can create the first folder under it - no separate empty state. -->
    <template v-else>
      <div
        v-for="(item, idx) in renderNodes"
        :key="item.kind === 'placeholder' ? '__folder-create__' : (item.node.id || 'root')"
      >
        <!-- Tree-surface create input row -->
        <div
          v-if="item.kind === 'placeholder'"
          class="folder-tree-node folder-tree-create-node"
          :style="{ paddingLeft: (item.level - 1) * 16 + 'px' }"
          @click.stop
          @keydown.stop
        >
          <span class="folder-tree-chevron-placeholder" aria-hidden="true"></span>
          <t-icon name="folder-add" size="16px" class="folder-tree-node-icon" aria-hidden="true" />
          <div class="folder-tree-create-input-wrap">
            <input
              :ref="setCreateInput"
              v-model.trim="treeCreateDraft"
              class="folder-tree-create-input"
              type="text"
              :placeholder="t('knowledgeBase.newFolder')"
              :aria-label="t('knowledgeBase.newFolder')"
              :aria-invalid="!!props.createError"
              @click.stop
              @keydown.enter.prevent="commitTreeCreate"
              @keydown.esc.prevent="cancelTreeCreate"
              @blur="commitTreeCreate"
            />
            <span v-if="props.createError" class="folder-tree-create-error" role="alert">
              {{ props.createError }}
            </span>
          </div>
        </div>
        <!-- Real tree node (existing markup; node -> item.node) -->
        <div
          v-else
          class="folder-tree-node"
          :class="{
            'is-current': item.node.isCurrent,
            'is-root': item.node.isRoot,
          }"
          role="treeitem"
          :data-folder-idx="item.visibleIdx"
          :aria-level="item.node.level"
          :aria-expanded="item.node.hasChildren ? item.node.isExpanded : undefined"
          :aria-selected="item.node.isCurrent"
          :tabindex="focusedIndex === item.visibleIdx ? 0 : -1"
          :style="{ paddingLeft: (item.node.level - 1) * 16 + 'px' }"
          @click="onNodeClick(item.node)"
        >
          <button
            v-if="item.node.hasChildren && !item.node.isRoot"
            type="button"
            class="folder-tree-chevron"
            tabindex="-1"
            :aria-label="item.node.isExpanded ? t('knowledgeBase.collapseFolder') : t('knowledgeBase.expandFolder')"
            @click="onChevronClick(item.node, $event)"
          >
            <t-icon
              name="chevron-right"
              size="14px"
              class="folder-tree-chevron-icon"
              aria-hidden="true"
            />
          </button>
          <span v-else class="folder-tree-chevron-placeholder" aria-hidden="true"></span>

          <t-icon
            :name="item.node.isRoot ? 'root-list' : (item.node.hasChildren && item.node.isExpanded ? 'folder-open' : 'folder')"
            size="16px"
            class="folder-tree-node-icon"
            aria-hidden="true"
          />

          <span class="folder-tree-node-label" :title="item.node.name">{{ item.node.name }}</span>

          <div
            v-if="!item.node.isRoot && editable"
            class="folder-tree-node-actions"
            @click.stop
            @keydown.stop
          >
            <t-dropdown trigger="click" placement="bottom-right" :min-column-width="148">
              <button
                type="button"
                class="folder-tree-actions-trigger"
                :tabindex="focusedId === item.node.id ? 0 : -1"
                :aria-label="t('knowledgeBase.folderActions')"
                @click.stop
              >
                <t-icon name="more" size="16px" aria-hidden="true" />
              </button>
              <template #dropdown>
                <t-dropdown-menu>
                  <t-dropdown-item @click="onAction('add-subfolder', item.node.id)">
                    <t-icon name="folder-add" /> {{ t('knowledgeBase.folderActionAddSubfolder') }}
                  </t-dropdown-item>
                  <t-dropdown-item @click="onAction('rename', item.node.id)">
                    <t-icon name="edit" /> {{ t('knowledgeBase.folderActionRename') }}
                  </t-dropdown-item>
                  <t-dropdown-item
                    theme="error"
                    class="folder-tree-action-delete"
                    @click="onAction('delete', item.node.id)"
                  >
                    <t-icon name="delete" /> {{ t('knowledgeBase.folderActionDelete') }}
                  </t-dropdown-item>
                </t-dropdown-menu>
              </template>
            </t-dropdown>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped lang="less">
.folder-tree {
  display: flex;
  flex-direction: column;
  padding: 4px 0;
  min-height: 0;
  overflow-y: auto;
}

.folder-tree-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 32px 16px;
  text-align: center;
}

.folder-tree-loading {
  .folder-tree-loading-icon {
    color: var(--td-text-color-placeholder);
    animation: folder-tree-spin 1s linear infinite;
  }
}

@keyframes folder-tree-spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.folder-tree-node {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 32px;
  padding-right: 4px;
  font-size: 13px;
  color: var(--td-text-color-primary);
  border-radius: 6px;
  cursor: pointer;
  outline: none;
  transition: background-color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:focus-visible {
    box-shadow: inset 0 0 0 2px var(--td-brand-color);
  }

  &.is-current {
    background: var(--td-brand-color-light);

    .folder-tree-node-icon,
    .folder-tree-node-label {
      color: var(--td-brand-color);
      font-weight: 600;
    }
  }

  // Current + hover: keep brand background, just reinforce.
  &.is-current:hover {
    background: var(--td-brand-color-light);
  }
}

.folder-tree-chevron {
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

  .folder-tree-chevron-icon {
    transition: transform 0.18s cubic-bezier(0.2, 0, 0, 1);
  }
}

.folder-tree-node[aria-expanded='true'] .folder-tree-chevron-icon {
  transform: rotate(90deg);
}

.folder-tree-chevron-placeholder {
  display: inline-block;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
}

// Tree-surface create input row. Mirrors the real node footprint (32px height,
// same indent via paddingLeft) so the input lines up with siblings.
.folder-tree-create-node {
  // The input wrap takes the label's flex slot; keep chevron/icon alignment.
  gap: 8px;

  &:hover {
    background: transparent;
  }
}

.folder-tree-create-input-wrap {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.folder-tree-create-input {
  width: 100%;
  height: 24px;
  padding: 0 6px;
  border: 1px solid var(--td-brand-color);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 13px;
  outline: none;
  box-sizing: border-box;

  &:focus {
    box-shadow: 0 0 0 2px var(--td-brand-color-light);
  }
}

.folder-tree-create-error {
  font-size: 11px;
  line-height: 16px;
  color: var(--td-error-color-6);
  white-space: normal;
  word-break: break-word;
}

.folder-tree-node-icon {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.folder-tree-node-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-primary);
}

// Actions menu. Hidden by default, revealed on hover / current / focus-within
// (行操作按钮默认隐藏, hover/选中/菜单打开时显示). Touch and keyboard remain
// reachable because the trigger becomes tabindex=0 when its node is focused.
.folder-tree-node-actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  opacity: 0;
  transition: opacity 0.15s ease;

  .folder-tree-node:hover &,
  .folder-tree-node.is-current &,
  .folder-tree-node:focus-within & {
    opacity: 1;
  }
}

.folder-tree-actions-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: none;
  background: transparent;
  border-radius: 4px;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-component);
    color: var(--td-text-color-primary);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 1px;
  }
}

:deep(.t-dropdown__item.folder-tree-action-delete) {
  border-top: 1px solid var(--td-component-stroke);
  margin-top: 4px;
  padding-top: 8px;
  color: var(--td-error-color-6);

  .t-icon {
    color: var(--td-error-color-6);
  }
}
</style>
