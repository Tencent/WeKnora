<script setup lang="ts">
import { ref, computed, nextTick } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatFileSize } from '@/utils/files';
import { useTagChipsOverflow } from '@/composables/useTagChipsOverflow';
import DocumentActionMenu from './DocumentActionMenu.vue';
import KnowledgeProcessingTimeline from '@/components/knowledge-processing-timeline.vue';

interface Tag {
  id: string;
  name: string;
  color?: string;
}

interface KnowledgeCard {
  id: string;
  knowledge_base_id?: string;
  parse_status: string;
  summary_status?: string;
  description?: string;
  file_name?: string;
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
  source?: string;
  created_at?: string;
  file_size?: number | string;
  channel?: string;
}

const props = defineProps<{
  items: KnowledgeCard[];
  selectedIds: Set<string>;
  batchMode: boolean;
  canEdit: boolean;
  canMutateKnowledge: boolean;
  traceAvailableById: Record<string, boolean>;
  tagList: Tag[];
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
  (e: 'action', action: 'edit' | 'view-trace' | 'reparse' | 'cancel-parse' | 'move' | 'batch-manage' | 'delete', item: KnowledgeCard): void;
  (e: 'tag-edit', item: KnowledgeCard): void;
  // Move sub-flow emits
  (e: 'move-select-target', kb: any): void;
  (e: 'move-back'): void;
  (e: 'move-confirm'): void;
  (e: 'update:moveMode', mode: 'reuse_vectors' | 'reparse'): void;
}>();

const { t } = useI18n();

const {
  setupTagChipsObserver,
  getTagLimit,
  hasTagOverflow,
  getOverflowCount,
} = useTagChipsOverflow('tagItemId');

// --- Menu index tracking ---
const activeMenuIndex = ref(-1);
const openMenu = (index: number) => {
  activeMenuIndex.value = index;
};
const onMenuVisibleChange = (visible: boolean, item: KnowledgeCard) => {
  if (!visible) {
    activeMenuIndex.value = -1;
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
  confluence: 'knowledgeBase.channelConfluence',
  dingtalk: 'knowledgeBase.channelDingtalk',
  slack: 'knowledgeBase.channelSlack',
  im: 'knowledgeBase.channelIm',
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
  emit('open', item);
};

// --- Hover popover ---
const hoveredCardItem = ref<KnowledgeCard | null>(null);
const cardPopoverPos = ref({ x: 0, y: 0 });
const CARD_POPOVER_OFFSET = 12;
const CARD_POPOVER_ESTIMATED_WIDTH = 360;
const CARD_POPOVER_ESTIMATED_HEIGHT = 300;
const cardHoverShowDelay = 300;
let cardHoverTimer: ReturnType<typeof setTimeout> | null = null;
let cardPopoverElement: HTMLElement | null = null;

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
  if (cardHoverTimer) {
    clearTimeout(cardHoverTimer);
    cardHoverTimer = null;
  }
  const cardElement = (ev.currentTarget as HTMLElement);
  cardHoverTimer = setTimeout(() => {
    cardHoverTimer = null;
    hoveredCardItem.value = item;
    const pos = calculatePopoverPositionFromCard(cardElement);
    cardPopoverPos.value = pos;
    nextTick(() => {
      cardPopoverElement = document.querySelector('.knowledge-card-hover-popover') as HTMLElement;
      if (cardPopoverElement) {
        const refinedPos = calculatePopoverPositionFromCard(cardElement);
        cardPopoverPos.value = refinedPos;
      }
    });
  }, cardHoverShowDelay);
};

const onCardMouseLeave = () => {
  if (cardHoverTimer) {
    clearTimeout(cardHoverTimer);
    cardHoverTimer = null;
  }
  hoveredCardItem.value = null;
  cardPopoverElement = null;
};

