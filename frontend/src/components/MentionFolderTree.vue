<script setup lang="ts">
import { ref, computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { knowledgeFolderApi } from '@/api/knowledge-base/folders';
import { buildFolderIndex, folderPathLabel } from '@/views/knowledge/folderModel';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

// MentionFolderTree renders a per-KB folder tree inside the @ panel's "folder"
// group. KBs are non-selectable parent rows (lazy-loaded on expand); folder
// leaves are single-select: click = emit pick + caller closes the panel.
// Independent of selectedKnowledgeBases: lists ALL mentionable KBs passed in.

const props = defineProps<{
  kbs: Array<{ id: string; name: string; orgName?: string }>;
  rootLabel?: string;
}>();

const emit = defineEmits<{
  (e: 'pick', folder: { id: string; name: string; kbId: string; folderPath: string }): void;
}>();

const { t } = useI18n();

// per-KB tree cache: kbId -> tree (or null if loaded-but-empty / errored)
const treeByKb = ref<Record<string, KnowledgeFolder[] | null>>({});
const expandedKbs = ref<Set<string>>(new Set());
const expandedFolders = ref<Set<string>>(new Set());
const loadingKb = ref<Set<string>>(new Set());

async function toggleKb(kbId: string) {
  if (expandedKbs.value.has(kbId)) {
    expandedKbs.value.delete(kbId);
    return;
  }
  expandedKbs.value.add(kbId);
  if (treeByKb.value[kbId] === undefined) {
    loadingKb.value.add(kbId);
    try {
      const tree = await knowledgeFolderApi.tree(kbId);
      treeByKb.value[kbId] = tree || [];
    } catch {
      treeByKb.value[kbId] = null; // errored
    } finally {
      loadingKb.value.delete(kbId);
    }
  }
}

function toggleFolder(id: string) {
  if (expandedFolders.value.has(id)) expandedFolders.value.delete(id);
  else expandedFolders.value.add(id);
}

interface FlatNode {
  kind: 'kb' | 'folder';
  kbId: string;
  kbName: string;
  folder?: KnowledgeFolder;
  level: number;
  hasChildren: boolean;
  isOpen: boolean;
  isLoading: boolean;
  errored: boolean;
}

const flatNodes = computed<FlatNode[]>(() => {
  const out: FlatNode[] = [];
  for (const kb of props.kbs) {
    const open = expandedKbs.value.has(kb.id);
    const tree = treeByKb.value[kb.id];
    const loading = loadingKb.value.has(kb.id);
    out.push({
      kind: 'kb', kbId: kb.id, kbName: kb.name, level: 0,
      hasChildren: true, isOpen: open, isLoading: loading, errored: tree === null,
    });
    if (open && tree && tree.length) {
      const visit = (nodes: KnowledgeFolder[], level: number) => {
        for (const n of nodes) {
          const childOpen = expandedFolders.value.has(n.id);
          out.push({
            kind: 'folder', kbId: kb.id, kbName: kb.name, folder: n, level,
            hasChildren: !!(n.children && n.children.length),
            isOpen: childOpen, isLoading: false, errored: false,
          });
          if (childOpen && n.children) visit(n.children, level + 1);
        }
      };
      visit(tree, 1);
    }
  }
  return out;
});

function pickFolder(n: FlatNode) {
  if (!n.folder) return;
  const index = buildFolderIndex(treeByKb.value[n.kbId] || []);
  const path = folderPathLabel(index, n.folder.id, props.rootLabel || '');
  emit('pick', { id: n.folder.id, name: n.folder.name, kbId: n.kbId, folderPath: path });
}

function toggleNode(n: FlatNode) {
  if (n.kind === 'kb') toggleKb(n.kbId);
  else if (n.hasChildren && n.folder) toggleFolder(n.folder.id);
}
</script>

<template>
  <div class="mention-folder-tree" role="tree">
    <div v-if="kbs.length === 0" class="folder-empty">{{ t('mentionDetail.folderEmpty') }}</div>
    <div
      v-for="(n, i) in flatNodes"
      :key="`${n.kbId}:${n.folder?.id || 'kb'}`"
      class="ft-row"
      :class="{ 'is-kb': n.kind === 'kb', 'is-folder': n.kind === 'folder' }"
      :style="{ paddingLeft: 4 + n.level * 16 + 'px' }"
      @click="n.kind === 'folder' ? pickFolder(n) : toggleNode(n)"
    >
      <span class="ft-chevron" :class="{ open: n.isOpen, leaf: !n.hasChildren }" @click.stop="toggleNode(n)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4"><path d="M9 6l6 6-6 6"/></svg>
      </span>
      <template v-if="n.kind === 'kb'">
        <svg class="ft-kb-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
        <span class="ft-name">{{ n.kbName }}</span>
        <span v-if="n.isLoading" class="ft-loading">…</span>
        <span v-else-if="n.errored" class="ft-error">无权限</span>
      </template>
      <template v-else>
        <svg class="ft-folder-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
        <span class="ft-name" :title="n.folder?.name">{{ n.folder?.name }}</span>
      </template>
    </div>
  </div>
</template>

<style scoped lang="less">
.mention-folder-tree { padding: 4px; }
.ft-row {
  display: flex; align-items: center; gap: 8px; height: 32px;
  padding: 0 8px 0 4px; border-radius: 6px; cursor: pointer;
  transition: background 0.12s;
}
.ft-row:hover { background: var(--td-bg-color-secondarycontainer-hover); }
.ft-row.is-kb { font-weight: 500; }
.ft-row.is-folder:hover { background: var(--td-brand-color-light); }
.ft-chevron {
  width: 16px; height: 16px; display: inline-flex; align-items: center; justify-content: center;
  color: var(--td-text-color-secondary); transition: transform 0.15s; flex-shrink: 0;
}
.ft-chevron.open { transform: rotate(90deg); }
.ft-chevron.leaf { visibility: hidden; }
.ft-kb-icon, .ft-folder-icon { width: 16px; height: 16px; flex-shrink: 0; }
.ft-kb-icon { color: var(--td-text-color-secondary); }
.ft-folder-icon { color: var(--td-brand-color); }
.ft-name { flex: 1; font-size: 13px; color: var(--td-text-color-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ft-loading, .ft-error { font-size: 11px; color: var(--td-text-color-placeholder); }
.folder-empty { padding: 12px 8px; font-size: 12px; color: var(--td-text-color-placeholder); text-align: center; }
</style>
