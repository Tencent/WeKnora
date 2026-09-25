<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatFileSize } from '@/utils/files';
import KnowledgeTagPopover from './KnowledgeTagPopover.vue';
import DocumentFileIcon from './DocumentFileIcon.vue';
import DocumentActionMenu from './DocumentActionMenu.vue';
import FolderPickerMenu, { type FolderOption } from './FolderPickerMenu.vue';
import { shownStall } from '@/utils/knowledgeProcessingStall';

interface Tag {
  id: string;
  name: string;
  color?: string;
}

interface KnowledgeItem {
  id: string;
  file_name: string;
  original_file_name?: string;
  folder_path?: string;
  file_type?: string;
  file_size?: number | string;
  type?: string;
  tags?: Tag[];
  parse_status?: string;
  summary_status?: string;
  updated_at?: string;
  source?: string;
  description?: string;
  channel?: string;
  isMore?: boolean;
  stalled_minutes?: number;
  stall_state?: string;
}

const props = defineProps<{
  items: KnowledgeItem[];
  kbId: string;
  selectedIds: Set<string>;
  canEdit: boolean;
  canDownload: boolean;
  canMutateKnowledge: boolean;
  traceVisibleIds: Record<string, boolean>;
  loading?: boolean;
  /** Every folder of the knowledge base, for the "move to folder" picker. */
  folderOptions?: FolderOption[];
  /**
   * Show each row's folder under its name. Only useful when the list spans
   * several folders, i.e. while filtering; inside one folder the path would be
   * identical on every row.
   */
  showFolderPath?: boolean;
  // Move sub-flow state
  moveMenuMode: 'normal' | 'targets' | 'confirm';
  moveTargetKbs: any[];
  moveTargetsLoading: boolean;
  moveSelectedTargetName: string;
  moveMode: 'reuse_vectors' | 'reparse';
  moveSubmitting: boolean;
}>();

const emit = defineEmits<{
  (e: 'open', item: KnowledgeItem): void;
  (e: 'toggle-row', id: string, checked: boolean, shiftKey: boolean): void;
  (e: 'toggle-all', checked: boolean): void;
  (e: 'action', action: 'download' | 'edit' | 'reparse' | 'cancel-parse' | 'move' | 'move-folder' | 'delete' | 'view-trace' | 'batch-manage', item: KnowledgeItem): void;
  (e: 'probe-trace', item: KnowledgeItem): void;
  (e: 'tags-changed', payload?: { deletedTagId?: string }): void;
  (e: 'open-folder', path: string): void;
  (e: 'move-to-folder', item: KnowledgeItem, folderPath: string): void;
  // Move sub-flow emits
  (e: 'move-select-target', kb: any): void;
  (e: 'move-back'): void;
  (e: 'move-confirm'): void;
  (e: 'update:moveMode', mode: 'reuse_vectors' | 'reparse'): void;
  (e: 'reset-move-state'): void;
}>();

const { t } = useI18n();
const tagEditorId = ref<string | null>(null);

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

const getSourceInfo = (item: KnowledgeItem): { icon: string; label: string } => {
  const ch = item.channel;
  if (ch === 'feishu') return { icon: 'cloud-download', label: t('knowledgeBase.channelFeishu') };
  // Drive (云盘) connectors use their own channel so Drive docs show
  // "飞书云盘" / "Lark 云盘", distinct from the wiki connector's "飞书".
  if (ch === 'feishu_drive') return { icon: 'cloud-download', label: t('knowledgeBase.channelFeishuDrive') };
  if (ch === 'lark_drive') return { icon: 'cloud-download', label: t('knowledgeBase.channelLarkDrive') };
  if (ch === 'notion') return { icon: 'cloud-download', label: t('knowledgeBase.channelNotion') };
  if (ch === 'yuque') return { icon: 'cloud-download', label: t('knowledgeBase.channelYuque') };
  if (ch === 'confluence') return { icon: 'cloud-download', label: t('knowledgeBase.channelConfluence') };
  if (ch === 'gitlab') return { icon: 'cloud-download', label: t('knowledgeBase.channelGitLab') };
  if (ch === 'ima') return { icon: 'cloud-download', label: t('knowledgeBase.channelIma') };
  if (ch === 'wechat') return { icon: 'cloud-download', label: t('knowledgeBase.channelWechat') };
  if (ch === 'wecom') return { icon: 'cloud-download', label: t('knowledgeBase.channelWecom') };
  if (ch === 'dingtalk') return { icon: 'cloud-download', label: t('knowledgeBase.channelDingtalk') };
  if (ch === 'slack') return { icon: 'cloud-download', label: t('knowledgeBase.channelSlack') };
  if (ch === 'im') return { icon: 'cloud-download', label: t('knowledgeBase.channelIm') };
  if (item.type === 'url') return { icon: 'link', label: t('knowledgeBase.channelUrl') };
  if (item.type === 'manual') return { icon: 'edit', label: t('knowledgeBase.channelManual') };
  return { icon: 'upload', label: t('knowledgeBase.channelUpload') };
};

