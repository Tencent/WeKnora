<template>
  <div class="folder-card-list">
    <div
      v-for="item in folderItems"
      :key="item.id"
      class="knowledge-card knowledge-card--folder"
      :class="{
        'is-selected': selectedKeys.includes(item.id),
        'batch-mode': batchMode,
      }"
      :data-select-id="item.id"
      @click="handleCardClick(item)"
      @dblclick="handleCardDoubleClick(item)"
    >
      <div class="card-content">
        <div class="card-content-nav">
          <div v-if="canEdit && batchMode" class="card-nav-check" @click.stop>
            <t-checkbox
              class="card-select-checkbox"
              size="small"
              :checked="selectedKeys.includes(item.id)"
              :title="item.name"
              @change="(checked: boolean, ctx?: { e?: Event }) => handleCheckChange(checked, item.id, ctx)"
            />
          </div>
          <t-icon name="folder" size="20px" class="folder-card-icon" />
          <span class="card-content-title" :title="item.name">{{ item.name }}</span>

          <!-- Action menu (non-batch mode) -->
          <t-popup
            v-if="canEdit && !batchMode"
            :visible="openMenuId === item.id"
            trigger="click"
            destroy-on-close
            placement="bottom-right"
            overlay-class-name="card-more"
            @visible-change="(v: boolean) => openMenuId = v ? item.id : null"
          >
            <div class="more-wrap" @click.stop>
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <div class="card-menu">
                <FolderActionMenu
                  :folder="item"
                  :can-edit="true"
                  @rename="() => { openMenuId = null; $emit('folder-rename', item) }"
                  @move="() => { openMenuId = null; $emit('folder-move', item) }"
                  @delete="() => { openMenuId = null; $emit('folder-delete', item) }"
                />
              </div>
            </template>
          </t-popup>
        </div>
      </div>

      <div class="card-bottom">
        <span class="card-bottom-tag">{{ $t('knowledgeFolder.itemCount', { count: item.knowledge_count || 0 }) }}</span>
        <span class="card-time">{{ formatDocTime(item.created_at) }}</span>
      </div>
    </div>

    <!-- Batch action bar -->
    <div v-if="selectedKeys.length > 0" class="batch-bar">
      <span>{{ $t('knowledgeBase.selectedCount', { count: selectedKeys.length }) }}</span>
      <t-button @click="handleBatchMove">{{ $t('knowledgeFolder.moveFolder') }}</t-button>
      <t-button theme="danger" @click="handleBatchDelete">{{ $t('common.delete') }}</t-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import FolderActionMenu from './FolderActionMenu.vue';
import type { KnowledgeFolder } from '@/types/knowledgeFolder';

interface Props {
  folders: KnowledgeFolder[];
  files?: any[];
  loading?: boolean;
  canEdit?: boolean;
  batchMode?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  files: () => [],
  loading: false,
  canEdit: true,
  batchMode: false,
});

const emit = defineEmits<{
  (e: 'enter-folder', folderId: string): void;
  (e: 'toggle-checkbox', id: string, checked: boolean, ctx?: { e?: Event }): void;
  (e: 'batch-move', items: any[]): void;
  (e: 'batch-delete', items: any[]): void;
  (e: 'folder-rename', folder: any): void;
  (e: 'folder-move', folder: any): void;
  (e: 'folder-delete', folder: any): void;
}>();

const selectedKeys = ref<string[]>([]);
const openMenuId = ref<string | null>(null);

// Map folders to card-compatible items
const folderItems = computed(() => {
  return props.folders.map((f) => ({
    ...f,
    type: 'folder' as const,
    isFolder: true,
  }));
});

const formatDocTime = (time?: string) => {
  if (!time) return '--';
  const d = new Date(time);
  if (Number.isNaN(d.getTime())) return '--';
  const yy = String(d.getFullYear()).slice(2);
  const MM = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  const hh = String(d.getHours()).padStart(2, '0');
  const mm = String(d.getMinutes()).padStart(2, '0');
  return `${yy}-${MM}-${dd} ${hh}:${mm}`;
};

