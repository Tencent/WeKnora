<script setup lang="ts">
import { ref, computed, nextTick, onBeforeUnmount, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatFileSize } from '@/utils/files';
import { formatReferenceSnippet } from '@/utils/referenceSources';
import KnowledgeTagPopover from './KnowledgeTagPopover.vue';
import DocumentFileIcon from './DocumentFileIcon.vue';
import DocumentActionMenu from './DocumentActionMenu.vue';
import FolderPickerMenu, { type FolderOption } from './FolderPickerMenu.vue';
import KnowledgeProcessingTimeline from '@/components/knowledge-processing-timeline.vue';
import { shownStall } from '@/utils/knowledgeProcessingStall';

interface KnowledgeCard {
  id: string;
  knowledge_base_id?: string;
  parse_status: string;
  summary_status?: string;
  description?: string;
  file_name?: string;
  folder_path?: string;
  original_file_name?: string;
  display_name?: string;
  title?: string;
  type?: string;
  updated_at?: string;
  file_type?: string;
  isMore?: boolean;
  metadata?: any;
  error_message?: string;
  tags?: Array<{ id: string; name: string; color?: string }>;
  stalled_minutes?: number;
  stall_state?: string;
  source?: string;
  created_at?: string;
  file_size?: number | string;
  channel?: string;
}

