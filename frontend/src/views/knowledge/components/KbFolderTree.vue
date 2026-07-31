<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  listKnowledgeFolders,
  createKnowledgeFolder,
  updateKnowledgeFolder,
  deleteKnowledgeFolder,
  organizeKnowledgeFoldersByPath,
  FOLDER_FILTER_ROOT,
  type KnowledgeFolderNode,
} from '@/api/knowledge-base';
import FolderTreePicker from './FolderTreePicker.vue';

const props = defineProps<{
  kbId: string;
  modelValue: string;
  canEdit: boolean;
  filtersActive?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', folderId: string): void;
  (e: 'changed'): void;
}>();

const { t } = useI18n();
const router = useRouter();

const allFolders = ref<KnowledgeFolderNode[]>([]);
const loading = ref(false);
const collapsed = ref<Set<string>>(new Set());
const searchQuery = ref('');

const editingId = ref<string | null>(null);
const editingName = ref('');
const creatingUnderParent = ref<string | null>(null);
const creatingName = ref('');
const newFolderInputRef = ref<{ focus?: () => void } | null>(null);
const renameInputRef = ref<{ focus?: () => void } | null>(null);
const menuOpenId = ref<string | null>(null);

const deleteDialogVisible = ref(false);
const deleteTarget = ref<KnowledgeFolderNode | null>(null);
const deletePromote = ref(false);
const deleteSubmitting = ref(false);
const movePickerVisible = ref(false);
const moveTarget = ref<KnowledgeFolderNode | null>(null);
const organizing = ref(false);

const DROP_ROOT_ZONE = '__rootzone__';
const draggedFolder = ref<{ id: string; path: string } | null>(null);
const dropTargetId = ref<string | null>(null); // null | root sentinel | folder id
const pendingMove = ref<{
  folder: KnowledgeFolderNode;
  targetId: string; // FOLDER_FILTER_ROOT | folder id
  targetLabel: string;
  x: number;
  y: number;
} | null>(null);

const folderById = computed(() =>
  new Map(allFolders.value.map((folder) => [folder.id, folder])),
);

const childrenByParent = computed<Map<string, KnowledgeFolderNode[]>>(() => {
  const map = new Map<string, KnowledgeFolderNode[]>();
  for (const f of allFolders.value) {
    const key = f.parent_id || '';
    if (!map.has(key)) map.set(key, []);
    map.get(key)!.push(f);
  }
  return map;
});

const matchesSearch = (f: KnowledgeFolderNode): boolean => {
  const q = searchQuery.value.trim().toLowerCase();
  if (!q) return true;
  return f.name.toLowerCase().includes(q);
};

interface TreeRow {
  folder: KnowledgeFolderNode;
  depth: number;
  hasChildren: boolean;
  isCollapsed: boolean;
}

const treeRows = computed<TreeRow[]>(() => {
  const q = searchQuery.value.trim().toLowerCase();
  const searching = q.length > 0;

  let effectiveCollapsed = collapsed.value;
  if (searching) {
    const forceOpen = new Set<string>();
    for (const f of allFolders.value) {
      if (matchesSearch(f)) {
        let cur: KnowledgeFolderNode | undefined = f;
        while (cur && cur.parent_id) {
          forceOpen.add(cur.parent_id);
          cur = folderById.value.get(cur.parent_id);
        }
      }
    }
    effectiveCollapsed = new Set(
      [...collapsed.value].filter((id) => !forceOpen.has(id)),
    );
  }

  const rows: TreeRow[] = [];
  const walk = (parentId: string, depth: number) => {
    const kids = childrenByParent.value.get(parentId) || [];
    for (const folder of kids) {
      if (searching && !subtreeMatches(folder.id)) continue;
      const hasChildren = childrenByParent.value.has(folder.id);
      rows.push({
        folder,
        depth,
        hasChildren,
        isCollapsed: effectiveCollapsed.has(folder.id),
      });
      if (hasChildren && !effectiveCollapsed.has(folder.id)) {
        walk(folder.id, depth + 1);
      }
    }
  };
  walk('', 0);
  return rows;
});

