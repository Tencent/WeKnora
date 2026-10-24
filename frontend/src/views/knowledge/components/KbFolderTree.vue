<template>
  <aside ref="treeElement" class="kb-folder-tree" :class="{ 'is-collapsed': collapsed }" :aria-busy="busy" @keydown.esc="cancelDrag(); closeFolderMenu()">
    <div class="kb-folder-tree__header">
      <template v-if="!collapsed">
        <span class="kb-folder-tree__title">{{ t('knowledgeBase.folderTree.title') }}</span>
        <span v-if="tree && !loading" class="kb-folder-tree__total"
          :title="t('knowledgeBase.folderTree.totalDocuments', { count: tree.total_document_count })">
          {{ t('knowledgeBase.folderTree.totalDocuments', { count: tree.total_document_count }) }}
        </span>
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
          :class="{
            active: selectedPath === row.path,
            'is-root': row.kind === 'root',
            'is-editable': canEdit && row.kind === 'folder',
            'is-menu-open': menuOpenPath === row.path,
            'is-dragging': draggingPath === row.path,
            'is-drop-target': dropTarget === row.path,
          }"
          :style="{ '--kb-folder-depth': row.depth }"
          :title="row.kind === 'root' ? t('knowledgeBase.folderTree.rootRowTip') : row.path"
          role="button"
          tabindex="0"
          :data-folder-path="row.path"
          :draggable="canEdit && !busy && !loading && row.kind === 'folder' && !isRenaming(row)"
          @dragstart.stop="startDrag($event, row)"
          @dragover="overFolder($event, row)"
          @dragleave="leaveFolder($event, row)"
          @drop="dropFolder($event, row)"
          @dragend.stop="cancelDrag"
          @click="emit('select', row.path)"
          @keydown.enter.self="emit('select', row.path)"
          @keydown.space.prevent.self="emit('select', row.path)"
        >
          <span
            v-if="row.hasChildren"
            class="kb-folder-row__toggle"
            role="button"
            tabindex="0"
            :aria-expanded="isExpanded(row.path)"
            @keydown.enter.stop.prevent="toggle(row.path)"
            @keydown.space.stop.prevent="toggle(row.path)"
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
            v-if="isRenaming(row)"
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
              {{ row.kind === 'root' ? (rootLabel || t('knowledgeBase.folderTree.rootRow')) : row.name }}
            </span>
            <span class="kb-folder-row__trailing">
              <span class="kb-folder-row__count"
                :title="t('knowledgeBase.folderTree.countHint', { direct: row.documentCount, total: row.totalCount })">
                {{ row.totalCount }}
              </span>
              <t-popup
                v-if="canEdit && row.kind === 'folder'"
                :visible="menuOpenPath === row.path"
                trigger="click"
                placement="bottom-right"
                destroy-on-close
                overlay-class-name="card-more-popup"
                @visible-change="(visible: boolean) => onFolderMenuVisible(row.path, visible)"
              >
                <button
                  type="button"
                  class="kb-folder-row__more"
                  :class="{ 'is-open': menuOpenPath === row.path }"
                  :aria-label="t('knowledgeBase.columnActions')"
                  :aria-expanded="menuOpenPath === row.path"
                  :data-folder-action-path="row.path"
                  :disabled="busy"
                  @click.stop
                >
                  <t-icon name="more" />
                </button>
                <template #content>
                  <div :ref="setMenuElement" class="popup-menu kb-folder-row__menu" @click.stop @keydown.esc.stop.prevent="closeFolderMenu">
                    <template v-if="movingPath === row.path">
                      <button type="button" class="popup-menu-item folder-action" @click="showMoveTargets(null)">{{ t('common.back') }}</button>
                      <p class="kb-folder-row__move-hint">{{ t('knowledgeBase.folderTree.moveHint') }}</p>
                      <button v-for="target in moveTargets(row.path)" :key="target.path" type="button"
                        class="popup-menu-item folder-action" @click="requestMove(row.path, target.path)">
                        <t-icon name="folder" class="menu-icon" />
                        <span>{{ target.path || rootLabel || t('knowledgeBase.folderTree.rootRow') }}</span>
                      </button>
                      <p v-if="!moveTargets(row.path).length" class="kb-folder-row__move-hint">{{ t('knowledgeBase.folderTree.noMoveTarget') }}</p>
                    </template>
                    <template v-else>
                    <button type="button" class="popup-menu-item folder-action" @click="onFolderMenuRename(row)">
                      <t-icon name="edit" class="menu-icon" />
                      <span>{{ t('knowledgeBase.folderTree.rename') }}</span>
                    </button>
                    <button type="button" class="popup-menu-item folder-action" @click="showMoveTargets(row.path)">
                      <t-icon name="folder-move" class="menu-icon" /><span>{{ t('knowledgeBase.moveToFolder.action') }}</span>
                    </button>
                    </template>
                  </div>
                </template>
              </t-popup>
              <span v-else class="kb-folder-row__more-placeholder" aria-hidden="true"></span>
            </span>
          </template>
        </div>
      </template>
    </div>
    <p v-if="canEdit && !collapsed" class="kb-folder-tree__hint" role="status" aria-live="polite">
      {{ moveStatus }}
    </p>
  </aside>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import type { KnowledgeFolderTree } from '@/api/knowledge-base/index'
