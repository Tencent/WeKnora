<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  createKnowledgeFolder,
  deleteKnowledgeFolder,
  listKnowledgeFolders,
  updateKnowledgeFolder,
} from '@/api/knowledge-folder'
import type {
  KnowledgeFolder,
  KnowledgeFolderBreadcrumb,
  KnowledgeFolderWithStats,
} from '@/types/knowledgeFolder'
import KnowledgeFolderActions from './KnowledgeFolderActions.vue'

const props = defineProps<{
  knowledgeBaseId: string
  modelValue?: string
  refreshKey?: number
  refreshParentId?: string
  canEdit?: boolean
}>()

const emit = defineEmits<{
  (event: 'update:modelValue', value: string | undefined): void
  (event: 'folder-created', value: {
    knowledgeBaseId: string
    parentId: string
    folder: KnowledgeFolder
  }): void
  (event: 'folder-renamed', value: {
    knowledgeBaseId: string
    folder: KnowledgeFolderWithStats
  }): void
  (event: 'folder-deleted', value: {
    knowledgeBaseId: string
    folderId: string
    parentId: string
  }): void
  (event: 'folder-operation-error', value: {
    action: 'create' | 'rename' | 'delete'
    folderId: string
    error: unknown
  }): void
  (event: 'start-folder-chat', value: {
    knowledgeBaseId: string
    folderId: string
    folderName: string
    breadcrumb: KnowledgeFolderBreadcrumb[]
    includeDescendants: true
  }): void
  (event: 'start-knowledge-base-chat', value: {
    knowledgeBaseId: string
  }): void
}>()

interface FolderLevelState {
  items: KnowledgeFolderWithStats[]
  total: number
  page: number
  pageSize: number
  loaded: boolean
  loading: boolean
  error: boolean
  requestId: number
}

interface FolderRow {
  kind: 'folder'
  folder: KnowledgeFolderWithStats
  depth: number
}

interface StatusRow {
  kind: 'status'
  parentId: string
  depth: number
  status: 'loading' | 'error' | 'empty' | 'more'
}

type VisibleRow = FolderRow | StatusRow

const { t } = useI18n()

const ROOT_PARENT_ID = ''
const ROOT_EXPANDED_KEY = '__knowledge_folder_root__'
const FOLDER_PAGE_SIZE = 50
const DEFAULT_WIDTH = 280
const MIN_WIDTH = 220
const MAX_WIDTH = 480
const INDENT_SIZE = 18
const ROW_GUTTER = 8

const width = ref(DEFAULT_WIDTH)
const resizing = ref(false)
const expandedIds = reactive(new Set<string>())
const levels = reactive<Record<string, FolderLevelState>>({})
const activeMutationKeys = reactive(new Set<string>())

let resizeStartX = 0
let resizeStartWidth = DEFAULT_WIDTH
let treeGeneration = 0
let treeRequestSequence = 0

const isAllFilesSelected = computed(() => props.modelValue === undefined)
const isRootSelected = computed(() => props.modelValue === ROOT_PARENT_ID)
const isRootExpanded = computed(() => expandedIds.has(ROOT_EXPANDED_KEY))

function levelKey(parentId: string): string {
  return parentId === ROOT_PARENT_ID ? ROOT_EXPANDED_KEY : parentId
}

function createLevel(): FolderLevelState {
  return {
    items: [],
    total: 0,
    page: 0,
    pageSize: FOLDER_PAGE_SIZE,
    loaded: false,
    loading: false,
    error: false,
    requestId: 0,
  }
}

function getOrCreateLevel(parentId: string): FolderLevelState {
  const key = levelKey(parentId)
  if (!levels[key]) levels[key] = createLevel()
  return levels[key]
}

function mergeUniqueFolders(
  current: KnowledgeFolderWithStats[],
  incoming: KnowledgeFolderWithStats[],
): KnowledgeFolderWithStats[] {
  const byId = new Map(current.map(folder => [folder.id, folder]))
  for (const folder of incoming) byId.set(folder.id, folder)
  return Array.from(byId.values())
}

