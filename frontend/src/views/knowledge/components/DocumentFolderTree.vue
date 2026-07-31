<template>
  <div
    class="document-folder-tree-host"
    :class="{ open }"
    data-testid="document-folder-tree"
  >
    <button
      v-if="open"
      type="button"
      class="document-folder-tree-backdrop"
      :aria-label="t('knowledgeBase.hideFolderTree')"
      @click="closeTree"
    />

    <aside
      v-show="open"
      class="document-folder-tree"
      :aria-label="t('knowledgeBase.folderTreeTitle')"
    >
      <header class="document-folder-tree__header">
        <strong>{{ t('knowledgeBase.folderTreeTitle') }}</strong>
        <button
          type="button"
          class="document-folder-tree__close"
          :aria-label="t('knowledgeBase.hideFolderTree')"
          @click="closeTree"
        >
          <t-icon name="chevron-left" aria-hidden="true" />
        </button>
      </header>

      <div class="document-folder-tree__search">
        <t-input
          v-model.trim="searchQuery"
          size="small"
          clearable
          :placeholder="t('knowledgeBase.folderTreeSearchPlaceholder')"
        >
          <template #prefix-icon>
            <t-icon name="search" aria-hidden="true" />
          </template>
        </t-input>
      </div>

      <nav class="document-folder-tree__body" :aria-label="t('knowledgeBase.folderTreeTitle')">
        <div class="document-folder-tree__shortcuts">
          <button
            type="button"
            class="document-folder-tree__shortcut document-folder-tree__root"
            :class="{ selected: selection.kind === 'root' }"
            @click="selectRoot"
          >
            <t-icon name="folder-open" aria-hidden="true" />
            <span>{{ t('knowledgeBase.rootFolder') }}</span>
          </button>
        </div>

        <div v-if="searchActive" class="document-folder-tree__section-label">
          {{ t('knowledgeBase.folderSearchResultCount', { count: searchTotal }) }}
        </div>
        <div v-else class="document-folder-tree__section-divider" aria-hidden="true" />

        <div class="document-folder-tree__scroll">
          <div v-if="!searchActive" class="document-folder-tree__rows">
            <template v-for="row in visibleRows" :key="row.key">
              <div
                v-if="row.kind === 'folder'"
                class="document-folder-tree__row"
                :class="{ selected: selection.kind === 'folder' && selection.id === row.folder.id }"
                :style="{ paddingLeft: `${8 + row.depth * 16}px` }"
              >
                <button
                  v-if="row.folder.has_children"
                  type="button"
                  class="document-folder-tree__expand"
                  :aria-label="t(expandedIds.has(row.folder.id)
                    ? 'knowledgeBase.collapseFolder'
                    : 'knowledgeBase.expandFolder')"
                  :aria-expanded="expandedIds.has(row.folder.id)"
                  @click.stop="toggleFolder(row.folder)"
                >
                  <t-icon
                    name="chevron-right"
                    :class="{ expanded: expandedIds.has(row.folder.id) }"
                    aria-hidden="true"
                  />
                </button>
                <span v-else class="document-folder-tree__expand-spacer" aria-hidden="true" />

                <button
                  type="button"
                  class="document-folder-tree__label"
                  :title="row.folder.name"
                  @click="selectFolder(row)"
                >
                  <t-icon
                    :name="expandedIds.has(row.folder.id) ? 'folder-open' : 'folder'"
                    aria-hidden="true"
                  />
                  <span>{{ row.folder.name }}</span>
                </button>
              </div>

              <div
                v-else-if="row.kind === 'loading'"
                class="document-folder-tree__feedback"
                :style="{ paddingLeft: `${28 + row.depth * 16}px` }"
                role="status"
              >
                <t-loading size="small" />
                <span>{{ t('common.loading') }}</span>
              </div>

              <button
                v-else-if="row.kind === 'load-more'"
                type="button"
                class="document-folder-tree__load-more"
                :style="{ paddingLeft: `${28 + row.depth * 16}px` }"
                @click="loadMore(row.parentId)"
              >
                {{ t('knowledgeBase.folderLoadMore') }}
              </button>

              <button
                v-else
                type="button"
                class="document-folder-tree__feedback document-folder-tree__feedback--error"
                :style="{ paddingLeft: `${28 + row.depth * 16}px` }"
                @click="loadBranch(row.parentId, { force: true })"
              >
                {{ t('knowledgeBase.retry') }}
              </button>
            </template>
          </div>

          <div v-else class="document-folder-tree__search-results">
            <div v-if="searchLoading && searchResults.length === 0" class="document-folder-tree__search-state">
              <t-loading size="small" />
              <span>{{ t('common.loading') }}</span>
            </div>

            <button
              v-for="result in searchResults"
              :key="result.id"
              type="button"
              class="document-folder-tree__search-result"
              :class="{ selected: selection.kind === 'folder' && selection.id === result.id }"
              @click="selectSearchResult(result)"
            >
              <t-icon name="folder" aria-hidden="true" />
              <span class="document-folder-tree__search-result-main">
                <strong :title="result.name">{{ result.name }}</strong>
                <small :title="result.path">{{ result.path }}</small>
              </span>
            </button>

            <button
              v-if="searchHasMore"
              type="button"
              class="document-folder-tree__search-more"
              :disabled="searchLoading"
              @click="loadSearch(true)"
            >
              <t-loading v-if="searchLoading" size="small" />
              <span v-else>{{ t('knowledgeBase.folderLoadMore') }}</span>
            </button>

            <button
              v-if="searchError && searchResults.length === 0"
              type="button"
              class="document-folder-tree__search-state document-folder-tree__search-state--error"
              @click="loadSearch(false)"
            >
              {{ t('knowledgeBase.retry') }}
            </button>

            <div
              v-else-if="!searchLoading && !searchError && searchResults.length === 0"
              class="document-folder-tree__search-state"
            >
              {{ t('knowledgeBase.folderSearchEmpty') }}
            </div>
          </div>

          <div v-if="!searchActive && rootIsEmpty" class="document-folder-tree__empty">
            {{ t('knowledgeBase.noFolders') }}
          </div>
        </div>
      </nav>
    </aside>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import {
  listDocumentFolders,
  searchDocumentFolders,
  type DocumentFolderNode,
  type DocumentFolderSearchResult,
} from '@/api/knowledge-base';
import {
  createRootDocumentFolderSelection,
  type DocumentFolderBreadcrumb,
  type DocumentFolderSelection,
} from './documentFolderViewTypes';

