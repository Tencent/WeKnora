<template>
  <div
    class="document-folder-surface"
    :class="[
      `document-folder-surface--${viewMode}`,
      {
        'document-folder-surface--documents-empty': viewMode === 'list'
          && !documentsLoading
          && selectedDocumentCount === 0,
      },
    ]"
  >
  <section
    class="folder-grid-toolbar"
    :aria-label="t('knowledgeBase.currentLocation')"
    data-testid="document-folder-grid-toolbar"
    @mousedown.stop
  >
    <div class="folder-grid-navigation">
      <button
        type="button"
        class="folder-tree-toggle"
        :class="{ active: folderTreeOpen }"
        :aria-label="t(folderTreeOpen
          ? 'knowledgeBase.hideFolderTree'
          : 'knowledgeBase.showFolderTree')"
        :title="t(folderTreeOpen
          ? 'knowledgeBase.hideFolderTree'
          : 'knowledgeBase.showFolderTree')"
        :aria-pressed="folderTreeOpen"
        @click="emit('update:folderTreeOpen', !folderTreeOpen)"
      >
        <t-icon name="folder-open" aria-hidden="true" />
        <span>{{ t('knowledgeBase.folderTreeTitle') }}</span>
      </button>

      <nav class="folder-grid-breadcrumb" :aria-label="t('knowledgeBase.currentLocation')">
        <button
          type="button"
          :class="{ current: selection.kind === 'root' }"
          @click="selectRoot"
        >
          <t-icon name="home" aria-hidden="true" />
          <span>{{ t('knowledgeBase.rootFolder') }}</span>
        </button>

        <template v-if="selection.kind !== 'all'">
          <template v-if="readonlyPath.length">
            <t-icon name="chevron-right" class="folder-grid-breadcrumb__separator" aria-hidden="true" />
            <span class="folder-grid-breadcrumb__readonly" :title="readonlyPath.join(' / ')">
              {{ readonlyPath.join(' / ') }}
            </span>
          </template>
          <template v-for="(crumb, index) in activeTrail" :key="crumb.id">
            <t-icon name="chevron-right" class="folder-grid-breadcrumb__separator" aria-hidden="true" />
            <button
              type="button"
              :class="{ current: index === activeTrail.length - 1 }"
              :title="crumb.name"
              @click="selectBreadcrumb(index)"
            >
              {{ crumb.name }}
            </button>
          </template>
        </template>

        <template v-else>
          <t-icon name="chevron-right" class="folder-grid-breadcrumb__separator" aria-hidden="true" />
          <strong>{{ t('knowledgeBase.allDocuments') }}</strong>
        </template>
      </nav>
    </div>

    <div class="folder-grid-toolbar__actions">
      <span v-if="selection.kind !== 'all'" class="folder-grid-count">
        <t-icon name="folder" aria-hidden="true" />
        {{ t('knowledgeBase.folderListLabel') }} {{ folders.length }}{{ hasMoreFolders ? '+' : '' }}
      </span>
      <span class="folder-grid-count">
        {{ t('knowledgeBase.documentCount', { count: selectedDocumentCount }) }}
      </span>

      <button
        v-if="!readonly && selection.kind !== 'all'"
        type="button"
        class="folder-grid-primary-action"
        :disabled="submitting"
        @click="startCreateFolder"
      >
        <t-icon name="folder-add" aria-hidden="true" />
        {{ t('knowledgeBase.newFolder') }}
      </button>

      <slot name="actions" />
    </div>
  </section>

  <DocumentResourceListHeader
    v-if="viewMode === 'list'"
    :checked="documentsAllSelected"
    :indeterminate="documentsSomeSelected"
    :disabled="selectableDocumentCount === 0"
    @toggle-all="checked => emit('toggleAllDocuments', checked)"
  />

  <template v-if="selection.kind !== 'all'">
    <div
      v-if="creatingFolder"
      class="folder-card folder-card--editing"
      data-testid="folder-create-card"
      @mousedown.stop
    >
      <div class="folder-card__content">
        <div class="folder-card__header">
          <strong>{{ t('knowledgeBase.newFolder') }}</strong>
        </div>
        <FolderNameEditor
          class="folder-card__editor"
          :placeholder="t('knowledgeBase.folderNamePlaceholder')"
          :loading="submitting"
          @submit="createFolder"
          @cancel="creatingFolder = false"
        />
      </div>
      <div class="folder-card__bottom">
        <span>{{ t('knowledgeBase.currentLocation') }}</span>
        <strong>{{ activeDirectory.name }}</strong>
      </div>
    </div>

    <article
      v-for="folder in sortedFolders"
      :key="folder.id"
      class="folder-card"
      :class="{
        'folder-card--editing': renamingFolderId === folder.id,
        'folder-card--actionable': !readonly && renamingFolderId !== folder.id,
        'folder-card--menu-open': actionFolderId === folder.id,
      }"
      :data-testid="`document-folder-${folder.id}`"
      @mousedown.stop
    >
      <template v-if="renamingFolderId === folder.id">
        <div class="folder-card__content" @click.stop>
          <div class="folder-card__header">
            <strong>{{ t('knowledgeBase.renameFolder') }}</strong>
          </div>
          <FolderNameEditor
            class="folder-card__editor"
            :initial-value="folder.name"
            icon="folder"
            :placeholder="t('knowledgeBase.folderNamePlaceholder')"
            :loading="submitting"
            @submit="name => renameFolder(folder, name)"
            @cancel="renamingFolderId = ''"
          />
        </div>
        <div class="folder-card__bottom">
          <span>{{ t('knowledgeBase.folderPanelTitle') }}</span>
          <strong>{{ t('knowledgeBase.rename') }}</strong>
        </div>
      </template>

      <template v-else>
        <button
          type="button"
          :class="viewMode === 'list' ? 'folder-list-row__open' : 'folder-card__open'"
          :aria-label="folder.name"
          @click="openFolder(folder)"
        >
          <template v-if="viewMode === 'list'">
            <span class="folder-list-cell folder-list-cell--check" aria-hidden="true" />
            <span class="folder-list-cell folder-list-cell--name">
              <span class="folder-card__list-icon">
                <t-icon name="folder" aria-hidden="true" />
              </span>
              <strong :title="folder.name">{{ folder.name }}</strong>
            </span>
            <span class="folder-list-cell folder-list-cell--tag">--</span>
            <span class="folder-list-cell folder-list-cell--source">
              <t-icon name="folder" aria-hidden="true" />
              <span>{{ t('knowledgeBase.folderListLabel') }}</span>
            </span>
            <span class="folder-list-cell folder-list-cell--size">
              {{ t('knowledgeBase.folderDirectDocuments', { count: folder.document_count || 0 }) }}
            </span>
            <span class="folder-list-cell folder-list-cell--status">
              {{ folder.has_children ? t('knowledgeBase.folderContainsSubfolders') : '--' }}
            </span>
            <span class="folder-list-cell folder-list-cell--time">
              {{ formatFolderTime(folder.updated_at) }}
            </span>
            <span class="folder-list-cell folder-list-cell--actions" aria-hidden="true" />
          </template>

          <span v-else class="folder-card__content">
            <span class="folder-card__header">
              <strong :title="folder.name">{{ folder.name }}</strong>
            </span>

            <span class="folder-card__body">
              <span class="folder-card__icon"><t-icon name="folder" aria-hidden="true" /></span>
              <span class="folder-card__metadata">
                <strong>{{ t('knowledgeBase.folderDirectDocuments', { count: folder.document_count || 0 }) }}</strong>
                <small v-if="folder.has_children">{{ t('knowledgeBase.folderContainsSubfolders') }}</small>
              </span>
            </span>
          </span>
        </button>

        <t-popup
          v-if="!readonly"
          class="folder-card__action-popup"
          :visible="actionFolderId === folder.id"
          trigger="click"
          placement="bottom-right"
          overlay-class-name="card-more-popup document-folder-browser-action-overlay"
          @visible-change="(open: boolean) => actionFolderId = open ? folder.id : ''"
        >
          <button
            type="button"
            class="folder-card__action"
            :aria-label="t('knowledgeBase.folderActions')"
            @click.stop
          >
            <t-icon name="more" />
          </button>
          <template #content>
            <div class="popup-menu" @click.stop>
              <button type="button" class="popup-menu-item" @click="startRenameFolder(folder)">
                <t-icon name="edit" class="menu-icon" />
                <span>{{ t('knowledgeBase.renameFolder') }}</span>
              </button>
              <button
                type="button"
                class="popup-menu-item delete"
                :aria-label="t('knowledgeBase.deleteFolder')"
                @click="requestDeleteFolder(folder)"
              >
                <t-icon name="delete" class="menu-icon" />
                <span>{{ t('knowledgeBase.deleteFolder') }}</span>
              </button>
            </div>
          </template>
        </t-popup>
      </template>
    </article>

    <button
      v-if="hasMoreFolders"
      type="button"
      class="folder-card folder-card--more"
      :disabled="foldersLoading"
      data-testid="document-folder-load-more"
      @click="loadMoreFolders"
    >
      <template v-if="viewMode === 'list'">
        <span class="folder-list-cell folder-list-cell--check" aria-hidden="true" />
        <span class="folder-list-cell folder-list-cell--name">
          <span class="folder-card__list-icon folder-card__icon--secondary">
            <t-icon name="folder-add" aria-hidden="true" />
          </span>
          <strong>{{ t('knowledgeBase.folderLoadMore') }}</strong>
        </span>
        <span class="folder-list-cell folder-list-cell--tag">--</span>
        <span class="folder-list-cell folder-list-cell--source">
          <t-icon name="folder" aria-hidden="true" />
          <span>{{ t('knowledgeBase.folderListLabel') }}</span>
        </span>
        <span class="folder-list-cell folder-list-cell--size">--</span>
        <span class="folder-list-cell folder-list-cell--status">--</span>
        <span class="folder-list-cell folder-list-cell--time">--</span>
        <span class="folder-list-cell folder-list-cell--actions">
          <t-loading v-if="foldersLoading" size="small" />
        </span>
      </template>

      <template v-else>
      <span class="folder-card__content">
        <span class="folder-card__header">
          <strong>{{ t('knowledgeBase.folderLoadMore') }}</strong>
        </span>
        <span class="folder-card__body">
          <span class="folder-card__icon folder-card__icon--secondary">
            <t-loading v-if="foldersLoading" size="small" />
            <t-icon v-else name="folder-add" aria-hidden="true" />
          </span>
          <span class="folder-card__metadata">
            <strong>{{ t('knowledgeBase.folderBrowseHint') }}</strong>
          </span>
        </span>
      </span>
      <span class="folder-card__bottom">
        <span>{{ t('knowledgeBase.folderLoadMore') }}</span>
        <strong>{{ t('knowledgeBase.folderPanelTitle') }}</strong>
      </span>
      </template>
    </button>

    <div
      v-for="index in foldersLoading && folders.length === 0 ? 3 : 0"
      :key="`folder-skeleton-${index}`"
      class="folder-card folder-card--skeleton"
      aria-hidden="true"
      @mousedown.stop
    />

    <div
      v-if="foldersError && folders.length === 0"
      class="folder-card folder-card--error"
      @mousedown.stop
    >
      <div class="folder-card__content">
        <div class="folder-card__header"><strong>{{ t('knowledgeBase.loadFoldersFailed') }}</strong></div>
        <div class="folder-card__body">
          <span class="folder-card__icon"><t-icon name="error-circle" aria-hidden="true" /></span>
          <span class="folder-card__metadata"><small>{{ foldersError }}</small></span>
        </div>
      </div>
      <button type="button" @click="loadFolders(true)">{{ t('knowledgeBase.retry') }}</button>
    </div>
  </template>

  <div v-if="showEmptyState" class="folder-grid-empty" @mousedown.stop>
    <span class="folder-grid-empty__icon"><t-icon name="folder-open" aria-hidden="true" /></span>
    <strong>
      {{ selection.kind === 'all'
        ? t('knowledgeBase.emptyKnowledgeDragDrop')
        : t('knowledgeBase.folderNoDocumentsTitle') }}
    </strong>
    <small v-if="selection.kind !== 'all'">{{ t('knowledgeBase.folderNoSubfoldersHint') }}</small>
  </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import {
  listDocumentFolders,
  type DocumentFolderNode,
} from '@/api/knowledge-base';
import { useDocumentFolderPages } from '@/composables/useDocumentFolderPages';
import { useDocumentFolderMutations } from '../composables/useDocumentFolderMutations';
import FolderNameEditor from './FolderNameEditor.vue';
import DocumentResourceListHeader from './DocumentResourceListHeader.vue';
import {
  createRootDocumentFolderSelection,
  type DocumentFolderBreadcrumb,
  type DocumentFolderSelection,
} from './documentFolderViewTypes';

