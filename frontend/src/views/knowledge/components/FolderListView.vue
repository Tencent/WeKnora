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
        <span v-if="row.type === 'folder'">{{ row.knowledge_count || 0 }} 项</span>
        <span v-else>{{ formatFileSize(row.file_size) }}</span>
      </template>

      <template #tags="{ row }">
        <t-tag v-for="tag in row.tags" :key="tag.id" size="small">{{ tag.name }}</t-tag>
      </template>
    </t-table>

    <div v-if="selectedKeys.length > 0" class="batch-bar">
      <span>已选择 {{ selectedKeys.length }} 项</span>
      <t-button @click="handleBatchMove">移动</t-button>
      <t-button theme="danger" @click="handleBatchDelete">删除</t-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import { FolderIcon, FileIcon } from 'tdesign-icons-vue-next';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

interface Props {
  folders: KnowledgeFolder[];
  files: any[];
  loading?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
});

const emit = defineEmits<{
  (e: 'enter-folder', folderId: string): void;
  (e: 'batch-move', items: any[]): void;
  (e: 'batch-delete', items: any[]): void;
}>();

const selectedKeys = ref<string[]>([]);

const columns = [
  { colKey: 'row-select', type: 'multiple', width: 50 },
  { colKey: 'name', title: '名称', width: '40%' },
  { colKey: 'updated_at', title: '修改时间', width: '20%' },
  { colKey: 'size', title: '大小', width: '15%' },
  { colKey: 'tags', title: '标签', width: '25%' },
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
      color: var(--td-warning-color);
    }
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
}
</style>
