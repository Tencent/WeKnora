<script setup lang="ts">
import { ref, nextTick } from 'vue';
import { useI18n } from 'vue-i18n';
import KnowledgeFolderActions from './KnowledgeFolderActions.vue';

export interface KnowledgeFolderTreeNodeData {
  id: string;
  name: string;
  depth: number;
  knowledgeCount: number;
  hasChildren: boolean;
  expanded: boolean;
  loaded: boolean;
  loading: boolean;
  children: KnowledgeFolderTreeNodeData[];
}

const props = defineProps<{
  node: KnowledgeFolderTreeNodeData;
  kbId: string;
  canEdit: boolean;
  selectedFolderId: string;
}>();

const emit = defineEmits<{
  (e: 'select', id: string): void;
  (e: 'toggle', node: KnowledgeFolderTreeNodeData): void;
  (e: 'create', parentId: string, name: string): void;
  (e: 'rename', folderId: string, name: string): void;
  (e: 'move', folderId: string): void;
  (e: 'delete', folderId: string): void;
}>();

const { t } = useI18n();

const editing = ref(false);
const editingName = ref('');
const editingInputRef = ref<{ focus: () => void } | null>(null);

function startRename() {
  editing.value = true;
  editingName.value = props.node.name;
  nextTick(() => editingInputRef.value?.focus());
}

function commitRename() {
  const value = editingName.value.trim();
  editing.value = false;
  if (value && value !== props.node.name) {
    emit('rename', props.node.id, value);
  }
}

function cancelRename() {
  editing.value = false;
}

function onClick() {
  if (editing.value) return;
  emit('select', props.node.id);
}
</script>

<template>
  <div class="kf-node" :class="{ active: selectedFolderId === node.id }" :style="{ '--kf-depth': node.depth }">
    <div class="kf-row" @click="onClick">
      <span
        class="kf-toggle"
        :class="{ 'kf-toggle--leaf': !node.hasChildren }"
        @click.stop="emit('toggle', node)"
      >
        <t-icon :name="node.expanded ? 'chevron-down' : 'chevron-right'" />
      </span>

      <span class="kf-folder-icon">
        <t-icon name="folder" />
      </span>

      <input
        v-if="editing"
        ref="editingInputRef"
        v-model="editingName"
        class="kf-rename-input"
        :placeholder="t('knowledgeBase.folderTreeTitle')"
        @click.stop
        @keydown.enter="commitRename"
        @keydown.esc="cancelRename"
        @blur="commitRename"
      />
      <template v-else>
        <span class="kf-name" :title="node.name">{{ node.name }}</span>
        <span v-if="node.knowledgeCount > 0" class="kf-count">{{ node.knowledgeCount }}</span>
      </template>

      <span v-if="canEdit && !editing" class="kf-actions">
        <KnowledgeFolderActions
          :name="node.name"
          :page-count="node.knowledgeCount"
          :has-children="node.hasChildren"
          @create="(name: string) => emit('create', node.id, name)"
          @rename="startRename"
          @move="() => emit('move', node.id)"
          @delete="() => emit('delete', node.id)"
        />
      </span>
    </div>

    <div v-if="node.expanded" v-show="node.children.length || node.loading" class="kf-children">
      <t-loading v-if="node.loading" size="small" class="kf-loading" />
      <KnowledgeFolderTreeNode
        v-for="child in node.children"
        :key="child.id"
        :node="child"
        :kb-id="kbId"
        :can-edit="canEdit"
        :selected-folder-id="selectedFolderId"
        @select="(id: string) => emit('select', id)"
        @toggle="(n: KnowledgeFolderTreeNodeData) => emit('toggle', n)"
        @create="(pid: string, name: string) => emit('create', pid, name)"
        @rename="(fid: string, name: string) => emit('rename', fid, name)"
        @move="(fid: string) => emit('move', fid)"
        @delete="(fid: string) => emit('delete', fid)"
      />
    </div>
  </div>
</template>

<style scoped lang="less">
.kf-node {
  display: flex;
  flex-direction: column;
}

.kf-row {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 32px;
  padding: 0 8px 0 calc(8px + var(--kf-depth) * 14px);
  border-radius: 6px;
  cursor: pointer;
  user-select: none;
  transition: background-color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  .kf-toggle {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    color: var(--td-text-color-placeholder);
    font-size: 14px;

    &--leaf {
      opacity: 0;
      pointer-events: none;
    }
  }

  .kf-folder-icon {
    flex: 0 0 auto;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    color: var(--td-text-color-secondary);
    font-size: 16px;
  }

  .kf-name {
    flex: 1 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 13px;
    color: var(--td-text-color-primary);
  }

  .kf-count {
    flex: 0 0 auto;
    margin-left: 6px;
    padding: 0 6px;
    height: 18px;
    line-height: 18px;
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 9px;
  }

  .kf-actions {
    flex: 0 0 auto;
    margin-left: 4px;
    opacity: 0;
    transition: opacity 0.15s ease;
  }

  &:hover .kf-actions {
    opacity: 1;
  }
}

.kf-node.active > .kf-row {
  background: var(--td-brand-color-light);
  .kf-name {
    color: var(--td-brand-color);
    font-weight: 500;
  }
  .kf-folder-icon {
    color: var(--td-brand-color);
  }
  .kf-actions {
    opacity: 1;
  }
}

.kf-rename-input {
  flex: 1 1 auto;
  min-width: 0;
  height: 24px;
  padding: 0 6px;
  font-size: 13px;
  border: 1px solid var(--td-brand-color);
  border-radius: 4px;
  outline: none;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
}

.kf-children {
  display: flex;
  flex-direction: column;
}

.kf-loading {
  margin: 6px 0 6px 28px;
}
</style>