const props = withDefaults(defineProps<{
  kbId: string;
  selectedFolderId?: string;
  selection: DocumentFolderSelection;
  searchQuery?: string;
  selectedDocumentCount?: number;
  documentsLoading?: boolean;
  readonly?: boolean;
  viewMode?: 'grid' | 'list';
  documentsAllSelected?: boolean;
  documentsSomeSelected?: boolean;
  selectableDocumentCount?: number;
  folderTreeOpen?: boolean;
}>(), {
  selectedFolderId: undefined,
  searchQuery: '',
  selectedDocumentCount: 0,
  documentsLoading: false,
  readonly: false,
  viewMode: 'grid',
  documentsAllSelected: false,
  documentsSomeSelected: false,
  selectableDocumentCount: 0,
  folderTreeOpen: true,
});

const emit = defineEmits<{
  (event: 'selectFolder', selection: DocumentFolderSelection): void;
  (event: 'folderDeleted'): void;
  (event: 'foldersChanged'): void;
  (event: 'toggleAllDocuments', checked: boolean): void;
  (event: 'update:folderTreeOpen', open: boolean): void;
}>();

const { t, locale } = useI18n();
const creatingFolder = ref(false);
const renamingFolderId = ref('');
const actionFolderId = ref('');
let searchTimer: ReturnType<typeof setTimeout> | null = null;