interface StatusInfo {
  label: string;
  theme: 'success' | 'warning' | 'danger' | 'primary' | 'default';
  icon?: string;
  spin?: boolean;
  hint?: string;
}
const computeStatus = (item: KnowledgeItem): StatusInfo => {
  const stall = shownStall(item.stall_state, item.stalled_minutes);
  if (stall === 'queued') {
    return {
      label: t('knowledgeBase.statusQueued'),
      theme: 'default',
      icon: 'time',
      hint: t('knowledgeBase.queuedHint', { minutes: item.stalled_minutes }),
    };
  }
  if (stall === 'stalled') {
    return {
      label: t('knowledgeBase.statusStalled'),
      theme: 'warning',
      icon: 'time',
      hint: t('knowledgeBase.stalledHint', { minutes: item.stalled_minutes }),
    };
  }
  if (item.parse_status === 'pending' || item.parse_status === 'processing') {
    return { label: t('knowledgeBase.statusProcessing'), theme: 'primary', icon: 'loading', spin: true };
  }
  // finalizing = primary parse done, enrichment subtasks still running.
  // While in this phase, prefer the specific "summary generating" copy
  // when summary is what's actually outstanding (preserves the old UX
  // where this label was tied to completed+summary_pending). Otherwise
  // fall back to the generic "finalizing" label — covers question gen
  // and graph extract, which the user historically had no visibility on.
  if (item.parse_status === 'finalizing') {
    if (item.summary_status === 'pending' || item.summary_status === 'processing') {
      return { label: t('knowledgeBase.generatingSummary'), theme: 'primary', icon: 'loading', spin: true };
    }
    return { label: t('knowledgeBase.statusFinalizing'), theme: 'primary', icon: 'loading', spin: true };
  }
  if (item.parse_status === 'failed') {
    return { label: t('knowledgeBase.statusFailed'), theme: 'danger', icon: 'close-circle' };
  }
  if (item.parse_status === 'cancelled') {
    return { label: t('knowledgeBase.statusCancelled'), theme: 'default', icon: 'stop-circle' };
  }
  if (item.parse_status === 'draft') {
    return { label: t('knowledgeBase.statusDraft'), theme: 'warning' };
  }
  // Legacy completed+summary_pending path: kept as a defensive fallback
  // for rows that bypassed finalizing (no enrichment configured, or
  // upgraded mid-flight from a pre-finalizing build).
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
  if (!visible && moreOpen.value !== id) return;
  if (visible && moreOpen.value !== id) folderPickerItemId.value = null;
  moreOpen.value = visible ? id : null;
  if (visible) {
    const it = props.items.find(i => i.id === id);
    if (it) emit('probe-trace', it);
  } else {
    folderPickerItemId.value = null;
    // Reset move state when popup closes naturally
    emit('reset-move-state');
  }
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

// Which row's action popup is currently showing the folder picker. Kept local so
// picking a folder stays inside the menu the user already opened, exactly like
// the "move to knowledge base" sub-menu next to it.
const folderPickerItemId = ref<string | null>(null);

const onFolderPicked = (item: KnowledgeItem, path: string) => {
  folderPickerItemId.value = null;
  moreOpen.value = null;
  item.isMore = false;
  emit('move-to-folder', item, path);
};

const handleAction = (action: 'download' | 'edit' | 'reparse' | 'cancel-parse' | 'move' | 'move-folder' | 'delete' | 'view-trace' | 'batch-manage', item: KnowledgeItem) => {
  // The folder picker opens inside this same popup, so keep the menu open.
  if (action === 'move-folder') {
    folderPickerItemId.value = item.id;
    return;
  }
  // Don't close popup for move — it triggers the move sub-flow
  if (action !== 'move') {
    moreOpen.value = null;
  }
  item.isMore = false;
  emit('action', action, item);
};

</script>

<template>
  <div class="doc-list-view" :class="{ 'is-loading': loading }" role="table" :aria-label="t('knowledgeEditor.document.title')">
    <div ref="stickySentinel" class="doc-list-sticky-sentinel" aria-hidden="true"></div>
    <div class="doc-list-header" :class="{ 'is-stuck': headerStuck }" role="row">
      <div class="cell cell-check" role="columnheader" @click.stop>
        <t-checkbox v-if="canEdit || canDownload" class="doc-list-check" size="small" :checked="allSelected" :indeterminate="someSelected"
          :disabled="!items.length" :title="t('knowledgeBase.selectAll')" @change="onHeaderCheckboxChange" />
      </div>
      <div class="cell cell-name" role="columnheader">{{ t('knowledgeBase.columnName') }}</div>
      <div class="cell cell-tags" role="columnheader">{{ t('knowledgeBase.columnTag') }}</div>
      <div class="cell cell-status" role="columnheader">{{ t('knowledgeBase.columnStatus') }}</div>
      <div class="cell cell-actions" role="columnheader" v-if="canEdit"></div>
    </div>

    <div class="doc-list-body" role="rowgroup">
      <div v-for="item in items" :key="item.id" class="doc-list-row"
        :class="{ selected: selectedIds.has(item.id), 'menu-open': moreOpen === item.id }" :data-select-id="item.id"
        role="row" @click="emit('open', item)">
        <div class="cell cell-check" role="cell" @click.stop>
          <t-checkbox v-if="canEdit || canDownload" class="doc-list-check" size="small" :checked="selectedIds.has(item.id)" :title="item.file_name"
            @change="(c: boolean, ctx?: { e?: Event }) => onRowCheckboxChange(item, c, ctx)" />
        </div>

        <div class="cell cell-name" role="cell">
          <DocumentFileIcon :source-type="item.type"
            :file-name="item.file_type ? `document.${item.file_type.toLowerCase()}` : (item.original_file_name || item.file_name)" />
          <div class="row-file-text">
            <div class="row-title-line">
              <button type="button" class="row-file-name" :title="item.description ? `${item.file_name}\n${item.description}` : item.file_name"
                @click.stop="emit('open', item)">{{ item.file_name }}</button>
            </div>
            <span class="row-file-meta">
              <span class="row-source"><t-icon :name="getSourceInfo(item).icon" />{{ getSourceInfo(item).label }}</span>
              <template v-if="formatFileSize(item.file_size)">
                <span class="meta-sep" aria-hidden="true">·</span>
                <span>{{ formatFileSize(item.file_size) }}</span>
              </template>
              <span class="meta-sep" aria-hidden="true">·</span>
              <span :title="t('knowledgeBase.columnUpdatedAt')">{{ formatTime(item.updated_at) }}</span>
            </span>
            <button v-if="showFolderPath && item.folder_path" type="button" class="row-file-folder"
              :title="item.folder_path" @click.stop="emit('open-folder', item.folder_path)">
              <t-icon name="folder" />
              <span>{{ item.folder_path }}</span>
            </button>
          </div>
        </div>

        <div class="cell cell-tags" role="cell">
          <KnowledgeTagPopover :kb-id="kbId" :knowledge-id="item.id" :tags="item.tags || []" :disabled="!canEdit"
            :visible="tagEditorId === item.id"
            @update:visible="(visible: boolean) => { if (visible) tagEditorId = item.id; else if (tagEditorId === item.id) tagEditorId = null }"
            @changed="emit('tags-changed', $event)">
            <div class="row-tag-trigger">
              <div v-if="item.tags?.length" class="row-tags">
                <button v-for="tag in item.tags.slice(0, 5)" :key="tag.id" type="button" class="row-tag"
                  :disabled="!canEdit" :title="tag.name">{{ tag.name }}</button>
                <button v-if="item.tags.length > 5" type="button" class="row-tag row-tag--overflow"
                  :disabled="!canEdit" :title="item.tags.slice(5).map(tag => tag.name).join(', ')">+{{ item.tags.length - 5 }}</button>
              </div>
              <button v-else-if="canEdit" type="button" class="row-tag-add">
                <t-icon name="add" size="12px" />
                {{ t('knowledgeBase.tagAddAction') }}
              </button>
            </div>
          </KnowledgeTagPopover>
        </div>

        <div class="cell cell-status" role="cell">
          <template v-if="statusByRow.get(item.id) as StatusInfo | undefined">
            <t-tag v-if="statusByRow.get(item.id)!.label !== '--'" size="small" :theme="statusByRow.get(item.id)!.theme"
              variant="light" class="row-status-tag" :class="`status-${statusByRow.get(item.id)!.theme}`"
              :title="statusByRow.get(item.id)!.hint">
              <template v-if="statusByRow.get(item.id)!.icon" #icon>
                <t-icon :name="statusByRow.get(item.id)!.icon!"
                  :class="{ 'icon-spin': statusByRow.get(item.id)!.spin }" />
              </template>
              {{ statusByRow.get(item.id)!.label }}
            </t-tag>
            <span v-else class="row-muted">--</span>
          </template>
        </div>

        <div class="cell cell-actions" role="cell" v-if="canEdit" @click.stop>
          <t-popup :visible="moreOpen === item.id" placement="bottom-right" trigger="click" destroy-on-close overlay-class-name="card-more"
            :on-visible-change="(v: boolean) => onMoreVisible(item.id, v)">
            <button class="row-more-btn" :class="{ active: moreOpen === item.id }" type="button"
              :aria-label="`${item.file_name} · ${t('knowledgeBase.columnActions')}`" :aria-expanded="moreOpen === item.id">
              <t-icon name="more" size="16px" />
            </button>
            <template #content>
              <!-- Move: folder picker (must win over the normal menu while open) -->
              <div v-if="folderPickerItemId === item.id" class="card-menu move-menu">
                <FolderPickerMenu
                  :options="folderOptions || []"
                  :current-path="item.folder_path || ''"
                  show-back
                  @back="folderPickerItemId = null"
                  @confirm="(path: string) => onFolderPicked(item, path)"
                />
              </div>

              <!-- Normal menu -->
              <div v-else-if="moveMenuMode === 'normal'" class="card-menu">
                <button type="button" class="card-menu-item row-tag-menu-action" @click.stop="moreOpen = null; tagEditorId = item.id">
                  <t-icon name="discount" class="icon" />
                  <span>{{ t('knowledgeBase.tagEditDialogHeading') }}</span>
                </button>
                <DocumentActionMenu
                  :item="item"
                  :can-download="canDownload"
                  :can-mutate-knowledge="canMutateKnowledge"
                  :trace-visible="!!traceVisibleIds[item.id] || (item.parse_status === 'pending' || item.parse_status === 'processing' || item.parse_status === 'finalizing')"
                  @download="handleAction('download', item)"
                  @edit="handleAction('edit', item)"
                  @view-trace="handleAction('view-trace', item)"
                  @reparse="handleAction('reparse', item)"
                  @cancel-parse="handleAction('cancel-parse', item)"
                  @move="handleAction('move', item)"
                  @move-folder="handleAction('move-folder', item)"
                  @batch-manage="handleAction('batch-manage', item)"
                  @delete="handleAction('delete', item)"
                />
              </div>

              <!-- Move: target KB list -->
              <div v-else-if="moveMenuMode === 'targets'" class="card-menu move-menu">
                <div class="move-menu-header" @click.stop="emit('move-back')">
                  <t-icon name="chevron-left" size="16px" />
                  <span>{{ $t('knowledgeBase.moveToKnowledgeBase') }}</span>
                </div>
                <div v-if="moveTargetsLoading" class="move-menu-loading">
                  <t-loading size="small" />
                </div>
                <div v-else-if="moveTargetKbs.length === 0" class="move-menu-empty">
                  {{ $t('knowledgeBase.moveNoTargets') }}
                </div>
                <template v-else>
                  <div v-for="kb in moveTargetKbs" :key="kb.id" class="card-menu-item"
                    @click.stop="emit('move-select-target', kb)">
                    <t-icon class="icon" name="root-list" />
                    <span class="move-target-name">{{ kb.name }}</span>
                    <span v-if="kb.knowledge_count !== undefined" class="move-target-count">{{ kb.knowledge_count }}</span>
                  </div>
                </template>
              </div>

              <!-- Move: confirm with mode selection -->
              <div v-else-if="moveMenuMode === 'confirm'" class="card-menu move-menu">
                <div class="move-menu-header" @click.stop="emit('move-back')">
                  <t-icon name="chevron-left" size="16px" />
                  <span>{{ $t('knowledgeBase.moveConfirmTitle') }}</span>
                </div>
                <div class="move-confirm-body">
                  <div class="move-target-info">
                    <t-icon name="arrow-right" size="14px" />
                    <span>{{ moveSelectedTargetName }}</span>
                  </div>
                  <div class="move-mode-item" :class="{ active: moveMode === 'reuse_vectors' }"
                    @click.stop="emit('update:moveMode', 'reuse_vectors')">
                    <t-radio :checked="moveMode === 'reuse_vectors'" />
                    <div class="move-mode-text">
                      <span class="move-mode-label">{{ $t('knowledgeBase.moveModeReuseVectors') }}</span>
                      <span class="move-mode-desc">{{ $t('knowledgeBase.moveModeReuseVectorsDesc') }}</span>
                    </div>
                  </div>
                  <div class="move-mode-item" :class="{ active: moveMode === 'reparse' }"
                    @click.stop="emit('update:moveMode', 'reparse')">
                    <t-radio :checked="moveMode === 'reparse'" />
                    <div class="move-mode-text">
                      <span class="move-mode-label">{{ $t('knowledgeBase.moveModeReparse') }}</span>
                      <span class="move-mode-desc">{{ $t('knowledgeBase.moveModeReparseDesc') }}</span>
                    </div>
                  </div>
                  <div class="move-confirm-actions">
                    <t-button size="small" variant="outline" @click.stop="emit('move-back')">{{
                      $t('common.cancel') }}</t-button>
                    <t-button size="small" theme="primary" :loading="moveSubmitting"
                      @click.stop="emit('move-confirm')">{{
                        $t('knowledgeBase.moveConfirm') }}</t-button>
                  </div>
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
  background: transparent;
  /* 不能用 overflow:hidden，否则表头 position:sticky 相对外层滚动区失效 */
  overflow: visible;
  animation: doc-list-fade-in 0.32s ease-out;
}

