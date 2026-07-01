<template>
  <div class="folder-breadcrumb">
    <t-breadcrumb separator=">">
      <!-- 根目录 -->
      <t-breadcrumb-item @click="handleNavigate(null)">
        <home-icon />
        <span>{{ t('knowledgeFolder.rootFolder') }}</span>
      </t-breadcrumb-item>

      <!-- 路径上的每一级文件夹 -->
      <t-breadcrumb-item
        v-for="folder in breadcrumbPath"
        :key="folder.id"
        @click="handleNavigate(folder.id)"
      >
        <folder-icon />
        <span>{{ folder.name }}</span>
      </t-breadcrumb-item>
    </t-breadcrumb>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { HomeIcon, FolderIcon } from 'tdesign-icons-vue-next';
import { getBreadcrumb } from '@/api/knowledge-folder';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

const { t } = useI18n();

interface Props {
  kbId: string;
  currentFolderId?: string | null;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  (e: 'navigate', folderId: string | null): void;
}>();

const breadcrumbPath = ref<KnowledgeFolder[]>([]);
const loading = ref(false);

// 加载面包屑路径
const loadBreadcrumb = async () => {
  if (!props.currentFolderId) {
    breadcrumbPath.value = [];
    return;
  }

  loading.value = true;
  try {
    const res = await getBreadcrumb(props.kbId, props.currentFolderId);
    breadcrumbPath.value = res || [];
  } catch (error) {
    console.error('Failed to load breadcrumb:', error);
    breadcrumbPath.value = [];
  } finally {
    loading.value = false;
  }
};

// 导航到指定文件夹
const handleNavigate = (folderId: string | null) => {
  emit('navigate', folderId);
};

// 监听当前文件夹变化
watch(
  () => props.currentFolderId,
  () => {
    loadBreadcrumb();
  },
  { immediate: true }
);
</script>

<script lang="ts">
export default {
  name: 'FolderBreadcrumb',
};
</script>

<style scoped lang="less">
.folder-breadcrumb {
  padding: 12px 0;

  :deep(.t-breadcrumb-item) {
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 4px;

    &:hover {
      color: var(--td-brand-color);
    }

    span {
      display: inline-block;
      max-width: 150px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
  }
}
</style>
