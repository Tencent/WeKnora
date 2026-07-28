<script setup lang="ts">
import { ref, watch, computed } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  listKnowledgeFolders,
  createKnowledgeFolder,
  updateKnowledgeFolder,
  deleteKnowledgeFolder,
  organizeKnowledgeFoldersByPath,
  type KnowledgeFolder,
  type KnowledgeFolderNode,
} from '@/api/knowledge-base';
import FolderTreePicker from './FolderTreePicker.vue';

// 多级文件夹导航条（#1311）：面包屑 + 当前层级的子文件夹 chips。
// 文档列表本身由父组件按 modelValue（当前文件夹 ID）过滤。
const props = defineProps<{
  kbId: string;
  modelValue: string; // 当前文件夹 ID（'' = 根目录）
  canEdit: boolean;
  // 搜索/筛选激活时隐藏 chips（此时列表是全树递归搜索）
  filtersActive?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', folderId: string): void;
  (e: 'changed'): void; // 文件夹树或文档归属变化，父组件需刷新文档列表
}>();

const { t } = useI18n();
const router = useRouter();

const crumbs = ref<KnowledgeFolder[]>([]); // 祖先链，当前文件夹在末尾；空 = 根
const children = ref<KnowledgeFolderNode[]>([]);
const loading = ref(false);
const menuOpenId = ref<string | null>(null);

const currentName = computed(() =>
  crumbs.value.length ? crumbs.value[crumbs.value.length - 1].name : t('knowledgeBase.folder.root'),
);

async function loadChildren() {
  if (!props.kbId) return;
  loading.value = true;
  try {
    const res: any = await listKnowledgeFolders(props.kbId, {
      parent_id: props.modelValue || undefined,
    });
    children.value = (res?.data || []) as KnowledgeFolderNode[];
  } catch (e) {
    console.error('Failed to load folders', e);
    children.value = [];
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.kbId,
  () => {
    crumbs.value = [];
    if (props.modelValue !== '') emit('update:modelValue', '');
    loadChildren();
  },
  { immediate: true },
);
watch(() => props.modelValue, loadChildren);

function enterFolder(folder: KnowledgeFolder) {
  crumbs.value = [...crumbs.value, folder];
  emit('update:modelValue', folder.id);
}

function navigateToCrumb(index: number) {
  // index === -1 表示根目录
  crumbs.value = index < 0 ? [] : crumbs.value.slice(0, index + 1);
  emit('update:modelValue', index < 0 ? '' : crumbs.value[crumbs.value.length - 1].id);
}

// --- 新建 / 重命名 ---
const nameDialogVisible = ref(false);
const nameDialogMode = ref<'create' | 'rename'>('create');
const nameDialogTarget = ref<KnowledgeFolder | null>(null);
const nameInput = ref('');
const nameSubmitting = ref(false);

function openCreateDialog() {
  nameDialogMode.value = 'create';
  nameDialogTarget.value = null;
  nameInput.value = '';
  nameDialogVisible.value = true;
}

function openRenameDialog(folder: KnowledgeFolder) {
  nameDialogMode.value = 'rename';
  nameDialogTarget.value = folder;
  nameInput.value = folder.name;
  nameDialogVisible.value = true;
  menuOpenId.value = null;
}

async function submitNameDialog() {
  const name = nameInput.value.trim();
  if (!name) return;
  nameSubmitting.value = true;
  try {
    if (nameDialogMode.value === 'create') {
      await createKnowledgeFolder(props.kbId, { name, parent_id: props.modelValue || undefined });
      MessagePlugin.success(t('knowledgeBase.folder.created'));
    } else if (nameDialogTarget.value) {
      await updateKnowledgeFolder(props.kbId, nameDialogTarget.value.id, { name });
      MessagePlugin.success(t('knowledgeBase.folder.renamed'));
    }
    nameDialogVisible.value = false;
    await loadChildren();
    emit('changed');
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('common.operationFailed'));
  } finally {
    nameSubmitting.value = false;
  }
}

// --- 移动文件夹 ---
const movePickerVisible = ref(false);
const moveTarget = ref<KnowledgeFolder | null>(null);

function openMovePicker(folder: KnowledgeFolder) {
  moveTarget.value = folder;
  movePickerVisible.value = true;
  menuOpenId.value = null;
}

async function onMoveConfirm(folderId: string) {
  if (!moveTarget.value) return;
  try {
    await updateKnowledgeFolder(props.kbId, moveTarget.value.id, {
      parent_id: folderId,
      move_parent: true,
    });
    MessagePlugin.success(t('knowledgeBase.folder.moved'));
    await loadChildren();
    emit('changed');
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('common.operationFailed'));
  }
}

