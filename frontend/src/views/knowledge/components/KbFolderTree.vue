<template>
  <aside class="kb-folder-tree" :class="{ 'is-collapsed': collapsed }">
    <div class="kb-folder-tree__header">
      <template v-if="!collapsed">
        <span class="kb-folder-tree__title">{{ t('knowledgeBase.folderTree.title') }}</span>
        <div class="kb-folder-tree__header-actions">
          <t-tooltip :content="t('knowledgeBase.folderTree.includeSubfolders')" placement="top">
            <button
              type="button"
              class="kb-folder-tree__icon-btn"
              :class="{ active: recursive }"
              :aria-pressed="recursive"
              :aria-label="t('knowledgeBase.folderTree.includeSubfolders')"
              @click="emit('update:recursive', !recursive)"
            >
              <t-icon name="tree-round-dot-vertical" size="15px" />
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
      <template v-if="loading && !rows.length">
        <div v-for="n in 5" :key="'folder-skel-' + n" class="kb-folder-tree__skeleton">
          <t-skeleton animation="gradient" :row-col="[{ width: '100%', height: '16px' }]" />
        </div>
      </template>
      <template v-else>
        <button
          type="button"
          class="kb-folder-row kb-folder-row--all"
          :class="{ active: selectedPath === null }"
          @click="emit('select', null)"
        >
          <t-icon name="folder-open" class="kb-folder-row__icon" />
          <span class="kb-folder-row__label">{{ t('knowledgeBase.folderTree.allDocuments') }}</span>
          <span class="kb-folder-row__count">{{ tree?.total_document_count ?? 0 }}</span>
        </button>

        <button
          v-if="showRootRow"
          type="button"
          class="kb-folder-row"
          :class="{ active: selectedPath === '' }"
          :style="{ '--kb-folder-depth': 0 }"
          @click="emit('select', '')"
        >
          <span class="kb-folder-row__toggle-placeholder" aria-hidden="true" />
          <t-icon name="folder" class="kb-folder-row__icon" />
          <span class="kb-folder-row__label">{{ t('knowledgeBase.folderTree.rootFolder') }}</span>
          <span class="kb-folder-row__count">{{ tree?.root_document_count ?? 0 }}</span>
        </button>

        <button
          v-for="row in rows"
          :key="row.node.path"
          type="button"
          class="kb-folder-row"
          :class="{ active: selectedPath === row.node.path }"
          :style="{ '--kb-folder-depth': row.depth }"
          :title="row.node.path"
          @click="emit('select', row.node.path)"
        >
          <span
            v-if="row.node.children?.length"
            class="kb-folder-row__toggle"
            role="button"
            :aria-label="t(expanded.has(row.node.path)
              ? 'knowledgeBase.folderTree.collapseFolder'
              : 'knowledgeBase.folderTree.expandFolder')"
            @click.stop="toggle(row.node.path)"
          >
            <t-icon :name="expanded.has(row.node.path) ? 'chevron-down' : 'chevron-right'" />
          </span>
          <span v-else class="kb-folder-row__toggle-placeholder" aria-hidden="true" />
          <t-icon
            :name="expanded.has(row.node.path) && row.node.children?.length ? 'folder-open' : 'folder'"
            class="kb-folder-row__icon"
          />
          <span class="kb-folder-row__label">{{ row.node.name }}</span>
          <span class="kb-folder-row__count">{{ recursive ? row.node.total_count : row.node.document_count }}</span>
        </button>

        <p v-if="!rows.length && !loading" class="kb-folder-tree__empty">
          {{ t('knowledgeBase.folderTree.empty') }}
        </p>
      </template>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KnowledgeFolderTree } from '@/api/knowledge-base/index'
import { flattenFolderRows, folderAncestorPaths } from '../folderTree'

const props = withDefaults(defineProps<{
  tree: KnowledgeFolderTree | null
  /** `null` = every folder, `''` = knowledge base root, otherwise a folder path. */
  selectedPath: string | null
  loading?: boolean
  collapsed?: boolean
  recursive?: boolean
}>(), {
  loading: false,
  collapsed: false,
  recursive: false,
})

const emit = defineEmits<{
  select: [path: string | null]
  'update:collapsed': [collapsed: boolean]
  'update:recursive': [recursive: boolean]
}>()

const { t } = useI18n()

const expanded = ref(new Set<string>())

/**
 * The root row only makes sense once folders exist: without it a knowledge base
 * with no folder upload would show a redundant second "all documents" row.
 */
const showRootRow = computed(() => (props.tree?.folders?.length ?? 0) > 0)

const rows = computed(() => flattenFolderRows(props.tree?.folders ?? [], expanded.value))

const toggle = (path: string) => {
  const next = new Set(expanded.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  expanded.value = next
}

// Keep the selected folder reachable: every ancestor of the active path is
// expanded, both on first load and when the selection changes from elsewhere
// (e.g. clicking a document's folder chip in the list).
watch(
  () => [props.selectedPath, props.tree] as const,
  () => {
    const ancestors = folderAncestorPaths(props.selectedPath)
    if (ancestors.length === 0) return
    const next = new Set(expanded.value)
    ancestors.forEach((path) => next.add(path))
    expanded.value = next
  },
  { immediate: true },
)

// First load auto-expands the top level so folders are visible without a click.
watch(
  () => props.tree,
  (tree) => {
    if (!tree?.folders?.length || expanded.value.size > 0) return
    expanded.value = new Set(tree.folders.map((folder) => folder.path))
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
}

.kb-folder-row--all {
  margin-bottom: 2px;
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

.kb-folder-tree__empty {
  margin: 8px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--td-text-color-placeholder);
}
</style>
