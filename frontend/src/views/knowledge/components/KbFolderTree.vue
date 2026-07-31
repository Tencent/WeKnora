<template>
  <!-- Folder sidebar for the document list.

       The whole tree is fetched in one request and flattened into rows here.
       A folder set is navigation-sized even when the base holds hundreds of
       thousands of documents, so lazy per-level loading would only add
       round-trips and a spinner per expand. -->
  <aside class="kb-folder-tree" :class="{ 'is-collapsed': collapsed }">
    <div class="kb-folder-tree__header">
      <button type="button" class="kb-folder-tree__collapse" :title="collapsed
        ? t('knowledgeBase.folderExpandPanel')
        : t('knowledgeBase.folderCollapsePanel')" @click="collapsed = !collapsed">
        <t-icon :name="collapsed ? 'menu-fold' : 'menu-unfold'" size="16px" />
      </button>
      <span v-if="!collapsed" class="kb-folder-tree__title">{{ t('knowledgeBase.folderPanelTitle') }}</span>
      <t-button v-if="!collapsed && canManage" variant="text" theme="default" size="small"
        class="kb-folder-tree__add" :title="t('knowledgeBase.folderNewRoot')" @click="startCreateRoot">
        <t-icon name="folder-add" size="16px" />
      </t-button>
    </div>

    <template v-if="!collapsed">
      <div class="kb-folder-tree__body">
        <!-- "All documents" clears the folder filter entirely rather than
             selecting a folder, which is what keeps the pre-folder listing
             behaviour reachable in one click. -->
        <div :class="['kb-folder-row', 'kb-folder-row--all', { 'is-active': modelValue === null }]"
          @click="select(null)">
          <t-icon name="view-list" class="kb-folder-row__icon" />
          <span class="kb-folder-row__label">{{ t('knowledgeBase.folderAllDocuments') }}</span>
        </div>

        <!-- Root bucket: documents that were never filed. It doubles as the
             drop target for "move back out of any folder". -->
        <div :class="['kb-folder-row', {
          'is-active': modelValue === '',
          'is-drop': dropTargetId === ROOT_ID,
        }]" @click="select('')" @dragover.prevent="onDragOverFolder($event, ROOT_ID)"
          @dragleave="onDragLeaveFolder(ROOT_ID)" @drop.prevent="onDrop($event, ROOT_ID)">
          <t-icon name="folder-open" class="kb-folder-row__icon" />
          <span class="kb-folder-row__label">{{ t('knowledgeBase.folderUnfiled') }}</span>
          <span class="kb-folder-row__count">{{ rootDocumentCount }}</span>
        </div>

        <div v-if="creatingRoot" class="kb-folder-row kb-folder-row--editing" @click.stop>
          <t-icon name="folder" class="kb-folder-row__icon" />
          <input ref="createRootInputRef" v-model="createRootName" class="kb-folder-row__input"
            :placeholder="t('knowledgeBase.folderNamePlaceholder')" @keydown.enter="submitCreateRoot"
            @keydown.esc="cancelCreateRoot" />
          <div class="kb-folder-row__inline-actions">
            <t-button variant="text" size="small" @click.stop="submitCreateRoot">
              <t-icon name="check" size="14px" />
            </t-button>
            <t-button variant="text" size="small" @click.stop="cancelCreateRoot">
              <t-icon name="close" size="14px" />
            </t-button>
          </div>
        </div>

        <t-loading v-if="loading && !rows.length" size="small" class="kb-folder-tree__loading" />

        <div v-for="row in rows" :key="row.id" :class="['kb-folder-row', {
          'is-active': modelValue === row.id,
          'is-drop': dropTargetId === row.id,
        }]" :style="{ '--kb-folder-depth': row.depth - 1 }" :draggable="canManage && editingId !== row.id"
          :title="row.namePath" @click="select(row.id)" @dragstart="onFolderDragStart($event, row)"
          @dragend="clearDropTarget" @dragover.prevent.stop="onDragOverFolder($event, row.id)"
          @dragleave.stop="onDragLeaveFolder(row.id)" @drop.prevent.stop="onDrop($event, row.id)">
          <t-icon v-if="row.hasChildren" :name="row.expanded ? 'chevron-down' : 'chevron-right'"
            class="kb-folder-row__toggle" @click.stop="toggleExpand(row.id)" />
          <span v-else class="kb-folder-row__toggle kb-folder-row__toggle--placeholder" />
          <t-icon :name="row.expanded && row.hasChildren ? 'folder-open' : 'folder'" class="kb-folder-row__icon" />

          <input v-if="editingId === row.id" ref="renameInputRef" v-model="editingName" class="kb-folder-row__input"
            :placeholder="t('knowledgeBase.folderNamePlaceholder')" @click.stop
            @keydown.enter="submitRename(row)" @keydown.esc="cancelRename" @blur="submitRename(row)" />
          <template v-else>
            <span class="kb-folder-row__label">{{ row.name }}</span>
            <span class="kb-folder-row__count">{{
              recursive ? row.total_document_count : row.document_count
            }}</span>
            <KbFolderActions v-if="canManage" :name="row.name" :document-count="row.total_document_count"
              :has-children="row.hasChildren" @create="(name: string) => createChild(row.id, name)"
              @rename="() => startRename(row)" @delete="(strategy: 'fail' | 'reparent') => remove(row, strategy)" />
          </template>
        </div>

        <div v-if="!loading && !rows.length && !creatingRoot" class="kb-folder-tree__empty">
          {{ t('knowledgeBase.folderEmptyHint') }}
        </div>
      </div>

      <!-- The recursive toggle only matters once a folder is selected; showing
           it against "all documents" would imply a filter that isn't applied. -->
      <label v-if="modelValue" class="kb-folder-tree__recursive">
        <t-checkbox :checked="recursive" @change="(v: boolean) => emit('update:recursive', v)">
          {{ t('knowledgeBase.folderIncludeSubfolders') }}
        </t-checkbox>
      </label>
    </template>
  </aside>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import KbFolderActions from './KbFolderActions.vue'