const handleCardClick = (item: any) => {
  if (props.batchMode) {
    emit('toggle-checkbox', item.id, !selectedKeys.value.includes(item.id));
    return;
  }
};

const handleCardDoubleClick = (item: any) => {
  if (!props.batchMode) {
    emit('enter-folder', item.id);
  }
};

const handleCheckChange = (checked: boolean, itemId: string, ctx?: { e?: Event }) => {
  if (checked) {
    selectedKeys.value.push(itemId);
  } else {
    selectedKeys.value = selectedKeys.value.filter((id) => id !== itemId);
  }
  emit('toggle-checkbox', itemId, checked, ctx);
};

const handleBatchMove = () => {
  const items = folderItems.value.filter((item) => selectedKeys.value.includes(item.id));
  emit('batch-move', items);
};

const handleBatchDelete = () => {
  const items = folderItems.value.filter((item) => selectedKeys.value.includes(item.id));
  emit('batch-delete', items);
};
</script>

<style scoped lang="less">
@keyframes folderCardFadeIn {
  from { opacity: 0; transform: translateY(6px); }
  to { opacity: 1; transform: translateY(0); }
}

.folder-card-list {
  box-sizing: border-box;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 12px;
  align-content: flex-start;
  width: 100%;
}

.knowledge-card--folder {
  min-width: 240px;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-border);
  height: 136px;
  border-radius: 8px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
  background: var(--td-bg-color-container);
  position: relative;
  cursor: pointer;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background-color 0.2s ease;

  &:hover {
    border-color: color-mix(in srgb, var(--td-component-stroke) 55%, var(--td-brand-color));
    box-shadow: 0 4px 14px rgba(0, 0, 0, 0.07);
  }

  &.is-selected :deep(.t-card) {
    border-color: var(--td-brand-color);
  }

  .card-nav-check {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 29px;
    margin-right: 8px;
    cursor: pointer;

    .card-select-checkbox {
      margin: 0;
      line-height: 0;

      :deep(.t-checkbox) { align-items: center; }
      :deep(.t-checkbox__label) { display: none !important; width: 0 !important; min-width: 0 !important; margin: 0 !important; padding: 0 !important; }
      :deep(.t-checkbox__input) { margin: 0; }
      :deep(.t-checkbox__input-wrapper) { margin: 0; }
    }
  }

  .card-content {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    padding: 10px 14px 8px;
  }

  .card-content-nav {
    flex-shrink: 0;
    display: flex;
    align-items: flex-start;
    gap: 0;
    margin-bottom: 6px;
  }

  .folder-card-icon {
    color: var(--td-brand-color);
    flex-shrink: 0;
  }

  .card-content-title {
    flex: 1;
    min-width: 0;
    height: 24px;
    line-height: 24px;
    display: inline-block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 14px;
    font-weight: 600;
    letter-spacing: 0.01em;
    margin: 0 8px;
  }

  .more-wrap {
    flex-shrink: 0;
    display: flex;
    width: 25px;
    height: 25px;
    justify-content: center;
    align-items: center;
    border-radius: 5px;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s, background 0.15s;

    &:hover {
      background: var(--td-component-stroke);
    }
  }

  .more-icon {
    width: 14px;
    height: 14px;
  }

  &:hover .more-wrap {
    opacity: 1;
  }

  .card-bottom {
    flex-shrink: 0;
    margin-top: auto;
    padding: 0 14px;
    box-sizing: border-box;
    height: 32px;
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--td-bg-color-container);
    border-top: 1px solid var(--td-component-stroke);
  }

  .card-bottom-tag {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);
    font-size: 12px;
    font-weight: 400;
    white-space: nowrap;
  }

  .card-time {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);
    font-size: 12px;
    font-weight: 400;
    white-space: nowrap;
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
