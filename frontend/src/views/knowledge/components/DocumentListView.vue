<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
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

const onHeaderCheckboxChange = (checked: boolean) => {
  emit('toggle-all', checked);
};

const onRowCheckboxChange = (item: KnowledgeItem, checked: boolean, ctx?: { e?: Event }) => {
  const me = ctx?.e as MouseEvent | undefined;
  emit('toggle-row', item.id, checked, !!me?.shiftKey);
};

const moreOpen = ref<string | null>(null);
const onMoreVisible = (id: string, visible: boolean) => {
  moreOpen.value = visible ? id : null;
};

// 吸顶检测：哨兵离开视口说明 header 已吸附在滚动容器顶部
const stickySentinel = ref<HTMLElement | null>(null);
const headerStuck = ref(false);
let stickyObserver: IntersectionObserver | null = null;
onMounted(() => {
  if (!stickySentinel.value || typeof IntersectionObserver === 'undefined') return;
  stickyObserver = new IntersectionObserver(
    (entries) => {
      headerStuck.value = !entries[0].isIntersecting;
    },
    { threshold: 0 },
  );
  stickyObserver.observe(stickySentinel.value);
});
onBeforeUnmount(() => {
  stickyObserver?.disconnect();
  stickyObserver = null;
});

const handleAction = (action: 'edit' | 'reparse' | 'move' | 'delete', item: KnowledgeItem) => {
  moreOpen.value = null;
  item.isMore = false;
  emit('action', action, item);
};

</script>

<template>
  <div class="doc-list-view" :class="{ 'is-loading': loading }">
    <div ref="stickySentinel" class="doc-list-sticky-sentinel" aria-hidden="true"></div>
    <div class="doc-list-header" :class="{ 'is-stuck': headerStuck }" role="row">
      <div class="cell cell-check" role="columnheader" @click.stop>
        <t-checkbox
          class="doc-list-check"
          size="small"
          :checked="allSelected"
          :indeterminate="someSelected"
          :disabled="!items.length"
          :title="t('knowledgeBase.selectAll')"
          @change="onHeaderCheckboxChange"
        />
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
          <t-checkbox
            class="doc-list-check"
            size="small"
            :checked="selectedIds.has(item.id)"
            :title="item.file_name"
            @change="(c, ctx) => onRowCheckboxChange(item, c, ctx)"
          />
        </div>

        <div class="cell cell-name">
          <span class="row-file-icon-wrap">
            <t-icon :name="getFileIcon(item)" />
          </span>
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
              variant="light-outline"
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
                  <t-icon class="icon" name="edit" />
                  <span>{{ t('knowledgeBase.editDocument') }}</span>
                </div>
                <div class="row-menu-item" @click.stop="handleAction('reparse', item)">
                  <t-icon class="icon" name="refresh" />
                  <span>{{ t('knowledgeBase.rebuildDocument') }}</span>
                </div>
                <div class="row-menu-item" @click.stop="handleAction('move', item)">
                  <t-icon class="icon" name="swap" />
                  <span>{{ t('knowledgeBase.moveDocument') }}</span>
                </div>
                <div class="row-menu-item danger" @click.stop="handleAction('delete', item)">
                  <t-icon class="icon" name="delete" />
                  <span>{{ t('knowledgeBase.deleteDocument') }}</span>
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
@keyframes doc-list-fade-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.doc-list-view {
  display: flex;
  flex-direction: column;
  width: 100%;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 9px;
  /* 不能用 overflow:hidden，否则表头 position:sticky 相对外层滚动区失效 */
  overflow: visible;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  animation: doc-list-fade-in 0.32s ease-out;
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
  padding: 0 16px;
}

.doc-list-sticky-sentinel {
  height: 0;
  margin: 0;
  padding: 0;
  border: 0;
  pointer-events: none;
}

.doc-list-header {
  position: sticky;
  top: 0;
  z-index: 3;
  height: 40px;
  font-size: 12px;
  font-weight: 500;
  font-family: var(--app-font-family);
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
  border-bottom: 1px solid var(--td-component-stroke);
  border-radius: 8px 8px 0 0;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.04);
  transition: border-radius 0.15s ease, box-shadow 0.2s ease;

  &.is-stuck {
    border-radius: 0;
    box-shadow: 0 4px 10px rgba(0, 0, 0, 0.08);
  }
}

.doc-list-body {
  display: flex;
  flex-direction: column;
  border-radius: 0 0 8px 8px;
  overflow: hidden;
}

.doc-list-row {
  position: relative;
  min-height: 52px;
  font-size: 13px;
  color: var(--td-text-color-primary);
  border-bottom: 1px solid var(--td-component-stroke);
  cursor: pointer;
  transition: background-color 0.2s ease, box-shadow 0.2s ease, border-color 0.2s ease;

  &:last-child {
    border-bottom: 0;
  }

  &:hover:not(.selected),
  &.menu-open:not(.selected) {
    background: var(--td-bg-color-secondarycontainer);
  }

  &:hover .row-more-btn,
  &.menu-open .row-more-btn,
  &.selected .row-more-btn {
    opacity: 1;
  }
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
  gap: 10px;
  font-family: var(--app-font-family);
}

.cell-size,
.cell-time {
  justify-content: flex-end;
}

.cell-actions {
  justify-content: flex-end;
}

/* TDesign 勾选框：去掉空白 label、与表格行对齐 */
.doc-list-check {
  margin: 0;

  :deep(.t-checkbox) {
    align-items: center;
  }

  :deep(.t-checkbox__label) {
    display: none !important;
    width: 0 !important;
    min-width: 0 !important;
    margin: 0 !important;
    padding: 0 !important;
  }

  :deep(.t-checkbox__input) {
    margin: 0;
  }

  :deep(.t-checkbox__input-wrapper) {
    margin: 0;
  }
}

.row-file-icon-wrap {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  border-radius: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
}

.row-file-name {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 14px;
  font-weight: 600;
  letter-spacing: 0.01em;
  color: var(--td-text-color-primary);
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
  font-family: var(--app-font-family);
  color: var(--td-text-color-secondary);
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
  border-radius: 5px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s ease, background-color 0.15s ease, color 0.15s ease;

  &:hover {
    background: var(--td-component-stroke);
    color: var(--td-text-color-primary);
  }

  &.active {
    opacity: 1;
    background: var(--td-component-stroke);
    color: var(--td-text-color-primary);
  }
}

.row-menu {
  display: flex;
  flex-direction: column;
  min-width: 140px;
  gap: 2px;
  padding: 4px 6px;
}

.row-menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  font-size: 14px;
  line-height: 20px;
  color: var(--td-text-color-primary);
  cursor: pointer;
  border-radius: 6px;
  transition: background-color 0.15s cubic-bezier(0.2, 0, 0, 1), transform 0.12s ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:active {
    background: var(--td-bg-color-container-active);
    transform: scale(0.98);
  }

  .icon {
    font-size: 16px;
    color: var(--td-text-color-secondary);
    transition: color 0.15s ease;
  }

  &:hover .icon {
    color: var(--td-text-color-primary);
  }

  &.danger {
    color: var(--td-error-color-6);
    margin-top: 4px;
    position: relative;

    &::before {
      content: '';
      position: absolute;
      top: -3px;
      left: 8px;
      right: 8px;
      height: 1px;
      background: var(--td-component-stroke);
    }

    .icon {
      color: var(--td-error-color-6);
    }

    &:hover {
      background: var(--td-error-color-1);
      color: var(--td-error-color-6);

      .icon {
        color: var(--td-error-color-6);
      }
    }

    &:active {
      background: var(--td-error-color-2);
    }
  }
}
</style>