function subtreeMatches(folderId: string): boolean {
  const list = childrenByParent.value.get(folderId) || [];
  for (const child of list) {
    if (matchesSearch(child) || subtreeMatches(child.id)) return true;
  }
  return false;
}

async function reloadTree() {
  if (!props.kbId) {
    allFolders.value = [];
    return;
  }
  loading.value = true;
  try {
    const res: any = await listKnowledgeFolders(props.kbId, { all: true });
    allFolders.value = (res?.data || []) as KnowledgeFolderNode[];
  } catch (e) {
    console.error('Failed to load folder tree', e);
    allFolders.value = [];
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.kbId,
  () => {
    collapsed.value = new Set();
    searchQuery.value = '';
    if (props.modelValue !== '') emit('update:modelValue', '');
    reloadTree();
  },
  { immediate: true },
);

watch(
  () => props.modelValue,
  (id) => {
    if (!id || id === FOLDER_FILTER_ROOT) return;
    let cur = folderById.value.get(id);
    const next = new Set(collapsed.value);
    while (cur?.parent_id) {
      next.delete(cur.parent_id);
      cur = folderById.value.get(cur.parent_id);
    }
    collapsed.value = next;
  },
);

function selectRow(id: string) {
  if (editingId.value || creatingUnderParent.value !== null) return;
  emit('update:modelValue', id === props.modelValue ? '' : id);
}

function toggleCollapse(id: string) {
  const next = new Set(collapsed.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  collapsed.value = next;
}

// --- Inline create ---
function startCreate(parentId: string) {
  creatingUnderParent.value = parentId;
  creatingName.value = '';
  menuOpenId.value = null;
  nextTick(() => newFolderInputRef.value?.focus?.());
}

function cancelCreate() {
  creatingUnderParent.value = null;
  creatingName.value = '';
}

async function submitCreate() {
  const name = creatingName.value.trim();
  if (!name) return;
  try {
    await createKnowledgeFolder(props.kbId, {
      name,
      parent_id: creatingUnderParent.value || undefined,
    });
    MessagePlugin.success(t('knowledgeBase.folder.created'));
    cancelCreate();
    await reloadTree();
    emit('changed');
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('common.operationFailed'));
  }
}

// --- Inline rename ---
function startRename(folder: KnowledgeFolderNode) {
  editingId.value = folder.id;
  editingName.value = folder.name;
  menuOpenId.value = null;
  nextTick(() => renameInputRef.value?.focus?.());
}

function cancelRename() {
  editingId.value = null;
  editingName.value = '';
}

async function commitRename(folder: KnowledgeFolderNode) {
  const name = editingName.value.trim();
  if (!name || name === folder.name) {
    cancelRename();
    return;
  }
  try {
    await updateKnowledgeFolder(props.kbId, folder.id, { name });
    MessagePlugin.success(t('knowledgeBase.folder.renamed'));
    cancelRename();
    await reloadTree();
    emit('changed');
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('common.operationFailed'));
  }
}

// --- Delete (with optional promote) ---
function openDeleteDialog(folder: KnowledgeFolderNode) {
  deleteTarget.value = folder;
  deletePromote.value = folder.knowledge_count > 0 || folder.has_children;
  deleteDialogVisible.value = true;
  menuOpenId.value = null;
}

async function submitDelete() {
  if (!deleteTarget.value) return;
  deleteSubmitting.value = true;
  try {
    await deleteKnowledgeFolder(
      props.kbId,
      deleteTarget.value.id,
      deletePromote.value ? 'promote' : undefined,
    );
    MessagePlugin.success(t('knowledgeBase.folder.deleted'));
    deleteDialogVisible.value = false;
    // If the deleted folder was the current selection, fall back to "show all".
    if (props.modelValue === deleteTarget.value.id) emit('update:modelValue', '');
    await reloadTree();
    emit('changed');
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('knowledgeBase.folder.deleteNotEmpty'));
  } finally {
    deleteSubmitting.value = false;
  }
}

