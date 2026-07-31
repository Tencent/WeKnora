<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { getFolderTree, createFolder, renameFolder, deleteFolder, moveKnowledgeToFolder, type FolderTreeNode } from '@/api/folder/index';

const { t } = useI18n();
const props = defineProps<{ kbId: string; modelValue: string; canEdit: boolean; selectedKnowledgeIds: Set<string> }>();
const emit = defineEmits<{ (e: 'update:modelValue', value: string): void; (e: 'select', folderId: string): void; (e: 'refresh'): void }>();

const folderTree = ref<FolderTreeNode[]>([]);
const loading = ref(false);
const expandedIds = ref<Set<string>>(new Set());
const menuOpen = ref('');
const menuPos = ref({ x: 0, y: 0 });
const renamingId = ref('');
const renameValue = ref('');
const creatingParentId = ref('');
const createName = ref('');
const isCreating = ref(false);

interface FlatNode { id: string; name: string; depth: number; hasChildren: boolean; knowledgeCount: number; isExpanded: boolean; }
const flatNodes = computed<FlatNode[]>(() => {
  const result: FlatNode[] = [];
  const walk = (nodes: FolderTreeNode[], depth: number) => {
    for (const node of nodes) {
      const expanded = expandedIds.value.has(node.id);
      result.push({ id: node.id, name: node.name, depth, hasChildren: node.children.length > 0, knowledgeCount: node.knowledge_count, isExpanded: expanded });
      if (expanded && node.children.length > 0) walk(node.children, depth + 1);
    }
  };
  walk(folderTree.value, 0);
  return result;
});
async function loadFolders() {
  if (!props.kbId) return;
  loading.value = true;
  try { const res = await getFolderTree(props.kbId); folderTree.value = (res as any).data || []; }
  catch (e: any) { console.error('Failed to load folders', e); }
  finally { loading.value = false; }
}

function toggleExpand(folderId: string) { const s = new Set(expandedIds.value); s.has(folderId) ? s.delete(folderId) : s.add(folderId); expandedIds.value = s; }
function handleSelect(folderId: string) { emit('update:modelValue', folderId); emit('select', folderId); }
function handleContextMenu(e: MouseEvent, folderId: string) { e.preventDefault(); e.stopPropagation(); menuPos.value = { x: e.clientX, y: e.clientY }; menuOpen.value = folderId; }
function closeMenu() { menuOpen.value = ''; }
function findNodeName(nodes: FolderTreeNode[], id: string): string {
  for (const node of nodes) { if (node.id === id) return node.name; const f = findNodeName(node.children, id); if (f) return f; }
  return '';
}
function onWindowClick() { if (menuOpen.value) menuOpen.value = ''; }