async function loadLevel(parentId: string): Promise<void> {
  if (!props.knowledgeBaseId) return

  const state = getOrCreateLevel(parentId)
  if (state.loading) return

  const generation = treeGeneration
  const knowledgeBaseId = props.knowledgeBaseId
  const requestId = ++treeRequestSequence
  const nextPage = state.page + 1
  state.loading = true
  state.error = false
  state.requestId = requestId

  try {
    const response = await listKnowledgeFolders(knowledgeBaseId, {
      parent_id: parentId,
      page: nextPage,
      page_size: FOLDER_PAGE_SIZE,
    })

    if (
      generation !== treeGeneration
      || knowledgeBaseId !== props.knowledgeBaseId
      || levels[levelKey(parentId)] !== state
      || state.requestId !== requestId
    ) return
    if (!response?.success || !response.data || !Array.isArray(response.data.data)) {
      throw new Error('Invalid knowledge folder list response')
    }

    const page = response.data
    state.items = nextPage === 1
      ? mergeUniqueFolders([], page.data)
      : mergeUniqueFolders(state.items, page.data)
    state.total = Math.max(Number(page.total) || 0, state.items.length)
    state.page = Number(page.page) > 0 ? Number(page.page) : nextPage
    state.pageSize = Number(page.page_size) > 0 ? Number(page.page_size) : FOLDER_PAGE_SIZE
    state.loaded = true
  } catch {
    if (
      generation === treeGeneration
      && knowledgeBaseId === props.knowledgeBaseId
      && levels[levelKey(parentId)] === state
      && state.requestId === requestId
    ) {
      state.error = true
    }
  } finally {
    if (
      generation === treeGeneration
      && levels[levelKey(parentId)] === state
      && state.requestId === requestId
    ) {
      state.loading = false
    }
  }
}

function appendLevelRows(
  rows: VisibleRow[],
  parentId: string,
  depth: number,
  ancestors: ReadonlySet<string>,
): void {
  const state = levels[levelKey(parentId)]
  if (!state) return

  for (const folder of state.items) {
    if (ancestors.has(folder.id)) continue

    rows.push({ kind: 'folder', folder, depth })
    if (folder.has_children && expandedIds.has(folder.id)) {
      const nextAncestors = new Set(ancestors)
      nextAncestors.add(folder.id)
      appendLevelRows(rows, folder.id, depth + 1, nextAncestors)
    }
  }

  if (state.loading) {
    rows.push({ kind: 'status', parentId, depth, status: 'loading' })
  } else if (state.error) {
    rows.push({ kind: 'status', parentId, depth, status: 'error' })
  } else if (state.loaded && state.items.length === 0) {
    rows.push({ kind: 'status', parentId, depth, status: 'empty' })
  } else if (state.loaded && state.items.length < state.total) {
    rows.push({ kind: 'status', parentId, depth, status: 'more' })
  }
}

const visibleRows = computed<VisibleRow[]>(() => {
  const rows: VisibleRow[] = []
  appendLevelRows(rows, ROOT_PARENT_ID, 2, new Set())
  return rows
})

function selectAllFiles(): void {
  emit('update:modelValue', undefined)
}

function selectRoot(): void {
  emit('update:modelValue', '')
}

function selectFolder(folderId: string): void {
  emit('update:modelValue', folderId)
}

function toggleRoot(): void {
  if (isRootExpanded.value) {
    expandedIds.delete(ROOT_EXPANDED_KEY)
    return
  }

  expandedIds.add(ROOT_EXPANDED_KEY)
  const rootLevel = levels[levelKey(ROOT_PARENT_ID)]
  if (!rootLevel?.loaded && !rootLevel?.loading) void loadLevel(ROOT_PARENT_ID)
}

function toggleFolder(folder: KnowledgeFolderWithStats): void {
  if (!folder.has_children) return

  if (expandedIds.has(folder.id)) {
    expandedIds.delete(folder.id)
    return
  }

  expandedIds.add(folder.id)
  const childLevel = levels[levelKey(folder.id)]
  if (!childLevel?.loaded && !childLevel?.loading) void loadLevel(folder.id)
}

function loadMore(parentId: string): void {
  void loadLevel(parentId)
}

function findContainingParentId(folderId: string): string | undefined {
  for (const [key, state] of Object.entries(levels)) {
    if (state.items.some(folder => folder.id === folderId)) {
      return key === ROOT_EXPANDED_KEY ? ROOT_PARENT_ID : key
    }
  }
  return undefined
}