// --- 删除 ---
const deleteDialogVisible = ref(false);
const deleteTarget = ref<KnowledgeFolderNode | null>(null);
const deletePromote = ref(false);
const deleteSubmitting = ref(false);

function openDeleteDialog(folder: KnowledgeFolderNode) {
  deleteTarget.value = folder;
  // 非空文件夹默认勾选"内容上移"，空文件夹直接删
  deletePromote.value = folder.knowledge_count > 0 || folder.has_children;
  deleteDialogVisible.value = true;
  menuOpenId.value = null;
}

async function submitDelete() {
  if (!deleteTarget.value) return;
  deleteSubmitting.value = true;
  try {
    await deleteKnowledgeFolder(props.kbId, deleteTarget.value.id, deletePromote.value ? 'promote' : undefined);
    MessagePlugin.success(t('knowledgeBase.folder.deleted'));
    deleteDialogVisible.value = false;
    await loadChildren();
    emit('changed');
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('knowledgeBase.folder.deleteNotEmpty'));
  } finally {
    deleteSubmitting.value = false;
  }
}

// --- 按路径整理存量文档 ---
const organizing = ref(false);
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
    await loadChildren();
    emit('changed');
  } catch (e: any) {
    MessagePlugin.error(e?.error?.message || e?.message || t('common.operationFailed'));
  } finally {
    organizing.value = false;
  }
}

// --- 在此文件夹内问答 ---
function askInFolder(folder: KnowledgeFolder) {
  menuOpenId.value = null;
  router.push({
    name: 'kbCreatChat',
    params: { kbId: props.kbId },
    query: { folder_id: folder.id, folder_name: folder.name },
  });
}
</script>

