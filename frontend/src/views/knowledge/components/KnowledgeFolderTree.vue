<script setup lang="ts">
import { ref, computed, onMounted, nextTick } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  listKnowledgeFolders,
  createKnowledgeFolder,
  updateKnowledgeFolder,
  deleteKnowledgeFolder,
} from '@/api/knowledge-base';
import KnowledgeFolderTreeNode, {
  type KnowledgeFolderTreeNodeData,
} from './KnowledgeFolderTreeNode.vue';

const props = withDefaults(
  defineProps<{
    kbId: string;
    canEdit?: boolean;
    selectedFolderId?: string;
    rootLabel?: string;
  }>(),
  { canEdit: false, selectedFolderId: '', rootLabel: '' },
);

const emit = defineEmits<{ (e: 'select', folderId: string): void }>();

const { t } = useI18n();

const rootChildren = ref<KnowledgeFolderTreeNodeData[]>([]);
const rootLoading = ref(false);
const rootLoaded = ref(false);

// inline-create state. creatingParentId '' means create at root, otherwise a
// subfolder under the given folder id (captured from the current selection).
const creatingRoot = ref(false);
const creatingParentId = ref('');
const creatingName = ref('');
const creatingInputRef = ref<{ focus: () => void } | null>(null);

// --- move folder dialog state ---
const moveVisible = ref(false);
const moveLoading = ref(false);
const movingFolderId = ref('');
const movingFolderName = ref('');
const moveTargetActived = ref<string[]>([]);
const moveTreeData = ref<any[]>([]);

function toNode(folder: any, depth: number): KnowledgeFolderTreeNodeData {
  return {
    id: String(folder.id || ''),
    name: String(folder.name || ''),
    depth,
    knowledgeCount: Number(folder.knowledge_count) || 0,
    hasChildren: !!folder.has_children,
    expanded: false,
    loaded: false,
    loading: false,
    children: [],
  };
}

function extractFolders(res: any): any[] {
  if (!res) return [];
  const body = res.data !== undefined ? res.data : res;
  return Array.isArray(body?.folders) ? body.folders : [];
}

async function loadRoot() {
  if (!props.kbId) return;
  rootLoading.value = true;
  try {
    const folders = extractFolders(await listKnowledgeFolders(props.kbId, ''));
    rootChildren.value = folders.map((f: any) => toNode(f, 0));
    rootLoaded.value = true;
  } catch (e: any) {
    MessagePlugin.error(t('knowledgeBase.folderLoadFailed'));
  } finally {
    rootLoading.value = false;
  }
}

async function loadChildren(node: KnowledgeFolderTreeNodeData) {
  if (node.loaded || node.loading) return;
  node.loading = true;
  try {
    const folders = extractFolders(await listKnowledgeFolders(props.kbId, node.id));
    node.children = folders.map((f: any) => toNode(f, node.depth + 1));
    node.loaded = true;
    node.hasChildren = node.children.length > 0;
  } catch (e: any) {
    MessagePlugin.error(t('knowledgeBase.folderLoadFailed'));
  } finally {
    node.loading = false;
  }
}

function onToggle(node: KnowledgeFolderTreeNodeData) {
  node.expanded = !node.expanded;
  if (node.expanded && !node.loaded) loadChildren(node);
}

function findNode(list: KnowledgeFolderTreeNodeData[], id: string): KnowledgeFolderTreeNodeData | null {
  for (const n of list) {
    if (n.id === id) return n;
    const found = findNode(n.children, id);
    if (found) return found;
  }
  return null;
}

function removeNode(list: KnowledgeFolderTreeNodeData[], id: string): boolean {
  const idx = list.findIndex((n) => n.id === id);
  if (idx >= 0) {
    list.splice(idx, 1);
    return true;
  }
  for (const n of list) {
    if (removeNode(n.children, id)) {
      // If the parent no longer has any visible children, clear its hasChildren flag
      // so the delete action is no longer blocked as "not empty".
      n.hasChildren = n.children.length > 0;
      return true;
    }
  }
  return false;
}

function onSelect(folderId: string) {
  emit('select', folderId);
}

function selectRoot() {
  emit('select', '');
}

// --- inline create (context-aware: root or under selected folder) ---
const createHint = computed(() => {
  if (creatingParentId.value === '') return t('knowledgeBase.createUnderRoot');
  const parent = findNode(rootChildren.value, creatingParentId.value);
  return t('knowledgeBase.createUnderFolder', { name: parent ? parent.name : '' });
});