// --- Action handlers ---
const handleAction = (action: 'edit' | 'view-trace' | 'reparse' | 'cancel-parse' | 'move' | 'batch-manage' | 'delete', item: KnowledgeCard) => {
  // Don't close menu for move — it triggers the sub-flow
  if (action !== 'move') {
    if (item.isMore !== undefined) item.isMore = false;
    activeMenuIndex.value = -1;
  }
  emit('action', action, item);
};
</script>

<template>
  <div class="doc-card-list doc-card-list-animated">
    <div
      class="knowledge-card"
      :class="{ 'is-selected': selectedIds.has(item.id), 'batch-mode': batchMode }"
      :data-select-id="item.id"
      v-for="(item, index) in items"
      :key="item.id"
      @click="onCardClick(item)"
      @mouseenter="onCardMouseEnter($event, item)"
      @mouseleave="onCardMouseLeave"
    >
      <div class="card-content">
        <div class="card-content-nav">
          <div v-if="canEdit && batchMode" class="card-nav-check" @click.stop>
            <t-checkbox
              class="card-select-checkbox"
              size="small"
              :checked="selectedIds.has(item.id)"
              :title="item.file_name"
              @change="(checked: boolean, ctx?: { e?: Event }) => emit('toggle-checkbox', item.id, checked, ctx)"
            />
          </div>
          <span class="card-content-title" :title="item.file_name">{{ item.file_name }}</span>
          <t-popup
            v-if="canEdit"
            v-model="item.isMore"
            overlayClassName="card-more"
            :on-visible-change="(v: boolean) => onMenuVisibleChange(v, item)"
            trigger="click"
            destroy-on-close
            placement="bottom-right"
          >
            <div
              variant="outline"
              class="more-wrap"
              @click.stop="openMenu(index)"
              :class="[activeMenuIndex === index ? 'active-more' : '']"
            >
              <img class="more-icon" src="@/assets/img/more.png" alt="" />
            </div>
            <template #content>
              <!-- Normal menu -->
              <div v-if="moveMenuMode === 'normal'" class="card-menu">
                <DocumentActionMenu
                  :item="item"
                  :can-mutate-knowledge="canMutateKnowledge"
                  :trace-visible="isTraceMenuVisible(item)"
                  @edit="handleAction('edit', item)"
                  @view-trace="handleAction('view-trace', item)"
                  @reparse="handleAction('reparse', item)"
                  @cancel-parse="handleAction('cancel-parse', item)"
                  @move="handleAction('move', item)"
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

        <!-- Parse status display -->
        <div v-if="isParseInFlight(item.parse_status)" class="card-analyze card-analyze-trace">
          <t-icon name="loading" class="card-analyze-loading"></t-icon>
          <span
            class="card-analyze-txt card-analyze-trace-link"
            role="button"
            tabindex="0"
            :title="$t('knowledgeStages.viewTrace')"
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
        <div v-else-if="item.parse_status === 'completed'" class="card-content-txt">
          {{ item.description }}
        </div>
      </div>

      <div class="card-bottom">
        <span class="card-time">{{ formatDocTime(item.updated_at) }}</span>
        <div class="card-bottom-right">
          <div v-if="tagList.length" class="card-tag-selector" @click.stop>
            <!-- Editable mode -->
            <template v-if="canEdit">
              <template v-if="(item.tags || []).length > 0">
                <t-tooltip
                  v-if="hasTagOverflow(item.id, (item.tags || []).length)"
                  :content="(item.tags || []).map((t: any) => t.name).join(', ')"
                  placement="top"
                >
                  <div
                    class="card-tag-chips"
                    :ref="(el: any) => setupTagChipsObserver(el, item.id, (item.tags || []).length)"
                    @click="emit('tag-edit', item)"
                  >
                    <t-tag v-for="tag in (item.tags || []).slice(0, getTagLimit(item.id))" :key="tag.id" size="small" variant="light-outline" class="card-tag-chip">
                      <span class="tag-text">{{ tag.name }}</span>
                    </t-tag>
                    <span class="card-tag-overflow">+{{ getOverflowCount(item.id, (item.tags || []).length) }}</span>
                  </div>
                </t-tooltip>
                <div
                  v-else
                  class="card-tag-chips"
                  :ref="(el: any) => setupTagChipsObserver(el, item.id, (item.tags || []).length)"
                  @click="emit('tag-edit', item)"
                >
                  <t-tag v-for="tag in (item.tags || []).slice(0, getTagLimit(item.id))" :key="tag.id" size="small" variant="light-outline" class="card-tag-chip">
                    <span class="tag-text">{{ tag.name }}</span>
                  </t-tag>
                </div>
              </template>
              <span v-else class="card-tag-add" @click="emit('tag-edit', item)">
                <t-icon name="add" size="12px" />
                <span>{{ $t('knowledgeBase.tagLabel') }}</span>
              </span>
            </template>
            <!-- Read-only mode -->
            <template v-else-if="(item.tags || []).length > 0">
              <t-tooltip
                v-if="hasTagOverflow(item.id, (item.tags || []).length)"
                :content="(item.tags || []).map((t: any) => t.name).join(', ')"
                placement="top"
              >
                <div
                  class="card-tag-chips"
                  :ref="(el: any) => setupTagChipsObserver(el, item.id, (item.tags || []).length)"
                >
                  <t-tag v-for="tag in (item.tags || []).slice(0, getTagLimit(item.id))" :key="tag.id" size="small" variant="light-outline" class="card-tag-chip">
                    <span class="tag-text">{{ tag.name }}</span>
                  </t-tag>
                  <span class="card-tag-overflow">+{{ getOverflowCount(item.id, (item.tags || []).length) }}</span>
                </div>
              </t-tooltip>
              <div
                v-else
                class="card-tag-chips"
                :ref="(el: any) => setupTagChipsObserver(el, item.id, (item.tags || []).length)"
              >
                <t-tag v-for="tag in (item.tags || []).slice(0, getTagLimit(item.id))" :key="tag.id" size="small" variant="light-outline" class="card-tag-chip">
                  <span class="tag-text">{{ tag.name }}</span>
                </t-tag>
              </div>
            </template>
          </div>
          <div class="card-type">
            <span>{{ getKnowledgeType(item) }}</span>
          </div>
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
          <div v-if="hoveredCardItem.description" class="card-popover-desc">{{ hoveredCardItem.description }}</div>
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