// --- Organize by upload path ---
async function runOrganize() {
  organizing.value = true;
  try {
    const res: any = await organizeKnowledgeFoldersByPath(props.kbId);
    const data = res?.data || {};
    MessagePlugin.success(
      t('knowledgeBase.folder.organizeResult', {
        organized: data.organized ?? 0,
        folders: data.folders_created ?? 0,
      }),
    );
    await reloadTree();
    emit('changed');
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('common.operationFailed'));
  } finally {
    organizing.value = false;
  }
}

// --- Ask inside this folder ---
function askInFolder(folder: KnowledgeFolderNode) {
  menuOpenId.value = null;
  router.push({
    name: 'kbCreatChat',
    params: { kbId: props.kbId },
    query: { folder_id: folder.id, folder_name: folder.name },
  });
}

// --- Move-to via picker (context-menu entry; drag is the primary path) ---
function openMovePicker(folder: KnowledgeFolderNode) {
  moveTarget.value = folder;
  movePickerVisible.value = true;
  menuOpenId.value = null;
}

async function onMovePickerConfirm(folderId: string) {
  if (!moveTarget.value) return;
  try {
    await updateKnowledgeFolder(props.kbId, moveTarget.value.id, {
      parent_id: folderId || undefined,
      move_parent: true,
    });
    MessagePlugin.success(t('knowledgeBase.folder.moved'));
    movePickerVisible.value = false;
    await reloadTree();
    emit('changed');
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('common.operationFailed'));
  }
}

// Firefox requires dataTransfer data to start native dragging.
function primeDragData(e: DragEvent) {
  if (!e.dataTransfer) return;
  e.dataTransfer.effectAllowed = 'move';
  try {
    e.dataTransfer.setData('text/plain', '');
  } catch {
    // Some browsers throw if setData is called outside a real dragstart; ignore.
  }
}

function onFolderDragStart(e: DragEvent, folder: KnowledgeFolderNode) {
  draggedFolder.value = { id: folder.id, path: folder.path };
  primeDragData(e);
}

function onDragEnd() {
  draggedFolder.value = null;
  dropTargetId.value = null;
}

function onRowDragOver(e: DragEvent, folder: KnowledgeFolderNode) {
  if (!draggedFolder.value) return;
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
  dropTargetId.value = folder.id;
}

function onRowDragLeave(folderId: string) {
  if (dropTargetId.value === folderId) dropTargetId.value = null;
}

function onRootDragOver(e: DragEvent, sentinel: string) {
  if (!draggedFolder.value) return;
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
  dropTargetId.value = sentinel;
}

function onRootDragLeave(sentinel: string) {
  if (dropTargetId.value === sentinel) dropTargetId.value = null;
}

function onDropOnRow(e: DragEvent, folder: KnowledgeFolderNode) {
  if (!draggedFolder.value) return;
  stageOrRejectMove(e, folder.id, folder.name);
}

function onDropOnRoot(e: DragEvent) {
  if (!draggedFolder.value) return;
  stageOrRejectMove(e, FOLDER_FILTER_ROOT, t('knowledgeBase.folder.root'));
}

function stageOrRejectMove(e: DragEvent, targetId: string, targetLabel: string) {
  const dragged = draggedFolder.value;
  draggedFolder.value = null;
  dropTargetId.value = null;
  if (!dragged || targetId === dragged.id) return;

  const draggedNode = folderById.value.get(dragged.id);
  const currentParent = draggedNode?.parent_id || '';
  const newParent = targetId === FOLDER_FILTER_ROOT ? '' : targetId;
  if (currentParent === newParent) return;

  if (targetId !== FOLDER_FILTER_ROOT) {
    const targetNode = folderById.value.get(targetId);
    if (targetNode && dragged.path) {
      if (
        targetNode.path === dragged.path ||
        targetNode.path.startsWith(`${dragged.path}/`)
      ) {
        MessagePlugin.warning(t('knowledgeBase.folder.moveIntoSelf'));
        return;
      }
    }
  }

  pendingMove.value = {
    folder: draggedNode || ({} as KnowledgeFolderNode),
    targetId,
    targetLabel,
    x: e.clientX,
    y: e.clientY,
  };
}