import { folderDropDestination } from '../folderDrop'
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
  rootLabel?: string
  selectedPath: string
  loading?: boolean
  collapsed?: boolean
  canEdit?: boolean
  busy?: boolean
}>(), {
  loading: false,
  collapsed: false,
  canEdit: false,
  busy: false,
})

const emit = defineEmits<{
  select: [path: string]
  'update:collapsed': [collapsed: boolean]
  rename: [payload: { from: string; to: string }]
  move: [payload: { from: string; to: string }]
}>()

const { t } = useI18n()

// The root starts expanded so the uploaded structure is visible without a click.
const expanded = ref(new Set<string>([ROOT_FOLDER_PATH]))
// null, not '', because '' is the root's own path: a falsy sentinel would put
// the root row into rename mode permanently.
const renamingPath = ref<string | null>(null)
const menuOpenPath = ref<string | null>(null)
const renameValue = ref('')
const renameInputRef = ref<HTMLInputElement | HTMLInputElement[] | null>(null)
const treeElement = ref<HTMLElement | null>(null)
const menuElement = ref<HTMLElement | null>(null)
const draggingPath = ref<string | null>(null)
const dropTarget = ref<string | null>(null)
const movingPath = ref<string | null>(null)
const focusMenu = async () => {
  await nextTick()
  const menu = menuElement.value
  // Popper reveals the teleported container asynchronously. Wait for an
  // actual visible frame, not a fixed delay that races slow rendering.
  const focusWhenVisible = (attempt = 0) => {
    if (!menu?.isConnected || menuOpenPath.value === null) return
    const button = menu.querySelector<HTMLButtonElement>('button')
    if (button && getComputedStyle(button).visibility === 'visible') button.focus()
    else if (attempt < 30) requestAnimationFrame(() => focusWhenVisible(attempt + 1))
  }
  requestAnimationFrame(() => focusWhenVisible())
}
// The popup content is teleported and mounts after visible-change fires.
// Focus on mount, rather than racing that deferred render with a timer.
const setMenuElement = (element: Element | ComponentPublicInstance | null) => {
  const next = element instanceof HTMLElement ? element : null
  if (next === menuElement.value) return
  menuElement.value = next
  if (next) void focusMenu()
}
const showMoveTargets = (path: string | null) => {
  movingPath.value = path
  void focusMenu()
}
const moveStatus = computed(() => {
  if (props.busy) return t('knowledgeBase.folderTree.moving')
  if (dropTarget.value !== null) return t('knowledgeBase.folderTree.dropTarget', {
    folder: dropTarget.value || props.rootLabel || t('knowledgeBase.folderTree.rootRow'),
  })
  return t(draggingPath.value ? 'knowledgeBase.folderTree.dragHint' : 'knowledgeBase.folderTree.reparentHint')
})
const closeFolderMenu = async () => {
  const path = menuOpenPath.value
  menuOpenPath.value = null
  movingPath.value = null
  if (path === null) return
  await nextTick()
  Array.from(treeElement.value?.querySelectorAll<HTMLButtonElement>('.kb-folder-row__more') || [])
    .find(button => button.dataset.folderActionPath === path)?.focus()
}
const allRows = computed(() => {
  const paths = new Set<string>([''])
  const visit = (nodes: KnowledgeFolderTree['folders']) => nodes.forEach(node => {
    paths.add(node.path)
    visit(node.children || [])
  })
  visit(props.tree?.folders || [])
  return buildFolderRows(props.tree, paths)
})
const destination = (from: string, parent: string) => folderDropDestination(from, parent, allRows.value.map(row => row.path))
const moveTargets = (from: string) => allRows.value.filter(row => destination(from, row.path) !== null)
const cancelDrag = () => { draggingPath.value = null; dropTarget.value = null }
const startDrag = (event: DragEvent, row: FolderRow) => {
  if (!props.canEdit || props.busy || props.loading || row.kind !== 'folder' || isRenaming(row)) {
    event.preventDefault()
    return
  }
  draggingPath.value = row.path
  menuOpenPath.value = null
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('application/x-weknora-folder', row.path)
  }
}
const overFolder = (event: DragEvent, row: FolderRow) => {
  if (!draggingPath.value) return // leave file uploads to the existing upload flow
  event.stopPropagation()
  const valid = props.canEdit && !props.busy && !props.loading && destination(draggingPath.value, row.path) !== null
  dropTarget.value = valid ? row.path : null
  if (event.dataTransfer) event.dataTransfer.dropEffect = valid ? 'move' : 'none'
  if (valid) event.preventDefault()
}
const leaveFolder = (event: DragEvent, row: FolderRow) => {
  if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null) && dropTarget.value === row.path) dropTarget.value = null
}
const requestMove = (from: string, parent: string) => {
  if (!props.canEdit || props.busy || props.loading) return
  const to = destination(from, parent)
  if (to === null) return
  menuOpenPath.value = null
  movingPath.value = null
  cancelDrag()
  emit('move', { from, to })
}
const dropFolder = (event: DragEvent, row: FolderRow) => {
  if (!draggingPath.value) return
  event.preventDefault()
  event.stopPropagation()
  requestMove(draggingPath.value, row.path)
  cancelDrag()
}
watch(() => [props.tree, props.canEdit, props.busy, props.loading], () => {
  cancelDrag()
  menuOpenPath.value = null
  movingPath.value = null
})