const newBtnTip = computed(() => {
  if (!props.selectedFolderId) return t('knowledgeBase.newRootFolder');
  const parent = findNode(rootChildren.value, props.selectedFolderId);
  return t('knowledgeBase.newFolderUnder', { name: parent ? parent.name : '' });
});

function startCreateRoot() {
  // capture the current selection as the parent at click time
  creatingParentId.value = props.selectedFolderId || '';
  creatingRoot.value = true;
  nextTick(() => creatingInputRef.value?.focus());
}

async function submitCreateRoot() {
  const name = creatingName.value.trim();
  if (!name) {
    creatingRoot.value = false;
    return;
  }
  const parentId = creatingParentId.value;
  try {
    await createKnowledgeFolder(props.kbId, parentId, name);
    MessagePlugin.success(t('knowledgeBase.folderCreateSuccess'));
    creatingRoot.value = false;
    creatingName.value = '';
    if (parentId === '') {
      await loadRoot();
    } else {
      const parent = findNode(rootChildren.value, parentId);
      if (parent) {
        parent.expanded = true;
        parent.loaded = false;
        await loadChildren(parent);
      } else {
        await loadRoot();
      }
    }
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('knowledgeBase.folderCreateFailed'));
  }
}

function cancelCreateRoot() {
  creatingRoot.value = false;
  creatingName.value = '';
}

// --- delegated CRUD from child nodes ---
async function handleCreate(parentId: string, name: string) {
  try {
    await createKnowledgeFolder(props.kbId, parentId, name);
    MessagePlugin.success(t('knowledgeBase.folderCreateSuccess'));
    if (parentId === '') {
      await loadRoot();
    } else {
      const parent = findNode(rootChildren.value, parentId);
      if (parent) {
        parent.expanded = true;
        parent.loaded = false;
        await loadChildren(parent);
      }
    }
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('knowledgeBase.folderCreateFailed'));
  }
}

async function handleRename(folderId: string, name: string) {
  try {
    await updateKnowledgeFolder(props.kbId, folderId, { name });
    MessagePlugin.success(t('knowledgeBase.folderRenameSuccess'));
    const node = findNode(rootChildren.value, folderId);
    if (node) node.name = name;
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('knowledgeBase.folderRenameFailed'));
  }
}

async function handleDelete(folderId: string) {
  try {
    await deleteKnowledgeFolder(props.kbId, folderId);
    MessagePlugin.success(t('knowledgeBase.folderDeleteSuccess'));
    removeNode(rootChildren.value, folderId);
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('knowledgeBase.folderDeleteFailed'));
  }
}

// --- move folder ---
// Recursively fetch the whole folder tree (the list API only returns direct
// children), skipping the subtree rooted at excludeId so a folder can't be
// moved into itself or one of its descendants.
async function buildMoveTree(parentId: string, excludeId: string): Promise<any[]> {
  const folders = extractFolders(await listKnowledgeFolders(props.kbId, parentId));
  const nodes: any[] = [];
  for (const f of folders) {
    const id = String(f.id || '');
    if (id === excludeId) continue;
    const children = await buildMoveTree(id, excludeId);
    nodes.push({ value: id, label: String(f.name || ''), children });
  }
  return nodes;
}

async function handleMove(folderId: string) {
  const node = findNode(rootChildren.value, folderId);
  movingFolderId.value = folderId;
  movingFolderName.value = node ? node.name : '';
  moveTargetActived.value = [];
  moveTreeData.value = [];
  moveVisible.value = true;
  moveLoading.value = true;
  try {
    const tree = await buildMoveTree('', folderId);
    // synthetic root option so the folder can be promoted to top level
    moveTreeData.value = [
      { value: '__ROOT__', label: t('knowledgeBase.moveFolderRoot'), children: tree },
    ];
  } catch (e: any) {
    MessagePlugin.error(t('knowledgeBase.folderLoadFailed'));
  } finally {
    moveLoading.value = false;
  }
}

async function confirmMove() {
  const picked = moveTargetActived.value[0];
  if (picked === undefined) return;
  const targetParentId = picked === '__ROOT__' ? '' : String(picked);
  moveLoading.value = true;
  try {
    await updateKnowledgeFolder(props.kbId, movingFolderId.value, {
      parent_id: targetParentId,
      move_parent: true,
    });
    MessagePlugin.success(t('knowledgeBase.moveFolderSuccess'));
    moveVisible.value = false;
    await loadRoot();
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('knowledgeBase.moveFolderFailed'));
  } finally {
    moveLoading.value = false;
  }
}

