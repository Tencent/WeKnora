<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { listKnowledgeFolders } from '@/api/knowledge-folder'
import type { FolderScopeSelection } from '@/types/knowledgeScope'
import type { KnowledgeFolderWithStats } from '@/types/knowledgeFolder'
import {
  folderScopeSelectionKey,
  normalizeFolderScopeSelections,
} from '@/utils/knowledgeScope'
import {
  confirmKnowledgeScopeDraft,
  createKnowledgeScopeDraft,
} from '@/utils/knowledgeScopeSelection'

interface KnowledgeScopeKnowledgeBase {
  id: string
  name: string
  kbType?: 'document' | 'faq'
  count?: number
}

interface FolderNode {
  folder: KnowledgeFolderWithStats
  ancestorFolderIds: string[]
  folderPath: string[]
  depth: number
}

interface FolderLevelState {
  items: FolderNode[]
  total: number
  page: number
  loaded: boolean
  loading: boolean
  error: boolean
  requestId: number
}

interface FolderStatusRow {
  kind: 'status'
  knowledgeBaseId: string
  parentId: string
  ancestorFolderIds: string[]
  folderPath: string[]
  depth: number
  status: 'loading' | 'error' | 'empty' | 'more'
}

interface FolderItemRow extends FolderNode {
  kind: 'folder'
  knowledgeBaseId: string
}

type FolderRow = FolderItemRow | FolderStatusRow

const props = defineProps<{
  knowledgeBases: KnowledgeScopeKnowledgeBase[]
  selectedKnowledgeBaseIds: string[]
  selectedFolders: FolderScopeSelection[]
}>()

const emit = defineEmits<{
  (
    event: 'confirm',
    value: {
      knowledgeBaseIds: string[]
      folders: FolderScopeSelection[]
    },
  ): void
  (event: 'cancel'): void
}>()

const { t } = useI18n()

const ROOT_PARENT_ID = ''
const PAGE_SIZE = 100

const sectionRef = ref<HTMLElement | null>(null)
const initialDraft = createKnowledgeScopeDraft(
  props.selectedKnowledgeBaseIds,
  props.selectedFolders,
)
const draftKnowledgeBaseIds = ref(initialDraft.knowledgeBaseIds)
const draftFolders = ref(initialDraft.folders)
const expandedIds = reactive(new Set<string>())
const levels = reactive<Record<string, FolderLevelState>>({})
let componentGeneration = 0
let requestSequence = 0

function levelKey(knowledgeBaseId: string, parentId: string): string {
  return `${knowledgeBaseId}:${parentId || '__root__'}`
}

function expandedKey(knowledgeBaseId: string, folderId: string): string {
  return `${knowledgeBaseId}:${folderId || '__root__'}`
}

function createLevel(): FolderLevelState {
  return {
    items: [],
    total: 0,
    page: 0,
    loaded: false,
    loading: false,
    error: false,
    requestId: 0,
  }
}

function getOrCreateLevel(knowledgeBaseId: string, parentId: string): FolderLevelState {
  const key = levelKey(knowledgeBaseId, parentId)
  if (!levels[key]) levels[key] = createLevel()
  return levels[key]
}

function mergeNodes(current: FolderNode[], incoming: FolderNode[]): FolderNode[] {
  const byId = new Map(current.map(node => [node.folder.id, node]))
  for (const node of incoming) byId.set(node.folder.id, node)
  return [...byId.values()]
}