const {
  items: folders,
  loading: foldersLoading,
  error: foldersError,
  hasMore: hasMoreFolders,
  load: loadFolderPage,
  reset: resetFolderPage,
  clearCache: clearFolderPageCache,
} = useDocumentFolderPages<DocumentFolderNode>({
  knowledgeBaseId: () => props.kbId,
  parentId: () => props.selectedFolderId || '',
  keyword: () => props.searchQuery,
  mapFolder: folder => folder,
  fetchPage: listDocumentFolders,
  pageSize: 12,
  cacheTtlMs: 15_000,
  clearItemsOnError: true,
  errorMessage: error => (
    (error as { message?: string })?.message || t('knowledgeBase.loadFoldersFailed')
  ),
});

const {
  submitting,
  createFolder: mutateCreateFolder,
  renameFolder: mutateRenameFolder,
  confirmDelete,
} = useDocumentFolderMutations({
  knowledgeBaseId: () => props.kbId,
  t,
});

const activeDirectory = computed(() => props.selection);
const activeTrail = computed(() => activeDirectory.value.trail || []);
const readonlyPath = computed(() => activeDirectory.value.path.slice(
  0,
  Math.max(0, activeDirectory.value.path.length - activeTrail.value.length),
));
const sortedFolders = computed(() => [...folders.value].sort((left, right) => (
  left.name.localeCompare(right.name, locale.value, { numeric: true, sensitivity: 'base' })
)));
const showEmptyState = computed(() => (
  !props.documentsLoading
  && !foldersLoading.value
  && !foldersError.value
  && !creatingFolder.value
  && folders.value.length === 0
  && props.selectedDocumentCount === 0
));

