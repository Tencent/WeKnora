<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Folder } from '@/types/folder'
import {
  KNOWLEDGE_FOLDER_SIDEBAR_MIN_WIDTH,
  useResizableKnowledgeFolderSidebar,
} from '@/composables/useResizableSidebar'
import {
  buildFolderBreadcrumb,
  buildFolderMoveDisabledIds,
  buildFolderTree,
  flattenVisibleFolderTree,
} from '../utils/folderTree'
import { isDragTargetAllowed, type ActiveDrag } from '../utils/folderDrag'

const props = defineProps<{
  folders: Folder[]
  currentFolderId: string | null
  loading: boolean
  loadFailed: boolean
  canEdit: boolean
  activeDrag: ActiveDrag | null
  dragSubmitting: boolean
}>()

const emit = defineEmits<{
  select: [folderId: string | null]
  create: []
  rename: [folder: Folder]
  move: [folder: Folder]
  delete: [folder: Folder]
  retry: []
  folderDragStart: [event: DragEvent, folder: Folder]
  dragEnd: []
  dropTarget: [event: DragEvent, folderId: string | null]
}>()

const { t } = useI18n()
const {
  width: sidebarWidth,
  maxWidth: sidebarMaxWidth,
  desktop: sidebarResizable,
  dragging: sidebarResizing,
  sidebarStyle,
  startResize,
  resetWidth: resetSidebarWidth,
  handleResizeKeydown,
} = useResizableKnowledgeFolderSidebar()
const expandedIds = ref<Set<string>>(new Set())
let knownFolderIds = new Set<string>()
const tree = computed(() => buildFolderTree(props.folders))
const rows = computed(() => flattenVisibleFolderTree(tree.value.roots, expandedIds.value))
const hoveredDropTarget = ref<string | null | undefined>(undefined)
const hoveredDropAllowed = ref(false)
const dragActive = computed(() => props.canEdit && props.activeDrag !== null)
const disabledDropIds = computed(() => {
  const disabled = new Set<string>()
  const drag = props.activeDrag
  if (!drag) return disabled
  if (drag.type === 'folder') {
    for (const id of buildFolderMoveDisabledIds(props.folders, drag.folderId, drag.sourceParentId)) {
      disabled.add(id)
    }
  } else if (drag.sourceFolderId) {
    disabled.add(drag.sourceFolderId)
  }
  for (const id of tree.value.cyclicIds) disabled.add(id)
  for (const id of tree.value.orphanIds) disabled.add(id)
  return disabled
})
const rootDropDisabled = computed(() =>
  !props.activeDrag || (
    props.activeDrag.type === 'folder'
      ? props.activeDrag.sourceParentId === null
      : props.activeDrag.sourceFolderId === null
  ),
)

watch(
  () => props.folders,
  folders => {
    const validIds = new Set(folders.map(folder => folder.id))
    const next = new Set([...expandedIds.value].filter(id => validIds.has(id)))
    // Expand folders on first load and newly-created folders afterwards, while
    // preserving explicit user collapses across rename/create/delete refreshes.
    for (const folder of folders) {
      if (!knownFolderIds.has(folder.id)) next.add(folder.id)
    }
    expandedIds.value = next
    knownFolderIds = validIds
  },
  { immediate: true },
)

watch(
  [() => props.currentFolderId, tree],
  ([currentFolderId]) => {
    const path = buildFolderBreadcrumb(currentFolderId, tree.value.byId)
    if (path.length === 0) return
    const next = new Set(expandedIds.value)
    for (const folder of path) next.add(folder.id)
    expandedIds.value = next
  },
  { immediate: true },
)

const toggleExpanded = (folderId: string) => {
  const next = new Set(expandedIds.value)
  if (next.has(folderId)) next.delete(folderId)
  else next.add(folderId)
  expandedIds.value = next
}

const isHoveredTarget = (folderId: string | null) => hoveredDropTarget.value === folderId
const isVisuallyDisabledTarget = (folderId: string | null) =>
  folderId === null ? rootDropDisabled.value : disabledDropIds.value.has(folderId)