interface BranchState {
  folders: DocumentFolderNode[];
  loading: boolean;
  loaded: boolean;
  error: string;
  nextCursor: string;
  hasMore: boolean;
}

interface FolderRow {
  kind: 'folder';
  key: string;
  depth: number;
  folder: DocumentFolderNode;
  trail: DocumentFolderBreadcrumb[];
}

interface BranchStatusRow {
  kind: 'loading' | 'load-more' | 'error';
  key: string;
  depth: number;
  parentId: string;
}

type TreeRow = FolderRow | BranchStatusRow;

const props = defineProps<{
  kbId: string;
  open: boolean;
  selection: DocumentFolderSelection;
}>();

const emit = defineEmits<{
  (event: 'update:open', value: boolean): void;
  (event: 'selectFolder', selection: DocumentFolderSelection): void;
}>();

const { t, locale } = useI18n();
const branches = reactive(new Map<string, BranchState>());
const expandedIds = ref(new Set<string>());
const searchQuery = ref('');
const searchResults = ref<DocumentFolderSearchResult[]>([]);
const searchLoading = ref(false);
const searchError = ref('');
const searchHasMore = ref(false);
const searchTotal = ref(0);
const searchOffset = ref(0);
const searchActive = computed(() => searchQuery.value.trim().length > 0);
let treeGeneration = 0;
let searchGeneration = 0;
let searchTimer: ReturnType<typeof setTimeout> | null = null;

function emptyBranch(): BranchState {
  return {
    folders: [],
    loading: false,
    loaded: false,
    error: '',
    nextCursor: '',
    hasMore: false,
  };
}