async function loadLevel(
  knowledgeBaseId: string,
  parentId: string,
  ancestorFolderIds: string[],
  folderPath: string[],
): Promise<void> {
  const state = getOrCreateLevel(knowledgeBaseId, parentId)
  if (state.loading) return

  const generation = componentGeneration
  const requestId = ++requestSequence
  const nextPage = state.page + 1
  state.loading = true
  state.error = false
  state.requestId = requestId

  try {
    const response = await listKnowledgeFolders(knowledgeBaseId, {
      parent_id: parentId,
      page: nextPage,
      page_size: PAGE_SIZE,
    })
    if (generation !== componentGeneration || state.requestId !== requestId) return
    if (!response?.success || !Array.isArray(response.data?.data)) {
      throw new Error('Invalid knowledge folder list response')
    }

    const incoming = response.data.data.map(folder => ({
      folder,
      ancestorFolderIds: [...ancestorFolderIds],
      folderPath: [...folderPath, folder.name],
      depth: ancestorFolderIds.length + 1,
    }))
    state.items = nextPage === 1
      ? mergeNodes([], incoming)
      : mergeNodes(state.items, incoming)
    state.total = Math.max(Number(response.data.total) || 0, state.items.length)
    state.page = Number(response.data.page) > 0 ? Number(response.data.page) : nextPage
    state.loaded = true
  } catch {
    if (generation === componentGeneration && state.requestId === requestId) {
      state.error = true
    }
  } finally {
    if (generation === componentGeneration && state.requestId === requestId) {
      state.loading = false
    }
  }
}

function appendRows(
  rows: FolderRow[],
  knowledgeBaseId: string,
  parentId: string,
  ancestorFolderIds: string[],
  folderPath: string[],
): void {
  const state = levels[levelKey(knowledgeBaseId, parentId)]
  if (!state) return

  for (const node of state.items) {
    rows.push({ kind: 'folder', knowledgeBaseId, ...node })
    if (node.folder.has_children && expandedIds.has(expandedKey(
      knowledgeBaseId,
      node.folder.id,
    ))) {
      appendRows(
        rows,
        knowledgeBaseId,
        node.folder.id,
        [...node.ancestorFolderIds, node.folder.id],
        node.folderPath,
      )
    }
  }

  let status: FolderStatusRow['status'] | undefined
  if (state.loading) status = 'loading'
  else if (state.error) status = 'error'
  else if (state.loaded && state.items.length === 0) status = 'empty'
  else if (state.loaded && state.items.length < state.total) status = 'more'
  if (status) {
    rows.push({
      kind: 'status',
      knowledgeBaseId,
      parentId,
      ancestorFolderIds,
      folderPath,
      depth: ancestorFolderIds.length + 1,
      status,
    })
  }
}

function folderRows(knowledgeBaseId: string): FolderRow[] {
  if (!expandedIds.has(expandedKey(knowledgeBaseId, ROOT_PARENT_ID))) return []
  const rows: FolderRow[] = []
  appendRows(rows, knowledgeBaseId, ROOT_PARENT_ID, [], [])
  return rows
}

const selectedCount = computed(() => (
  draftKnowledgeBaseIds.value.length + draftFolders.value.length
))

function isKnowledgeBaseSelected(knowledgeBaseId: string): boolean {
  return draftKnowledgeBaseIds.value.includes(knowledgeBaseId)
}

function toggleKnowledgeBase(knowledgeBaseId: string): void {
  if (isKnowledgeBaseSelected(knowledgeBaseId)) {
    draftKnowledgeBaseIds.value = draftKnowledgeBaseIds.value.filter(
      id => id !== knowledgeBaseId,
    )
    return
  }
  draftKnowledgeBaseIds.value = [...draftKnowledgeBaseIds.value, knowledgeBaseId]
  draftFolders.value = normalizeFolderScopeSelections(
    draftFolders.value,
    draftKnowledgeBaseIds.value,
  )
}

function isFolderSelected(knowledgeBaseId: string, folderId: string): boolean {
  const key = folderScopeSelectionKey({ knowledgeBaseId, folderId })
  return draftFolders.value.some(selection => folderScopeSelectionKey(selection) === key)
}

function isFolderCoveredByAncestor(
  knowledgeBaseId: string,
  ancestorFolderIds: readonly string[],
): boolean {
  return ancestorFolderIds.some(folderId => (
    isFolderSelected(knowledgeBaseId, folderId)
  ))
}