const onTargetDragOver = (event: DragEvent, folderId: string | null) => {
  if (!dragActive.value || props.dragSubmitting) return
  event.preventDefault()
  event.stopPropagation()
  if (isHoveredTarget(folderId)) {
    if (event.dataTransfer) event.dataTransfer.dropEffect = hoveredDropAllowed.value ? 'move' : 'none'
    return
  }
  const allowed = isDragTargetAllowed(props.activeDrag, folderId, props.folders)
  hoveredDropTarget.value = folderId
  hoveredDropAllowed.value = allowed
  if (event.dataTransfer) event.dataTransfer.dropEffect = allowed ? 'move' : 'none'
}

const onTargetDragLeave = (event: DragEvent, folderId: string | null) => {
  const current = event.currentTarget as HTMLElement | null
  const related = event.relatedTarget as Node | null
  if (current && related && current.contains(related)) return
  if (isHoveredTarget(folderId)) {
    hoveredDropTarget.value = undefined
    hoveredDropAllowed.value = false
  }
}

const onTargetDrop = (event: DragEvent, folderId: string | null) => {
  if (!dragActive.value || props.dragSubmitting) return
  event.preventDefault()
  event.stopPropagation()
  const allowed = isDragTargetAllowed(props.activeDrag, folderId, props.folders)
  hoveredDropTarget.value = undefined
  hoveredDropAllowed.value = false
  if (allowed) emit('dropTarget', event, folderId)
}

watch(
  [() => props.activeDrag, () => props.folders],
  () => {
    if (!props.activeDrag || (
      hoveredDropTarget.value && !tree.value.byId.has(hoveredDropTarget.value)
    )) {
      hoveredDropTarget.value = undefined
      hoveredDropAllowed.value = false
    }
  },
)
</script>