function branchFor(parentId: string): BranchState {
  if (!branches.has(parentId)) {
    branches.set(parentId, emptyBranch());
  }
  return branches.get(parentId)!;
}

function mergeFolders(
  current: DocumentFolderNode[],
  incoming: DocumentFolderNode[],
): DocumentFolderNode[] {
  const merged = new Map(current.map(folder => [folder.id, folder]));
  incoming.forEach(folder => merged.set(folder.id, folder));
  return [...merged.values()].sort((left, right) => (
    left.name.localeCompare(right.name, locale.value, {
      numeric: true,
      sensitivity: 'base',
    })
  ));
}

async function loadBranch(
  parentId: string,
  options: { append?: boolean; force?: boolean } = {},
): Promise<void> {
  if (!props.kbId) return;
  const branch = branchFor(parentId);
  if (branch.loading) return;
  if (branch.loaded && !options.append && !options.force) return;

  const requestGeneration = treeGeneration;
  const append = options.append === true;
  branch.loading = true;
  branch.error = '';
  try {
    const response = await listDocumentFolders(props.kbId, parentId, {
      cursor: append ? branch.nextCursor : undefined,
      page_size: 50,
    });
    if (requestGeneration !== treeGeneration) return;

    branch.folders = mergeFolders(append ? branch.folders : [], response?.folders || []);
    branch.nextCursor = response?.next_cursor || '';
    branch.hasMore = Boolean(response?.has_more && branch.nextCursor);
    branch.loaded = true;
  } catch (error: unknown) {
    if (requestGeneration !== treeGeneration) return;
    branch.error = (error as { message?: string })?.message || t('knowledgeBase.loadFoldersFailed');
  } finally {
    if (requestGeneration === treeGeneration) branch.loading = false;
  }
}

function clearSearchState() {
  searchGeneration += 1;
  searchResults.value = [];
  searchLoading.value = false;
  searchError.value = '';
  searchHasMore.value = false;
  searchTotal.value = 0;
  searchOffset.value = 0;
}

async function loadSearch(append = false): Promise<void> {
  const keyword = searchQuery.value.trim();
  if (!props.kbId || !keyword) {
    clearSearchState();
    return;
  }
  if (append && (searchLoading.value || !searchHasMore.value)) return;

  const offset = append ? searchOffset.value : 0;
  const requestGeneration = append ? searchGeneration : ++searchGeneration;
  searchLoading.value = true;
  searchError.value = '';
  try {
    const response = await searchDocumentFolders(keyword, offset, 30, {
      kb_ids: [props.kbId],
    });
    if (requestGeneration !== searchGeneration) return;

    const rows = Array.isArray(response?.data) ? response.data : [];
    searchResults.value = append
      ? mergeSearchResults(searchResults.value, rows)
      : rows;
    searchOffset.value = offset + rows.length;
    searchHasMore.value = response?.has_more === true;
    searchTotal.value = typeof response?.total === 'number'
      ? response.total
      : searchResults.value.length;
  } catch (error: unknown) {
    if (requestGeneration !== searchGeneration) return;
    searchError.value = (error as { message?: string })?.message || t('knowledgeBase.loadFoldersFailed');
    if (!append) searchResults.value = [];
  } finally {
    if (requestGeneration === searchGeneration) searchLoading.value = false;
  }
}

function mergeSearchResults(
  current: DocumentFolderSearchResult[],
  incoming: DocumentFolderSearchResult[],
): DocumentFolderSearchResult[] {
  const knownIds = new Set(current.map(result => result.id));
  return [...current, ...incoming.filter(result => !knownIds.has(result.id))];
}

function splitFolderPath(path: string, fallbackName: string): string[] {
  const segments = path
    .split(/[\\/]+/)
    .map(segment => segment.trim())
    .filter(Boolean);
  if (segments[0] === t('knowledgeBase.rootFolder')) segments.shift();
  if (segments[segments.length - 1] !== fallbackName) segments.push(fallbackName);
  return segments;
}