function isLevelVisible(parentId: string): boolean {
  return parentId === ROOT_PARENT_ID
    ? isRootExpanded.value
    : expandedIds.has(parentId)
}

function invalidateLevel(parentId: string, reload: boolean): void {
  delete levels[levelKey(parentId)]
  if (reload) void loadLevel(parentId)
}

function refreshTreeTarget(): void {
  const parentId = props.refreshParentId ?? ROOT_PARENT_ID
  if (parentId === ROOT_PARENT_ID) {
    invalidateLevel(ROOT_PARENT_ID, isRootExpanded.value)
    return
  }

  const containingParentId = findContainingParentId(parentId)
  invalidateLevel(parentId, expandedIds.has(parentId))
  if (containingParentId === undefined) {
    resetTree()
    return
  }
  invalidateLevel(containingParentId, isLevelVisible(containingParentId))
}

function mutationKey(folderId: string): string {
  return folderId === ROOT_PARENT_ID ? ROOT_EXPANDED_KEY : folderId
}

function isMutationLoading(folderId: string): boolean {
  return activeMutationKeys.has(mutationKey(folderId))
}

function isOperationCurrent(generation: number, knowledgeBaseId: string): boolean {
  return generation === treeGeneration && knowledgeBaseId === props.knowledgeBaseId
}

function findLoadedFolder(folderId: string): KnowledgeFolderWithStats | undefined {
  for (const state of Object.values(levels)) {
    const folder = state.items.find(item => item.id === folderId)
    if (folder) return folder
  }
  return undefined
}

function replaceLoadedFolder(
  folderId: string,
  replacement: KnowledgeFolder,
): KnowledgeFolderWithStats | undefined {
  let updated: KnowledgeFolderWithStats | undefined
  for (const state of Object.values(levels)) {
    const index = state.items.findIndex(folder => folder.id === folderId)
    if (index < 0) continue
    if (state.loading) {
      state.requestId = ++treeRequestSequence
      state.loading = false
    }
    updated = {
      ...state.items[index],
      ...replacement,
    }
    state.items[index] = updated
  }
  return updated
}

function buildLoadedBreadcrumb(
  folder: KnowledgeFolderWithStats,
): KnowledgeFolderBreadcrumb[] {
  const breadcrumb: KnowledgeFolderBreadcrumb[] = []
  const seen = new Set<string>()
  let current: KnowledgeFolderWithStats | undefined = folder

  while (current && !seen.has(current.id)) {
    seen.add(current.id)
    breadcrumb.unshift({
      id: current.id,
      parent_id: current.parent_id,
      name: current.name,
      depth: current.depth,
    })
    current = current.parent_id
      ? findLoadedFolder(current.parent_id)
      : undefined
  }
  return breadcrumb
}

function readRequestError(error: unknown): { status?: number; message?: string } {
  if (!error || typeof error !== 'object') return {}
  const candidate = error as { status?: unknown; message?: unknown }
  return {
    status: typeof candidate.status === 'number' ? candidate.status : undefined,
    message: typeof candidate.message === 'string' ? candidate.message : undefined,
  }
}

function reportFolderOperationError(
  action: 'create' | 'rename' | 'delete',
  folderId: string,
  error: unknown,
): void {
  const { status, message } = readRequestError(error)
  if (action === 'delete' && status === 409) {
    MessagePlugin.error(t('knowledgeFolder.notEmpty'))
  } else if (status !== undefined && message) {
    MessagePlugin.error(message)
  } else {
    MessagePlugin.error(t(`knowledgeFolder.${action}Failed`))
  }
  emit('folder-operation-error', { action, folderId, error })
}

async function createChildFolder(parentId: string, name: string): Promise<void> {
  if (!props.canEdit || isMutationLoading(parentId)) return

  const generation = treeGeneration
  const knowledgeBaseId = props.knowledgeBaseId
  const key = mutationKey(parentId)
  activeMutationKeys.add(key)
  try {
    const response = await createKnowledgeFolder(knowledgeBaseId, {
      parent_id: parentId,
      name,
      sort_order: 0,
    })
    if (!isOperationCurrent(generation, knowledgeBaseId)) return
    if (!response?.success || !response.data?.id) {
      throw new Error('Invalid knowledge folder create response')
    }
    refreshTreeTargetFor(parentId)
    MessagePlugin.success(t('knowledgeFolder.createSuccess'))
    emit('folder-created', {
      knowledgeBaseId,
      parentId,
      folder: response.data,
    })
  } catch (error) {
    if (isOperationCurrent(generation, knowledgeBaseId)) {
      reportFolderOperationError('create', parentId, error)
    }
  } finally {
    if (isOperationCurrent(generation, knowledgeBaseId)) {
      activeMutationKeys.delete(key)
    }
  }
}

