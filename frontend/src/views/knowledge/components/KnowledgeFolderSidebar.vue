<script setup lang="ts">
import { computed } from 'vue'
import type { KnowledgeFolder } from '@/types/knowledgeFolder'
import { flattenKnowledgeFolders } from '@/utils/knowledgeFolderTree'

const props = defineProps<{
  folders: KnowledgeFolder[]
  currentFolderId: string | null
  loading?: boolean
  canEdit?: boolean
}>()

const emit = defineEmits<{
  select: [folderId: string | null]
  create: [parentId: string | null]
  rename: [folder: KnowledgeFolder]
  move: [folder: KnowledgeFolder]
  delete: [folder: KnowledgeFolder]
}>()

const rows = computed(() => flattenKnowledgeFolders(props.folders))
</script>

<template>
  <aside class="knowledge-folder-sidebar">
    <header>
      <strong>{{ $t('knowledgeFolder.title') }}</strong>
      <button v-if="canEdit" type="button" :title="$t('knowledgeFolder.create')" @click="emit('create', currentFolderId)">
        <t-icon name="folder-add" />
      </button>
    </header>
    <div v-if="loading" class="folder-state"><t-loading size="small" /></div>
    <div v-else class="folder-tree">
      <button
        type="button"
        class="folder-row root"
        :class="{ active: currentFolderId === null }"
        @click="emit('select', null)"
      >
        <t-icon name="home" />
        <span>{{ $t('knowledgeFolder.root') }}</span>
      </button>
      <div
        v-for="row in rows"
        :key="row.folder.id"
        class="folder-row-wrap"
        :style="{ paddingLeft: `${row.depth * 16}px` }"
      >
        <button
          type="button"
          class="folder-row"
          :class="{ active: currentFolderId === row.folder.id }"
          @click="emit('select', row.folder.id)"
        >
          <t-icon :name="currentFolderId === row.folder.id ? 'folder-open' : 'folder'" />
          <span :title="row.folder.name">{{ row.folder.name }}</span>
        </button>
        <t-dropdown v-if="canEdit" trigger="click" :options="[
          { content: $t('knowledgeFolder.rename'), value: 'rename' },
          { content: $t('knowledgeFolder.move'), value: 'move' },
          { content: $t('knowledgeFolder.delete'), value: 'delete', theme: 'error' },
        ]" @click="({ value }: any) => emit(value, row.folder)">
          <button type="button" class="folder-more" @click.stop><t-icon name="more" /></button>
        </t-dropdown>
      </div>
      <div v-if="folders.length === 0" class="folder-state">{{ $t('knowledgeFolder.empty') }}</div>
    </div>
  </aside>
</template>

<style scoped lang="less">
.knowledge-folder-sidebar {
  width: 224px;
  min-width: 224px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}
header {
  height: 44px;
  padding: 0 10px 0 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--td-component-stroke);
}
header button, .folder-more {
  border: 0;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
}
.folder-tree { padding: 8px; overflow: auto; }
.folder-row-wrap { display: flex; align-items: center; }
.folder-row {
  min-width: 0;
  flex: 1;
  min-height: 34px;
  padding: 5px 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-secondary);
  display: flex;
  align-items: center;
  gap: 7px;
  cursor: pointer;
  text-align: left;
}
.folder-row span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.folder-row:hover { background: var(--td-bg-color-container-hover); }
.folder-row.active { color: var(--td-brand-color); background: var(--td-brand-color-light); }
.folder-more { width: 24px; }
.folder-state { padding: 20px 8px; color: var(--td-text-color-placeholder); text-align: center; font-size: 12px; }
@media (max-width: 1045px) {
  .knowledge-folder-sidebar {
    width: 180px;
    min-width: 180px;
  }
}
@media (max-width: 750px) {
  .knowledge-folder-sidebar {
    width: 100%;
    min-width: 0;
    max-height: 220px;
  }
}
</style>