async function findChildFolder(parentId: string, name: string): Promise<DocumentFolderNode | undefined> {
  await loadBranch(parentId);
  const loadedBranch = branchFor(parentId);
  let match = loadedBranch.folders.find(folder => folder.name === name);
  if (match) return match;

  const requestGeneration = treeGeneration;
  try {
    const response = await listDocumentFolders(props.kbId, parentId, {
      keyword: name,
      page_size: 200,
    });
    if (requestGeneration !== treeGeneration) return undefined;
    match = (response?.folders || []).find(folder => folder.name === name);
    if (match) loadedBranch.folders = mergeFolders(loadedBranch.folders, [match]);
    return match;
  } catch {
    return undefined;
  }
}

async function revealPath(path: string[], targetId?: string): Promise<DocumentFolderBreadcrumb[]> {
  const trail: DocumentFolderBreadcrumb[] = [];
  let parentId = '';
  for (let index = 0; index < path.length; index += 1) {
    const folder = await findChildFolder(parentId, path[index]);
    if (!folder) return [];
    if (index === path.length - 1 && targetId && folder.id !== targetId) return [];

    trail.push({ id: folder.id, name: folder.name });
    if (index < path.length - 1) {
      expandedIds.value.add(folder.id);
      parentId = folder.id;
    }
  }
  return trail;
}

function appendVisibleBranch(
  rows: TreeRow[],
  parentId: string,
  depth: number,
  trail: DocumentFolderBreadcrumb[],
) {
  const branch = branches.get(parentId);
  if (!branch) return;

  branch.folders.forEach(folder => {
    const folderTrail = [...trail, { id: folder.id, name: folder.name }];
    rows.push({
      kind: 'folder',
      key: `folder-${folder.id}`,
      depth,
      folder,
      trail: folderTrail,
    });
    if (folder.has_children && expandedIds.value.has(folder.id)) {
      appendVisibleBranch(rows, folder.id, depth + 1, folderTrail);
    }
  });

  if (branch.loading) {
    rows.push({ kind: 'loading', key: `loading-${parentId}`, depth, parentId });
  } else if (branch.error) {
    rows.push({ kind: 'error', key: `error-${parentId}`, depth, parentId });
  } else if (branch.hasMore) {
    rows.push({ kind: 'load-more', key: `more-${parentId}`, depth, parentId });
  }
}

const visibleRows = computed<TreeRow[]>(() => {
  const rows: TreeRow[] = [];
  appendVisibleBranch(rows, '', 0, []);
  return rows;
});

const rootIsEmpty = computed(() => {
  const root = branches.get('');
  return Boolean(root?.loaded && !root.loading && !root.error && root.folders.length === 0);
});

function isCompactLayout(): boolean {
  return typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia('(max-width: 1100px)').matches;
}

function closeTree() {
  emit('update:open', false);
}

function closeCompactTree() {
  if (isCompactLayout()) closeTree();
}

function selectRoot() {
  emit('selectFolder', createRootDocumentFolderSelection(t('knowledgeBase.rootFolder')));
  closeCompactTree();
}

function selectFolder(row: FolderRow) {
  emit('selectFolder', {
    id: row.folder.id,
    kind: 'folder',
    name: row.folder.name,
    path: row.trail.map(crumb => crumb.name),
    trail: row.trail.map(crumb => ({ ...crumb })),
  });
  closeCompactTree();
}

function selectSearchResult(result: DocumentFolderSearchResult) {
  const path = splitFolderPath(result.path, result.name);
  emit('selectFolder', {
    id: result.id,
    kind: 'folder',
    name: result.name,
    path,
    trail: [],
  });
  searchQuery.value = '';
  clearSearchState();
  closeCompactTree();

  void revealPath(path, result.id).then(trail => {
    if (trail[trail.length - 1]?.id !== result.id) return;
    if (props.selection.kind !== 'folder' || props.selection.id !== result.id) return;
    emit('selectFolder', {
      id: result.id,
      kind: 'folder',
      name: result.name,
      path: trail.map(crumb => crumb.name),
      trail,
    });
  });
}