.doc-list-header,
.doc-list-row {
  display: grid;
  grid-template-columns: 24px minmax(0, 1fr) minmax(180px, 24%) 100px 28px;
  align-items: center;
  column-gap: 12px;
  padding: 0 8px;
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
  font-size: var(--app-text-sm);
  font-weight: 500;
  font-family: var(--app-font-family);
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-container);
  border-bottom: 0;
  transition: border-radius var(--app-motion-fast) ease, box-shadow var(--app-motion-base) ease;

  &.is-stuck {
    border-radius: 0;
    box-shadow: 0 4px 10px rgba(0, 0, 0, 0.08);
  }
}

.doc-list-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 6px 0 72px;
}

.doc-list-row {
  position: relative;
  min-height: 68px;
  padding-top: 10px;
  padding-bottom: 10px;
  box-sizing: border-box;
  border-radius: var(--app-radius-md);
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
  cursor: pointer;
  transition: background-color var(--app-motion-base) ease, box-shadow var(--app-motion-base) ease, border-color var(--app-motion-base) ease;

  &:hover:not(.selected),
  &.menu-open:not(.selected) {
    background: var(--td-bg-color-secondarycontainer);
  }

  &:focus-within .row-more-btn,
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
  padding: 0;

  &:first-child {
    padding-left: 0;
  }

  &:last-child {
    padding-right: 0;
  }
}