.doc-card-list {
  box-sizing: border-box;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 12px;
  align-content: flex-start;
  width: 100%;

  &.doc-card-list-animated {
    animation: contentFadeIn 0.32s ease-out;
  }
}

.knowledge-card {
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

  .card-analyze {
    flex-shrink: 0;
    height: 52px;
    display: flex;
    align-items: flex-start;
  }

  .card-analyze-loading {
    display: block;
    color: var(--td-brand-color);
    font-size: 14px;
    margin-top: 2px;
  }

  .card-analyze-txt {
    color: var(--td-brand-color);
    font-family: var(--app-font-family);
    font-size: 11px;
    margin-left: 8px;
  }

  .card-analyze-trace {
    height: auto;
    min-height: 0;
    align-items: center;
    gap: 2px;
  }

  .card-analyze-trace-link {
    cursor: pointer;
    &:hover { text-decoration: underline; }
  }

  .card-analyze-trace-btn {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    margin: 0;
    padding: 2px;
    border: none;
    background: transparent;
    color: var(--td-brand-color);
    cursor: pointer;
    line-height: 1;
    border-radius: 4px;

    :deep(.t-icon) { font-size: 14px; }
    &:hover { background: var(--td-bg-color-component-hover); }
  }

  .card-analyze.failure .card-analyze-trace-btn { color: var(--td-error-color); }

  .failure { color: var(--td-error-color); }

  .card-content-nav {
    flex-shrink: 0;
    display: flex;
    align-items: flex-start;
    gap: 0;
    margin-bottom: 6px;
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
    margin-right: 8px;
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

    &:hover { background: var(--td-component-stroke); }
  }

  .more-icon { width: 14px; height: 14px; }
  .active-more { background: var(--td-component-stroke); }

  .card-content-txt {
    flex: 1;
    min-height: 0;
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    overflow: hidden;
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);
    font-size: 12px;
    font-weight: 400;
    line-height: 19px;
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

  .card-time {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);
    font-size: 12px;
    font-weight: 400;
    white-space: nowrap;
  }

  .card-type {
    flex-shrink: 0;
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
    font-size: 11px;
    font-weight: 500;
    padding: 0;
    background: transparent;
    letter-spacing: 0.02em;
  }
}