function toggleFolder(folder: DocumentFolderNode) {
  if (!folder.has_children) return;
  if (expandedIds.value.has(folder.id)) {
    expandedIds.value.delete(folder.id);
    return;
  }
  expandedIds.value.add(folder.id);
  void loadBranch(folder.id);
}

function loadMore(parentId: string) {
  const branch = branchFor(parentId);
  if (branch.loading || !branch.hasMore) return;
  void loadBranch(parentId, { append: true });
}

async function revealSelection(selection: DocumentFolderSelection) {
  if (selection.kind !== 'folder') return;
  if (!selection.trail?.length) {
    if (selection.path.length) await revealPath(selection.path, selection.id);
    return;
  }
  await loadBranch('');
  const ancestors = selection.trail.slice(0, -1);
  for (const ancestor of ancestors) {
    expandedIds.value.add(ancestor.id);
    await loadBranch(ancestor.id);
  }
}

async function refreshTree() {
  treeGeneration += 1;
  branches.clear();
  expandedIds.value.clear();
  if (!props.open) return;
  await loadBranch('');
  await revealSelection(props.selection);
}

watch(
  () => props.kbId,
  () => {
    searchQuery.value = '';
    clearSearchState();
    void refreshTree();
  },
  { immediate: true },
);

watch(
  () => props.selection,
  selection => {
    if (props.open) void revealSelection(selection);
  },
  { deep: true },
);

watch(
  () => props.open,
  open => {
    if (open && !branches.get('')?.loaded) void refreshTree();
  },
);

watch(searchQuery, () => {
  if (searchTimer) clearTimeout(searchTimer);
  clearSearchState();
  if (!searchQuery.value.trim()) return;
  searchTimer = setTimeout(() => {
    searchTimer = null;
    void loadSearch(false);
  }, 250);
});

defineExpose({ refreshTree });

onBeforeUnmount(() => {
  if (searchTimer) clearTimeout(searchTimer);
  clearSearchState();
  treeGeneration += 1;
  branches.clear();
});
</script>

<style scoped lang="less">
.document-folder-tree-host {
  width: 0;
  min-width: 0;
  flex: 0 0 0;
  overflow: hidden;
  transition: flex-basis .18s ease, width .18s ease;

  &.open {
    width: 232px;
    flex-basis: 232px;
    overflow: visible;
  }
}

.document-folder-tree-backdrop {
  display: none;
}

.document-folder-tree {
  display: flex;
  width: 232px;
  height: 100%;
  min-height: 0;
  box-sizing: border-box;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
}

.document-folder-tree__header {
  display: flex;
  height: 42px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: space-between;
  padding: 0 8px 0 12px;
  border-bottom: 1px solid var(--td-component-stroke);

  strong {
    font-size: 13px;
    font-weight: 600;
  }
}

.document-folder-tree__search {
  flex: 0 0 auto;
  padding: 8px 8px 0;

  :deep(.t-input) {
    border-color: transparent;
    border-radius: 6px;
    background: var(--td-bg-color-secondarycontainer);
    box-shadow: none;

    &:hover,
    &.t-is-focused {
      border-color: var(--td-component-border);
      background: var(--td-bg-color-container);
      box-shadow: none;
    }
  }
}

.document-folder-tree__close,
.document-folder-tree__expand {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
}