.cell-check {
  justify-content: center;
  padding: 0;
}

.cell-name {
  gap: 12px;
  font-family: var(--app-font-family);
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
  width: 32px;
  height: 38px;
  border-radius: var(--app-radius-sm);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 26px;
  background: transparent;
  color: var(--td-text-color-secondary);
}

.row-file-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.row-file-name {
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: var(--app-text-base);
  font-weight: 500;
  line-height: 1.4;
  text-align: left;
  border: 0;
  padding: 0;
  background: transparent;
  font-family: inherit;
  cursor: pointer;
  color: var(--td-text-color-primary);
}

.row-file-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  font-variant-numeric: tabular-nums;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.row-file-folder {
  display: inline-flex;
  align-items: center;
  align-self: flex-start;
  gap: 4px;
  max-width: 100%;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
  font-size: var(--app-text-sm);
  cursor: pointer;
  transition: color var(--app-motion-fast) ease;

  &:hover {
    color: var(--td-brand-color);
  }

  span {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .t-icon {
    flex: 0 0 auto;
    font-size: var(--app-text-md);
  }
}

.row-muted {
  color: var(--td-text-color-disabled);
}

.row-status-tag {
  background: transparent;
  border: 0;
  padding: 0;
  color: var(--td-text-color-secondary);
  &.status-success::before { content: ''; width: 5px; height: 5px; margin-right: 6px; border-radius: 50%; background: var(--td-success-color); }
  &.status-danger { color: var(--td-error-color); }
  &.status-warning { color: var(--td-warning-color); }
  &.status-primary { color: var(--td-brand-color); }
  &.status-default { color: var(--td-text-color-placeholder); }
}
.row-title-line { display: flex; align-items: center; min-width: 0; }
.row-tags { display: flex; flex-wrap: wrap; align-items: center; gap: 5px; min-width: 0; width: 100%; }
.row-tag {
  flex: 0 0 auto;
  max-width: min(180px, 100%);
  box-sizing: border-box;
  padding: 2px 7px;
  border: 0;
  border-radius: 5px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  line-height: 18px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
  &:disabled { cursor: default; }
  &:not(:disabled):hover { color: var(--td-brand-color); background: var(--td-brand-color-light); }
  &--overflow { background: transparent; color: var(--td-text-color-placeholder); }
}
.row-tag-add {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 7px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 5px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  font: inherit;
  font-size: var(--app-text-sm);
  line-height: 18px;
  white-space: nowrap;
  cursor: pointer;
  transition: color var(--app-motion-fast) ease, border-color var(--app-motion-fast) ease, background var(--app-motion-fast) ease;

  &:hover {
    color: var(--td-brand-color);
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}
.row-tag-menu-action {
  width: 100%; border: 0; background: transparent; text-align: left; font-family: inherit; cursor: pointer;
  &:hover { background: var(--td-bg-color-container-hover); }
}
.row-status-tag :deep(.t-icon) {
  margin-right: 2px;
}

.icon-spin {
  animation: wk-spin 0.9s linear infinite;
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
  transition: opacity var(--app-motion-fast) ease, background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

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

.row-source {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.meta-sep { opacity: 0.6; }
.doc-list-row.selected { background: var(--app-selection-bg); }
.row-file-name:focus-visible,
.row-file-folder:focus-visible,
.row-more-btn:focus-visible,
.row-tag:focus-visible,
.row-tag-add:focus-visible {
  outline: 2px solid var(--app-focus-border);
  outline-offset: 3px;
  border-radius: var(--app-radius-xs);
}
@container doc-card-area (max-width: 720px) {
  .doc-list-header, .doc-list-row {
    grid-template-columns: 24px minmax(0, 1fr) 100px 28px;
    column-gap: 8px;
  }
  .doc-list-header .cell-tags { display: none; }
  .doc-list-row .cell-tags { grid-column: 2; grid-row: 2; padding-left: 44px; }
  .row-tags { padding-top: 6px; }
  .row-tag-add { margin-top: 6px; }
  .cell-status { grid-column: 3; grid-row: 1; }
  .cell-actions { grid-column: 4; grid-row: 1; }
  .row-file-meta { flex-wrap: wrap; gap: 2px 6px; }
}
@media (hover: none) {
  .row-more-btn { opacity: 1; }
}
@media (prefers-reduced-motion: reduce) {
  .doc-list-view, .icon-spin { animation: none; }
}
</style>