const props = defineProps<{
  items: KnowledgeCard[];
  kbId: string;
  selectedIds: Set<string>;
  batchMode: boolean;
  canEdit: boolean;
  canDownload: boolean;
  canMutateKnowledge: boolean;
  traceAvailableById: Record<string, boolean>;
  /** Every folder of the knowledge base, for the "move to folder" picker. */
  folderOptions?: FolderOption[];
  /**
   * Replace the updated-at line with the card's folder. Only meaningful when
   * the grid spans several folders, i.e. while filtering.
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
  (e: 'open', item: KnowledgeCard): void;
  (e: 'toggle-checkbox', id: string, checked: boolean, ctx?: { e?: Event }): void;
  (e: 'menu-visible-change', visible: boolean, item: KnowledgeCard): void;
  (e: 'action', action: 'download' | 'edit' | 'view-trace' | 'reparse' | 'cancel-parse' | 'move' | 'move-folder' | 'batch-manage' | 'delete', item: KnowledgeCard): void;
  (e: 'tags-changed', payload?: { deletedTagId?: string }): void;
  (e: 'open-folder', path: string): void;
  (e: 'move-to-folder', item: KnowledgeCard, folderPath: string): void;
  // Move sub-flow emits
  (e: 'move-select-target', kb: any): void;
  (e: 'move-back'): void;
  (e: 'move-confirm'): void;
  (e: 'update:moveMode', mode: 'reuse_vectors' | 'reparse'): void;
}>();

const { t } = useI18n();
const tagEditorId = ref<string | null>(null);
const cardSummaries = computed(() => new Map(props.items.map(item => [item.id, formatReferenceSnippet(item.description)])));

// Which row's action popup is currently showing the folder picker. Kept local so
// picking a folder stays inside the menu the user already opened, exactly like
// the "move to knowledge base" sub-menu next to it.
const folderPickerItemId = ref<string | null>(null);

// --- Menu index tracking ---
const activeMenuIndex = ref(-1);
const onMenuVisibleChange = (visible: boolean, item: KnowledgeCard, index: number) => {
  // Let the popup own the trigger click. Opening it in the button's click
  // handler makes the popup interpret that same click as a request to close.
  if (visible) {
    dismissCardPopover();
    if (activeMenuIndex.value !== index) folderPickerItemId.value = null;
    activeMenuIndex.value = index;
  } else {
    // Closing the previous card must not dismiss a newly opened card's menu.
    if (activeMenuIndex.value !== index) return;
    activeMenuIndex.value = -1;
    folderPickerItemId.value = null;
  }
  emit('menu-visible-change', visible, item);
};

// --- Parse status helpers ---
const CANCELABLE_PARSE_STATUSES = new Set(['pending', 'processing', 'finalizing']);
const isParseInFlight = (status?: string): boolean =>
  CANCELABLE_PARSE_STATUSES.has(String(status ?? ''));

const isTraceMenuVisible = (item: KnowledgeCard): boolean => {
  if (!item?.id) return false;
  if (isParseInFlight(item.parse_status)) return true;
  return props.traceAvailableById[item.id] === true;
};

const inFlightCardStatusText = (item: KnowledgeCard): string => {
  const stall = shownStall(item.stall_state, item.stalled_minutes);
  if (stall) return t(stall === 'queued' ? 'knowledgeBase.statusQueued' : 'knowledgeBase.statusStalled');
  if (item.parse_status === 'finalizing') {
    if (item.summary_status === 'pending' || item.summary_status === 'processing') {
      return t('knowledgeBase.generatingSummary');
    }
    return t('knowledgeBase.statusFinalizing');
  }
  return t('knowledgeBase.parsingInProgress');
};

// --- Display helpers ---
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

const getKnowledgeType = (item: KnowledgeCard) => {
  if (item.type === 'url') return t('knowledgeBase.typeURL') || 'URL';
  if (item.type === 'manual') return t('knowledgeBase.typeManual');
  if (item.file_type) return item.file_type.toUpperCase();
  return '--';
};

const channelLabelMap: Record<string, string> = {
  web: 'knowledgeBase.channelWeb',
  api: 'knowledgeBase.channelApi',
  browser_extension: 'knowledgeBase.channelBrowserExtension',
  wechat: 'knowledgeBase.channelWechat',
  wecom: 'knowledgeBase.channelWecom',
  feishu: 'knowledgeBase.channelFeishu',
  gitlab: 'knowledgeBase.channelGitLab',
  confluence: 'knowledgeBase.channelConfluence',
  dingtalk: 'knowledgeBase.channelDingtalk',
  slack: 'knowledgeBase.channelSlack',
  im: 'knowledgeBase.channelIm',
  ima: 'knowledgeBase.channelIma',
};

const getChannelLabel = (channel: string) => {
  const key = channelLabelMap[channel];
  return key ? t(key) : t('knowledgeBase.channelUnknown');
};

// --- Card click handler ---
const onCardClick = (item: KnowledgeCard) => {
  if (props.batchMode) {
    emit('toggle-checkbox', item.id, !props.selectedIds.has(item.id));
    return;
  }
  dismissCardPopover();
  emit('open', item);
};

// --- Hover popover ---
const hoveredCardItem = ref<KnowledgeCard | null>(null);
const cardPopoverPos = ref({ x: 0, y: 0 });
const CARD_POPOVER_OFFSET = 12;
const CARD_POPOVER_ESTIMATED_WIDTH = 360;
const CARD_POPOVER_ESTIMATED_HEIGHT = 300;
const cardHoverShowDelay = 650;
let cardHoverTimer: ReturnType<typeof setTimeout> | null = null;
let cardPopoverElement: HTMLElement | null = null;

const dismissCardPopover = () => {
  if (cardHoverTimer) {
    clearTimeout(cardHoverTimer);
    cardHoverTimer = null;
  }
  hoveredCardItem.value = null;
  cardPopoverElement = null;
};

const calculatePopoverPositionFromCard = (cardElement: HTMLElement): { x: number; y: number } => {
  const cardRect = cardElement.getBoundingClientRect();
  const viewportWidth = window.innerWidth;
  const viewportHeight = window.innerHeight;

  let popoverWidth = CARD_POPOVER_ESTIMATED_WIDTH;
  let popoverHeight = CARD_POPOVER_ESTIMATED_HEIGHT;

  if (cardPopoverElement) {
    const rect = cardPopoverElement.getBoundingClientRect();
    if (rect.width > 0) popoverWidth = rect.width;
    if (rect.height > 0) popoverHeight = rect.height;
  }

  let x = 0;
  let y = 0;

  // Strategy 1: right side
  const rightX = cardRect.right + CARD_POPOVER_OFFSET;
  if (rightX + popoverWidth <= viewportWidth - 10) {
    x = rightX;
    y = cardRect.top;
    if (y + popoverHeight > viewportHeight - 10) y = viewportHeight - popoverHeight - 10;
    y = Math.max(10, y);
    return { x, y };
  }

  // Strategy 2: left side
  const leftX = cardRect.left - popoverWidth - CARD_POPOVER_OFFSET;
  if (leftX >= 10) {
    x = leftX;
    y = cardRect.top;
    if (y + popoverHeight > viewportHeight - 10) y = viewportHeight - popoverHeight - 10;
    y = Math.max(10, y);
    return { x, y };
  }

  // Strategy 3: below
  const bottomY = cardRect.bottom + CARD_POPOVER_OFFSET;
  if (bottomY + popoverHeight <= viewportHeight - 10) {
    y = bottomY;
    x = cardRect.left;
    if (x + popoverWidth > viewportWidth - 10) x = viewportWidth - popoverWidth - 10;
    x = Math.max(10, x);
    return { x, y };
  }

  // Strategy 4: above
  const topY = cardRect.top - popoverHeight - CARD_POPOVER_OFFSET;
  y = Math.max(10, topY);
  x = cardRect.left;
  if (x + popoverWidth > viewportWidth - 10) x = viewportWidth - popoverWidth - 10;
  x = Math.max(10, x);
  return { x, y };
};

const onCardMouseEnter = (ev: MouseEvent, item: KnowledgeCard) => {
  if (props.batchMode || activeMenuIndex.value !== -1) return;
  if (cardHoverTimer) {
    clearTimeout(cardHoverTimer);
    cardHoverTimer = null;
  }
  const cardElement = (ev.currentTarget as HTMLElement);
  cardHoverTimer = setTimeout(() => {
    cardHoverTimer = null;
    // Folder navigation can replace the card list before this delayed callback
    // runs. A detached card has a zero rect, which used to place the teleported
    // popover at the top-left corner of the viewport.
    if (!cardElement.isConnected || !props.items.some(candidate => candidate.id === item.id)) return;
    hoveredCardItem.value = item;
    const pos = calculatePopoverPositionFromCard(cardElement);
    cardPopoverPos.value = pos;
    nextTick(() => {
      if (!cardElement.isConnected || hoveredCardItem.value?.id !== item.id) return;
      cardPopoverElement = document.querySelector('.knowledge-card-hover-popover') as HTMLElement;
      if (cardPopoverElement) {
        const refinedPos = calculatePopoverPositionFromCard(cardElement);
        cardPopoverPos.value = refinedPos;
      }
    });
  }, cardHoverShowDelay);
};

const onCardMouseLeave = () => {
  dismissCardPopover();
};

// Browsing to another folder swaps the item collection without necessarily
// dispatching mouseleave on a card that Vue removes.
watch(() => props.items, dismissCardPopover);
onBeforeUnmount(dismissCardPopover);

const onOpenFolder = (path: string) => {
  dismissCardPopover();
  emit('open-folder', path);
};

const onFolderPicked = (item: KnowledgeCard, path: string) => {
  folderPickerItemId.value = null;
  if (item.isMore !== undefined) item.isMore = false;
  activeMenuIndex.value = -1;
  emit('move-to-folder', item, path);
};

const editTags = (item: KnowledgeCard) => {
  dismissCardPopover();
  activeMenuIndex.value = -1;
  item.isMore = false;
  emit('menu-visible-change', false, item);
  tagEditorId.value = item.id;
};

// --- Action handlers ---
const handleAction = (action: 'download' | 'edit' | 'view-trace' | 'reparse' | 'cancel-parse' | 'move' | 'move-folder' | 'batch-manage' | 'delete', item: KnowledgeCard) => {
  // The folder picker opens inside this same popup, so keep the menu open.
  if (action === 'move-folder') {
    folderPickerItemId.value = item.id;
    return;
  }
  // Don't close menu for move — it triggers the sub-flow
  if (action !== 'move') {
    if (item.isMore !== undefined) item.isMore = false;
    activeMenuIndex.value = -1;
  }
  emit('action', action, item);
};
</script>

<template>
  <div class="doc-card-view">

    <div class="doc-card-list doc-card-list-animated">
    <div
      class="knowledge-card"
      :class="{ 'is-selected': selectedIds.has(item.id), 'batch-mode': batchMode }"
      :data-select-id="item.id"
      v-for="(item, index) in items"
      :key="item.id"
      @click="onCardClick(item)"
    >
      <div class="card-content">
        <div class="card-content-nav">
          <div class="card-file-icon" :title="[getKnowledgeType(item), formatFileSize(Number(item.file_size))].filter(Boolean).join(' · ')">
            <DocumentFileIcon :source-type="item.type"
              :file-name="item.file_type ? `document.${item.file_type.toLowerCase()}` : (item.original_file_name || item.file_name || '')" />
          </div>
          <button type="button" class="card-content-title" :title="item.file_name"
            @click.stop="onCardClick(item)">{{ item.file_name }}</button>
          <div v-if="(canEdit || canDownload) && batchMode" class="card-nav-check" @click.stop>
            <t-checkbox
              class="card-select-checkbox"
              size="small"
              :checked="selectedIds.has(item.id)"
              :aria-label="item.file_name"
              :title="item.file_name"
              @change="(checked: boolean, ctx?: { e?: Event }) => emit('toggle-checkbox', item.id, checked, ctx)"
            />
          </div>
          <t-popup
            v-else-if="canEdit"
            :visible="activeMenuIndex === index"
            overlayClassName="card-more"
            :on-visible-change="(v: boolean) => onMenuVisibleChange(v, item, index)"
            trigger="click"
            destroy-on-close
            placement="bottom-right"
          >
            <button
              type="button"
              :aria-label="`${item.file_name} · ${t('knowledgeBase.columnActions')}`"
              :aria-expanded="activeMenuIndex === index"
              class="more-wrap"
              @click.stop
              :class="[activeMenuIndex === index ? 'active-more' : '']"
            >
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
                <button type="button" class="card-menu-item card-tag-menu-action" @click.stop="editTags(item)">
                  <t-icon class="icon" name="tag" />
                  <span>{{ t('knowledgeBase.tagEditDialogHeading') }}</span>
                </button>
                <DocumentActionMenu
                  :item="item"
                  :can-download="canDownload"
                  :can-mutate-knowledge="canMutateKnowledge"
                  :trace-visible="isTraceMenuVisible(item)"
                  :folders-available="Boolean(folderOptions?.length)"
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
                  <div
                    v-for="kb in moveTargetKbs"
                    :key="kb.id"
                    class="card-menu-item"
                    @click.stop="emit('move-select-target', kb)"
                  >
                    <t-icon class="icon" name="root-list" />
                    <span class="move-target-name">{{ kb.name }}</span>
                    <span v-if="kb.knowledge_count !== undefined" class="move-target-count">{{ kb.knowledge_count }}</span>
                  </div>
                </template>
              </div>

              <!-- Move: confirm -->
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
                  <div
                    class="move-mode-item"
                    :class="{ active: moveMode === 'reuse_vectors' }"
                    @click.stop="emit('update:moveMode', 'reuse_vectors')"
                  >
                    <t-radio :checked="moveMode === 'reuse_vectors'" />
                    <div class="move-mode-text">
                      <span class="move-mode-label">{{ $t('knowledgeBase.moveModeReuseVectors') }}</span>
                      <span class="move-mode-desc">{{ $t('knowledgeBase.moveModeReuseVectorsDesc') }}</span>
                    </div>
                  </div>
                  <div
                    class="move-mode-item"
                    :class="{ active: moveMode === 'reparse' }"
                    @click.stop="emit('update:moveMode', 'reparse')"
                  >
                    <t-radio :checked="moveMode === 'reparse'" />
                    <div class="move-mode-text">
                      <span class="move-mode-label">{{ $t('knowledgeBase.moveModeReparse') }}</span>
                      <span class="move-mode-desc">{{ $t('knowledgeBase.moveModeReparseDesc') }}</span>
                    </div>
                  </div>
                  <div class="move-confirm-actions">
                    <t-button size="small" variant="outline" @click.stop="emit('move-back')">{{
                      $t('common.cancel')
                    }}</t-button>
                    <t-button size="small" theme="primary" :loading="moveSubmitting" @click.stop="emit('move-confirm')">{{
                      $t('knowledgeBase.moveConfirm')
                    }}</t-button>
                  </div>
                </div>
              </div>
            </template>
          </t-popup>
        </div>

        <div class="card-preview" @mouseenter="onCardMouseEnter($event, item)" @mouseleave="onCardMouseLeave">
        <!-- Parse status display -->
        <div v-if="isParseInFlight(item.parse_status)" class="card-analyze card-analyze-trace"
          :class="shownStall(item.stall_state, item.stalled_minutes)">
          <t-icon :name="shownStall(item.stall_state, item.stalled_minutes) ? 'time' : 'loading'"
            class="card-analyze-loading"></t-icon>
          <span
            class="card-analyze-txt card-analyze-trace-link"
            role="button"
            tabindex="0"
            :title="shownStall(item.stall_state, item.stalled_minutes)
              ? $t(shownStall(item.stall_state, item.stalled_minutes) === 'queued'
                ? 'knowledgeBase.queuedHint' : 'knowledgeBase.stalledHint', { minutes: item.stalled_minutes })
              : $t('knowledgeStages.viewTrace')"
            @click.stop="handleAction('view-trace', item)"
            @keydown.enter.stop="handleAction('view-trace', item)"
            @keydown.space.prevent.stop="handleAction('view-trace', item)"
          >{{ inFlightCardStatusText(item) }}</span>
          <button
            type="button"
            class="card-analyze-trace-btn"
            :title="$t('knowledgeStages.viewTrace')"
            :aria-label="$t('knowledgeStages.viewTrace')"
            @click.stop="handleAction('view-trace', item)"
          >
            <t-icon name="chart-line" />
          </button>
        </div>
        <div v-else-if="item.parse_status === 'failed'" class="card-analyze failure card-analyze-trace">
          <t-icon name="close-circle" class="card-analyze-loading failure"></t-icon>
          <span
            class="card-analyze-txt failure card-analyze-trace-link"
            role="button"
            tabindex="0"
            :title="$t('knowledgeStages.viewTrace')"
            @click.stop="handleAction('view-trace', item)"
            @keydown.enter.stop="handleAction('view-trace', item)"
            @keydown.space.prevent.stop="handleAction('view-trace', item)"
          >{{ $t('knowledgeBase.parsingFailed') }}</span>
          <button
            type="button"
            class="card-analyze-trace-btn"
            :title="$t('knowledgeStages.viewTrace')"
            :aria-label="$t('knowledgeStages.viewTrace')"
            @click.stop="handleAction('view-trace', item)"
          >
            <t-icon name="chart-bar" />
          </button>
        </div>
        <div v-else-if="item.parse_status === 'draft'" class="card-draft">
          <t-tag size="small" theme="warning" variant="light-outline">{{ $t('knowledgeBase.draft') }}</t-tag>
          <span class="card-draft-tip">{{ $t('knowledgeBase.draftTip') }}</span>
        </div>
        <div
          v-else-if="item.parse_status === 'completed' && (item.summary_status === 'pending' || item.summary_status === 'processing')"
          class="card-analyze"
        >
          <t-icon name="loading" class="card-analyze-loading"></t-icon>
          <span class="card-analyze-txt">{{ $t('knowledgeBase.generatingSummary') }}</span>
        </div>
        <div v-else-if="item.parse_status === 'cancelled'" class="card-analyze card-cancelled">
          <t-icon name="stop-circle" />
          <span>{{ t('knowledgeBase.statusCancelled') }}</span>
        </div>
        <div v-else class="card-content-txt" :class="{ 'is-empty': !cardSummaries.get(item.id) }">
          {{ cardSummaries.get(item.id) || t('knowledgeBase.noDocumentSummary') }}
        </div>
        </div>
      </div>

      <div class="card-bottom">
        <KnowledgeTagPopover v-if="canEdit || item.tags?.length" class="card-tags-anchor"
          :kb-id="kbId" :knowledge-id="item.id" :tags="item.tags || []" :disabled="!canEdit"
          :visible="tagEditorId === item.id"
          @update:visible="(visible: boolean) => { if (visible) tagEditorId = item.id; else if (tagEditorId === item.id) tagEditorId = null }"
          @changed="emit('tags-changed', $event)">
          <div class="card-tags">
            <template v-if="item.tags?.length">
              <button v-for="tag in item.tags.slice(0, 1)" :key="tag.id" type="button" class="card-tag-chip"
                :disabled="!canEdit" :title="tag.name">{{ tag.name }}</button>
              <button v-if="item.tags.length > 1" type="button" class="card-tag-overflow" :disabled="!canEdit"
                :title="item.tags.slice(1).map(tag => tag.name).join('、')">+{{ item.tags.length - 1 }}</button>
            </template>
            <button v-else-if="canEdit" type="button" class="card-tag-add">
              <t-icon name="add" size="12px" />{{ t('knowledgeBase.tagAddAction') }}
            </button>
          </div>
        </KnowledgeTagPopover>

        <button v-if="showFolderPath && item.folder_path" type="button" class="card-folder"
          :title="item.folder_path" @click.stop="onOpenFolder(item.folder_path)">
          <t-icon name="folder" />
          <span>{{ item.folder_path }}</span>
        </button>
        <span v-else class="card-time" :title="t('knowledgeBase.columnUpdatedAt')">{{ formatDocTime(item.updated_at) }}</span>
      </div>
    </div>
    </div>
  </div>

  <!-- Hover popover -->
  <Teleport to="body">
    <div
      v-show="hoveredCardItem"
      class="knowledge-card-hover-popover"
      :style="{ left: cardPopoverPos.x + 'px', top: cardPopoverPos.y + 'px' }"
    >
      <template v-if="hoveredCardItem">
        <div class="card-popover-title">{{ hoveredCardItem.file_name }}</div>
        <div v-if="isParseInFlight(hoveredCardItem.parse_status)" class="card-popover-status parsing">
          <KnowledgeProcessingTimeline
            :knowledge-id="hoveredCardItem.id"
            :parse-status="hoveredCardItem.parse_status"
            :auto-poll="false"
            :compact="true"
          />
        </div>
        <div v-else-if="hoveredCardItem.parse_status === 'failed'" class="card-popover-status failure">
          <KnowledgeProcessingTimeline
            :knowledge-id="hoveredCardItem.id"
            :parse-status="hoveredCardItem.parse_status"
            :auto-poll="false"
            :compact="true"
          />
        </div>
        <div v-else-if="hoveredCardItem.parse_status === 'draft'" class="card-popover-status draft">
          {{ $t('knowledgeBase.draft') }}
        </div>
        <template v-else>
          <div v-if="cardSummaries.get(hoveredCardItem.id)" class="card-popover-desc">{{ cardSummaries.get(hoveredCardItem.id) }}</div>
          <div v-if="(hoveredCardItem as any).source" class="card-popover-source" :title="(hoveredCardItem as any).source">
            <t-icon name="link" size="12px" /> {{ (hoveredCardItem as any).source }}
          </div>
          <div class="card-popover-extra">
            <span v-if="(hoveredCardItem as any).created_at" class="card-popover-created">
              {{ $t('knowledgeBase.createdAt') }}：{{ formatDocTime((hoveredCardItem as any).created_at) }}
            </span>
            <span v-if="formatFileSize((hoveredCardItem as any).file_size)" class="card-popover-size">
              {{ formatFileSize((hoveredCardItem as any).file_size) }}
            </span>
          </div>
        </template>
        <div class="card-popover-meta">
          <span class="card-popover-time">{{ $t('knowledgeBase.updatedAt') }}：{{ formatDocTime(hoveredCardItem.updated_at) }}</span>
          <span
            v-if="(hoveredCardItem as any).channel && (hoveredCardItem as any).channel !== 'web'"
            class="card-popover-channel"
          >{{ getChannelLabel((hoveredCardItem as any).channel) }}</span>
          <div v-if="(hoveredCardItem as any).tags && (hoveredCardItem as any).tags.length > 0" class="card-popover-tags">
            <t-tag
              v-for="tag in (hoveredCardItem as any).tags"
              :key="tag.id"
              size="small"
              variant="light-outline"
              class="card-popover-tag-chip"
            >
              <span class="tag-text">{{ tag.name }}</span>
            </t-tag>
          </div>
          <span class="card-popover-type">{{ getKnowledgeType(hoveredCardItem) }}</span>
        </div>
        <div class="card-popover-hint">{{ $t('knowledgeBase.clickToViewFull') }}</div>
      </template>
    </div>
  </Teleport>
</template>

<style scoped lang="less">
@keyframes contentFadeIn {
  from { opacity: 0; transform: translateY(6px); }
  to { opacity: 1; transform: translateY(0); }
}

.doc-card-view {
  width: 100%;
  padding-top: 12px;
}

.doc-card-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(260px, 100%), 1fr));
  gap: 10px;
  width: 100%;
  &.doc-card-list-animated { animation: contentFadeIn 0.32s ease-out; }
}

.knowledge-card {
  min-width: 0;
  min-height: 120px;
  display: flex;
  flex-direction: column;
  padding: 10px;
  box-sizing: border-box;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  cursor: pointer;
  transition: border-color var(--app-motion-base) ease, box-shadow var(--app-motion-base) ease;
  &:hover {
    border-color: var(--app-selection-border);
    box-shadow: 0 3px 12px rgba(0, 0, 0, 0.035);
  }

  &.is-selected { border-color: var(--app-selection-border); background: var(--td-bg-color-container); box-shadow: none; }

  .card-content { min-width: 0; }
  .card-content-nav { display: flex; align-items: flex-start; gap: 7px; height: 36px; margin-bottom: 4px; }
  .card-content-title {
    flex: 1;
    min-width: 0;
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    overflow: hidden;
    overflow-wrap: anywhere;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--td-text-color-primary);
    font: inherit;
    font-size: var(--app-text-base);
    font-weight: 600;
    line-height: 18px;
    text-align: left;
    cursor: pointer;
  }
  .more-wrap {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    margin: -3px -5px 0 0;
    padding: 0;
    border: 0;
    border-radius: var(--app-radius-xs);
    background: transparent;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;
    &:hover, &.active-more {
      background: var(--td-bg-color-container-hover);
      color: var(--td-text-color-secondary);
    }
    &:focus-visible {
      outline: 2px solid var(--app-focus-border);
      outline-offset: 2px;
    }
  }
  .card-file-icon {
    position: relative;
    flex: 0 0 26px;
    width: 26px;
    height: 31px;
    > :deep(*) { transform: scale(0.8125); transform-origin: top left; }
  }
  .card-nav-check {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    margin: -3px -5px 0 0;
    .card-select-checkbox { display: flex; margin: 0; padding: 0; line-height: 1; }
    :deep(.t-checkbox__label) { display: none; }
  }
  .card-preview { min-height: 32px; }
  .card-content-txt {
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    overflow: hidden;
    overflow-wrap: anywhere;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-sm);
    line-height: 16px;
    &.is-empty { color: var(--td-text-color-placeholder); }
  }
  .card-bottom {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-top: auto;
    padding-top: 6px;
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-xs);
    line-height: 18px;
  }
  .card-time { flex-shrink: 0; margin-left: auto; white-space: nowrap; font-variant-numeric: tabular-nums; }
  .card-tags-anchor { flex: 1; min-width: 0; width: auto; }
  .card-folder {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
    max-width: 50%;
    margin-left: auto;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--td-text-color-secondary);
    font: inherit;
    cursor: pointer;
    &:hover { color: var(--td-brand-color); }
    span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .t-icon { flex-shrink: 0; }
  }
}

.card-tags {
  display: flex;
  align-items: flex-start;
  align-content: flex-start;
  flex-wrap: nowrap;
  gap: 5px;
  height: 20px;
  overflow: hidden;
  button {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    max-width: 100%;
    height: 20px;
    padding: 0 6px;
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-xs);
    background: var(--td-bg-color-container);
    color: var(--td-text-color-secondary);
    font: inherit;
    font-size: var(--app-text-xs);
    line-height: 18px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    cursor: pointer;
    &:disabled { cursor: default; }
    &:not(:disabled):hover { color: var(--td-brand-color); border-color: var(--app-selection-border); background: var(--app-selection-bg); }
  }
  .card-tag-chip { display: block; min-width: 0; }
  .card-tag-add { max-width: none; padding: 0; border-color: transparent; color: var(--td-text-color-placeholder); background: transparent; }
  .card-tag-overflow { flex-shrink: 0; }
}
.card-tag-menu-action { width: 100%; border: 0; background: transparent; font-family: inherit; text-align: left; }

.card-analyze, .card-draft {
  display: flex;
  align-items: center;
  gap: 7px;
  min-height: 28px;
  color: var(--td-brand-color);
  font-size: var(--app-text-sm);
}
.card-analyze-loading { flex-shrink: 0; }
.card-analyze-trace-link { cursor: pointer; &:hover { text-decoration: underline; } }
.card-analyze-trace-btn {
  display: inline-flex;
  align-items: center;
  padding: 3px;
  border: 0;
  border-radius: var(--app-radius-xs);
  background: transparent;
  color: inherit;
  cursor: pointer;
  &:hover { background: var(--td-bg-color-component-hover); }
}
.card-analyze.failure { color: var(--td-error-color); }
.card-analyze.stalled { color: var(--td-warning-color); }
.card-analyze.queued { color: var(--td-text-color-secondary); }
.card-draft { color: var(--td-warning-color); }
.card-cancelled { color: var(--td-text-color-placeholder); }
.card-draft-tip { font-size: var(--app-text-xs); }
.knowledge-card button:focus-visible {
  outline: 2px solid var(--app-focus-border);
  outline-offset: 3px;
}
@media (prefers-reduced-motion: reduce) {
  .doc-card-list.doc-card-list-animated { animation: none; }
}

// --- Hover popover ---
.knowledge-card-hover-popover {
  position: fixed;
  z-index: 9999;
  pointer-events: none;
  min-width: 220px;
  max-width: 360px;
  padding: 12px 14px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.12);
  font-family: var(--app-font-family);
  transition: opacity var(--app-motion-fast) ease;
  will-change: transform;
  backface-visibility: hidden;
  -webkit-backface-visibility: hidden;
  transform: translateZ(0);
  -webkit-transform: translateZ(0);

  .card-popover-title {
    font-size: var(--app-text-base);
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin-bottom: 8px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .card-popover-status {
    font-size: var(--app-text-sm);
    margin-bottom: 6px;
    display: flex;
    align-items: center;
    gap: 6px;

    &.parsing { color: var(--td-brand-color); }
    &.failure { color: var(--td-error-color); }
    &.draft { color: var(--td-warning-color); }
  }

  .card-popover-desc {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
    line-height: 1.5;
    margin-bottom: 8px;
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 5;
    line-clamp: 5;
    overflow: hidden;
  }

  .card-popover-source {
    font-size: var(--app-text-xs);
    color: var(--td-brand-color);
    margin-bottom: 6px;
    display: flex;
    align-items: center;
    gap: 4px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 100%;
  }

  .card-popover-extra {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
    font-size: var(--app-text-xs);
    color: var(--td-text-color-secondary);
    margin-bottom: 6px;
  }

  .card-popover-created,
  .card-popover-size { flex-shrink: 0; }

  .card-popover-meta {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    font-size: var(--app-text-xs);
    color: var(--td-text-color-secondary);
  }

  .card-popover-channel {
    padding: 1px 6px;
    background: var(--td-warning-color-light);
    color: var(--td-warning-color);
    border-radius: var(--app-radius-xs);
  }

  .card-popover-tags {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 4px;
    max-width: 100%;
  }

  .card-popover-tag-chip {
    max-width: 120px;
    height: 18px;
    line-height: 18px;
    border-radius: var(--app-radius-pill);
    border-color: var(--td-component-stroke);
    color: var(--td-text-color-secondary);
    padding: 0 6px;
    background: transparent;

    .tag-text {
      display: inline-block;
      max-width: 80px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      vertical-align: middle;
      font-size: var(--app-text-xs);
    }
  }

  .card-popover-type {
    padding: 1px 6px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    border-radius: var(--app-radius-xs);
  }

  .card-popover-hint {
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid var(--td-component-stroke);
    font-size: var(--app-text-xs);
    color: var(--td-text-color-secondary);
  }
}
</style>
