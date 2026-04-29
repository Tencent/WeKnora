<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatFileSize, getFileIcon } from '@/utils/files';

interface Tag {
  id: string;
  name: string;
  color?: string;
}

interface KnowledgeItem {
  id: string;
  file_name: string;
  file_type?: string;
  file_size?: number | string;
  type?: string;
  tag_id?: string | number;
  parse_status?: string;
  summary_status?: string;
  updated_at?: string;
  source?: string;
  isMore?: boolean;
}

const props = defineProps<{
  items: KnowledgeItem[];
  selectedIds: Set<string>;
  canEdit: boolean;
  tagList: Tag[];
  loading?: boolean;
}>();

const emit = defineEmits<{
  (e: 'open', item: KnowledgeItem): void;
  (e: 'toggle-row', id: string, checked: boolean, shiftKey: boolean): void;
  (e: 'toggle-all', checked: boolean): void;
  (e: 'action', action: 'edit' | 'reparse' | 'move' | 'delete', item: KnowledgeItem): void;
}>();

const { t } = useI18n();

const tagMap = computed(() => {
  const map: Record<string, Tag> = {};
  for (const tag of props.tagList) map[String(tag.id)] = tag;
  return map;
});
const getTagName = (tagId?: string | number) => {
  if (!tagId && tagId !== 0) return '';
  return tagMap.value[String(tagId)]?.name || '';
};