.document-folder-tree__close {
  width: 28px;
  height: 28px;
  border-radius: 6px;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.document-folder-tree__body {
  display: flex;
  min-height: 0;
  flex: 1;
  flex-direction: column;
  overflow: hidden;
  padding-top: 8px;
}

.document-folder-tree__shortcuts {
  flex: 0 0 auto;
  padding: 0 8px;
}

.document-folder-tree__scroll {
  min-height: 0;
  flex: 1;
  overflow: auto;
  padding: 0 8px 8px;
  scrollbar-width: thin;
}

.document-folder-tree__shortcut,
.document-folder-tree__row {
  display: flex;
  width: 100%;
  min-width: 0;
  height: 32px;
  box-sizing: border-box;
  align-items: center;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
}

.document-folder-tree__shortcut {
  gap: 8px;
  padding: 0 8px;
  cursor: pointer;
  font-family: var(--app-font-family);
  font-size: 12px;
  text-align: left;

  &:hover,
  &.selected {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }

  &.selected {
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.document-folder-tree__section-label {
  flex: 0 0 auto;
  padding: 14px 16px 6px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  font-weight: 500;
}

.document-folder-tree__section-divider {
  flex: 0 0 auto;
  height: 1px;
  margin: 8px 12px;
  background: var(--td-component-stroke);
}

.document-folder-tree__root {
  margin-bottom: 2px;
}

.document-folder-tree__row {
  padding-right: 4px;

  &:hover,
  &.selected {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.selected .document-folder-tree__label {
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.document-folder-tree__expand,
.document-folder-tree__expand-spacer {
  width: 20px;
  height: 28px;
}

.document-folder-tree__expand {
  border-radius: 4px;

  &:hover {
    color: var(--td-text-color-primary);
  }

  .t-icon {
    font-size: 12px;
    transition: transform .15s ease;

    &.expanded {
      transform: rotate(90deg);
    }
  }
}

.document-folder-tree__label {
  display: flex;
  min-width: 0;
  height: 100%;
  flex: 1;
  align-items: center;
  gap: 7px;
  padding: 0 4px;
  border: 0;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  font-family: var(--app-font-family);
  font-size: 12px;
  text-align: left;

  .t-icon {
    flex: 0 0 auto;
    color: var(--td-warning-color);
    font-size: 16px;
  }

  span {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.document-folder-tree__feedback,
.document-folder-tree__load-more {
  display: flex;
  width: 100%;
  min-height: 28px;
  box-sizing: border-box;
  align-items: center;
  gap: 6px;
  border: 0;
  background: transparent;
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
  font-size: 11px;
  text-align: left;
}

.document-folder-tree__load-more,
.document-folder-tree__feedback--error {
  cursor: pointer;

  &:hover {
    color: var(--td-brand-color);
  }
}

.document-folder-tree__empty {
  padding: 18px 8px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  text-align: center;
}

.document-folder-tree__search-results {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.document-folder-tree__search-result {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  gap: 8px;
  padding: 7px 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  font-family: var(--app-font-family);
  text-align: left;

  > .t-icon {
    flex: 0 0 auto;
    color: var(--td-warning-color);
    font-size: 16px;
  }

  &:hover,
  &.selected {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.selected strong {
    color: var(--td-brand-color);
  }
}

.document-folder-tree__search-result-main {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
  gap: 2px;

  strong,
  small {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  strong {
    color: var(--td-text-color-primary);
    font-size: 12px;
    font-weight: 500;
  }

  small {
    color: var(--td-text-color-placeholder);
    font-size: 10px;
  }
}

.document-folder-tree__search-state,
.document-folder-tree__search-more {
  display: flex;
  min-height: 32px;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 8px;
  border: 0;
  background: transparent;
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
  font-size: 11px;
}

.document-folder-tree__search-more,
.document-folder-tree__search-state--error {
  cursor: pointer;

  &:hover {
    color: var(--td-brand-color);
  }
}

@media (max-width: 1100px) {
  .document-folder-tree-host,
  .document-folder-tree-host.open {
    position: absolute;
    z-index: 20;
    inset: 0;
    width: auto;
    flex-basis: auto;
    overflow: visible;
    pointer-events: none;
  }

  .document-folder-tree-host.open {
    pointer-events: auto;
  }

  .document-folder-tree-backdrop {
    position: absolute;
    display: block;
    z-index: 0;
    inset: 0;
    padding: 0;
    border: 0;
    background: rgb(0 0 0 / 22%);
    cursor: default;
  }

  .document-folder-tree {
    position: relative;
    z-index: 1;
    width: min(280px, calc(100% - 32px));
    border-radius: 8px;
    box-shadow: 0 12px 32px rgb(0 0 0 / 18%);
  }
}
</style>
