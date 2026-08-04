<template>
  <aside class="kb-folder-tree" :class="{ 'is-collapsed': collapsed }">
    <div class="kb-folder-tree__header">
      <template v-if="!collapsed">
        <span class="kb-folder-tree__title">{{ t('knowledgeBase.folderTree.title') }}</span>
        <div class="kb-folder-tree__header-actions">
          <t-tooltip
            :content="recursive
              ? t('knowledgeBase.folderTree.scopeSubtreeTip')
              : t('knowledgeBase.folderTree.scopeDirectTip')"
            placement="top"
          >
            <button
              type="button"
              class="kb-folder-tree__icon-btn"
              :class="{ active: recursive }"
              :aria-pressed="recursive"
              :aria-label="t('knowledgeBase.folderTree.scopeToggle')"
              @click="emit('update:recursive', !recursive)"
            >
              <t-icon :name="recursive ? 'tree-round-dot-vertical' : 'file-1'" size="15px" />
            </button>
          </t-tooltip>
          <t-tooltip :content="t('knowledgeBase.folderTree.collapse')" placement="top">
            <button
              type="button"
              class="kb-folder-tree__icon-btn"
              :aria-label="t('knowledgeBase.folderTree.collapse')"
              @click="emit('update:collapsed', true)"
            >
              <t-icon name="chevron-left-double" size="15px" />
            </button>
          </t-tooltip>
        </div>
      </template>
      <t-tooltip v-else :content="t('knowledgeBase.folderTree.expand')" placement="right">
        <button
          type="button"
          class="kb-folder-tree__icon-btn"
          :aria-label="t('knowledgeBase.folderTree.expand')"
          @click="emit('update:collapsed', false)"
        >
          <t-icon name="chevron-right-double" size="15px" />
        </button>
      </t-tooltip>
    </div>

    <div v-if="!collapsed" class="kb-folder-tree__body">
      <template v-if="loading && !tree">
        <div v-for="n in 5" :key="'folder-skel-' + n" class="kb-folder-tree__skeleton">
          <t-skeleton animation="gradient" :row-col="[{ width: '100%', height: '16px' }]" />
        </div>
      </template>
      <template v-else>
        <button
          v-for="row in rows"
          :key="row.path || '__root__'"
          type="button"
          class="kb-folder-row"
          :class="{ active: selectedPath === row.path, 'is-root': row.kind === 'root' }"
          :style="{ '--kb-folder-depth': row.depth }"
          :title="rowTitle(row)"
          @click="emit('select', row.path)"
        >
          <span
            v-if="row.hasChildren"
            class="kb-folder-row__toggle"
            role="button"
            :aria-label="t(isExpanded(row.path)
              ? 'knowledgeBase.folderTree.collapseFolder'
              : 'knowledgeBase.folderTree.expandFolder')"
            @click.stop="toggle(row.path)"
          >
            <t-icon :name="isExpanded(row.path) ? 'chevron-down' : 'chevron-right'" />
          </span>
          <span v-else class="kb-folder-row__toggle-placeholder" aria-hidden="true" />
          <t-icon :name="rowIcon(row)" class="kb-folder-row__icon" />
          <span class="kb-folder-row__label">{{ rowLabel(row) }}</span>
          <span class="kb-folder-row__count">{{ folderRowCount(row, recursive) }}</span>
        </button>
      </template>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KnowledgeFolderTree } from '@/api/knowledge-base/index'
import {
  buildFolderRows,
  folderAncestorPaths,
  folderRowCount,
  ROOT_FOLDER_PATH,
  type FolderRow,
} from '../folderTree'

const props = withDefaults(defineProps<{
  tree: KnowledgeFolderTree | null
  /** Selected folder path; the empty string is the knowledge base root. */
  selectedPath: string
  loading?: boolean
  collapsed?: boolean
  /** When true a folder lists its sub-folders' documents too. */
  recursive?: boolean
}>(), {
  loading: false,
  collapsed: false,
  recursive: true,
})