function cancelPendingMove() {
  pendingMove.value = null;
}

async function confirmPendingMove() {
  const move = pendingMove.value;
  pendingMove.value = null;
  if (!move) return;
  const parentId = move.targetId === FOLDER_FILTER_ROOT ? undefined : move.targetId;
  try {
    await updateKnowledgeFolder(props.kbId, move.folder.id, {
      parent_id: parentId,
      move_parent: true,
    });
    MessagePlugin.success(t('knowledgeBase.folder.moved'));
    await reloadTree();
    emit('changed');
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('common.operationFailed'));
  }
}
</script>

<template>
  <aside class="kb-folder-tree-aside">
    <div class="kb-folder-tree-header">
      <t-input v-model="searchQuery" size="small" clearable
        :placeholder="t('knowledgeBase.folder.searchPlaceholder')">
        <template #prefix-icon>
          <t-icon name="search" size="14px" />
        </template>
      </t-input>
      <div v-if="canEdit" class="kb-folder-tree-actions">
        <t-tooltip :content="t('knowledgeBase.folder.new')" placement="top">
          <button type="button" class="kb-tree-action-btn" @click="startCreate('')">
            <t-icon name="folder-add" size="15px" />
          </button>
        </t-tooltip>
        <t-popconfirm theme="default"
          :content="t('knowledgeBase.folder.organizeConfirm')" placement="bottom"
          @confirm="runOrganize">
          <t-tooltip :content="t('knowledgeBase.folder.organizeByPath')" placement="top">
            <button type="button" class="kb-tree-action-btn" :disabled="organizing">
              <t-icon :name="organizing ? 'loading' : 'folder-import'" size="15px"
                :class="{ 'kb-tree-icon-spin': organizing }" />
            </button>
          </t-tooltip>
        </t-popconfirm>
      </div>
    </div>

    <!-- Root drop zone: the explicit root row plus any empty space below the
         tree both reparent to the root. Folder-row and root-row dragover are
         stopped from bubbling here so the container only rings on genuine
         empty-space hovers. -->
    <div class="kb-folder-tree-list"
      :class="{ 'is-root-drop': dropTargetId === DROP_ROOT_ZONE }"
      @dragover.prevent="onRootDragOver($event, DROP_ROOT_ZONE)"
      @dragleave="onRootDragLeave(DROP_ROOT_ZONE)" @drop.prevent="onDropOnRoot">
      <!-- Root row: scopes the list to documents filed in no folder. It sits
           above the tree as a sibling-of-nothing, so it gets no depth indent
           and no chevron slot. Dropping a folder here reparents it to the root,
           same as the empty-space zone. -->
      <div class="kb-tree-row kb-tree-row--root" :class="{
        'is-active': modelValue === FOLDER_FILTER_ROOT,
        'is-drop': dropTargetId === FOLDER_FILTER_ROOT,
      }" @click="selectRow(FOLDER_FILTER_ROOT)"
        @dragover.prevent.stop="onRootDragOver($event, FOLDER_FILTER_ROOT)"
        @dragleave.stop="onRootDragLeave(FOLDER_FILTER_ROOT)"
        @drop.prevent.stop="onDropOnRoot">
        <t-icon name="home" class="kb-tree-icon kb-tree-icon--root" />
        <span class="kb-tree-name">{{ t('knowledgeBase.folder.root') }}</span>
      </div>

      <!-- Root-level inline create row -->
      <div v-if="creatingUnderParent === ''" class="kb-tree-row kb-tree-row--editing"
        :style="{ '--kb-tree-depth': 0 }" @click.stop>
        <span class="kb-tree-toggle is-leaf" />
        <t-icon name="folder" class="kb-tree-icon" />
        <t-input ref="newFolderInputRef" v-model="creatingName" size="small"
          :placeholder="t('knowledgeBase.folder.namePlaceholder')" :maxlength="100"
          @enter="submitCreate" @keydown.esc="cancelCreate" @blur="cancelCreate" />
      </div>

      <div v-for="row in treeRows" :key="row.folder.id" class="kb-tree-row"
        :class="{
          'is-active': row.folder.id === modelValue,
          'is-drop': dropTargetId === row.folder.id,
          'is-dragging': draggedFolder?.id === row.folder.id,
          'is-editing': editingId === row.folder.id,
        }" :style="{ '--kb-tree-depth': row.depth }"
        :draggable="editingId !== row.folder.id && creatingUnderParent === null && canEdit"
        @click="selectRow(row.folder.id)"
        @dragstart="onFolderDragStart($event, row.folder)" @dragend="onDragEnd"
        @dragover.prevent.stop="onRowDragOver($event, row.folder)"
        @dragleave.stop="onRowDragLeave(row.folder.id)"
        @drop.prevent.stop="onDropOnRow($event, row.folder)">
        <span class="kb-tree-toggle" :class="{ 'is-leaf': !row.hasChildren }"
          @click.stop="row.hasChildren && toggleCollapse(row.folder.id)">
          <t-icon :name="row.isCollapsed ? 'chevron-right' : 'chevron-down'" size="14px" />
        </span>
        <t-icon :name="row.isCollapsed ? 'folder' : 'folder-open'" class="kb-tree-icon" />
        <!-- Inline rename input replaces the name span on the editing row -->
        <t-input v-if="editingId === row.folder.id" ref="renameInputRef" v-model="editingName"
          size="small" :maxlength="100" class="kb-tree-rename-input" @click.stop
          @enter="commitRename(row.folder)" @keydown.esc="cancelRename"
          @blur="commitRename(row.folder)" />
        <template v-else>
          <span class="kb-tree-name" :title="row.folder.path">{{ row.folder.name }}</span>
          <span class="kb-tree-count" v-if="!filtersActive && row.folder.knowledge_count">
            {{ row.folder.knowledge_count }}
          </span>
          <div class="kb-tree-trailing" v-if="canEdit" @click.stop>
            <t-popup placement="right" trigger="click" destroy-on-close overlay-class-name="card-more"
              :visible="menuOpenId === row.folder.id"
              @visible-change="(v: boolean) => (menuOpenId = v ? row.folder.id : null)">
              <button type="button" class="kb-tree-more"
                :aria-label="t('knowledgeBase.columnActions')">
                <t-icon name="more" size="14px" />
              </button>
              <template #content>
                <div class="card-menu">
                  <div class="card-menu-item" @click.stop="askInFolder(row.folder)">
                    <t-icon class="icon" name="chat" />{{ t('knowledgeBase.folder.askInFolder') }}
                  </div>
                  <div class="card-menu-item" @click.stop="startCreate(row.folder.id)">
                    <t-icon class="icon" name="folder-add" />{{ t('knowledgeBase.folder.newSubfolder') }}
                  </div>
                  <div class="card-menu-item" @click.stop="startRename(row.folder)">
                    <t-icon class="icon" name="edit" />{{ t('knowledgeBase.folder.rename') }}
                  </div>
                  <div class="card-menu-item" @click.stop="openMovePicker(row.folder)">
                    <t-icon class="icon" name="folder-move" />{{ t('knowledgeBase.folder.moveTo') }}
                  </div>
                  <div class="card-menu-item danger" @click.stop="openDeleteDialog(row.folder)">
                    <t-icon class="icon" name="delete" />{{ t('knowledgeBase.folder.delete') }}
                  </div>
                </div>
              </template>
            </t-popup>
          </div>
        </template>
      </div>

      <!-- Sub-folder inline create row (rendered right after its parent) -->
      <div v-if="creatingUnderParent !== null && creatingUnderParent !== ''"
        :key="`create:${creatingUnderParent}`" class="kb-tree-row kb-tree-row--editing"
        :style="{ '--kb-tree-depth': (folderById.get(creatingUnderParent)?.depth ?? 0) + 1 }"
        @click.stop>
        <span class="kb-tree-toggle is-leaf" />
        <t-icon name="folder" class="kb-tree-icon" />
        <t-input v-model="creatingName" size="small"
          :placeholder="t('knowledgeBase.folder.namePlaceholder')" :maxlength="100"
          @enter="submitCreate" @keydown.esc="cancelCreate" @blur="cancelCreate" />
      </div>

      <div v-if="loading" class="kb-tree-empty"><t-loading size="small" /></div>
      <div v-else-if="searchQuery && !treeRows.length" class="kb-tree-empty">
        {{ t('knowledgeBase.folder.noMatchedFolders') }}
      </div>
      <div v-else-if="!allFolders.length" class="kb-tree-empty">
        {{ t('knowledgeBase.folder.noFolders') }}
      </div>
    </div>

    <!-- Delete dialog (with optional content-promote) — migrated from KbFolderBar -->
    <t-dialog :visible="deleteDialogVisible" width="420px" :header="t('knowledgeBase.folder.delete')"
      :confirm-btn="{ content: t('knowledgeBase.confirmDelete'), theme: 'danger', loading: deleteSubmitting }"
      :cancel-btn="{ content: t('common.cancel') }" @confirm="submitDelete"
      @close="deleteDialogVisible = false"
      @update:visible="(v: boolean) => (deleteDialogVisible = v)">
      <div class="kb-tree-delete-body">
        <p>{{ t('knowledgeBase.folder.deleteConfirm', { name: deleteTarget?.name || '' }) }}</p>
        <t-checkbox v-if="deleteTarget && (deleteTarget.knowledge_count > 0 || deleteTarget.has_children)"
          v-model="deletePromote">
          {{ t('knowledgeBase.folder.deletePromote') }}
        </t-checkbox>
      </div>
    </t-dialog>

    <!-- Move-to picker (context-menu entry). Drag is the primary path; this is
         the explicit "move to" for users who prefer a dialog. -->
    <FolderTreePicker v-model:visible="movePickerVisible" :kb-id="kbId" allow-root
      :title="t('knowledgeBase.folder.moveTo')"
      :disabled-ids="moveTarget ? [moveTarget.id] : []" @confirm="onMovePickerConfirm" />
  </aside>

  <!-- In-place move confirmation, anchored at the drop point. Confirming runs
       the actual reparent API; cancelling discards the staged move. -->
  <Teleport to="body">
    <div v-if="pendingMove" class="kb-tree-move-mask" @click="cancelPendingMove">
      <div class="kb-tree-move-card"
        :style="{ left: `${pendingMove.x}px`, top: `${pendingMove.y}px` }" @click.stop>
        <div class="kb-tree-move-title">{{ t('knowledgeBase.folder.moveConfirmTitle') }}</div>
        <div class="kb-tree-move-body">
          {{ t('knowledgeBase.folder.moveConfirm', { target: pendingMove.targetLabel }) }}
        </div>
        <div class="kb-tree-move-footer">
          <t-button variant="outline" size="small" @click="cancelPendingMove">
            {{ t('common.cancel') }}
          </t-button>
          <t-button theme="primary" size="small" @click="confirmPendingMove">
            {{ t('common.confirm') }}
          </t-button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped lang="less">