<template>
  <div class="kb-folder-bar">
    <div class="folder-breadcrumb" role="navigation">
      <button type="button" class="crumb" :class="{ current: !crumbs.length }" @click="navigateToCrumb(-1)">
        <t-icon name="home" size="14px" />
        <span>{{ t('knowledgeBase.folder.root') }}</span>
      </button>
      <template v-for="(crumb, index) in crumbs" :key="crumb.id">
        <t-icon name="chevron-right" size="14px" class="crumb-sep" />
        <button type="button" class="crumb" :class="{ current: index === crumbs.length - 1 }"
          @click="navigateToCrumb(index)">{{ crumb.name }}</button>
      </template>

      <div class="folder-bar-actions" v-if="canEdit">
        <t-tooltip :content="t('knowledgeBase.folder.new')" placement="top">
          <button type="button" class="folder-bar-btn" @click="openCreateDialog">
            <t-icon name="folder-add" size="15px" />
          </button>
        </t-tooltip>
        <t-popconfirm v-if="!crumbs.length" theme="default" :content="t('knowledgeBase.folder.organizeConfirm')"
          placement="bottom" @confirm="runOrganize">
          <t-tooltip :content="t('knowledgeBase.folder.organizeByPath')" placement="top">
            <button type="button" class="folder-bar-btn" :disabled="organizing">
              <t-icon :name="organizing ? 'loading' : 'folder-import'" size="15px"
                :class="{ 'folder-icon-spin': organizing }" />
            </button>
          </t-tooltip>
        </t-popconfirm>
      </div>
    </div>

    <div v-if="!filtersActive && (children.length || loading)" class="folder-chip-row">
      <div v-for="node in children" :key="node.id" class="folder-chip" :class="{ 'menu-open': menuOpenId === node.id }"
        @click="enterFolder(node)">
        <t-icon name="folder" class="folder-chip-icon" />
        <span class="folder-chip-name" :title="node.path">{{ node.name }}</span>
        <span class="folder-chip-count" v-if="node.knowledge_count">{{ node.knowledge_count }}</span>
        <t-popup v-if="canEdit" placement="bottom-right" trigger="click" destroy-on-close
          overlay-class-name="card-more"
          :visible="menuOpenId === node.id"
          @visible-change="(v: boolean) => (menuOpenId = v ? node.id : null)">
          <button type="button" class="folder-chip-more" @click.stop
            :aria-label="t('knowledgeBase.columnActions')">
            <t-icon name="more" size="14px" />
          </button>
          <template #content>
            <div class="card-menu">
              <div class="card-menu-item" @click.stop="askInFolder(node)">
                <t-icon class="icon" name="chat" />{{ t('knowledgeBase.folder.askInFolder') }}
              </div>
              <div class="card-menu-item" @click.stop="openRenameDialog(node)">
                <t-icon class="icon" name="edit" />{{ t('knowledgeBase.folder.rename') }}
              </div>
              <div class="card-menu-item" @click.stop="openMovePicker(node)">
                <t-icon class="icon" name="folder-move" />{{ t('knowledgeBase.folder.moveTo') }}
              </div>
              <div class="card-menu-item danger" @click.stop="openDeleteDialog(node)">
                <t-icon class="icon" name="delete" />{{ t('knowledgeBase.folder.delete') }}
              </div>
            </div>
          </template>
        </t-popup>
      </div>
    </div>

    <!-- 新建 / 重命名对话框 -->
    <t-dialog :visible="nameDialogVisible" width="400px"
      :header="nameDialogMode === 'create' ? t('knowledgeBase.folder.new') : t('knowledgeBase.folder.rename')"
      :confirm-btn="{ content: t('common.confirm'), loading: nameSubmitting, disabled: !nameInput.trim() }"
      :cancel-btn="{ content: t('common.cancel') }" @confirm="submitNameDialog"
      @close="nameDialogVisible = false" @update:visible="(v: boolean) => (nameDialogVisible = v)">
      <t-input v-model="nameInput" :placeholder="t('knowledgeBase.folder.namePlaceholder')" :maxlength="100"
        autofocus @enter="submitNameDialog" />
      <div v-if="nameDialogMode === 'create' && currentName" class="folder-dialog-hint">
        {{ t('knowledgeBase.folder.createUnder', { name: currentName }) }}
      </div>
    </t-dialog>

    <!-- 删除对话框（可选内容上移） -->
    <t-dialog :visible="deleteDialogVisible" width="420px" :header="t('knowledgeBase.folder.delete')"
      :confirm-btn="{ content: t('knowledgeBase.confirmDelete'), theme: 'danger', loading: deleteSubmitting }"
      :cancel-btn="{ content: t('common.cancel') }" @confirm="submitDelete"
      @close="deleteDialogVisible = false" @update:visible="(v: boolean) => (deleteDialogVisible = v)">
      <div class="folder-delete-body">
        <p>{{ t('knowledgeBase.folder.deleteConfirm', { name: deleteTarget?.name || '' }) }}</p>
        <t-checkbox v-if="deleteTarget && (deleteTarget.knowledge_count > 0 || deleteTarget.has_children)"
          v-model="deletePromote">
          {{ t('knowledgeBase.folder.deletePromote') }}
        </t-checkbox>
      </div>
    </t-dialog>

    <!-- 移动文件夹选择器：目标不能是自己（子树环由后端校验并报错） -->
    <FolderTreePicker v-model:visible="movePickerVisible" :kb-id="kbId" allow-root
      :title="t('knowledgeBase.folder.moveTo')" :disabled-ids="moveTarget ? [moveTarget.id] : []"
      @confirm="onMoveConfirm" />
  </div>
</template>

<style scoped lang="less">
.kb-folder-bar {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 2px 0 8px;
}

.folder-breadcrumb {
  display: flex;
  align-items: center;
  gap: 2px;
  min-height: 28px;
  flex-wrap: wrap;
}

.crumb {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 26px;
  padding: 0 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  font-size: 12.5px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }

  &.current {
    color: var(--td-text-color-primary);
    font-weight: 600;
    cursor: default;
  }
}

.crumb-sep {
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

.folder-bar-actions {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-left: 8px;
}

.folder-bar-btn {
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

.folder-icon-spin {
  animation: kb-folder-spin 0.9s linear infinite;
}

@keyframes kb-folder-spin {
  to {
    transform: rotate(360deg);
  }
}

.folder-chip-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.folder-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 6px 0 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  font-size: 13px;
  color: var(--td-text-color-primary);
  cursor: pointer;
  max-width: 240px;
  transition: border-color 0.15s ease, background-color 0.15s ease, box-shadow 0.15s ease;

  &:hover,
  &.menu-open {
    border-color: var(--td-brand-color);
    box-shadow: 0 1px 4px rgba(0, 0, 0, 0.06);

    .folder-chip-more {
      opacity: 1;
    }
  }
}

.folder-chip-icon {
  flex-shrink: 0;
  color: #d97706;
  font-size: 16px;
}

.folder-chip-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 500;
}

.folder-chip-count {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  font-variant-numeric: tabular-nums;
}

.folder-chip-more {
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

.folder-dialog-hint {
  margin-top: 8px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.folder-delete-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  font-size: 13px;
}
</style>