async function renameFolder(folder: KnowledgeFolderWithStats, name: string): Promise<void> {
  if (!props.canEdit || isMutationLoading(folder.id)) return

  const generation = treeGeneration
  const knowledgeBaseId = props.knowledgeBaseId
  const key = mutationKey(folder.id)
  activeMutationKeys.add(key)
  try {
    const response = await updateKnowledgeFolder(knowledgeBaseId, folder.id, { name })
    if (!isOperationCurrent(generation, knowledgeBaseId)) return
    if (!response?.success || !response.data?.id) {
      throw new Error('Invalid knowledge folder update response')
    }
    const updated = replaceLoadedFolder(folder.id, response.data) ?? {
      ...folder,
      ...response.data,
    }
    MessagePlugin.success(t('knowledgeFolder.renameSuccess'))
    emit('folder-renamed', {
      knowledgeBaseId,
      folder: updated,
    })
  } catch (error) {
    if (isOperationCurrent(generation, knowledgeBaseId)) {
      reportFolderOperationError('rename', folder.id, error)
    }
  } finally {
    if (isOperationCurrent(generation, knowledgeBaseId)) {
      activeMutationKeys.delete(key)
    }
  }
}

async function deleteFolder(folder: KnowledgeFolderWithStats): Promise<void> {
  if (!props.canEdit || isMutationLoading(folder.id)) return

  const generation = treeGeneration
  const knowledgeBaseId = props.knowledgeBaseId
  const key = mutationKey(folder.id)
  activeMutationKeys.add(key)
  try {
    await deleteKnowledgeFolder(knowledgeBaseId, folder.id)
    if (!isOperationCurrent(generation, knowledgeBaseId)) return

    expandedIds.delete(folder.id)
    delete levels[levelKey(folder.id)]
    if (props.modelValue === folder.id) {
      emit('update:modelValue', folder.parent_id)
    }
    refreshTreeTargetFor(folder.parent_id)
    MessagePlugin.success(t('knowledgeFolder.deleteSuccess'))
    emit('folder-deleted', {
      knowledgeBaseId,
      folderId: folder.id,
      parentId: folder.parent_id,
    })
  } catch (error) {
    if (isOperationCurrent(generation, knowledgeBaseId)) {
      reportFolderOperationError('delete', folder.id, error)
    }
  } finally {
    if (isOperationCurrent(generation, knowledgeBaseId)) {
      activeMutationKeys.delete(key)
    }
  }
}

function refreshTreeTargetFor(parentId: string): void {
  if (parentId === ROOT_PARENT_ID) {
    invalidateLevel(ROOT_PARENT_ID, isRootExpanded.value)
    return
  }

  const containingParentId = findContainingParentId(parentId)
  invalidateLevel(parentId, expandedIds.has(parentId))
  if (containingParentId !== undefined) {
    invalidateLevel(containingParentId, isLevelVisible(containingParentId))
  }
}

function startFolderChat(folder: KnowledgeFolderWithStats): void {
  emit('start-folder-chat', {
    knowledgeBaseId: props.knowledgeBaseId,
    folderId: folder.id,
    folderName: folder.name,
    breadcrumb: buildLoadedBreadcrumb(folder),
    includeDescendants: true,
  })
}

function startKnowledgeBaseChat(): void {
  emit('start-knowledge-base-chat', {
    knowledgeBaseId: props.knowledgeBaseId,
  })
}

function rowPadding(depth: number): string {
  return `${ROW_GUTTER + (depth - 1) * INDENT_SIZE}px`
}

function clampWidth(nextWidth: number): number {
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, nextWidth))
}

function onResizeMove(event: MouseEvent): void {
  if (!resizing.value) return
  width.value = clampWidth(resizeStartWidth + event.clientX - resizeStartX)
}

function stopResize(): void {
  if (typeof document !== 'undefined') {
    document.removeEventListener('mousemove', onResizeMove)
    document.removeEventListener('mouseup', stopResize)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }
  resizing.value = false
}

