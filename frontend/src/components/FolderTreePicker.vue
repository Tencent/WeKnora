<template>
  <div class="folder-tree-picker">
    <div class="ftp-search">
      <t-input
        v-model="filterText"
        :placeholder="$t('knowledge.folder.searchPlaceholder') || '搜索文件夹'"
        clearable
        size="small"
      >
        <template #prefix-icon>
          <t-icon name="search" />
        </template>
      </t-input>
    </div>

    <div class="ftp-body">
      <div v-if="!kbs.length" class="ftp-empty">
        {{ emptyHint || $t('chat.noKnowledgeBase') || '请先选择知识库' }}
      </div>

      <div v-for="kb in kbs" :key="kb.id" class="ftp-kb">
        <div class="ftp-kb-header" @click="toggleKb(kb.id)">
          <span class="ftp-kb-chevron" :class="{ collapsed: collapsedKbs.has(kb.id) }">
            <t-icon name="chevron-right" />
          </span>
          <t-icon name="root-list" class="ftp-kb-icon" />
          <span class="ftp-kb-name" :title="kb.name">{{ kb.name }}</span>
          <t-loading v-if="loadingKbs.has(kb.id)" size="small" class="ftp-kb-loading" />
        </div>

        <template v-if="!collapsedKbs.has(kb.id)">
          <div
            v-if="!trees[kb.id] && !loadingKbs.has(kb.id)"
            class="ftp-kb-hint"
          >{{ $t('knowledge.folder.empty') || '暂无文件夹' }}</div>

          <div
            v-for="node in visibleNodes(kb.id)"
            :key="node.id"
            class="ftp-folder"
            :class="{ active: selectedFolderIds.includes(node.id), 'is-leaf': !node.hasChildren }"
            :style="{ paddingLeft: indent(node) }"
            @click="onSelect(node)"
          >
            <span
              class="ftp-folder-toggle"
              :class="{ 'is-leaf': !node.hasChildren }"
              @click.stop="toggleFolder(node.id)"
            >
              <t-icon :name="expandedIds.has(node.id) ? 'chevron-down' : 'chevron-right'" />
            </span>
            <t-icon name="folder" class="ftp-folder-icon" />
            <span class="ftp-folder-name" :title="node.name">{{ node.name }}</span>
            <span v-if="node.knowledgeCount" class="ftp-folder-count">{{ node.knowledgeCount }}</span>
            <t-icon v-if="selectedFolderIds.includes(node.id)" name="check" class="ftp-folder-check" />
          </div>
        </template>
      </div>
    </div>

    <div class="ftp-footer">
      <span class="ftp-footer-hint">{{ $t('knowledge.folder.pickerHint') || '点击文件夹即可加入提问范围' }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { listAllKnowledgeFolders } from '@/api/knowledge-base';

interface FolderNode {
  id: string;
  name: string;
  kbId: string;
  kbName: string;
  parentId: string;
  depth: number;
  hasChildren: boolean;
  knowledgeCount: number;
  children: FolderNode[];
}

const props = withDefaults(defineProps<{
  kbs: { id: string; name: string }[];
  selectedFolderIds?: string[];
  emptyHint?: string;
}>(), {
  selectedFolderIds: () => [],
  emptyHint: '',
});

const emit = defineEmits<{
  (e: 'select', item: { id: string; name: string; kbId: string; kbName: string; parentId: string; depth: number; hasChildren: boolean }): void;
}>();

const { t } = useI18n();

const trees = ref<Record<string, FolderNode[]>>({});
const loadingKbs = ref<Set<string>>(new Set());
const expandedIds = ref<Set<string>>(new Set());
const collapsedKbs = ref<Set<string>>(new Set());
const filterText = ref('');

let loadToken = 0;

function buildTree(flat: FolderNode[]): FolderNode[] {
  const map = new Map<string, FolderNode>();
  flat.forEach((n) => map.set(n.id, { ...n, children: [] }));
  const roots: FolderNode[] = [];
  map.forEach((n) => {
    if (n.parentId && map.has(n.parentId)) {
      map.get(n.parentId)!.children.push(n);
    } else {
      roots.push(n);
    }
  });
  const sortRec = (nodes: FolderNode[]) => {
    nodes.sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }));
    nodes.forEach((n) => sortRec(n.children));
  };
  sortRec(roots);
  return roots;
}

function flatten(nodes: FolderNode[], expanded: Set<string>, out: FolderNode[]) {
  for (const n of nodes) {
    out.push(n);
    if (expanded.has(n.id) && n.children.length) flatten(n.children, expanded, out);
  }
}

