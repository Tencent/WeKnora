<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

// FolderTree is a presentational, accessible folder tree. It renders data from
// props and emits navigation / expand / action events; it never calls folder
// APIs and contains no drag handling (first version explicitly cancels drag).
//
// The root is a LOCAL VIRTUAL NODE (id ""). The root API sentinel never
// appears in this component. Tree node height 32-36px, indent 16px per level,
// icon-name gap 8px. Current folder uses light brand background + brand
// icon/name; expanded chevron rotates 90deg.

export type FolderActionType = 'rename' | 'delete' | 'add-subfolder';

const props = withDefaults(defineProps<{
  tree: KnowledgeFolder[];
  currentFolderId: string;
  expandedFolderIds: Set<string>;
  editable: boolean;
  loading?: boolean;
  rootLabel?: string;
}>(), {
  loading: false,
  rootLabel: '',
});

const emit = defineEmits<{
  (e: 'navigate', folderId: string): void;
  (e: 'toggle-expand', folderId: string): void;
  (e: 'action', action: FolderActionType, folderId: string): void;
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

const isEmpty = computed(() => props.tree.length === 0);

// Roving tabindex: exactly one node (the focused one, or the current node by
// default) has tabindex=0; all others have tabindex=-1. This is the ARIA APG
// treeview pattern - a single tab stop within the tree.
//
// null = "no explicit focus" (fall back to current/root via focusedIndex).
// '' = root is focused (root's id is '' per ROOT_FOLDER_ID). The nullable type
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

// ARIA treeview keyboard model (APG Multi-Select Treeview pattern, single
// selection). Arrow keys move/expand/collapse; Enter navigates; Home/End jump
// to first/last visible node.
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

    <!-- Empty state: 空文件夹 居中空状态, 主说明 14px, 辅助说明 12px. -->
    <div
      v-else-if="isEmpty"
      class="folder-tree-state folder-tree-empty"
      role="status"
    >
      <t-icon name="folder-add" size="32px" class="folder-tree-empty-icon" aria-hidden="true" />
      <p class="folder-tree-empty-title">{{ t('knowledgeBase.emptyFolderTree') }}</p>
      <p class="folder-tree-empty-hint">{{ t('knowledgeBase.emptyFolderTreeHint') }}</p>
    </div>

    <!-- Visible nodes (flat list, hierarchy expressed via aria-level + indent). -->
    <template v-else>
      <div
        v-for="(node, idx) in visibleNodes"
        :key="node.id || 'root'"
        class="folder-tree-node"
        :class="{
          'is-current': node.isCurrent,
          'is-root': node.isRoot,
        }"
        role="treeitem"
        :data-folder-idx="idx"
        :aria-level="node.level"
        :aria-expanded="node.hasChildren ? node.isExpanded : undefined"
        :aria-selected="node.isCurrent"
        :tabindex="focusedIndex === idx ? 0 : -1"
        :style="{ paddingLeft: (node.level - 1) * 16 + 'px' }"
        @click="onNodeClick(node)"
      >
        <!-- Expand/collapse chevron. Root has no chevron (always expanded);
             leaf nodes get a placeholder for alignment. -->
        <button
          v-if="node.hasChildren && !node.isRoot"
          type="button"
          class="folder-tree-chevron"
          tabindex="-1"
          :aria-label="node.isExpanded ? t('knowledgeBase.collapseFolder') : t('knowledgeBase.expandFolder')"
          @click="onChevronClick(node, $event)"
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
          :name="node.isRoot ? 'root-list' : (node.hasChildren && node.isExpanded ? 'folder-open' : 'folder')"
          size="16px"
          class="folder-tree-node-icon"
          aria-hidden="true"
        />

        <span class="folder-tree-node-label" :title="node.name">{{ node.name }}</span>

        <!-- Per-node actions menu. Only for editable users and non-root nodes.
             Root-level creation goes through the panel header button. -->
        <div
          v-if="!node.isRoot && editable"
          class="folder-tree-node-actions"
          @click.stop
          @keydown.stop
        >
          <t-dropdown trigger="click" placement="bottom-right" :min-column-width="148">
            <button
              type="button"
              class="folder-tree-actions-trigger"
              :tabindex="focusedId === node.id ? 0 : -1"
              :aria-label="t('knowledgeBase.folderActions')"
              @click.stop
            >
              <t-icon name="more" size="16px" aria-hidden="true" />
            </button>
            <template #dropdown>
              <t-dropdown-menu>
                <t-dropdown-item @click="onAction('add-subfolder', node.id)">
                  <t-icon name="folder-add" /> {{ t('knowledgeBase.folderActionAddSubfolder') }}
                </t-dropdown-item>
                <t-dropdown-item @click="onAction('rename', node.id)">
                  <t-icon name="edit" /> {{ t('knowledgeBase.folderActionRename') }}
                </t-dropdown-item>
                <t-dropdown-item
                  theme="error"
                  class="folder-tree-action-delete"
                  @click="onAction('delete', node.id)"
                >
                  <t-icon name="delete" /> {{ t('knowledgeBase.folderActionDelete') }}
                </t-dropdown-item>
              </t-dropdown-menu>
            </template>
          </t-dropdown>
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

// 空文件夹 - 主说明 14px, 辅助说明 12px.
.folder-tree-empty {
  .folder-tree-empty-icon {
    color: var(--td-text-color-placeholder);
    margin-bottom: 8px;
  }
  .folder-tree-empty-title {
    margin: 0 0 4px;
    font-size: 14px;
    color: var(--td-text-color-primary);
  }
  .folder-tree-empty-hint {
    margin: 0;
    font-size: 12px;
    line-height: 18px;
    color: var(--td-text-color-placeholder);
  }
}

// 树节点高度 32-36px (using 32px); 图标与名称间距 8px.
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
    // Visible focus ring (焦点态可见).
    box-shadow: inset 0 0 0 2px var(--td-brand-color);
  }

  // 当前文件夹 - 浅品牌背景, 图标/名称品牌色, 字重 500-600.
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

  // chevron 旋转 90°, 0.15-0.2s 过渡.
  .folder-tree-chevron-icon {
    transition: transform 0.18s cubic-bezier(0.2, 0, 0, 1);
  }
}

// Expanded treeitem: rotate the chevron icon 90deg.
.folder-tree-node[aria-expanded='true'] .folder-tree-chevron-icon {
  transform: rotate(90deg);
}

.folder-tree-chevron-placeholder {
  display: inline-block;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
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