function toggleFolderSelection(
  knowledgeBase: KnowledgeScopeKnowledgeBase,
  node: FolderItemRow,
): void {
  if (
    isKnowledgeBaseSelected(knowledgeBase.id)
    || isFolderCoveredByAncestor(knowledgeBase.id, node.ancestorFolderIds)
  ) return

  const key = folderScopeSelectionKey({
    knowledgeBaseId: knowledgeBase.id,
    folderId: node.folder.id,
  })
  if (isFolderSelected(knowledgeBase.id, node.folder.id)) {
    draftFolders.value = draftFolders.value.filter(
      selection => folderScopeSelectionKey(selection) !== key,
    )
    return
  }

  draftFolders.value = normalizeFolderScopeSelections([
    ...draftFolders.value,
    {
      knowledgeBaseId: knowledgeBase.id,
      knowledgeBaseName: knowledgeBase.name,
      folderId: node.folder.id,
      folderName: node.folder.name,
      folderPath: node.folderPath,
      ancestorFolderIds: node.ancestorFolderIds,
      includeDescendants: true,
    },
  ], draftKnowledgeBaseIds.value)
}

function toggleKnowledgeBaseExpanded(knowledgeBase: KnowledgeScopeKnowledgeBase): void {
  if (knowledgeBase.kbType === 'faq') return
  const key = expandedKey(knowledgeBase.id, ROOT_PARENT_ID)
  if (expandedIds.has(key)) {
    expandedIds.delete(key)
    return
  }
  expandedIds.add(key)
  const state = levels[levelKey(knowledgeBase.id, ROOT_PARENT_ID)]
  if (!state?.loaded && !state?.loading) {
    void loadLevel(knowledgeBase.id, ROOT_PARENT_ID, [], [])
  }
}

function toggleFolderExpanded(knowledgeBaseId: string, node: FolderItemRow): void {
  if (!node.folder.has_children) return
  const key = expandedKey(knowledgeBaseId, node.folder.id)
  if (expandedIds.has(key)) {
    expandedIds.delete(key)
    return
  }
  expandedIds.add(key)
  const state = levels[levelKey(knowledgeBaseId, node.folder.id)]
  if (!state?.loaded && !state?.loading) {
    void loadLevel(
      knowledgeBaseId,
      node.folder.id,
      [...node.ancestorFolderIds, node.folder.id],
      node.folderPath,
    )
  }
}

function retryOrLoadMore(row: FolderStatusRow): void {
  void loadLevel(
    row.knowledgeBaseId,
    row.parentId,
    row.ancestorFolderIds,
    row.folderPath,
  )
}

function clearSelection(): void {
  draftKnowledgeBaseIds.value = []
  draftFolders.value = []
}

function confirmSelection(): void {
  emit('confirm', confirmKnowledgeScopeDraft({
    knowledgeBaseIds: draftKnowledgeBaseIds.value,
    folders: draftFolders.value,
  }))
}

function focusFirstControl(): void {
  sectionRef.value
    ?.querySelector<HTMLElement>(
      'button:not(:disabled), input:not(:disabled), [tabindex]:not([tabindex="-1"])',
    )
    ?.focus()
}

defineExpose({ focusFirstControl })

onBeforeUnmount(() => {
  componentGeneration += 1
})
</script>