import {
  listKnowledgeFolders,
  createKnowledgeFolder,
  updateKnowledgeFolder,
  deleteKnowledgeFolder,
  moveKnowledgeToFolder,
  type KnowledgeFolderNode,
} from '@/api/knowledge-base'
import { KB_DOC_DRAG_TYPE, KB_FOLDER_DRAG_TYPE } from '../folderDrag'

const ROOT_ID = ''

const props = withDefaults(defineProps<{
  kbId: string
  /** Selected folder: null = no filter, '' = unfiled root, otherwise a folder id. */
  modelValue: string | null
  recursive?: boolean
  canManage?: boolean
}>(), {
  recursive: false,
  canManage: false,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string | null): void
  (e: 'update:recursive', value: boolean): void
  /** Documents were filed elsewhere; the parent should refresh its list. */
  (e: 'documents-moved', count: number): void
}>()

const { t } = useI18n()

const collapsed = ref(false)
const loading = ref(false)
const folders = ref<KnowledgeFolderNode[]>([])
const rootDocumentCount = ref(0)
const expanded = ref<Set<string>>(new Set())
const dropTargetId = ref<string | null>(null)

const editingId = ref<string | null>(null)
const editingName = ref('')
const renameInputRef = ref<HTMLInputElement[] | HTMLInputElement | null>(null)
// Guards the blur handler so pressing Enter (which also blurs) does not submit twice.
let renameSubmitting = false

const creatingRoot = ref(false)
const createRootName = ref('')
const createRootInputRef = ref<HTMLInputElement | null>(null)

const byId = computed(() => {
  const map = new Map<string, KnowledgeFolderNode>()
  folders.value.forEach((folder) => map.set(folder.id, folder))
  return map
})

const childrenOf = computed(() => {
  const map = new Map<string, KnowledgeFolderNode[]>()
  folders.value.forEach((folder) => {
    const siblings = map.get(folder.parent_id) || []
    siblings.push(folder)
    map.set(folder.parent_id, siblings)
  })
  // Server order is depth-first-friendly already; sort defensively so a
  // rename does not reshuffle rows relative to a fresh load.
  map.forEach((list) => list.sort((a, b) => (a.sort_order - b.sort_order) || a.name.localeCompare(b.name)))
  return map
})

interface FolderRow extends KnowledgeFolderNode {
  expanded: boolean
  hasChildren: boolean
  namePath: string
}

/**
 * Flattens the tree into the visible rows, skipping collapsed subtrees.
 * A flat list keeps the template a single v-for, which avoids the recursive
 * component indirection the wiki browser needs for its nested markup.
 */
const rows = computed<FolderRow[]>(() => {
  const out: FolderRow[] = []
  const walk = (parentId: string) => {
    const children = childrenOf.value.get(parentId) || []
    for (const folder of children) {
      const hasChildren = (childrenOf.value.get(folder.id) || []).length > 0
      const isExpanded = expanded.value.has(folder.id)
      out.push({
        ...folder,
        expanded: isExpanded,
        hasChildren,
        namePath: (folder.name_path || [folder.name]).join(' / '),
      })
      if (hasChildren && isExpanded) walk(folder.id)
    }
  }
  walk(ROOT_ID)
  return out
})