function formatFolderTime(value?: string): string {
  if (!value) return '--';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '--';
  const year = String(date.getFullYear()).slice(2);
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  const hour = String(date.getHours()).padStart(2, '0');
  const minute = String(date.getMinutes()).padStart(2, '0');
  return `${year}-${month}-${day} ${hour}:${minute}`;
}

function rootSelection(): DocumentFolderSelection {
  return createRootDocumentFolderSelection(t('knowledgeBase.rootFolder'));
}

function applySelection(selection: DocumentFolderSelection) {
  emit('selectFolder', selection);
}

function selectRoot() {
  applySelection(rootSelection());
}

function selectBreadcrumb(index: number) {
  const trail = activeTrail.value.slice(0, index + 1).map(crumb => ({ ...crumb }));
  const target = trail[trail.length - 1];
  if (!target) return;
  applySelection({
    id: target.id,
    kind: 'folder',
    name: target.name,
    path: [...readonlyPath.value, ...trail.map(crumb => crumb.name)],
    trail,
  });
}

function openFolder(folder: DocumentFolderNode) {
  if (renamingFolderId.value === folder.id || actionFolderId.value === folder.id) return;
  const trail: DocumentFolderBreadcrumb[] = [
    ...activeTrail.value.map(crumb => ({ ...crumb })),
    { id: folder.id, name: folder.name },
  ];
  applySelection({
    id: folder.id,
    kind: 'folder',
    name: folder.name,
    path: [...activeDirectory.value.path, folder.name],
    trail,
  });
}