async function loadKb(kb: { id: string; name: string }, token: number) {
  loadingKbs.value = new Set(loadingKbs.value).add(kb.id);
  try {
    const res: any = await listAllKnowledgeFolders(kb.id);
    if (token !== loadToken) return;
    const payload = res?.data ?? res;
    const list = Array.isArray(payload?.data?.folders)
      ? payload.data.folders
      : Array.isArray(payload?.folders)
        ? payload.folders
        : [];
    const flat: FolderNode[] = list.map((f: any) => ({
      id: f.id,
      name: f.name,
      kbId: kb.id,
      kbName: kb.name,
      parentId: f.parent_id || '',
      depth: typeof f.depth === 'number' ? f.depth : 0,
      hasChildren: !!f.has_children,
      knowledgeCount: Number(f.knowledge_count || 0),
      children: [],
    }));
    const built = buildTree(flat);
    trees.value = { ...trees.value, [kb.id]: built };
    // 默认展开根层文件夹，便于一眼看到结构
    const rootIds = built.filter((n) => n.parentId === '' || n.depth === 0).map((n) => n.id);
    expandedIds.value = new Set([...expandedIds.value, ...rootIds]);
  } catch (e) {
    console.error('[FolderTreePicker] loadKb error', kb.id, e);
  } finally {
    if (token === loadToken) {
      const next = new Set(loadingKbs.value);
      next.delete(kb.id);
      loadingKbs.value = next;
    }
  }
}

watch(
  () => props.kbs,
  (kbs) => {
    loadToken += 1;
    const token = loadToken;
    kbs.forEach((kb) => {
      if (!trees.value[kb.id]) loadKb(kb, token);
    });
  },
  { immediate: true, deep: true }
);

function toggleKb(kbId: string) {
  const next = new Set(collapsedKbs.value);
  if (next.has(kbId)) next.delete(kbId);
  else next.add(kbId);
  collapsedKbs.value = next;
}

function toggleFolder(folderId: string) {
  const next = new Set(expandedIds.value);
  if (next.has(folderId)) next.delete(folderId);
  else next.add(folderId);
  expandedIds.value = next;
}

function collectAll(nodes: FolderNode[], out: FolderNode[]) {
  for (const n of nodes) {
    out.push(n);
    if (n.children.length) collectAll(n.children, out);
  }
}

function visibleNodes(kbId: string): FolderNode[] {
  const tree = trees.value[kbId];
  if (!tree) return [];
  if (filterText.value.trim()) {
    const kw = filterText.value.trim().toLowerCase();
    const all: FolderNode[] = [];
    collectAll(tree, all);
    return all.filter((n) => n.name.toLowerCase().includes(kw));
  }
  const out: FolderNode[] = [];
  flatten(tree, expandedIds.value, out);
  return out;
}

function indent(node: FolderNode) {
  // KB 头已占一层，文件夹按 depth 再右移
  return `${10 + (node.depth || 0) * 16}px`;
}

function onSelect(node: FolderNode) {
  emit('select', {
    id: node.id,
    name: node.name,
    kbId: node.kbId,
    kbName: node.kbName,
    parentId: node.parentId,
    depth: node.depth,
    hasChildren: node.hasChildren,
  });
}
</script>

<style scoped>
.folder-tree-picker {
  width: 300px;
  max-height: 380px;
  display: flex;
  flex-direction: column;
  font-size: 13px;
  color: var(--td-text-color-primary, #1a1a1a);
}

.ftp-search {
  padding: 8px 10px 6px;
  border-bottom: 1px solid var(--td-component-stroke, #e7e7e7);
}

.ftp-body {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}

.ftp-empty,
.ftp-kb-hint {
  padding: 10px 14px;
  color: var(--td-text-color-placeholder, #999);
  font-size: 12px;
}

.ftp-kb-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 10px;
  cursor: pointer;
  font-weight: 600;
  background: var(--td-bg-color-page, #f3f3f3);
}

.ftp-kb-chevron {
  display: inline-flex;
  transition: transform 0.15s ease;
  color: var(--td-text-color-placeholder, #999);
}

.ftp-kb-chevron.collapsed {
  transform: rotate(0deg);
}

.ftp-kb-chevron:not(.collapsed) {
  transform: rotate(90deg);
}

.ftp-kb-icon {
  color: var(--td-brand-color, #0052d9);
}

.ftp-kb-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ftp-kb-loading {
  margin-left: 4px;
}

.ftp-folder {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 5px 10px;
  cursor: pointer;
  border-radius: 4px;
}

.ftp-folder:hover {
  background: var(--td-bg-color-container-hover, #f0f0f0);
}

.ftp-folder.active {
  background: var(--td-brand-color-light, #e0ecff);
  color: var(--td-brand-color, #0052d9);
}

.ftp-folder-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  color: var(--td-text-color-placeholder, #999);
  flex-shrink: 0;
}

.ftp-folder-toggle.is-leaf {
  visibility: hidden;
}

.ftp-folder-icon {
  color: var(--td-text-color-secondary, #666);
  flex-shrink: 0;
}

.ftp-folder.active .ftp-folder-icon {
  color: var(--td-brand-color, #0052d9);
}

.ftp-folder-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ftp-folder-count {
  font-size: 11px;
  color: var(--td-text-color-placeholder, #999);
}

.ftp-folder-check {
  color: var(--td-brand-color, #0052d9);
  flex-shrink: 0;
}

.ftp-footer {
  padding: 6px 10px;
  border-top: 1px solid var(--td-component-stroke, #e7e7e7);
}

.ftp-footer-hint {
  font-size: 11px;
  color: var(--td-text-color-placeholder, #999);
}
</style>