async function handleCreateUnder(parentId: string) { closeMenu(); creatingParentId.value = parentId; createName.value = ''; isCreating.value = true; if (parentId) { const s = new Set(expandedIds.value); s.add(parentId); expandedIds.value = s; } }
async function submitCreate() { const name = createName.value.trim(); if (!name) return; try { await createFolder(props.kbId, name, creatingParentId.value); MessagePlugin.success(t('knowledgeBase.folderCreated')); isCreating.value = false; creatingParentId.value = ''; createName.value = ''; await loadFolders(); } catch (e: any) { MessagePlugin.error(e?.message || t('knowledgeBase.folderCreateFailed')); } }
function cancelCreate() { isCreating.value = false; createName.value = ''; creatingParentId.value = ''; }
function startRename(folderId: string) { closeMenu(); renamingId.value = folderId; renameValue.value = findNodeName(folderTree.value, folderId); }
async function commitRename() { const name = renameValue.value.trim(); const fid = renamingId.value; if (!name || !fid) return; try { await renameFolder(props.kbId, fid, name); MessagePlugin.success(t('knowledgeBase.folderRenamed')); renamingId.value = ''; await loadFolders(); } catch (e: any) { MessagePlugin.error(e?.message || t('knowledgeBase.folderRenameFailed')); } }
function cancelRename() { renamingId.value = ''; }
async function handleDelete(folderId: string) { closeMenu(); try { await deleteFolder(props.kbId, folderId); MessagePlugin.success(t('knowledgeBase.folderDeleted')); if (props.modelValue === folderId) handleSelect(''); await loadFolders(); } catch (e: any) { MessagePlugin.error(e?.message || t('knowledgeBase.folderDeleteFailed')); } }
function onDragOver(e: DragEvent) { e.preventDefault(); if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'; }
async function onDrop(e: DragEvent, targetFolderId: string) { e.preventDefault(); if (props.selectedKnowledgeIds.size === 0) return; try { await moveKnowledgeToFolder(Array.from(props.selectedKnowledgeIds), targetFolderId); MessagePlugin.success(t('knowledgeBase.folderMoveSuccess')); emit('refresh'); await loadFolders(); } catch (e: any) { MessagePlugin.error(e?.message || t('knowledgeBase.folderMoveFailed')); } }
function expandAll() { const walk = (nodes: FolderTreeNode[]) => { for (const n of nodes) { expandedIds.value.add(n.id); walk(n.children); } }; walk(folderTree.value); expandedIds.value = new Set(expandedIds.value); }
function collapseAll() { expandedIds.value = new Set(); }

onMounted(() => { if (props.kbId) loadFolders(); window.addEventListener('click', onWindowClick); });
onUnmounted(() => { window.removeEventListener('click', onWindowClick); });
watch(() => props.kbId, (n) => { if (n) loadFolders(); });
</script>
<template>
  <div class="folder-tree-sidebar">
    <div class="folder-tree-header">
      <span class="folder-tree-title">{{ $t('knowledgeBase.folders') }}</span>
      <div class="folder-tree-actions">
        <button class="folder-tree-action-btn" title="Expand all" @click="expandAll"><t-icon name="chevron-right-double" size="14px" /></button>
        <button class="folder-tree-action-btn" title="Collapse all" @click="collapseAll"><t-icon name="chevron-left-double" size="14px" /></button>
        <button v-if="canEdit" class="folder-tree-action-btn" title="Create folder" @click="handleCreateUnder('')"><t-icon name="add" size="14px" /></button>
      </div>
    </div>

    <div class="folder-tree-list">
      <div v-if="loading && !flatNodes.length" class="folder-tree-empty"><t-icon name="loading" size="20px" /></div>

      <div class="folder-tree-item root-item" :class="{ active: modelValue === '' }" @click="handleSelect('')" @dragover="onDragOver" @drop="onDrop($event, '')">
        <t-icon name="home" size="16px" class="folder-icon" />
        <span class="folder-name">{{ $t('knowledgeBase.allDocuments') }}</span>
      </div>

      <div v-if="isCreating && creatingParentId === ''" class="folder-create-row">
        <input v-model="createName" class="folder-rename-input" :placeholder="$t('knowledgeBase.folderNamePlaceholder')" @keydown.enter="submitCreate" @keydown.escape="cancelCreate" @blur="cancelCreate" />
      </div>

      <template v-for="node in flatNodes" :key="node.id">
        <div class="folder-tree-item" :class="{ active: modelValue === node.id }" :style="{ paddingLeft: (12 + node.depth * 16) + 'px' }" @click="handleSelect(node.id)" @contextmenu="handleContextMenu($event, node.id)" @dragover="onDragOver" @drop="onDrop($event, node.id)">
          <span class="folder-expand-arrow" :class="{ 'folder-expand-placeholder': !node.hasChildren }" @click.stop="toggleExpand(node.id)">
            <t-icon v-if="node.hasChildren" :name="node.isExpanded ? 'chevron-down' : 'chevron-right'" size="14px" />
          </span>
          <t-icon :name="node.isExpanded ? 'folder-open' : 'folder'" size="16px" class="folder-icon" />
          <template v-if="renamingId === node.id">
            <input v-model="renameValue" class="folder-rename-input" @keydown.enter="commitRename" @keydown.escape="cancelRename" @blur="commitRename" @click.stop />
          </template>
          <template v-else>
            <span class="folder-name">{{ node.name }}</span>
            <span class="folder-count">{{ node.knowledgeCount }}</span>
          </template>
        </div>
        <div v-if="isCreating && creatingParentId === node.id" class="folder-create-row" :style="{ paddingLeft: (12 + (node.depth + 1) * 16) + 'px' }">
          <input v-model="createName" class="folder-rename-input" :placeholder="$t('knowledgeBase.folderNamePlaceholder')" @keydown.enter="submitCreate" @keydown.escape="cancelCreate" @blur="cancelCreate" />
        </div>
      </template>
      <div v-if="!loading && !flatNodes.length" class="folder-tree-empty-text">{{ $t('knowledgeBase.folderEmpty') }}</div>
    </div>
    <teleport to="body">
      <div v-if="menuOpen" class="folder-context-menu" :style="{ left: menuPos.x + 'px', top: menuPos.y + 'px' }" @click.stop>
        <div v-if="canEdit" class="folder-menu-item" @click="handleCreateUnder(menuOpen)"><t-icon name="add" size="14px" /><span>{{ $t('knowledgeBase.folderCreateChild') }}</span></div>
        <div v-if="canEdit" class="folder-menu-item" @click="startRename(menuOpen)"><t-icon name="edit" size="14px" /><span>{{ $t('knowledgeBase.folderRename') }}</span></div>
        <div v-if="canEdit" class="folder-menu-item folder-menu-item--danger" @click="handleDelete(menuOpen)"><t-icon name="delete" size="14px" /><span>{{ $t('knowledgeBase.folderDelete') }}</span></div>
      </div>
    </teleport>
  </div>
</template>
<style scoped>
.folder-tree-sidebar { width: 240px; min-width: 200px; max-width: 320px; display: flex; flex-direction: column; border-right: 1px solid var(--td-border-level-1-color, #e7e7e7); background: var(--td-bg-color-container, #fff); height: 100%; overflow: hidden; user-select: none; }
.folder-tree-header { display: flex; align-items: center; justify-content: space-between; padding: 12px 12px 8px; border-bottom: 1px solid var(--td-border-level-1-color, #e7e7e7); flex-shrink: 0; }
.folder-tree-title { font-size: 13px; font-weight: 600; color: var(--td-text-color-primary); }
.folder-tree-actions { display: flex; gap: 2px; }
.folder-tree-action-btn { display: inline-flex; align-items: center; justify-content: center; width: 24px; height: 24px; border: none; background: transparent; border-radius: 4px; cursor: pointer; color: var(--td-text-color-secondary); }
.folder-tree-action-btn:hover { background: var(--td-bg-color-container-hover); color: var(--td-text-color-primary); }
.folder-tree-list { flex: 1; overflow-y: auto; padding: 4px 0; }
.folder-tree-empty { display: flex; justify-content: center; padding: 20px; color: var(--td-text-color-placeholder); }
.folder-tree-empty-text { text-align: center; padding: 20px; font-size: 12px; color: var(--td-text-color-placeholder); }
.folder-tree-item { display: flex; align-items: center; gap: 4px; padding: 6px 12px; cursor: pointer; transition: background 0.15s; font-size: 13px; color: var(--td-text-color-primary); }
.folder-tree-item:hover { background: var(--td-bg-color-container-hover); }
.folder-tree-item.active { background: var(--td-brand-color-light); color: var(--td-brand-color); }
.folder-tree-item.root-item { font-weight: 500; margin-bottom: 4px; border-bottom: 1px solid var(--td-border-level-1-color, #e7e7e7); }
.folder-expand-arrow { width: 16px; height: 16px; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; color: var(--td-text-color-placeholder); cursor: pointer; }
.folder-expand-placeholder { visibility: hidden; cursor: default; }
.folder-icon { flex-shrink: 0; color: var(--td-warning-color); }
.folder-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.folder-count { font-size: 11px; color: var(--td-text-color-placeholder); flex-shrink: 0; min-width: 20px; text-align: right; }
.folder-create-row { padding: 4px 12px 4px 30px; }
.folder-rename-input { width: 100%; padding: 2px 6px; border: 1px solid var(--td-brand-color); border-radius: 4px; font-size: 13px; outline: none; background: var(--td-bg-color-container); color: var(--td-text-color-primary); }
.folder-context-menu { position: fixed; z-index: 10000; background: var(--td-bg-color-container); border: 1px solid var(--td-border-level-1-color, #e7e7e7); border-radius: 8px; box-shadow: 0 4px 16px rgba(0,0,0,0.1); min-width: 160px; padding: 4px 0; }
.folder-menu-item { display: flex; align-items: center; gap: 8px; padding: 8px 16px; font-size: 13px; cursor: pointer; color: var(--td-text-color-primary); }
.folder-menu-item:hover { background: var(--td-bg-color-container-hover); }
.folder-menu-item--danger { color: var(--td-error-color); }
</style>