<template>
  <section
    ref="sectionRef"
    class="knowledge-scope-selector"
    @keydown.esc.stop.prevent="emit('cancel')"
  >
    <div class="knowledge-scope-selector__tree" role="tree">
      <div
        v-for="knowledgeBase in knowledgeBases"
        :key="knowledgeBase.id"
        class="knowledge-scope-selector__knowledge-base"
      >
        <div class="knowledge-scope-selector__row knowledge-scope-selector__row--kb">
          <button
            v-if="knowledgeBase.kbType !== 'faq'"
            type="button"
            class="knowledge-scope-selector__expand"
            :aria-label="t(
              expandedIds.has(expandedKey(knowledgeBase.id, ROOT_PARENT_ID))
                ? 'knowledgeFolder.collapseFolder'
                : 'knowledgeFolder.expandFolder',
              { name: knowledgeBase.name },
            )"
            @click.stop="toggleKnowledgeBaseExpanded(knowledgeBase)"
          >
            <t-icon
              :name="expandedIds.has(expandedKey(knowledgeBase.id, ROOT_PARENT_ID))
                ? 'chevron-down'
                : 'chevron-right'"
            />
          </button>
          <span v-else class="knowledge-scope-selector__expand-placeholder" />
          <t-checkbox
            :checked="isKnowledgeBaseSelected(knowledgeBase.id)"
            @click.stop
            @change="toggleKnowledgeBase(knowledgeBase.id)"
          />
          <button
            type="button"
            class="knowledge-scope-selector__select"
            @click="toggleKnowledgeBase(knowledgeBase.id)"
          >
            <t-icon
              :name="knowledgeBase.kbType === 'faq' ? 'chat-bubble-help' : 'folder'"
              class="knowledge-scope-selector__icon"
            />
            <span class="knowledge-scope-selector__name" :title="knowledgeBase.name">
              {{ knowledgeBase.name }}
            </span>
            <span class="knowledge-scope-selector__count">
              {{ knowledgeBase.count || 0 }}
            </span>
          </button>
        </div>

        <template
          v-if="expandedIds.has(expandedKey(knowledgeBase.id, ROOT_PARENT_ID))"
        >
          <div
            v-for="row in folderRows(knowledgeBase.id)"
            :key="row.kind === 'folder'
              ? `${knowledgeBase.id}:${row.folder.id}`
              : `${knowledgeBase.id}:${row.parentId}:${row.status}`"
            class="knowledge-scope-selector__row"
            :class="{ 'knowledge-scope-selector__row--status': row.kind === 'status' }"
            :style="{ paddingLeft: `${10 + row.depth * 18}px` }"
            :role="row.kind === 'folder' ? 'treeitem' : undefined"
            :aria-level="row.kind === 'folder' ? row.depth : undefined"
            :aria-expanded="(
              row.kind === 'folder' && row.folder.has_children
                ? expandedIds.has(expandedKey(knowledgeBase.id, row.folder.id))
                : undefined
            )"
          >
            <template v-if="row.kind === 'folder'">
              <button
                v-if="row.folder.has_children"
                type="button"
                class="knowledge-scope-selector__expand"
                :aria-label="t(
                  expandedIds.has(expandedKey(knowledgeBase.id, row.folder.id))
                    ? 'knowledgeFolder.collapseFolder'
                    : 'knowledgeFolder.expandFolder',
                  { name: row.folder.name },
                )"
                @click.stop="toggleFolderExpanded(knowledgeBase.id, row)"
              >
                <t-icon
                  :name="expandedIds.has(expandedKey(knowledgeBase.id, row.folder.id))
                    ? 'chevron-down'
                    : 'chevron-right'"
                />
              </button>
              <span v-else class="knowledge-scope-selector__expand-placeholder" />
              <t-checkbox
                :checked="(
                  isFolderSelected(knowledgeBase.id, row.folder.id)
                  || isFolderCoveredByAncestor(knowledgeBase.id, row.ancestorFolderIds)
                )"
                :disabled="(
                  isKnowledgeBaseSelected(knowledgeBase.id)
                  || isFolderCoveredByAncestor(knowledgeBase.id, row.ancestorFolderIds)
                )"
                @click.stop
                @change="toggleFolderSelection(knowledgeBase, row)"
              />
              <button
                type="button"
                class="knowledge-scope-selector__select"
                :disabled="(
                  isKnowledgeBaseSelected(knowledgeBase.id)
                  || isFolderCoveredByAncestor(knowledgeBase.id, row.ancestorFolderIds)
                )"
                @click="toggleFolderSelection(knowledgeBase, row)"
              >
                <t-icon
                  :name="expandedIds.has(expandedKey(knowledgeBase.id, row.folder.id))
                    ? 'folder-open'
                    : 'folder'"
                  class="knowledge-scope-selector__icon"
                />
                <span
                  class="knowledge-scope-selector__name"
                  :title="`${knowledgeBase.name} / ${row.folderPath.join(' / ')}`"
                >
                  {{ row.folder.name }}
                </span>
                <span class="knowledge-scope-selector__count">
                  {{ row.folder.knowledge_count }}
                </span>
              </button>
            </template>

            <template v-else-if="row.status === 'loading'">
              <t-loading size="small" />
              <span>{{ t('knowledgeFolder.loading') }}</span>
            </template>
            <template v-else-if="row.status === 'error'">
              <span>{{ t('knowledgeFolder.loadFailed') }}</span>
              <button
                type="button"
                class="knowledge-scope-selector__inline-action"
                @click="retryOrLoadMore(row)"
              >
                {{ t('knowledgeFolder.retry') }}
              </button>
            </template>
            <template v-else-if="row.status === 'empty'">
              <span>{{ t('knowledgeFolder.empty') }}</span>
            </template>
            <button
              v-else
              type="button"
              class="knowledge-scope-selector__inline-action"
              @click="retryOrLoadMore(row)"
            >
              {{ t('knowledgeFolder.loadMore') }}
            </button>
          </div>
        </template>
      </div>

      <div
        v-if="knowledgeBases.length === 0"
        class="knowledge-scope-selector__empty"
      >
        {{ t('common.noResult') }}
      </div>
    </div>

    <footer class="knowledge-scope-selector__footer">
      <span class="knowledge-scope-selector__selected-count">
        {{ t('knowledgeScope.selectedCount', { count: selectedCount }) }}
      </span>
      <button
        type="button"
        class="knowledge-scope-selector__clear"
        :disabled="selectedCount === 0"
        @click="clearSelection"
      >
        {{ t('common.clear') }}
      </button>
      <span class="knowledge-scope-selector__footer-spacer" />
      <t-button variant="outline" size="small" @click="emit('cancel')">
        {{ t('common.cancel') }}
      </t-button>
      <t-button theme="primary" size="small" @click="confirmSelection">
        {{ t('common.confirm') }}
      </t-button>
    </footer>
  </section>