const rows = computed(() => buildFolderRows(props.tree, expanded.value))

const isExpanded = (path: string) => expanded.value.has(path)

// The root has no name of its own to edit, and excluding it here means no
// sentinel value can ever put it into rename mode.
const isRenaming = (row: FolderRow) => row.kind === 'folder' && renamingPath.value === row.path

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

const onFolderMenuVisible = (path: string, visible: boolean) => {
  if (visible && (!props.canEdit || props.busy || props.loading)) return
  movingPath.value = null
  menuOpenPath.value = visible ? path : null
  if (visible) void focusMenu()
}

const onFolderMenuRename = async (row: FolderRow) => {
  menuOpenPath.value = null
  await startRename(row)
}

const cancelRename = () => {
  renamingPath.value = null
  renameValue.value = ''
}

const commitRename = (row: FolderRow) => {
  if (!isRenaming(row)) return
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
  width: 220px;
  min-height: 0;
  padding-right: 12px;
  margin-right: 16px;
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
  padding-bottom: 16px;
  flex-shrink: 0;
}

.kb-folder-tree__title {
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.kb-folder-tree__total {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-xs);
  font-weight: 400;
  color: var(--td-text-color-placeholder);
}

.kb-folder-tree__icon-btn {
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

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
  padding: 8px 0 12px;
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
  gap: 4px;
  box-sizing: border-box;
  width: 100%;
  height: 32px;
  margin-bottom: 2px;
  padding: 0 4px 0 calc(4px + var(--kb-folder-depth, 0) * var(--kb-folder-indent));
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: var(--app-text-md);
  text-align: left;
  cursor: pointer;
  user-select: none;
  transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:focus-visible {
    outline: none;
    outline: 2px solid var(--app-focus-border);
    outline-offset: 2px;
  }

  &.active {
    background: var(--app-selection-bg);

    .kb-folder-row__label,
    .kb-folder-row__icon {
      color: var(--td-brand-color);
    }
    .kb-folder-row__label { font-weight: 500; }
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
  border-radius: var(--app-radius-xs);
}

.kb-folder-row__toggle {
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-secondarycontainer);
  }

  .t-icon {
    font-size: var(--app-text-base);
  }
}

