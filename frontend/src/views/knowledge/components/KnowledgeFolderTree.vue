<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import FolderActionMenu from './FolderActionMenu.vue';
import {
  createKnowledgeFolder,
  updateKnowledgeFolder,
  type KnowledgeFolder,
} from '@/api/knowledge-base';

interface FolderRow extends KnowledgeFolder {
  level: number;
}

const props = defineProps<{
  knowledgeBaseId: string;
  folders: KnowledgeFolder[];
  loading?: boolean;
  selectedFolderId: string | null;
  canEdit: boolean;
}>();

const emit = defineEmits<{
  (e: 'select', folder: KnowledgeFolder | null): void;
  (e: 'ask', folder: KnowledgeFolder): void;
  (e: 'folder-action', action: 'ask' | 'create-child' | 'rename' | 'move' | 'reparse' | 'delete', folder: KnowledgeFolder): void;
  (e: 'changed'): void;
}>();

const { t } = useI18n();
const editorVisible = ref(false);
const editorMode = ref<'create' | 'rename'>('create');
const editorParentId = ref<string | null>(null);
const editingFolder = ref<KnowledgeFolder | null>(null);
const editorName = ref('');
const saving = ref(false);
const moveVisible = ref(false);
const movingFolder = ref<KnowledgeFolder | null>(null);
const moveParentId = ref('');
const moving = ref(false);
const expanded = ref(new Set<string>());
const folderMenuVisible = reactive<Record<string, boolean>>({});

const handleFolderMenuAction = (
  action: 'ask' | 'create-child' | 'rename' | 'move' | 'reparse' | 'delete',
  folder: KnowledgeFolder,
) => {
  folderMenuVisible[folder.id] = false;
  emit('folder-action', action, folder);
};

const moveOptions = computed(() => {
  const current = movingFolder.value;
  if (!current) return [];
  const blocked = new Set<string>([current.id]);
  let changed = true;
  while (changed) {
    changed = false;
    for (const folder of props.folders) {
      if (folder.parent_folder_id && blocked.has(folder.parent_folder_id) && !blocked.has(folder.id)) {
        blocked.add(folder.id);
        changed = true;
      }
    }
  }
  return [
    { label: t('knowledgeBase.folderRoot'), value: '' },
    ...props.folders
      .filter(folder => !blocked.has(folder.id))
      .map(folder => ({
        label: '  '.repeat(Math.max(0, folder.depth - 1)) + folder.name,
        value: folder.id,
      })),
  ];
});

const rows = computed<FolderRow[]>(() => {
  const children = new Map<string, KnowledgeFolder[]>();
  for (const folder of props.folders) {
    const parent = folder.parent_folder_id || '';
    const siblings = children.get(parent) || [];
    siblings.push(folder);
    children.set(parent, siblings);
  }
  for (const siblings of children.values()) {
    siblings.sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name));
  }
  const output: FolderRow[] = [];
  const visit = (parentId: string, level: number) => {
    for (const folder of children.get(parentId) || []) {
      output.push({ ...folder, level });
      if (expanded.value.has(folder.id)) visit(folder.id, level + 1);
    }
  };
  visit('', 0);
  return output;
});

const toggle = (folder: KnowledgeFolder) => {
  if (!folder.has_children) return;
  if (expanded.value.has(folder.id)) expanded.value.delete(folder.id);
  else expanded.value.add(folder.id);
};

const openCreate = (parent: KnowledgeFolder | null = null) => {
  editorMode.value = 'create';
  editorParentId.value = parent?.id || null;
  editingFolder.value = null;
  editorName.value = '';
  editorVisible.value = true;
};

const openRename = (folder: KnowledgeFolder) => {
  editorMode.value = 'rename';
  editingFolder.value = folder;
  editorParentId.value = folder.parent_folder_id || null;
  editorName.value = folder.name;
  editorVisible.value = true;
};

const openMove = (folder: KnowledgeFolder) => {
  movingFolder.value = folder;
  moveParentId.value = folder.parent_folder_id || '';
  moveVisible.value = true;
};