.kb-folder-tree-aside {
  width: 240px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: transparent;
  border-right: 1px solid var(--td-component-stroke);
  box-shadow: 1px 0 4px rgba(0, 0, 0, 0.03);
  padding: 8px 8px 8px 0;
  overflow: hidden;
}

.kb-folder-tree-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 10px 8px;
}

.kb-folder-tree-actions {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.kb-tree-action-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &:disabled {
    cursor: default;
    opacity: 0.6;
  }
}

.kb-tree-icon-spin {
  animation: kb-tree-spin 0.9s linear infinite;
}

@keyframes kb-tree-spin {
  to {
    transform: rotate(360deg);
  }
}

.kb-folder-tree-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  scrollbar-width: none;
  padding: 0 4px 8px 6px;

  &::-webkit-scrollbar {
    display: none;
  }

  // Root-drop highlight: inset ring on the whole list so dropping on empty
  // space reads as "move to root" (box-shadow keeps layout stable mid-drag).
  &.is-root-drop {
    box-shadow: inset 0 0 0 2px var(--td-brand-color-light);
    background: var(--td-brand-color-light);
  }
}

.kb-tree-row {
  display: flex;
  align-items: center;
  gap: 4px;
  min-height: 30px;
  padding: 0 6px;
  border-radius: 6px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;
  padding-left: calc(var(--kb-tree-depth, 0) * 14px + 6px);

  &:hover {
    background: var(--td-bg-color-secondarycontainer);

    .kb-tree-more {
      opacity: 1;
    }
  }

  &.is-active {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);

    .kb-tree-icon {
      color: var(--td-brand-color);
    }

    .kb-tree-name {
      font-weight: 500;
    }
  }

  &.is-drop {
    background: var(--td-brand-color-light);
    box-shadow: inset 0 0 0 1.5px var(--td-brand-color);
  }

  &.is-dragging {
    opacity: 0.5;
  }

  &.is-editing {
    cursor: default;
    background: transparent;
  }
}