onMounted(loadRoot);
</script>

<template>
  <div class="kf-tree">
    <div class="kf-header">
      <span class="kf-title">{{ t('knowledgeBase.folderTreeTitle') }}</span>
      <t-tooltip v-if="canEdit" :content="newBtnTip" placement="top">
        <button type="button" class="kf-new-btn" :aria-label="newBtnTip" @click.stop="startCreateRoot">
          <t-icon name="folder-add" />
        </button>
      </t-tooltip>
    </div>

    <div class="kf-body">
      <div class="kf-node kf-root" :class="{ active: selectedFolderId === '' }" @click="selectRoot">
        <div class="kf-row">
          <span class="kf-toggle kf-toggle--root"><t-icon name="app" /></span>
          <span class="kf-name">{{ rootLabel || t('knowledgeBase.folderAll') }}</span>
        </div>
      </div>

      <div v-if="creatingRoot" class="kf-root-editing" :style="{ '--kf-depth': 0 }">
        <div class="kf-create-hint">{{ createHint }}</div>
        <input
          ref="creatingInputRef"
          v-model="creatingName"
          class="kf-rename-input"
          :placeholder="t('knowledgeBase.newSubfolder')"
          @click.stop
          @keydown.enter="submitCreateRoot"
          @keydown.esc="cancelCreateRoot"
          @blur="submitCreateRoot"
        />
      </div>

      <t-loading v-if="rootLoading" size="small" class="kf-loading" />

      <template v-else>
        <div v-if="rootLoaded && !rootChildren.length && !creatingRoot" class="kf-empty">
          {{ t('knowledgeBase.folderEmpty') }}
        </div>

        <KnowledgeFolderTreeNode
          v-for="node in rootChildren"
          :key="node.id"
          :node="node"
          :kb-id="kbId"
          :can-edit="canEdit"
          :selected-folder-id="selectedFolderId"
          @select="onSelect"
          @toggle="onToggle"
          @create="handleCreate"
          @rename="handleRename"
          @move="handleMove"
          @delete="handleDelete"
        />
      </template>
    </div>

    <t-dialog
      v-model:visible="moveVisible"
      :header="t('knowledgeBase.moveFolderTitle')"
      :confirm-btn="{ content: t('knowledgeBase.moveFolderConfirm'), loading: moveLoading, disabled: !moveTargetActived.length }"
      :cancel-btn="t('common.cancel')"
      width="420px"
      @confirm="confirmMove"
    >
      <div class="kf-move-body">
        <p class="kf-move-tip">{{ t('knowledgeBase.moveFolderBody', { name: movingFolderName }) }}</p>
        <t-loading v-if="moveLoading && !moveTreeData.length" size="small" />
        <t-tree
          v-else
          :data="moveTreeData"
          :actived="moveTargetActived"
          activable
          hover
          expand-all
          :expand-on-click-node="false"
          @active="(val: string[]) => (moveTargetActived = val)"
        />
      </div>
    </t-dialog>
  </div>
</template>

<style scoped lang="less">
.kf-tree {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 9px;
}

.kf-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;

  .kf-title {
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .kf-new-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border: none;
    background: transparent;
    color: var(--td-text-color-secondary);
    border-radius: 6px;
    cursor: pointer;
    transition: background-color 0.15s ease, color 0.15s ease;

    &:hover {
      background: var(--td-bg-color-container-hover);
      color: var(--td-brand-color);
    }
  }
}

.kf-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding: 6px;
}

.kf-root {
  .kf-row {
    padding-left: 8px;
  }

  .kf-toggle--root {
    color: var(--td-brand-color);
  }
}

.kf-root-editing {
  padding: 4px 8px 8px;

  .kf-create-hint {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    margin: 2px 0 4px;
  }

  .kf-rename-input {
    width: 100%;
    height: 28px;
    padding: 0 8px;
    font-size: 13px;
    border: 1px solid var(--td-brand-color);
    border-radius: 4px;
    outline: none;
    background: var(--td-bg-color-container);
    color: var(--td-text-color-primary);
    box-sizing: border-box;
  }
}

.kf-move-body {
  .kf-move-tip {
    margin: 0 0 10px;
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }
}

.kf-empty {
  padding: 16px 12px;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  text-align: center;
}

.kf-loading {
  margin: 10px 12px;
}
</style>