async function load() {
  if (!props.kbId) return
  loading.value = true
  try {
    const res: any = await listKnowledgeFolders(props.kbId, { recursive: true })
    const data = res?.data || res
    folders.value = data?.folders || []
    rootDocumentCount.value = data?.root_document_count || 0
  } catch {
    folders.value = []
    rootDocumentCount.value = 0
  } finally {
    loading.value = false
  }
}

defineExpose({ reload: load })

watch(() => props.kbId, () => {
  expanded.value = new Set()
  folders.value = []
  load()
}, { immediate: true })

function select(id: string | null) {
  emit('update:modelValue', id)
}

function toggleExpand(id: string) {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

/** Reveals a folder by expanding every ancestor on its materialized id path. */
function revealFolder(folder: { path: string; id: string }) {
  const ids = folder.path.split('/').filter(Boolean)
  const next = new Set(expanded.value)
  ids.forEach((id) => {
    if (id !== folder.id) next.add(id)
  })
  expanded.value = next
}

function startCreateRoot() {
  creatingRoot.value = true
  createRootName.value = ''
  nextTick(() => createRootInputRef.value?.focus())
}

function cancelCreateRoot() {
  creatingRoot.value = false
  createRootName.value = ''
}

async function submitCreateRoot() {
  const name = createRootName.value.trim()
  if (!name) {
    cancelCreateRoot()
    return
  }
  cancelCreateRoot()
  await doCreate(ROOT_ID, name)
}

function createChild(parentId: string, name: string) {
  // Expand first so the new folder is visible where it lands rather than
  // silently appearing inside a collapsed branch.
  const next = new Set(expanded.value)
  next.add(parentId)
  expanded.value = next
  doCreate(parentId, name)
}

async function doCreate(parentId: string, name: string) {
  try {
    await createKnowledgeFolder(props.kbId, { name, parent_id: parentId })
    MessagePlugin.success(t('knowledgeBase.folderCreateSuccess'))
    await load()
  } catch (err: any) {
    MessagePlugin.error(resolveError(err, t('knowledgeBase.folderCreateFailed')))
  }
}

function startRename(row: FolderRow) {
  editingId.value = row.id
  editingName.value = row.name
  renameSubmitting = false
  nextTick(() => {
    const el = Array.isArray(renameInputRef.value) ? renameInputRef.value[0] : renameInputRef.value
    el?.focus()
    el?.select()
  })
}

function cancelRename() {
  editingId.value = null
  editingName.value = ''
}

async function submitRename(row: FolderRow) {
  if (renameSubmitting || editingId.value !== row.id) return
  const name = editingName.value.trim()
  if (!name || name === row.name) {
    cancelRename()
    return
  }
  renameSubmitting = true
  cancelRename()
  try {
    await updateKnowledgeFolder(props.kbId, row.id, { name })
    await load()
  } catch (err: any) {
    MessagePlugin.error(resolveError(err, t('knowledgeBase.folderRenameFailed')))
  } finally {
    renameSubmitting = false
  }
}

async function remove(row: FolderRow, strategy: 'fail' | 'reparent') {
  try {
    await deleteKnowledgeFolder(props.kbId, row.id, { strategy })
    MessagePlugin.success(t('knowledgeBase.folderDeleteSuccess'))
    // Selecting a folder that no longer exists would leave the list stuck on
    // an empty page with no obvious way back.
    if (props.modelValue === row.id) select(null)
    await load()
    if (strategy === 'reparent') emit('documents-moved', 0)
  } catch (err: any) {
    MessagePlugin.error(resolveError(err, t('knowledgeBase.folderDeleteFailed')))
  }
}

/* ------------------------------------------------------------------ *
 * Drag and drop
 * ------------------------------------------------------------------ */

function onFolderDragStart(e: DragEvent, row: FolderRow) {
  if (!props.canManage) return
  e.dataTransfer?.setData(KB_FOLDER_DRAG_TYPE, row.id)
  // Some browsers cancel a drag with no text/plain payload.
  e.dataTransfer?.setData('text/plain', row.name)
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}

function onDragOverFolder(e: DragEvent, id: string) {
  if (!props.canManage) return
  const types = e.dataTransfer?.types || []
  if (!types.includes(KB_DOC_DRAG_TYPE) && !types.includes(KB_FOLDER_DRAG_TYPE)) return
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  dropTargetId.value = id
}

function onDragLeaveFolder(id: string) {
  if (dropTargetId.value === id) dropTargetId.value = null
}

function clearDropTarget() {
  dropTargetId.value = null
}

async function onDrop(e: DragEvent, targetId: string) {
  clearDropTarget()
  if (!props.canManage) return

  const folderId = e.dataTransfer?.getData(KB_FOLDER_DRAG_TYPE)
  if (folderId) {
    await moveFolder(folderId, targetId)
    return
  }

  const raw = e.dataTransfer?.getData(KB_DOC_DRAG_TYPE)
  if (!raw) return
  let ids: string[] = []
  try {
    ids = JSON.parse(raw)
  } catch {
    return
  }
  if (!ids.length) return
  await moveDocuments(ids, targetId)
}

async function moveFolder(folderId: string, targetId: string) {
  if (folderId === targetId) return
  const source = byId.value.get(folderId)
  const target = targetId ? byId.value.get(targetId) : null
  if (!source) return
  if (source.parent_id === targetId) return
  // Local guard so the obvious mistake gives instant feedback; the server
  // enforces the same rule against the materialized path.
  if (target && target.path.startsWith(source.path)) {
    MessagePlugin.warning(t('knowledgeBase.folderMoveIntoSelf'))
    return
  }
  try {
    await updateKnowledgeFolder(props.kbId, folderId, { parent_id: targetId, move_parent: true })
    if (target) revealFolder(target)
    MessagePlugin.success(t('knowledgeBase.folderMoveSuccess'))
    await load()
  } catch (err: any) {
    MessagePlugin.error(resolveError(err, t('knowledgeBase.folderMoveFailed')))
  }
}

async function moveDocuments(ids: string[], targetId: string) {
  try {
    const res: any = await moveKnowledgeToFolder(props.kbId, ids, targetId)
    const moved = res?.moved ?? ids.length
    MessagePlugin.success(t('knowledgeBase.folderMoveDocsSuccess', { count: moved }))
    await load()
    emit('documents-moved', moved)
  } catch (err: any) {
    MessagePlugin.error(resolveError(err, t('knowledgeBase.folderMoveDocsFailed')))
  }
}

function resolveError(err: any, fallback: string) {
  return err?.response?.data?.error?.message || err?.message || fallback
}
</script>

<style lang="less" scoped>
.kb-folder-tree {
  flex: 0 0 236px;
  width: 236px;
  display: flex;
  flex-direction: column;
  min-height: 0;
  margin-right: 16px;
  border-right: 1px solid var(--td-component-stroke);
  padding-right: 8px;
  transition: flex-basis 0.15s, width 0.15s;

  &.is-collapsed {
    flex-basis: 36px;
    width: 36px;
    padding-right: 0;
    margin-right: 8px;
  }
}

.kb-folder-tree__header {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 32px;
  flex-shrink: 0;
}

.kb-folder-tree__collapse {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: none;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  border-radius: 4px;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-brand-color);
  }
}

