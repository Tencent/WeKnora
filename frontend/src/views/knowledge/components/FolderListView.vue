<template>
  <div class="folder-list-view">
    <t-table
      :data="tableData"
      :columns="columns"
      :loading="loading"
      row-key="id"
      :selected-row-keys="selectedKeys"
      @select-change="handleSelectChange"
      @row-dblclick="handleRowDoubleClick"
    >
      <template #name="{ row }">
        <div class="name-cell">
          <folder-icon v-if="row.type === 'folder'" class="item-icon folder-icon" />
          <file-icon v-else class="item-icon" />
          <span class="item-name">{{ row.name }}</span>
        </div>
      </template>

      <template #size="{ row }">
        <span v-if="row.type === 'folder'">{{ $t('knowledgeFolder.itemCount', { count: row.knowledge_count || 0 }) }}</span>
        <span v-else>{{ formatFileSize(row.file_size) }}</span>
      </template>

      <template #tags="{ row }">
        <t-tag v-for="tag in row.tags" :key="tag.id" size="small">{{ tag.name }}</t-tag>
      </template>

      <template #actions="{ row }">
        <div v-if="row.type === 'folder' && canEdit" class="actions-cell">
          <t-popup
            :visible="openMenuId === row.id"
            trigger="click"
            destroy-on-close
            placement="bottom-right"
            overlay-class-name="card-more"
            @visible-change="(v: boolean) => openMenuId = v ? row.id : null"
          >
            <button class="row-more-btn" type="button" @click.stop>
              <t-icon name="more" size="16px" />
            </button>
            <template #content>
              <div class="card-menu">
                <FolderActionMenu
                  :folder="row"
                  :can-edit="true"
                  @rename="() => { openMenuId = null; $emit('folder-rename', row) }"
                  @move="() => { openMenuId = null; $emit('folder-move', row) }"
                  @delete="() => { openMenuId = null; $emit('folder-delete', row) }"
                />
              </div>
            </template>
          </t-popup>
        </div>
        <div v-else-if="row.type !== 'folder'" class="actions-cell">
          <!-- File actions placeholder; used when mixed content is shown -->
          <span class="row-muted">--</span>
        </div>
      </template>
    </t-table>

    <div v-if="selectedKeys.length > 0" class="batch-bar">
      <span>{{ $t('knowledgeBase.selectedCount', { count: selectedKeys.length }) }}</span>
      <t-button @click="handleBatchMove">{{ $t('knowledgeFolder.moveFolder') }}</t-button>
      <t-button theme="danger" @click="handleBatchDelete">{{ $t('common.delete') }}</t-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import { FolderIcon, FileIcon } from 'tdesign-icons-vue-next';
import FolderActionMenu from './FolderActionMenu.vue';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

interface Props {
  folders: KnowledgeFolder[];
  files: any[];
  loading?: boolean;
  canEdit?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
  canEdit: true,
});

const emit = defineEmits<{
  (e: 'enter-folder', folderId: string): void;
  (e: 'batch-move', items: any[]): void;
  (e: 'batch-delete', items: any[]): void;
  (e: 'folder-rename', folder: any): void;
  (e: 'folder-move', folder: any): void;
  (e: 'folder-delete', folder: any): void;
}>();

const selectedKeys = ref<string[]>([]);
const openMenuId = ref<string | null>(null);

const columns = [
  { colKey: 'row-select', type: 'multiple', width: 50 },
  { colKey: 'name', title: '名称', width: '35%' },
  { colKey: 'updated_at', title: '修改时间', width: '18%' },
  { colKey: 'size', title: '大小', width: '12%' },
  { colKey: 'tags', title: '标签', width: '20%' },
  { colKey: 'actions', title: '操作', width: '10%' },
];

const tableData = computed(() => {
  const foldersWithType = props.folders.map((f) => ({ ...f, type: 'folder' }));
  const filesWithType = props.files.map((f) => ({ ...f, type: 'file' }));
  return [...foldersWithType, ...filesWithType];
});

const formatFileSize = (bytes: number) => {
  if (!bytes) return '-';
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(2) + ' KB';
  return (bytes / (1024 * 1024)).toFixed(2) + ' MB';
};

const handleSelectChange = (value: string[]) => {
  selectedKeys.value = value;
};

const handleRowDoubleClick = ({ row }: any) => {
  if (row.type === 'folder') {
    emit('enter-folder', row.id);
  }
};

const handleBatchMove = () => {
  const items = tableData.value.filter((item) => selectedKeys.value.includes(item.id));
  emit('batch-move', items);
};

const handleBatchDelete = () => {
  const items = tableData.value.filter((item) => selectedKeys.value.includes(item.id));
  emit('batch-delete', items);
};
</script>

<style scoped lang="less">
.folder-list-view {
  .name-cell {
    display: flex;
    align-items: center;
    gap: 8px;

    .folder-icon {
      color: var(--td-brand-color);
    }
  }

  .actions-cell {
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .row-more-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border: none;
    border-radius: 4px;
    background: transparent;
    cursor: pointer;
    color: var(--td-text-color-secondary);

    &:hover {
      background: var(--td-component-stroke);
      color: var(--td-brand-color);
    }
  }

  .row-muted {
    color: var(--td-text-color-placeholder);
    font-size: 12px;
  }
}

.batch-bar {
  position: fixed;
  bottom: 20px;
  left: 50%;
  transform: translateX(-50%);
  background: white;
  padding: 12px 24px;
  border-radius: 6px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  display: flex;
  gap: 12px;
  align-items: center;
  z-index: 100;
}
</style>