function startResize(event: MouseEvent): void {
  if (typeof document === 'undefined') return
  resizing.value = true
  resizeStartX = event.clientX
  resizeStartWidth = width.value
  document.addEventListener('mousemove', onResizeMove)
  document.addEventListener('mouseup', stopResize)
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
}

function resizeWithKeyboard(delta: number): void {
  width.value = clampWidth(width.value + delta)
}

function resetTree(): void {
  treeGeneration += 1
  expandedIds.clear()
  expandedIds.add(ROOT_EXPANDED_KEY)
  activeMutationKeys.clear()
  for (const key of Object.keys(levels)) delete levels[key]
  if (props.knowledgeBaseId) void loadLevel(ROOT_PARENT_ID)
}

watch(() => props.knowledgeBaseId, resetTree, { immediate: true })
watch(() => props.refreshKey, (refreshKey, previousRefreshKey) => {
  if (refreshKey === previousRefreshKey) return
  refreshTreeTarget()
})

onBeforeUnmount(() => {
  treeGeneration += 1
  stopResize()
})
</script>

<template>
  <aside
    class="knowledge-folder-tree"
    :class="{ 'knowledge-folder-tree--resizing': resizing }"
    :style="{ width: `${width}px` }"
    :aria-label="t('knowledgeFolder.title')"
  >
    <header class="knowledge-folder-tree__header">
      <t-icon name="folder" aria-hidden="true" />
      <h2>{{ t('knowledgeFolder.title') }}</h2>
    </header>

    <div
      class="knowledge-folder-tree__scroll"
      role="tree"
      :aria-label="t('knowledgeFolder.title')"
    >
      <div
        class="knowledge-folder-tree__row"
        :class="{ 'knowledge-folder-tree__row--selected': isAllFilesSelected }"
        role="treeitem"
        aria-level="1"
        :aria-selected="isAllFilesSelected"
      >
        <span class="knowledge-folder-tree__chevron-placeholder" aria-hidden="true" />
        <button
          class="knowledge-folder-tree__select"
          type="button"
          @click="selectAllFiles"
        >
          <t-icon name="file" class="knowledge-folder-tree__folder-icon" aria-hidden="true" />
          <span class="knowledge-folder-tree__name">{{ t('knowledgeFolder.allFiles') }}</span>
        </button>
      </div>

      <div
        class="knowledge-folder-tree__row"
        :class="{ 'knowledge-folder-tree__row--selected': isRootSelected }"
        role="treeitem"
        aria-level="1"
        :aria-expanded="isRootExpanded"
        :aria-selected="isRootSelected"
      >
        <button
          class="knowledge-folder-tree__chevron"
          type="button"
          :aria-label="t(isRootExpanded ? 'knowledgeFolder.collapseFolder' : 'knowledgeFolder.expandFolder', {
            name: t('knowledgeFolder.root'),
          })"
          @click="toggleRoot"
        >
          <t-icon :name="isRootExpanded ? 'chevron-down' : 'chevron-right'" aria-hidden="true" />
        </button>
        <button
          class="knowledge-folder-tree__select"
          type="button"
          @click="selectRoot"
        >
          <t-icon
            :name="isRootExpanded ? 'folder-open' : 'folder'"
            class="knowledge-folder-tree__folder-icon"
            aria-hidden="true"
          />
          <span class="knowledge-folder-tree__name">{{ t('knowledgeFolder.root') }}</span>
        </button>
        <KnowledgeFolderActions
          kind="root"
          :can-edit="canEdit"
          :loading="isMutationLoading(ROOT_PARENT_ID)"
          :revealed="isRootSelected"
          @create="(name) => createChildFolder(ROOT_PARENT_ID, name)"
          @start-chat="startKnowledgeBaseChat"
        />
      </div>

      <template v-if="isRootExpanded">
        <div
          v-for="row in visibleRows"
          :key="row.kind === 'folder'
            ? `folder:${row.folder.id}`
            : `status:${row.parentId}:${row.status}`"
          class="knowledge-folder-tree__row"
          :class="{
            'knowledge-folder-tree__row--selected':
              row.kind === 'folder' && modelValue === row.folder.id,
            'knowledge-folder-tree__row--status': row.kind === 'status',
          }"
          :style="{ paddingLeft: rowPadding(row.depth) }"
          :role="row.kind === 'folder'
            ? 'treeitem'
            : row.status === 'error' ? 'alert' : 'status'"
          :aria-level="row.kind === 'folder' ? row.depth : undefined"
          :aria-selected="row.kind === 'folder' ? modelValue === row.folder.id : undefined"
          :aria-expanded="row.kind === 'folder' && row.folder.has_children
            ? expandedIds.has(row.folder.id)
            : undefined"
        >
          <template v-if="row.kind === 'folder'">
            <button
              v-if="row.folder.has_children"
              class="knowledge-folder-tree__chevron"
              type="button"
              :aria-label="t(
                expandedIds.has(row.folder.id)
                  ? 'knowledgeFolder.collapseFolder'
                  : 'knowledgeFolder.expandFolder',
                { name: row.folder.name },
              )"
              @click="toggleFolder(row.folder)"
            >
              <t-icon
                :name="expandedIds.has(row.folder.id) ? 'chevron-down' : 'chevron-right'"
                aria-hidden="true"
              />
            </button>
            <span
              v-else
              class="knowledge-folder-tree__chevron-placeholder"
              aria-hidden="true"
            />
            <button
              class="knowledge-folder-tree__select"
              type="button"
              @click="selectFolder(row.folder.id)"
            >
              <t-icon
                :name="expandedIds.has(row.folder.id) ? 'folder-open' : 'folder'"
                class="knowledge-folder-tree__folder-icon"
                aria-hidden="true"
              />
              <span class="knowledge-folder-tree__name" :title="row.folder.name">
                {{ row.folder.name }}
              </span>
              <span class="knowledge-folder-tree__count">{{ row.folder.knowledge_count }}</span>
            </button>
            <KnowledgeFolderActions
              kind="folder"
              :name="row.folder.name"
              :can-edit="canEdit"
              :loading="isMutationLoading(row.folder.id)"
              :revealed="modelValue === row.folder.id"
              @create="(name) => createChildFolder(row.folder.id, name)"
              @rename="(name) => renameFolder(row.folder, name)"
              @delete="deleteFolder(row.folder)"
              @start-chat="startFolderChat(row.folder)"
            />
          </template>

          <template v-else-if="row.status === 'loading'">
            <t-icon name="loading" class="knowledge-folder-tree__loading" aria-hidden="true" />
            <span>{{ t('knowledgeFolder.loading') }}</span>
          </template>
          <template v-else-if="row.status === 'error'">
            <span>{{ t('knowledgeFolder.loadFailed') }}</span>
            <button
              class="knowledge-folder-tree__inline-action"
              type="button"
              @click="loadMore(row.parentId)"
            >
              {{ t('knowledgeFolder.retry') }}
            </button>
          </template>
          <template v-else-if="row.status === 'empty'">
            <span>{{ t('knowledgeFolder.empty') }}</span>
          </template>
          <template v-else>
            <button
              class="knowledge-folder-tree__load-more"
              type="button"
              @click="loadMore(row.parentId)"
            >
              {{ t('knowledgeFolder.loadMore') }}
            </button>
          </template>
        </div>
      </template>
    </div>

    <div
      class="knowledge-folder-tree__resize-handle"
      :class="{ 'knowledge-folder-tree__resize-handle--active': resizing }"
      role="separator"
      aria-orientation="vertical"
      :aria-label="t('knowledgeFolder.resizeLabel')"
      :aria-valuemin="MIN_WIDTH"
      :aria-valuemax="MAX_WIDTH"
      :aria-valuenow="width"
      tabindex="0"
      @mousedown.prevent="startResize"
      @keydown.left.prevent="resizeWithKeyboard(-16)"
      @keydown.right.prevent="resizeWithKeyboard(16)"
    />
  </aside>