<template>
  <aside
    class="folder-sidebar"
    :class="{ 'is-resizing': sidebarResizing }"
    :style="sidebarStyle"
    :aria-label="t('knowledgeFolder.title')"
  >
    <div class="folder-sidebar__header">
      <span>{{ t('knowledgeFolder.title') }}</span>
      <span v-if="dragSubmitting" class="folder-sidebar__moving">
        <t-loading size="small" />
        {{ t('knowledgeFolderDrag.moving') }}
      </span>
      <t-tooltip v-else-if="canEdit" :content="t('knowledgeFolder.createInCurrent')" placement="top">
        <t-button size="small" variant="text" shape="square" @click="emit('create')">
          <t-icon name="folder-add" />
        </t-button>
      </t-tooltip>
    </div>

    <div v-if="loading" class="folder-sidebar__state">
      <t-loading size="small" :text="t('knowledgeFolder.loading')" />
    </div>
    <div v-else-if="loadFailed" class="folder-sidebar__state">
      <span>{{ t('knowledgeFolder.loadFailed') }}</span>
      <t-button size="small" variant="text" @click="emit('retry')">
        {{ t('common.retry') }}
      </t-button>
    </div>
    <div v-else class="folder-tree" role="tree">
      <button
        type="button"
        role="treeitem"
        class="folder-row folder-row--root"
        :class="{
          active: currentFolderId === null,
          'drop-available': dragActive && !rootDropDisabled,
          'drop-disabled': dragActive && rootDropDisabled,
          'drop-hover': isHoveredTarget(null) && hoveredDropAllowed,
          'drop-hover-invalid': isHoveredTarget(null) && !hoveredDropAllowed,
        }"
        :aria-selected="currentFolderId === null"
        :title="dragActive
          ? t(rootDropDisabled ? 'knowledgeFolderDrag.cannotDrop' : 'knowledgeFolderDrag.dropHere')
          : undefined"
        @click="emit('select', null)"
        @dragover="onTargetDragOver($event, null)"
        @dragleave="onTargetDragLeave($event, null)"
        @drop="onTargetDrop($event, null)"
      >
        <span class="folder-row__toggle" aria-hidden="true" />
        <t-icon name="home" class="folder-row__icon" />
        <span class="folder-row__name">{{ t('knowledgeFolder.root') }}</span>
        <span v-if="isHoveredTarget(null)" class="folder-row__drop-label">
          {{ t(hoveredDropAllowed ? 'knowledgeFolderDrag.dropHere' : 'knowledgeFolderDrag.cannotDrop') }}
        </span>
      </button>

      <div
        v-for="row in rows"
        :key="row.folder.id"
        class="folder-row-wrap"
        :style="{ '--folder-depth': row.depth }"
      >
        <button
          type="button"
          role="treeitem"
          class="folder-row"
          :class="{
            active: currentFolderId === row.folder.id,
            'is-dragging': activeDrag?.type === 'folder' && activeDrag.folderId === row.folder.id,
            'drop-available': dragActive && !isVisuallyDisabledTarget(row.folder.id),
            'drop-disabled': dragActive && isVisuallyDisabledTarget(row.folder.id),
            'drop-hover': isHoveredTarget(row.folder.id) && hoveredDropAllowed,
            'drop-hover-invalid': isHoveredTarget(row.folder.id) && !hoveredDropAllowed,
          }"
          :aria-selected="currentFolderId === row.folder.id"
          :aria-expanded="row.hasChildren ? expandedIds.has(row.folder.id) : undefined"
          :title="dragActive
            ? t(isVisuallyDisabledTarget(row.folder.id) ? 'knowledgeFolderDrag.cannotDrop' : 'knowledgeFolderDrag.dropHere')
            : row.folder.name"
          @click="emit('select', row.folder.id)"
          @dragover="onTargetDragOver($event, row.folder.id)"
          @dragleave="onTargetDragLeave($event, row.folder.id)"
          @drop="onTargetDrop($event, row.folder.id)"
        >
          <span
            v-if="canEdit"
            class="folder-row__drag-handle"
            :class="{ disabled: dragSubmitting }"
            :draggable="!dragSubmitting"
            :title="t('knowledgeFolderDrag.dragHandle', { name: row.folder.name })"
            :aria-label="t('knowledgeFolderDrag.dragHandle', { name: row.folder.name })"
            @click.stop
            @dragstart.stop="emit('folderDragStart', $event, row.folder)"
            @dragend.stop="emit('dragEnd')"
          >
            <t-icon name="move" />
          </span>
          <span
            class="folder-row__toggle"
            :class="{ visible: row.hasChildren, expanded: expandedIds.has(row.folder.id) }"
            @click.stop="row.hasChildren && toggleExpanded(row.folder.id)"
          >
            <t-icon v-if="row.hasChildren" name="chevron-right" />
          </span>
          <t-icon :name="currentFolderId === row.folder.id ? 'folder-open' : 'folder'" class="folder-row__icon" />
          <span class="folder-row__name" :title="row.folder.name">{{ row.folder.name }}</span>
          <span v-if="isHoveredTarget(row.folder.id)" class="folder-row__drop-label">
            {{ t(hoveredDropAllowed ? 'knowledgeFolderDrag.dropHere' : 'knowledgeFolderDrag.cannotDrop') }}
          </span>
        </button>
        <div v-if="canEdit" class="folder-row__actions">
          <t-tooltip :content="t('knowledgeFolder.rename')" placement="top">
            <button type="button" @click.stop="emit('rename', row.folder)">
              <t-icon name="edit" />
            </button>
          </t-tooltip>
          <t-tooltip :content="t('knowledgeFolderMove.moveFolder')" placement="top">
            <button type="button" @click.stop="emit('move', row.folder)">
              <t-icon name="arrow-right" />
            </button>
          </t-tooltip>
          <t-tooltip :content="t('knowledgeFolder.delete')" placement="top">
            <button type="button" class="danger" @click.stop="emit('delete', row.folder)">
              <t-icon name="delete" />
            </button>
          </t-tooltip>
        </div>
      </div>

      <div v-if="folders.length === 0" class="folder-tree__empty">
        {{ t('knowledgeFolder.noFolders') }}
      </div>
    </div>
    <div
      v-if="sidebarResizable"
      class="folder-sidebar__resize-handle"
      role="separator"
      tabindex="0"
      aria-orientation="vertical"
      :aria-label="t('knowledgeFolder.resizeSidebar')"
      :aria-valuemin="KNOWLEDGE_FOLDER_SIDEBAR_MIN_WIDTH"
      :aria-valuemax="sidebarMaxWidth"
      :aria-valuenow="sidebarWidth"
      :title="t('knowledgeFolder.resizeSidebarHint')"
      draggable="false"
      @mousedown.stop.prevent="startResize"
      @keydown="handleResizeKeydown"
      @dblclick.stop.prevent="resetSidebarWidth"
      @dragstart.stop.prevent
    />
  </aside>
</template>

<style scoped lang="less">
.folder-sidebar {
  position: relative;
  width: 224px;
  min-width: 224px;
  min-height: 0;
  display: flex;
  flex-direction: column;
  margin-right: 20px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  overflow: visible;

  &.is-resizing {
    cursor: col-resize;
  }
}