</template>

<style scoped>
.knowledge-scope-selector {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  width: 420px;
  max-width: calc(100vw - 32px);
  height: 440px;
  min-height: 0;
  max-height: min(60vh, 520px);
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
}

.knowledge-scope-selector__tree {
  flex: 1 1 auto;
  min-height: 0;
  padding: 8px;
  overflow: auto;
}

.knowledge-scope-selector__row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  height: 34px;
  padding-right: 8px;
  border-radius: var(--td-radius-default);
  color: var(--td-text-color-secondary);
}

.knowledge-scope-selector__row:hover {
  background: var(--td-bg-color-container-hover);
}

.knowledge-scope-selector__row--kb {
  color: var(--td-text-color-primary);
  font-weight: 500;
}

.knowledge-scope-selector__row--status {
  gap: 8px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.knowledge-scope-selector__row--status:hover {
  background: transparent;
}

.knowledge-scope-selector__expand,
.knowledge-scope-selector__expand-placeholder {
  display: inline-flex;
  flex: 0 0 24px;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 28px;
}

.knowledge-scope-selector__expand {
  padding: 0;
  border: 0;
  border-radius: var(--td-radius-small);
  background: transparent;
  color: inherit;
  cursor: pointer;
}

.knowledge-scope-selector__expand:hover {
  background: var(--td-bg-color-container-active);
}

.knowledge-scope-selector__select {
  display: flex;
  flex: 1 1 auto;
  align-items: center;
  gap: 7px;
  min-width: 0;
  height: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.knowledge-scope-selector__select:disabled {
  cursor: default;
}

.knowledge-scope-selector__icon {
  flex: 0 0 auto;
  color: var(--td-text-color-secondary);
}

.knowledge-scope-selector__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.knowledge-scope-selector__count {
  flex: 0 0 auto;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.knowledge-scope-selector__inline-action,
.knowledge-scope-selector__clear {
  padding: 2px 5px;
  border: 0;
  border-radius: var(--td-radius-small);
  background: transparent;
  color: var(--td-brand-color);
  cursor: pointer;
}

.knowledge-scope-selector__clear:disabled {
  color: var(--td-text-color-disabled);
  cursor: default;
}

.knowledge-scope-selector__empty {
  padding: 36px 12px;
  color: var(--td-text-color-placeholder);
  text-align: center;
}

.knowledge-scope-selector__footer {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 8px;
  min-height: 52px;
  padding: 8px 12px;
  border-top: 1px solid var(--td-component-stroke);
}

.knowledge-scope-selector__selected-count {
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.knowledge-scope-selector__footer-spacer {
  flex: 1 1 auto;
}
</style>
