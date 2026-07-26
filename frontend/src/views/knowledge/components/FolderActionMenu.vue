<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import type { KnowledgeFolder } from '@/api/knowledge-base';

type FolderAction = 'ask' | 'create-child' | 'rename' | 'move' | 'reparse' | 'delete';

const props = defineProps<{
  folder: KnowledgeFolder;
}>();

const emit = defineEmits<{
  (e: 'action', action: FolderAction, folder: KnowledgeFolder): void;
}>();

const { t } = useI18n();

const trigger = (action: FolderAction) => emit('action', action, props.folder);

const options = computed(() => [
  { content: t('knowledgeBase.folderAsk'), value: 'ask' },
  {
    content: t('knowledgeBase.folderCreateChild'),
    value: 'create-child',
    disabled: props.folder.depth >= 10,
  },
  { content: t('knowledgeBase.folderRename'), value: 'rename' },
  { content: t('knowledgeBase.folderRelocate'), value: 'move' },
  { content: t('knowledgeBase.folderReparseAll'), value: 'reparse', divider: true },
  { content: t('knowledgeBase.folderDeleteAll'), value: 'delete', theme: 'error' },
]);

const handleClick = (item: { value?: FolderAction }) => {
  if (item.value) trigger(item.value);
};
</script>

<template>
  <t-dropdown-menu class="folder-action-menu" :options="options" @click="handleClick" />
</template>

<style scoped lang="less">
.folder-action-menu {
  min-width: 152px;
}
</style>