.kb-folder-row__icon {
  flex: 0 0 auto;
  font-size: var(--app-text-xl);
  color: var(--td-text-color-secondary);
}

.kb-folder-row__label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kb-folder-row__trailing {
  flex: 0 0 auto;
  gap: 2px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-left: 2px;
}

.kb-folder-row__count {
  min-width: 16px;
  text-align: right;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
  line-height: 1;
}

.kb-folder-row__more-placeholder { width: 20px; }

.kb-folder-row__more {
  display: inline-flex;
  opacity: 0;
  pointer-events: none;
  width: 20px;
  height: 20px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-xs);
  background: transparent;
  color: var(--td-text-color-placeholder);
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: color var(--app-motion-fast) ease, background-color var(--app-motion-fast) ease;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-bg-color-secondarycontainer);
  }

  .t-icon {
    font-size: var(--app-text-base);
  }
}

.kb-folder-row.is-editable:hover,
.kb-folder-row.is-editable:focus-within,
.kb-folder-row.is-menu-open {
  .kb-folder-row__more {
    opacity: 1;
    pointer-events: auto;
  }
}

.kb-folder-row__more.is-open {
  color: var(--td-brand-color);
}

.kb-folder-row__menu {
  min-width: 140px;
  max-width: 280px;
  max-height: 320px;
  overflow: auto;
}
.folder-action { border: 0; background: transparent; width: 100%; text-align: left; font: inherit; }
.folder-action span { overflow-wrap: anywhere; white-space: normal; }
.folder-action:focus-visible, .kb-folder-row__more:focus-visible { outline: 2px solid var(--app-focus-border); outline-offset: -2px; }
.kb-folder-row__move-hint, .kb-folder-tree__hint { margin: 6px 4px; font-size: var(--app-text-xs); line-height: 1.6; color: var(--td-text-color-secondary); }
.kb-folder-row.is-dragging { opacity: .55; }
.kb-folder-row.is-drop-target { background: var(--app-selection-bg); outline: 2px dashed var(--td-brand-color); outline-offset: -2px; }
.kb-folder-row[draggable="true"] { cursor: grab; }
.kb-folder-row__more:disabled { opacity: .4; cursor: wait; }
@media (hover: none) { .kb-folder-row.is-editable .kb-folder-row__more { opacity: 1; pointer-events: auto; } }

.kb-folder-row__rename {
  flex: 1;
  min-width: 0;
  height: 22px;
  padding: 0 6px;
  border: 1px solid var(--td-brand-color);
  border-radius: var(--app-radius-xs);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: var(--app-text-md);
  outline: none;
}
</style>