const moveFolder = async () => {
  if (!movingFolder.value || moving.value) return;
  moving.value = true;
  try {
    await updateKnowledgeFolder(props.knowledgeBaseId, movingFolder.value.id, moveParentId.value
      ? { parent_folder_id: moveParentId.value }
      : { move_to_root: true });
    moveVisible.value = false;
    emit('changed');
    MessagePlugin.success(t('knowledgeBase.folderMoved'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('knowledgeBase.folderMoveFolderFailed'));
  } finally {
    moving.value = false;
  }
};

const saveFolder = async () => {
  const name = editorName.value.trim();
  if (!name || saving.value) return;
  saving.value = true;
  try {
    if (editorMode.value === 'rename' && editingFolder.value) {
      await updateKnowledgeFolder(props.knowledgeBaseId, editingFolder.value.id, { name });
    } else {
      await createKnowledgeFolder(props.knowledgeBaseId, {
        parent_folder_id: editorParentId.value,
        name,
      });
      if (editorParentId.value) expanded.value.add(editorParentId.value);
    }
    editorVisible.value = false;
    emit('changed');
    MessagePlugin.success(t('knowledgeBase.folderSaved'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('knowledgeBase.folderSaveFailed'));
  } finally {
    saving.value = false;
  }
};

watch(
  () => props.folders,
  (folders) => {
    for (const folder of folders) {
      if (folder.has_children) expanded.value.add(folder.id);
    }
    if (props.selectedFolderId && !folders.some(folder => folder.id === props.selectedFolderId)) {
      emit('select', null);
    }
  },
  { immediate: true },
);

defineExpose({ createChild: openCreate, rename: openRename, move: openMove });
</script>

<template>
  <aside class="folder-panel" aria-label="Knowledge folders">
    <div class="folder-panel__header">
      <span class="folder-panel__title">{{ t('knowledgeBase.folders') }}</span>
      <t-tooltip v-if="canEdit" :content="t('knowledgeBase.folderCreateRoot')">
        <t-button shape="square" variant="text" size="small" @click="openCreate(null)">
          <t-icon name="add" />
        </t-button>
      </t-tooltip>
    </div>

    <div class="folder-panel__body">
      <button
        type="button"
        class="folder-row folder-row--root"
        :class="{ active: selectedFolderId === null }"
        @click="emit('select', null)"
      >
        <span class="folder-row__indent" />
        <t-icon name="folder-open" class="folder-row__icon" />
        <span class="folder-row__name">{{ t('knowledgeBase.folderRoot') }}</span>
      </button>

      <t-loading v-if="loading" size="small" class="folder-loading" />
      <template v-else>
        <div
          v-for="folder in rows"
          :key="folder.id"
          class="folder-row-wrap"
          :style="{ '--folder-level': folder.level }"
        >
          <button
            type="button"
            class="folder-row"
            :class="{ active: selectedFolderId === folder.id }"
            @click="emit('select', folder)"
          >
            <span class="folder-row__indent" />
            <span class="folder-row__toggle" @click.stop="toggle(folder)">
              <t-icon v-if="folder.has_children" :name="expanded.has(folder.id) ? 'chevron-down' : 'chevron-right'" />
            </span>
            <t-icon :name="expanded.has(folder.id) ? 'folder-open' : 'folder'" class="folder-row__icon" />
            <span class="folder-row__name" :title="folder.name">{{ folder.name }}</span>
            <span class="folder-row__count">{{ folder.recursive_knowledge_count }}</span>
          </button>
          <t-popup
            v-if="canEdit"
            v-model="folderMenuVisible[folder.id]"
            trigger="click"
            placement="bottom-right"
            attach="body"
            destroy-on-close
          >
            <t-button
              shape="square"
              variant="text"
              size="small"
              class="folder-row__menu"
              @click.stop
            >
              <t-icon name="more" />
            </t-button>
            <template #content>
              <FolderActionMenu :folder="folder" @action="handleFolderMenuAction" />
            </template>
          </t-popup>
          <t-tooltip v-else :content="t('knowledgeBase.folderAsk')">
            <t-button shape="square" variant="text" size="small" class="folder-row__menu" @click.stop="emit('ask', folder)">
              <t-icon name="chat-message" />
            </t-button>
          </t-tooltip>
        </div>
        <div v-if="folders.length === 0" class="folder-empty">{{ t('knowledgeBase.folderEmpty') }}</div>
      </template>
    </div>

    <t-dialog
      v-model:visible="editorVisible"
      :header="editorMode === 'rename' ? t('knowledgeBase.folderRename') : t('knowledgeBase.folderCreate')"
      :confirm-btn="{ content: t('common.confirm'), loading: saving, disabled: !editorName.trim() }"
      :cancel-btn="t('common.cancel')"
      width="420px"
      @confirm="saveFolder"
    >
      <t-input v-model="editorName" :maxlength="255" :placeholder="t('knowledgeBase.folderNamePlaceholder')" autofocus @enter="saveFolder" />
    </t-dialog>

    <t-dialog
      v-model:visible="moveVisible"
      :header="t('knowledgeBase.folderRelocateTitle')"
      :confirm-btn="{ content: t('common.confirm'), loading: moving }"
      :cancel-btn="t('common.cancel')"
      width="420px"
      @confirm="moveFolder"
    >
      <t-select v-model="moveParentId" :options="moveOptions" filterable />
    </t-dialog>
  </aside>
</template>

<style scoped lang="less">
.folder-panel {
  width: 240px;
  min-width: 200px;
  max-width: 280px;
  border-right: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding-right: 12px;
}
.folder-panel__header { display: flex; align-items: center; justify-content: space-between; height: 36px; padding: 0 4px 8px 10px; }
.folder-panel__title { font-size: 13px; font-weight: 600; color: var(--td-text-color-primary); }
.folder-panel__body { min-height: 0; overflow: auto; }
.folder-row-wrap { position: relative; display: flex; align-items: center; }
.folder-row {
  width: 100%; height: 34px; border: 0; border-radius: 6px; background: transparent; color: var(--td-text-color-secondary);
  display: flex; align-items: center; min-width: 0; padding: 0 30px 0 8px; cursor: pointer; text-align: left;
}
.folder-row:hover { background: var(--td-bg-color-container-hover); color: var(--td-text-color-primary); }
.folder-row.active { background: var(--td-brand-color-light); color: var(--td-brand-color); }
.folder-row--root { padding-right: 8px; }
.folder-row__indent { width: calc(var(--folder-level, 0) * 16px); flex: 0 0 auto; }
.folder-row__toggle { width: 18px; height: 24px; display: inline-flex; align-items: center; justify-content: center; flex: 0 0 auto; }
.folder-row__toggle :deep(.t-icon) { font-size: 14px; }
.folder-row__icon { flex: 0 0 auto; margin-right: 6px; }
.folder-row__name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; }
.folder-row__count { flex: 0 0 auto; margin-left: 6px; font-size: 11px; color: var(--td-text-color-placeholder); }
.folder-row__menu { position: absolute; right: 2px; opacity: 0; }
.folder-row-wrap:hover .folder-row__menu, .folder-row__menu:focus { opacity: 1; }
.folder-loading { display: flex; justify-content: center; padding: 24px 0; }
.folder-empty { padding: 16px 12px; color: var(--td-text-color-placeholder); font-size: 12px; text-align: center; }
@media (max-width: 900px) {
  .folder-panel { width: 190px; min-width: 170px; }
}
</style>
