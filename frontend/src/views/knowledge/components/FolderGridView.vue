<template>
  <div class="folder-grid-view">
    <div class="grid-container">
      <div
        v-for="item in gridData"
        :key="item.id"
        class="grid-item"
        :class="{ selected: selectedKeys.includes(item.id) }"
        @click="handleItemClick(item)"
        @dblclick="handleItemDoubleClick(item)"
      >
        <t-card :bordered="true" hover-shadow>
          <div class="item-checkbox" @click.stop>
            <t-checkbox
              :checked="selectedKeys.includes(item.id)"
              @change="(checked) => handleCheckChange(checked, item.id)"
            />
          </div>

          <div class="item-icon">
            <folder-icon v-if="item.type === 'folder'" size="48px" class="folder-icon" />
            <file-icon v-else size="48px" class="file-icon" />
          </div>

          <div class="item-name" :title="item.name">{{ item.name }}</div>

          <div class="item-meta">
            <span v-if="item.type === 'folder'">{{ item.knowledge_count || 0 }} 项</span>
            <span v-else>{{ formatFileSize(item.file_size) }}</span>
          </div>

          <div v-if="item.tags && item.tags.length > 0" class="item-tags">
            <t-tag v-for="tag in item.tags.slice(0, 2)" :key="tag.id" size="small">
              {{ tag.name }}
            </t-tag>
          </div>
        </t-card>
      </div>
    </div>

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

const gridData = computed(() => {
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

const handleItemClick = (item: any) => {
  // 单击逻辑
};

const handleItemDoubleClick = (item: any) => {
  if (item.type === 'folder') {
    emit('enter-folder', item.id);
  }
};

const handleCheckChange = (checked: boolean, itemId: string) => {
  if (checked) {
    selectedKeys.value.push(itemId);
  } else {
    selectedKeys.value = selectedKeys.value.filter((id) => id !== itemId);
  }
};

const handleBatchMove = () => {
  const items = gridData.value.filter((item) => selectedKeys.value.includes(item.id));
  emit('batch-move', items);
};

const handleBatchDelete = () => {
  const items = gridData.value.filter((item) => selectedKeys.value.includes(item.id));
  emit('batch-delete', items);
};
</script>

<style scoped lang="less">
.folder-grid-view {
  .grid-container {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 16px;
    padding: 16px;
  }

  .grid-item {
    cursor: pointer;
    position: relative;
    transition: transform 0.2s;

    &:hover {
      transform: translateY(-2px);
    }

    &.selected :deep(.t-card) {
      border-color: var(--td-brand-color);
    }

    .item-checkbox {
      position: absolute;
      top: 8px;
      right: 8px;
      z-index: 1;
    }

    .item-icon {
      display: flex;
      justify-content: center;
      height: 80px;
      margin-bottom: 12px;

      .folder-icon {
        color: var(--td-warning-color);
      }
    }

    .item-name {
      font-weight: 500;
      font-size: 14px;
      text-align: center;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      margin-top: 4px;
    }

    .item-meta {
      font-size: 12px;
      color: var(--td-text-color-secondary);
      text-align: center;
      margin-top: 8px;
    }

    .item-tags {
      display: flex;
      justify-content: center;
      gap: 4px;
      margin-top: 8px;
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

@media (max-width: 768px) {
  .folder-grid-view .grid-container {
    grid-template-columns: repeat(2, 1fr);
  }
}

@media (min-width: 769px) and (max-width: 1199px) {
  .folder-grid-view .grid-container {
    grid-template-columns: repeat(4, 1fr);
  }
}

@media (min-width: 1200px) {
  .folder-grid-view .grid-container {
    grid-template-columns: repeat(6, 1fr);
  }
}
</style>
