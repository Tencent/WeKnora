<template>
  <aside class="kb-folder-tree" :class="{ 'is-collapsed': collapsed }">
    <div class="kb-folder-tree__header">
      <template v-if="!collapsed">
        <span class="kb-folder-tree__title">{{ t('knowledgeBase.folderTree.title') }}</span>
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
        <div
          v-for="row in rows"
          :key="row.path || '__root__'"
          class="kb-folder-row"
          :class="{ active: selectedPath === row.path, 'is-root': row.kind === 'root' }"
          :style="{ '--kb-folder-depth': row.depth }"
          :title="row.kind === 'root' ? t('knowledgeBase.folderTree.rootRowTip') : row.path"
          role="button"
          tabindex="0"
          @click="emit('select', row.path)"
          @keydown.enter="emit('select', row.path)"
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

          <t-icon
            :name="row.kind === 'root' || (row.hasChildren && isExpanded(row.path)) ? 'folder-open' : 'folder'"
            class="kb-folder-row__icon"
          />

          <input
            v-if="renamingPath === row.path"
            ref="renameInputRef"
            v-model="renameValue"
            class="kb-folder-row__rename"
            :placeholder="t('knowledgeBase.folderTree.renamePlaceholder')"
            @click.stop
            @keydown.enter="commitRename(row)"
            @keydown.esc="cancelRename"
            @blur="commitRename(row)"
          />
          <template v-else>
            <span class="kb-folder-row__label">
              {{ row.kind === 'root' ? t('knowledgeBase.folderTree.rootRow') : row.name }}
            </span>
            <t-tooltip v-if="canEdit && row.kind === 'folder'"
              :content="t('knowledgeBase.folderTree.rename')" placement="top">
              <button type="button" class="kb-folder-row__action"
                :aria-label="t('knowledgeBase.folderTree.rename')" @click.stop="startRename(row)">
                <t-icon name="edit" />
              </button>
            </t-tooltip>
            <span class="kb-folder-row__count">{{ row.totalCount }}</span>
          </template>
        </div>
      </template>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KnowledgeFolderTree } from '@/api/knowledge-base/index'
import {
  buildFolderRows,
  folderAncestorPaths,
  joinFolderPath,
  ROOT_FOLDER_PATH,
  type FolderRow,
} from '../folderTree'

const props = withDefaults(defineProps<{
  tree: KnowledgeFolderTree | null
  /** Selected folder path; the empty string is the knowledge base top level. */
  selectedPath: string
  loading?: boolean
  collapsed?: boolean
  canEdit?: boolean
}>(), {
  loading: false,
  collapsed: false,
  canEdit: false,
})

const emit = defineEmits<{
  select: [path: string]
  'update:collapsed': [collapsed: boolean]
  rename: [payload: { from: string; to: string }]
}>()

const { t } = useI18n()

// The root starts expanded so the uploaded structure is visible without a click.
const expanded = ref(new Set<string>([ROOT_FOLDER_PATH]))
const renamingPath = ref('')
const renameValue = ref('')
const renameInputRef = ref<HTMLInputElement | HTMLInputElement[] | null>(null)

const rows = computed(() => buildFolderRows(props.tree, expanded.value))

const isExpanded = (path: string) => expanded.value.has(path)

const toggle = (path: string) => {
  const next = new Set(expanded.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  expanded.value = next
}

const startRename = async (row: FolderRow) => {
  renamingPath.value = row.path
  renameValue.value = row.name
  await nextTick()
  const input = Array.isArray(renameInputRef.value) ? renameInputRef.value[0] : renameInputRef.value
  input?.focus()
  input?.select()
}

const cancelRename = () => {
  renamingPath.value = ''
  renameValue.value = ''
}

const commitRename = (row: FolderRow) => {
  if (renamingPath.value !== row.path) return
  const name = renameValue.value.trim()
  cancelRename()
  // Only the last segment is edited here; the folder keeps its place in the tree.
  if (!name || name === row.name) return
  const parent = row.path.slice(0, Math.max(0, row.path.length - row.name.length - 1))
  emit('rename', { from: row.path, to: joinFolderPath(parent, name) })
}

// Keep the selected folder reachable: expand the root and every folder above
// the active path, both on first load and when the selection changes from
// elsewhere (e.g. opening a folder from the document list).
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

    .kb-folder-row__action {
      opacity: 1;
    }
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

.kb-folder-row__action {
  flex: 0 0 auto;
  width: 20px;
  height: 20px;
  padding: 0;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s ease, color 0.15s ease, background-color 0.15s ease;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-bg-color-secondarycontainer);
  }

  .t-icon {
    font-size: 13px;
  }
}

.kb-folder-row__rename {
  flex: 1;
  min-width: 0;
  height: 22px;
  padding: 0 6px;
  border: 1px solid var(--td-brand-color);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 13px;
  outline: none;
}

.kb-folder-row__count {
  flex: 0 0 auto;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
}
</style>