const formatTime = (time?: string) => {
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

const getTypeLabel = (item: KnowledgeItem) => {
  if (item.type === 'url') return 'URL';
  if (item.type === 'manual') return t('knowledgeBase.typeManual');
  if (item.file_type) return item.file_type.toUpperCase();
  return '--';
};

interface StatusInfo {
  label: string;
  theme: 'success' | 'warning' | 'danger' | 'primary' | 'default';
  icon?: string;
  spin?: boolean;
}
const computeStatus = (item: KnowledgeItem): StatusInfo => {
  if (item.parse_status === 'pending' || item.parse_status === 'processing') {
    return { label: t('knowledgeBase.statusProcessing'), theme: 'primary', icon: 'loading', spin: true };
  }
  if (item.parse_status === 'failed') {
    return { label: t('knowledgeBase.statusFailed'), theme: 'danger', icon: 'close-circle' };
  }
  if (item.parse_status === 'draft') {
    return { label: t('knowledgeBase.statusDraft'), theme: 'warning' };
  }
  if (
    item.parse_status === 'completed' &&
    (item.summary_status === 'pending' || item.summary_status === 'processing')
  ) {
    return { label: t('knowledgeBase.generatingSummary'), theme: 'primary', icon: 'loading', spin: true };
  }
  if (item.parse_status === 'completed') {
    return { label: t('knowledgeBase.statusCompleted'), theme: 'success' };
  }
  return { label: '--', theme: 'default' };
};

const statusByRow = computed(() => {
  const map = new Map<string, StatusInfo>();
  for (const item of props.items) map.set(item.id, computeStatus(item));
  return map;
});

const allSelected = computed(() => {
  return props.items.length > 0 && props.items.every(i => props.selectedIds.has(i.id));
});
const someSelected = computed(() => {
  return props.items.some(i => props.selectedIds.has(i.id)) && !allSelected.value;
});

const onHeaderToggle = (e: Event) => {
  const checked = (e.target as HTMLInputElement).checked;
  emit('toggle-all', checked);
};

const onRowToggle = (item: KnowledgeItem, e: MouseEvent) => {
  const checked = !props.selectedIds.has(item.id);
  emit('toggle-row', item.id, checked, e.shiftKey);
};

const moreOpen = ref<string | null>(null);
const onMoreVisible = (id: string, visible: boolean) => {
  moreOpen.value = visible ? id : null;
};

const handleAction = (action: 'edit' | 'reparse' | 'move' | 'delete', item: KnowledgeItem) => {
  moreOpen.value = null;
  item.isMore = false;
  emit('action', action, item);
};
</script>

<template>
  <div class="doc-list-view" :class="{ 'is-loading': loading }">
    <div class="doc-list-header" role="row">
      <div class="cell cell-check" role="columnheader">
        <label class="checkbox-wrap" @click.stop>
          <input
            type="checkbox"
            :checked="allSelected"
            :indeterminate.prop="someSelected"
            :disabled="!items.length"
            @change="onHeaderToggle"
            :aria-label="t('knowledgeBase.selectAll')"
          />
        </label>
      </div>
      <div class="cell cell-name" role="columnheader">{{ t('knowledgeBase.columnName') }}</div>
      <div class="cell cell-tag" role="columnheader">{{ t('knowledgeBase.columnTag') }}</div>
      <div class="cell cell-size" role="columnheader">{{ t('knowledgeBase.columnSize') }}</div>
      <div class="cell cell-type" role="columnheader">{{ t('knowledgeBase.columnType') }}</div>
      <div class="cell cell-status" role="columnheader">{{ t('knowledgeBase.columnStatus') }}</div>
      <div class="cell cell-time" role="columnheader">{{ t('knowledgeBase.columnUpdatedAt') }}</div>
      <div class="cell cell-actions" role="columnheader" v-if="canEdit"></div>
    </div>

    <div class="doc-list-body">
      <div
        v-for="item in items"
        :key="item.id"
        class="doc-list-row"
        :class="{ selected: selectedIds.has(item.id), 'menu-open': moreOpen === item.id }"
        role="row"
        @click="emit('open', item)"
      >
        <div class="cell cell-check" @click.stop>
          <label class="checkbox-wrap">
            <input
              type="checkbox"
              :checked="selectedIds.has(item.id)"
              @click="onRowToggle(item, $event as unknown as MouseEvent)"
              :aria-label="item.file_name"
            />
          </label>
        </div>

        <div class="cell cell-name">
          <t-icon :name="getFileIcon(item)" class="row-file-icon" />
          <span class="row-file-name" :title="item.file_name">{{ item.file_name }}</span>
        </div>


        <div class="cell cell-tag">
          <t-tag v-if="getTagName(item.tag_id)" size="small" variant="light-outline" class="row-tag">
            {{ getTagName(item.tag_id) }}
          </t-tag>
          <span v-else class="row-muted">--</span>
        </div>

        <div class="cell cell-size">
          <span class="row-mono">{{ formatFileSize(item.file_size) || '--' }}</span>
        </div>

        <div class="cell cell-type">
          <span class="row-mono">{{ getTypeLabel(item) }}</span>
        </div>

        <div class="cell cell-status">
          <template v-if="statusByRow.get(item.id) as StatusInfo | undefined">
            <t-tag
              v-if="statusByRow.get(item.id)!.label !== '--'"
              size="small"
              :theme="statusByRow.get(item.id)!.theme"
              variant="light"
              class="row-status-tag"
            >
              <template v-if="statusByRow.get(item.id)!.icon" #icon>
                <t-icon
                  :name="statusByRow.get(item.id)!.icon!"
                  :class="{ 'icon-spin': statusByRow.get(item.id)!.spin }"
                />
              </template>
              {{ statusByRow.get(item.id)!.label }}
            </t-tag>
            <span v-else class="row-muted">--</span>
          </template>
        </div>

        <div class="cell cell-time">
          <span class="row-mono">{{ formatTime(item.updated_at) }}</span>
        </div>

        <div class="cell cell-actions" v-if="canEdit" @click.stop>
          <t-popup
            placement="bottom-right"
            trigger="click"
            destroy-on-close
            :on-visible-change="(v: boolean) => onMoreVisible(item.id, v)"
          >
            <button class="row-more-btn" :class="{ active: moreOpen === item.id }" type="button" :aria-label="t('knowledgeBase.columnActions')">
              <t-icon name="more" size="16px" />
            </button>
            <template #content>
              <div class="row-menu">
                <div
                  v-if="item.type === 'manual'"
                  class="row-menu-item"
                  @click.stop="handleAction('edit', item)"
                >
                  <t-icon name="edit" /> <span>{{ t('knowledgeBase.editDocument') }}</span>
                </div>
                <div class="row-menu-item" @click.stop="handleAction('reparse', item)">
                  <t-icon name="refresh" /> <span>{{ t('knowledgeBase.rebuildDocument') }}</span>
                </div>
                <div class="row-menu-item" @click.stop="handleAction('move', item)">
                  <t-icon name="swap" /> <span>{{ t('knowledgeBase.moveDocument') }}</span>
                </div>
                <div class="row-menu-item danger" @click.stop="handleAction('delete', item)">
                  <t-icon name="delete" /> <span>{{ t('knowledgeBase.deleteDocument') }}</span>
                </div>
              </div>
            </template>
          </t-popup>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped lang="less">
.doc-list-view {
  display: flex;
  flex-direction: column;
  width: 100%;
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-stroke, #f0f0f0);
  border-radius: 8px;
  overflow: hidden;
}

.doc-list-header,
.doc-list-row {
  display: grid;
  grid-template-columns:
    44px                       // checkbox
    minmax(220px, 2.4fr)       // name
    minmax(100px, 0.9fr)       // tag
    96px                       // size
    72px                       // type
    minmax(96px, 0.7fr)        // status
    140px                      // updated_at
    48px;                      // actions
  align-items: center;
  column-gap: 0;
  padding: 0 12px;
}

.doc-list-header {
  position: sticky;
  top: 0;
  z-index: 2;
  height: 36px;
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0.02em;
  color: var(--td-text-color-placeholder, #a6a6a6);
  background: var(--td-bg-color-page, #fafbfc);
  border-bottom: 1px solid var(--td-component-stroke, #f0f0f0);
}

.doc-list-body {
  display: flex;
  flex-direction: column;
}

.doc-list-row {
  position: relative;
  height: 48px;
  font-size: 13px;
  color: var(--td-text-color-primary, #232323);
  border-bottom: 1px solid var(--td-component-stroke, #f3f3f3);
  cursor: pointer;
  transition: background-color 0.12s ease, box-shadow 0.12s ease;

  &:last-child { border-bottom: 0; }

  &:hover:not(.selected),
  &.menu-open:not(.selected) {
    background: var(--td-bg-color-page, #f7f8fa);
  }

  &.selected {
    background: var(--td-brand-color-1, #f2f5fc);
    box-shadow: inset 3px 0 0 var(--td-brand-color, #0052d9);

    &:hover { background: var(--td-brand-color-light, #e8eefc); }
  }

  &:hover .row-more-btn,
  &.menu-open .row-more-btn,
  &.selected .row-more-btn { opacity: 1; }
}

.cell {
  display: flex;
  align-items: center;
  min-width: 0;
  padding: 0 8px;
  &:first-child { padding-left: 0; }
  &:last-child { padding-right: 0; }
}

.cell-check {
  justify-content: center;
  padding: 0;
}

.cell-name {
  gap: 8px;
  font-weight: 500;
}

.cell-size,
.cell-time {
  justify-content: flex-end;
}

.cell-actions {
  justify-content: flex-end;
}

.checkbox-wrap {
  display: inline-flex;
  align-items: center;
  cursor: pointer;
  input[type='checkbox'] {
    width: 14px;
    height: 14px;
    accent-color: var(--td-brand-color, #0052d9);
    cursor: pointer;
    margin: 0;
  }
}

.row-file-icon {
  flex-shrink: 0;
  font-size: 16px;
  color: var(--td-text-color-secondary, #888);
}

.row-file-name {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.row-tag {
  max-width: 100%;
  :deep(.t-tag__text) {
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 120px;
    display: inline-block;
  }
}

.row-muted {
  color: var(--td-text-color-disabled, #bbb);
}

.row-mono {
  font-variant-numeric: tabular-nums;
  font-size: 12px;
  color: var(--td-text-color-secondary, #666);
}

.row-status-tag :deep(.t-icon) {
  margin-right: 2px;
}
.icon-spin {
  animation: doc-list-spin 0.9s linear infinite;
}
@keyframes doc-list-spin {
  to { transform: rotate(360deg); }
}

.row-more-btn {
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  border-radius: 6px;
  color: var(--td-text-color-secondary, #666);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.12s ease, background-color 0.12s ease;

  &:hover { background: var(--td-bg-color-component-hover, #ececec); }
  &.active { opacity: 1; background: var(--td-bg-color-component-active, #e0e0e0); }
}

.row-menu {
  min-width: 160px;
  padding: 4px 0;
}

.row-menu-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  font-size: 13px;
  color: var(--td-text-color-primary, #232323);
  cursor: pointer;
  transition: background-color 0.12s ease;

  &:hover { background: var(--td-bg-color-component-hover, #f5f5f5); }
  &.danger { color: var(--td-error-color, #d54941); }
  &.danger:hover { background: var(--td-error-color-1, #fff1f0); }

  .t-icon { font-size: 14px; }
}
</style>