function loadFolders(force = false, append = false): Promise<void> {
  if (force) clearFolderPageCache();
  if (props.selectedFolderId === undefined) {
    resetFolderPage();
    return Promise.resolve();
  }
  return loadFolderPage({ force, append });
}

function loadMoreFolders() {
  if (foldersLoading.value || !hasMoreFolders.value) return;
  void loadFolders(false, true);
}

function startCreateFolder() {
  if (props.readonly || props.selection.kind === 'all') return;
  creatingFolder.value = true;
  renamingFolderId.value = '';
  actionFolderId.value = '';
}

async function createFolder(name: string) {
  const knowledgeBaseId = props.kbId;
  const parentId = props.selectedFolderId || '';
  await mutateCreateFolder(parentId, name, async () => {
    if (props.kbId !== knowledgeBaseId || (props.selectedFolderId || '') !== parentId) return;
    creatingFolder.value = false;
    await loadFolders(true);
    emit('foldersChanged');
  });
}

function startRenameFolder(folder: DocumentFolderNode) {
  actionFolderId.value = '';
  creatingFolder.value = false;
  renamingFolderId.value = folder.id;
}

async function renameFolder(folder: DocumentFolderNode, name: string) {
  if (name === folder.name) {
    renamingFolderId.value = '';
    return;
  }
  const knowledgeBaseId = props.kbId;
  const parentId = props.selectedFolderId || '';
  await mutateRenameFolder(folder.id, folder.name, name, async () => {
    if (props.kbId !== knowledgeBaseId || (props.selectedFolderId || '') !== parentId) return;
    renamingFolderId.value = '';
    await loadFolders(true);
    emit('foldersChanged');
  });
}

function requestDeleteFolder(folder: DocumentFolderNode) {
  actionFolderId.value = '';
  const knowledgeBaseId = props.kbId;
  const parentId = props.selectedFolderId || '';
  void confirmDelete(folder.id, folder.name, async () => {
    if (props.kbId !== knowledgeBaseId || (props.selectedFolderId || '') !== parentId) return;
    await loadFolders(true);
    emit('foldersChanged');
    emit('folderDeleted');
  });
}

watch(
  () => [props.kbId, props.selectedFolderId],
  () => {
    if (searchTimer) clearTimeout(searchTimer);
    creatingFolder.value = false;
    renamingFolderId.value = '';
    actionFolderId.value = '';
    void loadFolders();
  },
  { immediate: true },
);

watch(
  () => props.searchQuery,
  () => {
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      searchTimer = null;
      void loadFolders();
    }, 250);
  },
);

function refreshFolders() {
  return loadFolders(true);
}

defineExpose({ refreshFolders });

onBeforeUnmount(() => {
  if (searchTimer) clearTimeout(searchTimer);
  resetFolderPage();
});
</script>

<style scoped lang="less">
@import (reference) "./document-card-shell.less";
@import (reference) "./document-resource-list.less";

.document-folder-surface--grid {
  display: contents;
}

.document-folder-surface--list {
  display: contents;
}