.kb-folder-tree__title {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kb-folder-tree__body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding-bottom: 8px;
}

.kb-folder-tree__loading,
.kb-folder-tree__empty {
  padding: 12px 8px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.kb-folder-tree__recursive {
  flex-shrink: 0;
  padding: 8px 4px 4px;
  border-top: 1px solid var(--td-component-stroke);
  font-size: 12px;
}

.kb-folder-row {
  --kb-folder-depth: 0;
  display: flex;
  align-items: center;
  gap: 4px;
  height: 30px;
  padding-right: 4px;
  padding-left: calc(6px + var(--kb-folder-depth) * 12px);
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
  color: var(--td-text-color-primary);
  user-select: none;

  &:hover {
    background: var(--td-bg-color-container-hover);

    :deep(.kb-folder-action) {
      opacity: 1;
    }
  }

  &.is-active {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
    font-weight: 500;
  }

  // Highlight the row a dragged item would land in. An outline (rather than a
  // border) avoids shifting the row by a pixel mid-drag, which browsers treat
  // as the cursor leaving the element and cancels the drop.
  &.is-drop {
    outline: 1px dashed var(--td-brand-color);
    outline-offset: -1px;
    background: var(--td-brand-color-light);
  }

  &--all {
    padding-left: 6px;
  }

  &--editing {
    cursor: default;
  }
}

.kb-folder-row__toggle {
  flex: 0 0 auto;
  width: 14px;
  height: 14px;
  font-size: 14px;
  color: var(--td-text-color-placeholder);

  &--placeholder {
    display: inline-block;
  }
}

.kb-folder-row__icon {
  flex: 0 0 auto;
  font-size: 15px;
  color: var(--td-text-color-secondary);
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
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
}

.kb-folder-row__input {
  flex: 1;
  min-width: 0;
  height: 22px;
  border: 1px solid var(--td-brand-color);
  border-radius: 4px;
  padding: 0 6px;
  font-size: 13px;
  outline: none;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
}

.kb-folder-row__inline-actions {
  display: flex;
  align-items: center;
  gap: 2px;
}
</style>