.kb-tree-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
  border-radius: 3px;
  cursor: pointer;

  &:hover {
    background: var(--td-component-stroke);
  }

  // Leaf rows keep the slot (so names stay aligned) but hide the chevron and
  // must not swallow the row click that selects the folder.
  &.is-leaf {
    pointer-events: none;
    visibility: hidden;
  }
}

.kb-tree-icon {
  flex-shrink: 0;
  color: #d97706;
  font-size: 16px;
}

// The root row is a scope anchor, not a folder in the tree: it drops the depth
// indent and the chevron slot entirely, and keeps a neutral icon colour so the
// amber folder icons still read as the actual tree.
.kb-tree-row--root {
  padding-left: 6px;
  color: var(--td-text-color-secondary);
}

.kb-tree-icon--root {
  color: var(--td-text-color-secondary);
  font-size: 15px;
}

.kb-tree-row--root.is-active {
  color: var(--td-brand-color);

  .kb-tree-icon--root {
    color: var(--td-brand-color);
  }
}

.kb-tree-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 400;
  line-height: 1.4;
}

.kb-tree-count {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
  margin-left: 4px;
}

.kb-tree-trailing {
  flex-shrink: 0;
  margin-left: auto;
}

.kb-tree-more {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s ease, background-color 0.15s ease;

  &:hover {
    background: var(--td-component-stroke);
  }
}

.kb-tree-rename-input {
  flex: 1;
  min-width: 0;

  :deep(.t-input) {
    font-size: 13px;
    background-color: transparent;
    border: none;
    border-radius: 0;
    box-shadow: none;
    padding: 0;
  }

  :deep(.t-input__inner) {
    padding: 0;
    color: var(--td-text-color-primary);
    caret-color: var(--td-brand-color);
  }
}

.kb-tree-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 64px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.kb-tree-delete-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  font-size: 13px;
}

.kb-tree-move-mask {
  position: fixed;
  inset: 0;
  z-index: 6000;
  background: rgba(0, 0, 0, 0.12);
}

.kb-tree-move-card {
  position: fixed;
  z-index: 6001;
  min-width: 220px;
  max-width: 320px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.12);
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  // Keep the card inside the viewport even when dropped near the edges.
  transform: translate(-50%, -100%);
}

.kb-tree-move-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.kb-tree-move-body {
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.kb-tree-move-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