.folder-grid-toolbar {
  display: flex;
  min-height: 44px;
  grid-column: 1 / -1;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  box-sizing: border-box;
  padding: 6px 8px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.folder-grid-breadcrumb,
.folder-grid-navigation,
.folder-grid-toolbar__actions,
.folder-grid-count,
.folder-grid-primary-action {
  display: flex;
  align-items: center;
}

.folder-grid-navigation {
  min-width: 0;
  gap: 5px;
}

.folder-tree-toggle {
  display: inline-flex;
  height: 28px;
  box-sizing: border-box;
  flex: 0 0 auto;
  align-items: center;
  gap: 5px;
  padding: 0 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  font-family: var(--app-font-family);
  font-size: 12px;

  &:hover {
    border-color: var(--td-component-border);
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &.active {
    border-color: var(--td-brand-color);
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }
}

.folder-grid-breadcrumb {
  min-width: 0;
  gap: 3px;
  overflow: hidden;
  color: var(--td-text-color-placeholder);
  white-space: nowrap;

  button,
  span,
  strong {
    max-width: 180px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  button {
    height: 28px;
    gap: 4px;
    padding: 0 7px;
    border: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--td-text-color-secondary);
    cursor: pointer;

    &:hover,
    &.current {
      background: var(--td-bg-color-secondarycontainer);
      color: var(--td-brand-color);
    }
  }

  strong {
    color: var(--td-text-color-primary);
    font-size: 12px;
  }
}

.folder-grid-breadcrumb__separator {
  flex: 0 0 auto;
  font-size: 12px;
}

.folder-grid-breadcrumb__readonly {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.folder-grid-toolbar__actions {
  flex: 0 0 auto;
  gap: 8px;
}

.folder-grid-count {
  gap: 4px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  white-space: nowrap;
}

.folder-grid-primary-action {
  height: 30px;
  gap: 5px;
  padding: 0 10px;
  border-radius: 7px;
  cursor: pointer;
  font-size: 12px;
  transition: border-color .2s ease, background-color .2s ease, color .2s ease;
}

.folder-grid-primary-action {
  border: 1px solid var(--td-brand-color);
  background: var(--td-brand-color);
  color: var(--td-text-color-anti);

  &:disabled {
    cursor: not-allowed;
    opacity: .6;
  }
}

.folder-card {
  .document-card-shell();
  padding: 0;
  color: inherit;
  text-align: left;
}

.document-folder-surface--list .folder-card {
  width: 100%;
  min-width: @document-resource-list-min-width;
  height: auto;
  min-height: 60px;
  flex: 0 0 auto;
  border: 1px solid var(--td-component-stroke);
  border-top: 0;
  border-radius: 0;
  box-shadow: none;

  &:hover {
    border-color: var(--td-component-stroke);
    background: var(--td-bg-color-secondarycontainer);
    box-shadow: none;
  }
}

.document-folder-surface--list.document-folder-surface--documents-empty > .folder-card:last-child {
  border-radius: 0 0 8px 8px;
}

.document-folder-surface--list .folder-grid-toolbar {
  margin-bottom: 12px;
}

.folder-list-row__open {
  .document-resource-list-grid();
  width: 100%;
  min-height: 59px;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.folder-list-cell {
  .document-resource-list-cell();
  overflow: hidden;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.folder-list-cell--check {
  justify-content: center;
  padding: 0;
}

.folder-list-cell--name {
  gap: 10px;

  > strong {
    min-width: 0;
    overflow: hidden;
    color: var(--td-text-color-primary);
    font-size: 14px;
    font-weight: 600;
    letter-spacing: .01em;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.folder-list-cell--source {
  gap: 6px;

  > .t-icon {
    flex: 0 0 auto;
    color: var(--td-text-color-secondary);
    font-size: 14px;
  }
}

.folder-list-cell--size,
.folder-list-cell--time,
.folder-list-cell--actions {
  justify-content: flex-end;
}

.folder-list-cell--size,
.folder-list-cell--time {
  font-family: var(--app-font-family);
  font-variant-numeric: tabular-nums;
}

.folder-card__list-icon {
  display: inline-flex;
  width: 28px;
  height: 28px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  background: var(--td-warning-color-light);
  color: var(--td-warning-color);
  font-size: 17px;
}

.document-folder-surface--list .folder-card__action-popup {
  top: 50%;
  right: 20px;
  transform: translateY(-50%);
}

.document-folder-surface--list .folder-card__action {
  width: 28px;
  height: 28px;
  opacity: 0;
  transition: opacity .15s ease, background-color .15s ease, color .15s ease;
}

.document-folder-surface--list .folder-card:hover .folder-card__action,
.document-folder-surface--list .folder-card--menu-open .folder-card__action {
  opacity: 1;
}

.document-folder-surface--list .folder-card--editing {
  display: grid;
  grid-template-columns: 130px minmax(240px, 460px) minmax(120px, 1fr);
  align-items: center;
  gap: 12px;
  padding: 10px 14px;

  &:hover {
    background: var(--td-bg-color-container);
  }
}

.document-folder-surface--list .folder-card--editing .folder-card__content {
  display: contents;
}

.document-folder-surface--list .folder-card--editing .folder-card__header {
  grid-column: 1;
  padding: 0;
}

.document-folder-surface--list .folder-card--editing .folder-card__editor {
  grid-column: 2;
  margin: 0;
}

.document-folder-surface--list .folder-card--editing .folder-card__bottom {
  width: auto;
  height: auto;
  grid-column: 3;
  justify-content: flex-end;
  gap: 6px;
  margin: 0;
  padding: 0;
  border: 0;
}

.document-folder-surface--list .folder-card--more {
  .document-resource-list-grid();
  border-style: solid;
}

.folder-card__content {
  .document-card-content-shell();
}

.folder-card__open {
  display: flex;
  width: 100%;
  min-width: 0;
  height: 100%;
  flex-direction: column;
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.folder-card--actionable .folder-card__open .folder-card__header {
  padding-right: 28px;
}

.folder-card__header {
  display: flex;
  height: 24px;
  flex: 0 0 auto;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;

  > strong {
    min-width: 0;
    flex: 1;
    overflow: hidden;
    color: var(--td-text-color-primary);
    font-size: 14px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  > span {
    flex: 0 0 auto;
    color: var(--td-warning-color);
    font-size: 10px;
  }
}

.folder-card__body {
  display: flex;
  min-height: 0;
  flex: 1;
  align-items: center;
  gap: 10px;
}

.folder-card__metadata {
  display: flex;
  min-width: 0;
  flex-direction: column;

  strong {
    color: var(--td-text-color-secondary);
    font-size: 12px;
    font-weight: 500;
  }

  small {
    margin-top: 3px;
    color: var(--td-text-color-placeholder);
    font-size: 11px;
  }
}

.folder-card__icon {
  display: inline-flex;
  width: 40px;
  height: 40px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 9px;
  background: var(--td-warning-color-light);
  color: var(--td-warning-color);
  font-size: 25px;
}

.folder-card__icon--secondary {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-brand-color);
}

.folder-card__bottom {
  .document-card-bottom-shell();
  color: var(--td-text-color-secondary);
  font-size: 12px;

  strong {
    min-width: 0;
    overflow: hidden;
    color: var(--td-text-color-placeholder);
    font-size: 11px;
    font-weight: 500;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.folder-card__action-popup {
  position: absolute;
  z-index: 1;
  top: 10px;
  right: 10px;
}

.folder-card__action {
  display: inline-flex;
  width: 24px;
  height: 24px;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }
}

.folder-card__editor {
  margin-top: 4px;
}

.folder-card--editing {
  cursor: default;
  border-color: var(--td-brand-color);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-brand-color) 10%, transparent);
}

.folder-card--more {
  border-style: dashed;
}

.folder-card--skeleton {
  cursor: default;
  border-color: transparent;
  background: linear-gradient(
    100deg,
    var(--td-bg-color-secondarycontainer) 25%,
    var(--td-bg-color-container-hover) 37%,
    var(--td-bg-color-secondarycontainer) 63%
  );
  background-size: 400% 100%;
  animation: folderCardSkeleton 1.4s ease infinite;
}

.folder-card--error {
  cursor: default;
  border-color: var(--td-error-color);

  .folder-card__icon {
    background: var(--td-error-color-light);
    color: var(--td-error-color);
  }

  > button {
    height: 32px;
    border: 0;
    border-top: 1px solid var(--td-component-stroke);
    background: transparent;
    color: var(--td-brand-color);
    cursor: pointer;
  }
}

.folder-grid-empty {
  display: flex;
  min-height: 136px;
  grid-column: 1 / -1;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 6px;
  color: var(--td-text-color-secondary);
  text-align: center;

  small {
    max-width: 520px;
    color: var(--td-text-color-placeholder);
  }
}

.document-folder-surface--list .folder-grid-empty {
  min-width: @document-resource-list-min-width;
  box-sizing: border-box;
  border: 1px solid var(--td-component-stroke);
  border-top: 0;
  border-radius: 0 0 8px 8px;
  background: var(--td-bg-color-container);
}

.folder-grid-empty__icon {
  display: inline-flex;
  width: 44px;
  height: 44px;
  align-items: center;
  justify-content: center;
  border-radius: 12px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-placeholder);
  font-size: 24px;
}

.popup-menu {
  min-width: 150px;
  padding: 4px;
}

.popup-menu-item {
  display: flex;
  width: 100%;
  height: 34px;
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-primary);
  cursor: pointer;
  text-align: left;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.delete {
    color: var(--td-error-color);
  }

  &.disabled {
    opacity: .45;
  }
}

@keyframes folderCardSkeleton {
  0% { background-position: 100% 50%; }
  100% { background-position: 0 50%; }
}

@media (max-width: 980px) {
  .folder-grid-toolbar {
    align-items: flex-start;
    flex-direction: column;
    gap: 5px;
  }

  .folder-grid-toolbar__actions {
    width: 100%;
    flex-wrap: wrap;
  }

  .folder-grid-navigation {
    width: 100%;
  }
}
</style>