.card-bottom-right {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  overflow: hidden;
}

// --- Card draft ---
.card-draft {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  flex-shrink: 0;
}

.card-draft-tip {
  color: var(--td-warning-color);
  font-size: 11px;
}

// --- Tag selector ---
.card-tag-selector {
  display: flex;
  align-items: center;

  .card-tag-chips {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    flex-wrap: nowrap;
    cursor: pointer;
  }

  .card-tag-overflow {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    height: 18px;
    min-width: 18px;
    padding: 0 5px;
    border-radius: 999px;
    border: 1px solid var(--td-component-stroke);
    color: var(--td-text-color-placeholder);
    font-size: 10px;
    line-height: 1;
    cursor: pointer;
    transition: all 0.2s ease;

    &:hover {
      border-color: var(--td-brand-color);
      color: var(--td-brand-color);
      background: var(--td-bg-color-secondarycontainer);
    }
  }

  :deep(.t-tag) {
    cursor: pointer;
    max-width: 120px;
    height: 18px;
    line-height: 18px;
    border-radius: 999px;
    border-color: var(--td-component-stroke);
    color: var(--td-text-color-secondary);
    padding: 0 6px;
    background: transparent;
    transition: all 0.2s ease;

    &:hover {
      border-color: var(--td-brand-color);
      color: var(--td-brand-color-active);
      background: var(--td-bg-color-secondarycontainer);
    }
  }

  .tag-text {
    display: inline-block;
    max-width: 80px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: middle;
    font-size: 11px;
  }

  .card-tag-add {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    height: 18px;
    padding: 0 6px;
    border-radius: 999px;
    border: 1px dashed var(--td-component-stroke);
    color: var(--td-text-color-placeholder);
    font-size: 11px;
    cursor: pointer;
    transition: all 0.2s ease;

    .t-icon { font-size: 12px; }

    &:hover {
      border-color: var(--td-brand-color);
      color: var(--td-brand-color-active);
      background: var(--td-bg-color-secondarycontainer);
      border-style: solid;
    }
  }
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
  border-radius: 8px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.12);
  font-family: var(--app-font-family);
  transition: opacity 0.15s ease;
  will-change: transform;
  backface-visibility: hidden;
  -webkit-backface-visibility: hidden;
  transform: translateZ(0);
  -webkit-transform: translateZ(0);

  .card-popover-title {
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin-bottom: 8px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .card-popover-status {
    font-size: 12px;
    margin-bottom: 6px;
    display: flex;
    align-items: center;
    gap: 6px;

    &.parsing { color: var(--td-brand-color); }
    &.failure { color: var(--td-error-color); }
    &.draft { color: var(--td-warning-color); }
  }

  .card-popover-desc {
    font-size: 12px;
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
    font-size: 11px;
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
    font-size: 11px;
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
    font-size: 11px;
    color: var(--td-text-color-secondary);
  }

  .card-popover-channel {
    padding: 1px 6px;
    background: var(--td-warning-color-light);
    color: var(--td-warning-color);
    border-radius: 4px;
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
    border-radius: 999px;
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
      font-size: 11px;
    }
  }

  .card-popover-type {
    padding: 1px 6px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    border-radius: 4px;
  }

  .card-popover-hint {
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid var(--td-component-stroke);
    font-size: 11px;
    color: var(--td-text-color-secondary);
  }
}
</style>