.folder-sidebar__resize-handle {
  position: absolute;
  z-index: 4;
  top: 8px;
  right: -7px;
  bottom: 8px;
  width: 13px;
  border-radius: 8px;
  outline: none;
  cursor: col-resize;
  touch-action: none;

  &::after {
    content: '';
    position: absolute;
    top: 0;
    bottom: 0;
    left: 6px;
    width: 1px;
    border-radius: 1px;
    background: transparent;
    transition: width 0.15s ease, background-color 0.15s ease;
  }

  &:hover::after,
  &:focus-visible::after,
  .folder-sidebar.is-resizing &::after {
    width: 2px;
    background: var(--td-brand-color);
  }
}

.folder-sidebar__header {
  min-height: 44px;
  padding: 0 10px 0 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: var(--td-text-color-primary);
  font-size: 14px;
  font-weight: 600;
  border-bottom: 1px solid var(--td-component-stroke);
}

.folder-sidebar__moving {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--td-brand-color);
  font-size: 11px;
  font-weight: 400;
}

.folder-sidebar__state {
  flex: 1;
  min-height: 120px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 16px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  text-align: center;
}

.folder-tree {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 8px;
  overscroll-behavior: contain;
}

.folder-row-wrap {
  position: relative;
  display: flex;
  align-items: center;
  // Preserve hierarchy while capping runaway indentation for very deep trees.
  padding-left: min(calc(var(--folder-depth) * 14px), 168px);
  min-width: 196px;

  &:hover .folder-row__actions,
  &:focus-within .folder-row__actions {
    opacity: 1;
    pointer-events: auto;
  }
}

.folder-row {
  width: auto;
  flex: 1 0 196px;
  min-height: 34px;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 76px 5px 4px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  text-align: left;
  transition: background-color 0.15s ease, box-shadow 0.15s ease, opacity 0.15s ease;

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &.active {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
    font-weight: 500;
  }

  &.is-dragging {
    opacity: 0.5;
  }

  &.drop-available {
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--td-brand-color) 35%, transparent);
  }

  &.drop-disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  &.drop-hover {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
    box-shadow: inset 0 0 0 2px var(--td-brand-color);
  }

  &.drop-hover-invalid {
    color: var(--td-error-color-6);
    background: var(--td-error-color-1);
    box-shadow: inset 0 0 0 2px var(--td-error-color-6);
    cursor: not-allowed;
  }
}

.folder-row--root {
  width: 100%;
  flex: none;
  padding-right: 8px;
}

.folder-row__toggle {
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  color: var(--td-text-color-placeholder);

  &.visible:hover {
    background: var(--td-bg-color-component-hover);
  }

  :deep(.t-icon) {
    transition: transform 0.15s ease;
  }

  &.expanded :deep(.t-icon) {
    transform: rotate(90deg);
  }
}

.folder-row__drag-handle {
  width: 16px;
  height: 20px;
  flex: 0 0 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-placeholder);
  cursor: grab;

  &:active {
    cursor: grabbing;
  }

  &.disabled {
    cursor: not-allowed;
    opacity: 0.45;
  }
}

.folder-row__icon {
  flex: 0 0 auto;
  font-size: 16px;
}

.folder-row__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.folder-row__drop-label {
  flex: 0 0 auto;
  margin-left: auto;
  overflow: hidden;
  color: inherit;
  font-size: 10px;
  font-weight: 500;
  white-space: nowrap;
}

.folder-row__actions {
  position: sticky;
  right: 6px;
  flex: 0 0 72px;
  margin-left: -76px;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  opacity: 0;
  pointer-events: none;
  background: linear-gradient(90deg, transparent, var(--td-bg-color-container) 18%);
  padding-left: 10px;

  button {
    width: 24px;
    height: 26px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: none;
    border-radius: 4px;
    background: var(--td-bg-color-container);
    color: var(--td-text-color-placeholder);
    cursor: pointer;

    &:hover {
      color: var(--td-brand-color);
      background: var(--td-bg-color-component-hover);
    }

    &.danger:hover {
      color: var(--td-error-color-6);
    }
  }
}

.folder-tree__empty {
  padding: 12px 8px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  text-align: center;
}

@media (max-width: 980px) {
  .folder-sidebar {
    width: 184px;
    min-width: 184px;
    margin-right: 12px;
  }

  .folder-sidebar__resize-handle {
    display: none;
  }
}
</style>