const emit = defineEmits<{
  select: [path: string]
  'update:collapsed': [collapsed: boolean]
  'update:recursive': [recursive: boolean]
}>()

const { t } = useI18n()

// The root starts expanded so the uploaded structure is visible without a click.
const expanded = ref(new Set<string>([ROOT_FOLDER_PATH]))

const rows = computed(() => buildFolderRows(props.tree, expanded.value))

const isExpanded = (path: string) => expanded.value.has(path)

const rowLabel = (row: FolderRow) =>
  row.kind === 'root' ? t('knowledgeBase.folderTree.rootRow') : row.name

const rowIcon = (row: FolderRow) => {
  if (row.kind === 'root') return 'folder-open'
  return row.hasChildren && isExpanded(row.path) ? 'folder-open' : 'folder'
}

const rowTitle = (row: FolderRow) =>
  row.kind === 'root' ? t('knowledgeBase.folderTree.rootRowTip') : row.path

const toggle = (path: string) => {
  const next = new Set(expanded.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  expanded.value = next
}

// Keep the selected folder reachable: expand the root and every folder above
// the active path, both on first load and when the selection changes from
// elsewhere (e.g. clicking a document's folder chip in the list).
watch(
  () => [props.selectedPath, props.tree] as const,
  () => {
    const next = new Set(expanded.value)
    folderAncestorPaths(props.selectedPath).forEach((path) => next.add(path))
    expanded.value = next
  },
  { immediate: true },
)

// First load also opens the top-level folders, so a two-level upload is visible
// in full without any expanding.
watch(
  () => props.tree,
  (tree) => {
    if (!tree?.folders?.length || expanded.value.size > 1) return
    const next = new Set(expanded.value)
    tree.folders.forEach((folder) => next.add(folder.path))
    expanded.value = next
  },
  { immediate: true },
)
</script>

<style scoped lang="less">
.kb-folder-tree {
  --kb-folder-indent: 14px;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  width: 232px;
  min-height: 0;
  padding-right: 12px;
  margin-right: 12px;
  border-right: 1px solid var(--td-component-stroke);
  box-sizing: border-box;

  &.is-collapsed {
    width: auto;
    padding-right: 8px;
    margin-right: 8px;
  }
}

.kb-folder-tree__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  height: 32px;
  flex-shrink: 0;
}

.kb-folder-tree__title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.kb-folder-tree__header-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.kb-folder-tree__icon-btn {
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-bg-color-container-hover);
  }

  &.active {
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
  }
}

.kb-folder-tree__body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 4px 0 12px;
  scrollbar-width: thin;

  &::-webkit-scrollbar {
    width: 4px;
  }

  &::-webkit-scrollbar-thumb {
    border-radius: 2px;
    background: var(--td-scrollbar-color);
  }
}

.kb-folder-tree__skeleton {
  padding: 7px 8px;
}

.kb-folder-row {
  display: flex;
  align-items: center;
  gap: 6px;
  box-sizing: border-box;
  width: 100%;
  height: 30px;
  padding: 0 8px 0 calc(var(--kb-folder-depth, 0) * var(--kb-folder-indent) + 8px);
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
  user-select: none;
  transition: background 0.15s ease, color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:focus-visible {
    outline: none;
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-brand-color) 30%, transparent);
  }

  &.active {
    background: var(--td-bg-color-container-hover);

    .kb-folder-row__label,
    .kb-folder-row__icon {
      color: var(--td-brand-color);
    }
  }

  &.is-root .kb-folder-row__label {
    font-weight: 500;
  }
}

.kb-folder-row__toggle,
.kb-folder-row__toggle-placeholder {
  flex: 0 0 auto;
  width: 16px;
  height: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-placeholder);
  border-radius: 4px;
}

.kb-folder-row__toggle {
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-secondarycontainer);
  }

  .t-icon {
    font-size: 14px;
  }
}

.kb-folder-row__icon {
  flex: 0 0 auto;
  font-size: 15px;
  color: var(--td-text-color-placeholder);
}

.kb-folder-row__label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kb-folder-row__count {
  flex: 0 0 auto;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
}
</style>