</template>

<style scoped>
.knowledge-folder-tree {
  position: relative;
  display: flex;
  flex: 0 0 auto;
  flex-direction: column;
  min-width: 220px;
  max-width: 480px;
  height: 100%;
  min-height: 0;
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
  border-right: 1px solid var(--td-component-border);
  box-sizing: border-box;
}

.knowledge-folder-tree--resizing {
  user-select: none;
}

.knowledge-folder-tree__header {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 8px;
  height: 48px;
  padding: 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.knowledge-folder-tree__header h2 {
  min-width: 0;
  margin: 0;
  overflow: hidden;
  font-size: 14px;
  font-weight: 600;
  line-height: 22px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.knowledge-folder-tree__scroll {
  flex: 1 1 auto;
  min-height: 0;
  padding: 8px;
  overflow: auto;
}

.knowledge-folder-tree__row {
  display: flex;
  align-items: center;
  min-width: 0;
  height: 34px;
  margin-bottom: 2px;
  padding-right: 6px;
  border-radius: var(--td-radius-default);
  box-sizing: border-box;
  color: var(--td-text-color-secondary);
}

.knowledge-folder-tree__row:hover {
  background: var(--td-bg-color-container-hover);
}

.knowledge-folder-tree__row--selected {
  color: var(--td-brand-color);
  background: var(--td-brand-color-light);
}

.knowledge-folder-tree__row--selected:hover {
  background: var(--td-brand-color-light);
}

.knowledge-folder-tree__row:hover :deep(.knowledge-folder-actions__trigger),
.knowledge-folder-tree__row--selected :deep(.knowledge-folder-actions__trigger) {
  opacity: 1;
}

.knowledge-folder-tree__chevron,
.knowledge-folder-tree__chevron-placeholder {
  display: inline-flex;
  flex: 0 0 24px;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 28px;
}

.knowledge-folder-tree__chevron {
  padding: 0;
  color: inherit;
  cursor: pointer;
  background: transparent;
  border: 0;
  border-radius: var(--td-radius-small);
}

.knowledge-folder-tree__chevron:hover,
.knowledge-folder-tree__chevron:focus-visible {
  background: var(--td-bg-color-container-active);
  outline: none;
}

.knowledge-folder-tree__select {
  display: flex;
  flex: 1 1 auto;
  align-items: center;
  gap: 7px;
  min-width: 0;
  height: 100%;
  padding: 0;
  overflow: hidden;
  color: inherit;
  text-align: left;
  cursor: pointer;
  background: transparent;
  border: 0;
  outline: none;
}

.knowledge-folder-tree__select:focus-visible {
  border-radius: var(--td-radius-small);
  box-shadow: inset 0 0 0 2px var(--td-brand-color-focus);
}

.knowledge-folder-tree__folder-icon {
  flex: 0 0 auto;
  color: var(--td-warning-color);
}

.knowledge-folder-tree__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  font-size: 13px;
  line-height: 20px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.knowledge-folder-tree__count {
  flex: 0 0 auto;
  min-width: 18px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 20px;
  text-align: right;
}

.knowledge-folder-tree__row--status {
  gap: 7px;
  height: auto;
  min-height: 32px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.knowledge-folder-tree__row--status:hover {
  background: transparent;
}

.knowledge-folder-tree__loading {
  animation: knowledge-folder-tree-spin 0.9s linear infinite;
}

.knowledge-folder-tree__inline-action,
.knowledge-folder-tree__load-more {
  padding: 2px 4px;
  color: var(--td-brand-color);
  font-size: 12px;
  cursor: pointer;
  background: transparent;
  border: 0;
  border-radius: var(--td-radius-small);
}

.knowledge-folder-tree__inline-action:hover,
.knowledge-folder-tree__inline-action:focus-visible,
.knowledge-folder-tree__load-more:hover,
.knowledge-folder-tree__load-more:focus-visible {
  background: var(--td-brand-color-light);
  outline: none;
}

.knowledge-folder-tree__load-more {
  margin-left: 24px;
}

.knowledge-folder-tree__resize-handle {
  position: absolute;
  top: 0;
  right: -4px;
  bottom: 0;
  z-index: 2;
  width: 8px;
  cursor: col-resize;
  outline: none;
}

.knowledge-folder-tree__resize-handle::after {
  position: absolute;
  top: 50%;
  left: 3px;
  width: 2px;
  height: 40px;
  background: var(--td-brand-color);
  border-radius: 1px;
  opacity: 0;
  content: "";
  transform: translateY(-50%);
  transition: opacity 0.15s ease;
}

.knowledge-folder-tree__resize-handle:hover::after,
.knowledge-folder-tree__resize-handle:focus-visible::after,
.knowledge-folder-tree__resize-handle--active::after {
  opacity: 1;
}

@keyframes knowledge-folder-tree-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
