<template>
  <div v-if="visible" class="mention-menu" :style="[style, stableHeightStyle, browsingFiles ? browserMenuStyle : undefined]" ref="menuRef" @click.stop>
    <MentionFileBrowser v-if="browsingFiles && browserContext" ref="fileBrowserRef"
      :knowledge-bases="browserKnowledgeBases || []" :context="browserContext"
      :supported-file-types="browserFileTypes || []" :selected-file-ids="browserSelectedIds || []"
      :whole-kb-ids="browserWholeKbIds || []" :tag-kb-ids="browserTagKbIds || []"
      @select="$emit('select', $event)" @back="leaveFileBrowser" />
    <template v-else>
    <div class="mention-search">
      <div class="mention-search__box" :class="{ 'is-readonly': !searchable }">
        <span class="mention-search__icon">
          <t-loading v-if="loading" size="12px" />
          <t-icon v-else name="search" />
        </span>
        <button
          v-if="currentGroup"
          type="button"
          class="mention-search__crumb"
          :title="$t('mentionDetail.back')"
          @mousedown.prevent
          @click.stop="leaveGroup"
        >
          <t-icon name="chevron-left" />
          <span>{{ currentGroup.label }}</span>
        </button>
        <input
          ref="searchInputRef"
          class="mention-search__input"
          type="text"
          :value="query ?? ''"
          :readonly="!searchable"
          :tabindex="searchable ? 0 : -1"
          :placeholder="searchPlaceholder"
          autocomplete="off"
          spellcheck="false"
          @mousedown="!searchable && $event.preventDefault()"
          @input="onSearchInput"
          @compositionstart="isComposing = true"
          @compositionend="onCompositionEnd"
          @keydown="onSearchKeydown"
        />
      </div>
    </div>

    <button v-if="currentGroupType === 'file' && browserKnowledgeBases?.length" type="button"
      class="mention-browse-entry" @click="openFileBrowser">{{ $t('mentionBrowser.browse') }} <t-icon name="chevron-right" /></button>
    <div class="mention-list" ref="listRef" @scroll="onScroll">
      <!-- 根层级：分组入口 -->
      <template v-if="isRootView">
        <button
          v-for="(group, index) in groupRows"
          :key="group.type"
          type="button"
          class="mention-row mention-group-entry"
          :class="{ active: index === groupActiveIndex }"
          @click.stop="enterGroup(group.type)"
          @mouseenter="groupActiveIndex = index"
        >
          <span class="mention-row__icon">
            <t-icon :name="group.icon" />
          </span>
          <span class="mention-row__name">{{ group.label }}</span>
          <span class="mention-row__meta">{{ formatGroupCount(group) }}</span>
          <t-icon class="mention-row__arrow" name="chevron-right" />
        </button>
        <div v-if="groupRows.length === 0 && !loading" class="empty">
          {{ emptyHint || $t('common.noResult') }}
        </div>
      </template>

      <!-- 全局搜索结果 / 分组内列表 -->
      <template v-else>
        <template v-for="group in visibleGroups" :key="group.type">
          <div v-if="isFlatMode" class="mention-section-header">
            <span class="mention-section-header__label">{{ group.label }}</span>
            <span class="mention-section-header__count">{{ formatGroupCount(group) }}</span>
            <button
              v-if="group.hasHidden"
              type="button"
              class="mention-section-header__more"
              @mousedown.prevent
              @click.stop="enterGroup(group.type)"
            >
              {{ $t('mentionDetail.viewAll') }}
              <t-icon name="chevron-right" />
            </button>
          </div>
          <div class="mention-group" :data-group-type="group.type">
            <t-popup
              v-for="(item, index) in group.visibleItems"
              :key="`${item.type}:${item.id}`"
              placement="right-start"
              trigger="hover"
              :show-arrow="false"
              :delay="[320, 80]"
              :disabled="isScrolling"
              :overlay-class-name="'mention-detail-popup'"
              :overlay-inner-class-name="'mention-detail-popup-wrap'"
              @visible-change="(v: boolean) => v && fetchDetail(item)"
            >
              <div
                class="mention-row mention-item"
                :class="{ active: group.offset + index === activeIndex }"
                @click="$emit('select', item)"
                @mouseenter="$emit('update:activeIndex', group.offset + index)"
              >
                <span class="mention-row__icon" :class="item.type === 'kb' && item.kbType === 'faq' ? 'faq-icon' : `${item.type}-icon`">
                  <t-icon :name="itemIcon(item)" />
                </span>
                <span class="mention-row__name">
                  <template v-for="(part, pi) in highlightParts(item.name)" :key="pi">
                    <mark v-if="part.hit" class="mention-hit">{{ part.text }}</mark>
                    <template v-else>{{ part.text }}</template>
                  </template>
                </span>
                <span
                  v-if="item.type === 'kb'"
                  class="mention-row__meta"
                >{{ item.count || 0 }}</span>
                <span
                  v-else-if="item.type === 'mcp' && item.catalogSynced"
                  class="mention-row__meta"
                  :class="{ 'is-stale': item.catalogStale }"
                >{{ item.toolCount ?? 0 }}</span>
                <span
                  v-else-if="(item.type === 'file' || item.type === 'tag') && item.kbName"
                  class="mention-row__meta mention-row__meta--text"
                >{{ item.kbName }}</span>
              </div>
              <template #content>
                <div class="mention-detail-content">
                  <!-- 知识库详情 -->
                  <template v-if="item.type === 'kb'">
                    <template v-if="detailCache[item.id]?.loading">
                      <div class="detail-loading"><t-loading size="small" /></div>
                    </template>
                    <template v-else-if="detailCache[item.id]?.error">
                      <div class="detail-error">{{ detailCache[item.id].error }}</div>
                    </template>
                    <template v-else-if="detailCache[item.id]?.data">
                      <div class="detail-header">
                        <span class="detail-name">{{ detailCache[item.id].data.name }}</span>
                        <span class="detail-type-badge" :class="detailCache[item.id].data.type === 'faq' ? 'faq' : 'doc'">
                          {{ detailCache[item.id].data.type === 'faq' ? $t('knowledgeEditor.basic.typeFAQ') : $t('knowledgeEditor.basic.typeDocument') }}
                        </span>
                      </div>
                      <p v-if="detailCache[item.id].data.description" class="detail-desc">{{ detailCache[item.id].data.description }}</p>
                      <div class="detail-meta">
                        <span v-if="detailCache[item.id].data.type === 'faq'">
                          {{ $t('mentionDetail.faqCount', { count: detailCache[item.id].data.chunk_count ?? detailCache[item.id].data.count ?? 0 }) }}
                        </span>
                        <span v-else>
                          {{ $t('mentionDetail.kbCount', { count: detailCache[item.id].data.knowledge_count ?? detailCache[item.id].data.count ?? 0 }) }}
                        </span>
                        <span v-if="detailCache[item.id].data.org_name || item.orgName" class="detail-org">
                          <img src="@/assets/img/organization-green.svg" class="detail-icon-img" alt="" aria-hidden="true" />
                          <span class="detail-label">{{ $t('mentionDetail.belongsToOrg') }}</span>
                          <span
                            class="detail-value clickable"
                            @click.stop="handleOrgClick(detailCache[item.id].data.org_name || item.orgName)"
                          >
                            {{ detailCache[item.id].data.org_name || item.orgName }}
                          </span>
                        </span>
                        <span v-if="agentIdForDetail && (detailCache[item.id].data.org_name || item.orgName)" class="detail-readonly-hint">
                          {{ $t('mentionDetail.readOnlyFromAgent') }}
                        </span>
                      </div>
                    </template>
                  </template>

                  <!-- 文件详情 -->
                  <template v-else-if="item.type === 'file'">
                    <template v-if="detailCache[item.id]?.loading">
                      <div class="detail-loading"><t-loading size="small" /></div>
                    </template>
                    <template v-else-if="detailCache[item.id]?.error">
                      <div class="detail-error">{{ detailCache[item.id].error }}</div>
                    </template>
                    <template v-else-if="detailCache[item.id]?.data">
                      <div class="detail-header">
                        <span class="detail-name">{{ detailCache[item.id].data.title || detailCache[item.id].data.file_name || item.name }}</span>
                      </div>
                      <p v-if="detailCache[item.id].data.description" class="detail-desc">{{ detailCache[item.id].data.description }}</p>
                      <div class="detail-meta">
                        <span v-if="detailCache[item.id].data.knowledge_base_name || item.kbName" class="detail-kb">
                          <t-icon name="folder" class="detail-icon" />
                          <span class="detail-label">{{ $t('mentionDetail.belongsToKb') }}</span>
                          <span
                            class="detail-value clickable"
                            @click.stop="handleKbClick(detailCache[item.id].data.knowledge_base_id || item.kbId)"
                          >
                            {{ detailCache[item.id].data.knowledge_base_name || item.kbName }}
                          </span>
                        </span>
                        <span v-if="item.orgName" class="detail-org">
                          <img src="@/assets/img/organization-green.svg" class="detail-icon-img" alt="" aria-hidden="true" />
                          <span class="detail-label">{{ $t('mentionDetail.belongsToOrg') }}</span>
                          <span
                            class="detail-value clickable"
                            @click.stop="handleOrgClick(item.orgName)"
                          >
                            {{ item.orgName }}
                          </span>
                        </span>
                      </div>
                    </template>
                  </template>

                  <!-- 标签 / MCP / 技能 -->
                  <template v-else>
                    <div class="detail-header">
                      <span class="detail-name">{{ item.name }}</span>
                    </div>
                    <p v-if="item.description" class="detail-desc">{{ item.description }}</p>
                    <div v-if="item.type === 'mcp'" class="detail-meta">
                      <span class="detail-kb">
                        <t-icon name="tools" class="detail-icon" />
                        <span v-if="item.catalogSynced" class="detail-value">
                          {{ $t('mentionDetail.mcpToolCount', { count: item.toolCount ?? 0 }) }}
                        </span>
                        <span v-else class="detail-value">{{ $t('mentionDetail.mcpNotSynced') }}</span>
                      </span>
                      <span v-if="item.catalogSynced && item.catalogStale" class="detail-stale">
                        {{ $t('mentionDetail.mcpStale') }}
                      </span>
                    </div>
                    <div v-else-if="item.kbName || item.serviceName" class="detail-meta">
                      <span v-if="item.kbName" class="detail-kb">
                        <t-icon name="folder" class="detail-icon" />
                        <span class="detail-label">{{ $t('mentionDetail.belongsToKb') }}</span>
                        <span
                          class="detail-value clickable"
                          @click.stop="handleKbClick(item.kbId)"
                        >
                          {{ item.kbName }}
                        </span>
                      </span>
                      <span v-if="item.serviceName" class="detail-kb">
                        <t-icon name="tools" class="detail-icon" />
                        <span class="detail-label">MCP：</span>
                        <span class="detail-value">{{ item.serviceName }}</span>
                      </span>
                    </div>
                  </template>
                </div>
              </template>
            </t-popup>
          </div>
        </template>

        <div v-if="loading && currentGroupType === 'file' && visibleItems.length > 0" class="loading-more">
          <t-loading size="small" />
        </div>

        <div v-if="visibleItems.length === 0" class="empty">
          <template v-if="!loading">{{ emptyHint || $t('common.noResult') }}</template>
        </div>
      </template>
    </div>

    </template>
  </div>
</template>

<script setup lang="ts">
import MentionFileBrowser from './MentionFileBrowser.vue';
import type { FileBrowserContext } from '@/composables/useMentionFileBrowser';
import type { BrowserKnowledgeBase } from '@/utils/mentionFileBrowser';
import { fitBrowserViewport } from '@/utils/mentionFileBrowser';
import { getRootZoom } from '@/utils/zoom';
import { computed, watch, ref, nextTick, onBeforeUnmount } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { getKnowledgeBaseById } from '@/api/knowledge-base';
import { getKnowledgeDetails } from '@/api/knowledge-base';
import { useSettingsStore } from '@/stores/settings';
import { SKILL_ICON, type MentionItem, type MentionItemType } from '@/types/mention';

type DetailState = { loading: boolean; error?: string; data?: any };

const props = defineProps<{
  visible: boolean;
  style: any;
  items: MentionItem[];
  /** 当前可见列表（全局搜索结果或分组内列表）中的高亮下标 */
  activeIndex: number;
  hasMore?: boolean;
  loading?: boolean;
  // 空态下替换默认 "无结果" 文案，用于给上游（如"被智能体工具兼容性过滤掉了"）透传具体原因
  emptyHint?: string;
  browserKnowledgeBases?: BrowserKnowledgeBase[];
  browserContext?: FileBrowserContext;
  browserFileTypes?: string[];
  browserSelectedIds?: string[];
  browserWholeKbIds?: string[];
  browserTagKbIds?: string[];
  // 搜索关键词；在根层级非空时做跨分组的全局搜索，在分组内则只过滤该分组
  query?: string;
  // 为 true 时顶部搜索框可编辑并自动聚焦（按钮触发）；否则关键词来自输入框中 @ 之后的文本
  searchable?: boolean;
  // 分组入口展示用的总数（如文件搜索的 total），避免仅用首屏已加载条数
  groupCounts?: Partial<Record<MentionItemType, number>>;
}>();

const emit = defineEmits(['select', 'update:activeIndex', 'update:query', 'loadMore', 'close', 'focus-input']);

const FLAT_GROUP_LIMIT = 5;

const router = useRouter();
const { t } = useI18n();
const settingsStore = useSettingsStore();
const menuRef = ref<HTMLElement | null>(null);
const listRef = ref<HTMLElement | null>(null);
const searchInputRef = ref<HTMLInputElement | null>(null);
const detailCache = ref<Record<string, DetailState>>({});
const isScrolling = ref(false);
const isComposing = ref(false);
const currentGroupType = ref<MentionItemType | null>(null);
const groupActiveIndex = ref(0);
const browsingFiles = ref(false);
const fileBrowserRef = ref<InstanceType<typeof MentionFileBrowser>>();
const browserMenuStyle = ref<Record<string, string>>({});
watch([browsingFiles, () => props.visible, () => props.style], (_, __, onCleanup) => {
  let disposed = false;
  const viewport = window.visualViewport;
  const fit = () => {
    if (disposed || !browsingFiles.value || !props.visible || !menuRef.value) return;
    browserMenuStyle.value = fitBrowserViewport(menuRef.value.getBoundingClientRect(), {
      left: viewport?.offsetLeft || 0, top: viewport?.offsetTop || 0,
      width: viewport?.width || window.innerWidth, height: viewport?.height || window.innerHeight,
    }, getRootZoom());
  };
  onCleanup(() => {
    disposed = true;
    window.removeEventListener('resize', fit);
    viewport?.removeEventListener('resize', fit);
    viewport?.removeEventListener('scroll', fit);
  });
  browserMenuStyle.value = {};
  if (!browsingFiles.value || !props.visible) return;
  void nextTick(fit);
  window.addEventListener('resize', fit);
  viewport?.addEventListener('resize', fit);
  viewport?.addEventListener('scroll', fit);
}, { flush: 'post' });
const openFileBrowser = () => { browsingFiles.value = true; };
const leaveFileBrowser = () => { browsingFiles.value = false; focusSearch(); if (!props.searchable) emit('focus-input'); };
watch(() => props.browserContext?.key, () => { browsingFiles.value = false; }, { flush: 'sync' });
let scrollTimer: ReturnType<typeof setTimeout> | null = null;

onBeforeUnmount(() => {
  if (scrollTimer) clearTimeout(scrollTimer);
});

// 共享智能体上下文：用于请求知识库/知识详情时带 agent_id，后端据此校验权限
const agentIdForDetail = computed(() => {
  const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
  const agentId = settingsStore.selectedAgentId;
  return sourceTenantId && agentId ? agentId : undefined;
});
const agentSourceTenantIdForDetail = computed(() => settingsStore.selectedAgentSourceTenantId ?? undefined);

const mentionGroupDefs = computed<Array<{ type: MentionItemType; label: string; icon: string }>>(() => [
  { type: 'kb', label: t('common.knowledgeBase'), icon: 'folder' },
  { type: 'tag', label: t('mentionDetail.tag'), icon: 'tag' },
  { type: 'mcp', label: 'MCP', icon: 'tools' },
  { type: 'skill', label: t('common.skill'), icon: SKILL_ICON },
  { type: 'file', label: t('common.file'), icon: 'file' },
]);

const mentionGroups = computed(() => mentionGroupDefs.value.map(def => {
  const items = props.items.filter(item => item.type === def.type);
  const loadedCount = items.length;
  const count = props.groupCounts?.[def.type] ?? loadedCount;
  return { ...def, items, count, loadedCount };
}));

type MentionGroup = (typeof mentionGroups.value)[number];

const trimmedQuery = computed(() => (props.query ?? '').trim());
const currentGroup = computed(() => mentionGroups.value.find(group => group.type === currentGroupType.value) || null);
const isFlatMode = computed(() => !currentGroupType.value && trimmedQuery.value.length > 0);
const isRootView = computed(() => !currentGroupType.value && !isFlatMode.value);
const groupRows = computed(() => mentionGroups.value.filter(group => group.count > 0 || (group.type === 'file' && !!props.browserKnowledgeBases?.length)));

const visibleGroups = computed(() => {
  const source: MentionGroup[] = currentGroup.value
    ? [currentGroup.value]
    : isFlatMode.value
      ? mentionGroups.value.filter(group => group.loadedCount > 0)
      : [];
  let offset = 0;
  return source.map(group => {
    const visibleItems = isFlatMode.value ? group.items.slice(0, FLAT_GROUP_LIMIT) : group.items;
    const hasHidden = isFlatMode.value && (group.loadedCount > visibleItems.length || group.count > visibleItems.length);
    const row = { ...group, visibleItems, hasHidden, offset };
    offset += visibleItems.length;
    return row;
  });
});

const visibleItems = computed(() => visibleGroups.value.flatMap(group => group.visibleItems));

// 菜单贴着输入框向上展开，结果条数变化会让顶边上下跳；搜索期间高度只增不减
const stableMinHeight = ref(0);
const stableHeightStyle = computed(() => (
  !isRootView.value && stableMinHeight.value ? { minHeight: `${stableMinHeight.value}px` } : undefined
));

watch(
  [() => visibleItems.value.length, () => props.loading, isRootView],
  () => {
    if (isRootView.value) {
      stableMinHeight.value = 0;
      return;
    }
    const height = menuRef.value?.offsetHeight ?? 0;
    if (height > stableMinHeight.value) stableMinHeight.value = height;
  },
  { flush: 'post' },
);

const searchPlaceholder = computed(() => {
  if (currentGroup.value) return t('mentionDetail.searchInGroup', { group: currentGroup.value.label });
  return props.searchable ? t('mentionDetail.searchPlaceholder') : t('mentionDetail.typeToSearch');
});

const formatGroupCount = (group: { type: MentionItemType; count: number; loadedCount: number }) => {
  if (props.groupCounts?.[group.type] != null) {
    return props.groupCounts[group.type]!;
  }
  if (group.type === 'file' && props.hasMore) {
    return `${group.loadedCount}+`;
  }
  return group.count;
};

const itemIcon = (item: MentionItem) => {
  if (item.type === 'kb') return item.kbType === 'faq' ? 'chat-bubble-help' : 'folder';
  return mentionGroupDefs.value.find(def => def.type === item.type)?.icon || 'file';
};

const highlightParts = (name: string) => {
  const q = trimmedQuery.value;
  if (!q || !name) return [{ text: name, hit: false }];
  const idx = name.toLowerCase().indexOf(q.toLowerCase());
  if (idx < 0) return [{ text: name, hit: false }];
  return [
    { text: name.slice(0, idx), hit: false },
    { text: name.slice(idx, idx + q.length), hit: true },
    { text: name.slice(idx + q.length), hit: false },
  ].filter(part => part.text);
};

const resetListScroll = () => {
  nextTick(() => {
    if (listRef.value) listRef.value.scrollTop = 0;
  });
};

const enterGroup = (type: MentionItemType) => {
  const group = mentionGroups.value.find(item => item.type === type && (item.count > 0 || (type === 'file' && !!props.browserKnowledgeBases?.length)));
  if (!group) return;
  currentGroupType.value = type;
  emit('update:activeIndex', 0);
  resetListScroll();
  focusSearch();
};

const leaveGroup = () => {
  if (browsingFiles.value) { fileBrowserRef.value?.back(); return true; }
  if (!currentGroupType.value) return false;
  const rowIndex = groupRows.value.findIndex(group => group.type === currentGroupType.value);
  groupActiveIndex.value = Math.max(0, rowIndex);
  currentGroupType.value = null;
  emit('update:activeIndex', 0);
  resetListScroll();
  focusSearch();
  return true;
};

/** 全局搜索结果中，从当前高亮项进入其所在分组查看全部 */
const enterActiveGroup = () => {
  if (currentGroupType.value === 'file' && props.browserKnowledgeBases?.length) {
    openFileBrowser(); return true;
  }
  if (isRootView.value) {
    confirmActive();
    return true;
  }
  if (!isFlatMode.value) return false;
  const group = visibleGroups.value.find(g => props.activeIndex >= g.offset && props.activeIndex < g.offset + g.visibleItems.length);
  if (!group) return false;
  enterGroup(group.type);
  return true;
};

watch(groupRows, (groups) => {
  if (groupActiveIndex.value >= groups.length) {
    groupActiveIndex.value = Math.max(0, groups.length - 1);
  }
});

watch(() => visibleItems.value.length, (len) => {
  if (!isRootView.value && props.activeIndex >= len) {
    emit('update:activeIndex', Math.max(0, len - 1));
  }
});

const moveActive = (delta: number) => {
  if (browsingFiles.value) { fileBrowserRef.value?.moveActive(delta); return; }
  if (isRootView.value) {
    const maxIndex = Math.max(0, groupRows.value.length - 1);
    groupActiveIndex.value = Math.min(maxIndex, Math.max(0, groupActiveIndex.value + delta));
    scrollToRow(groupActiveIndex.value);
    return;
  }
  const len = visibleItems.value.length;
  if (!len) return;
  const next = Math.min(len - 1, Math.max(0, props.activeIndex + delta));
  emit('update:activeIndex', next);
  scrollToRow(next);
  if (currentGroupType.value === 'file' && next >= len - 3 && props.hasMore && !props.loading) {
    emit('loadMore');
  }
};

const confirmActive = () => {
  if (browsingFiles.value) { fileBrowserRef.value?.confirmActive(); return; }
  if (currentGroupType.value === 'file' && !visibleItems.value.length && props.browserKnowledgeBases?.length) { openFileBrowser(); return; }
  if (isRootView.value) {
    const group = groupRows.value[groupActiveIndex.value];
    if (group) enterGroup(group.type);
    return;
  }
  const item = visibleItems.value[props.activeIndex];
  if (item) emit('select', item);
};

const focusSearch = () => {
  if (!props.searchable) return;
  nextTick(() => searchInputRef.value?.focus({ preventScroll: true }));
};

const onSearchInput = (e: Event) => {
  if (isComposing.value) return;
  emit('update:query', (e.target as HTMLInputElement).value);
};

const onCompositionEnd = (e: CompositionEvent) => {
  isComposing.value = false;
  emit('update:query', (e.target as HTMLInputElement).value);
};

const onSearchKeydown = (e: KeyboardEvent) => {
  if (!props.searchable) return;
  if (isComposing.value || e.isComposing || e.keyCode === 229) return;
  switch (e.key) {
    case 'ArrowUp':
      e.preventDefault();
      moveActive(-1);
      break;
    case 'ArrowDown':
      e.preventDefault();
      moveActive(1);
      break;
    case 'Enter':
      e.preventDefault();
      confirmActive();
      break;
    case 'Tab':
      if (enterActiveGroup()) e.preventDefault();
      break;
    case 'Escape':
      e.preventDefault();
      if (!leaveGroup()) emit('close');
      break;
    case 'Backspace':
      if (!(props.query ?? '') && leaveGroup()) e.preventDefault();
      break;
  }
};

defineExpose({
  moveActive,
  confirmActive,
  leaveGroup,
  enterActiveGroup,
});

const fetchDetail = (item: MentionItem) => {
  if (item.type === 'kb') return fetchKbDetail(item);
  if (item.type === 'file') return fetchFileDetail(item);
};

async function fetchKbDetail(item: { id: string }) {
  if (detailCache.value[item.id]?.data || detailCache.value[item.id]?.loading) return;
  detailCache.value = { ...detailCache.value, [item.id]: { loading: true } };
  try {
    const opts = agentIdForDetail.value ? {
      agent_id: agentIdForDetail.value,
      agent_source_tenant_id: agentSourceTenantIdForDetail.value,
    } : undefined;
    const res: any = await getKnowledgeBaseById(item.id, opts);
    detailCache.value = { ...detailCache.value, [item.id]: { loading: false, data: res?.data ?? res } };
  } catch (e: any) {
    detailCache.value = { ...detailCache.value, [item.id]: { loading: false, error: e?.message || 'Failed to load' } };
  }
}

async function fetchFileDetail(item: { id: string }) {
  if (detailCache.value[item.id]?.data || detailCache.value[item.id]?.loading) return;
  detailCache.value = { ...detailCache.value, [item.id]: { loading: true } };
  try {
    const opts = agentIdForDetail.value ? {
      agent_id: agentIdForDetail.value,
      agent_source_tenant_id: agentSourceTenantIdForDetail.value,
    } : undefined;
    const res: any = await getKnowledgeDetails(item.id, opts);
    detailCache.value = { ...detailCache.value, [item.id]: { loading: false, data: res?.data ?? res } };
  } catch (e: any) {
    detailCache.value = { ...detailCache.value, [item.id]: { loading: false, error: e?.message || 'Failed to load' } };
  }
}

function handleKbClick(kbId: string | undefined) {
  if (!kbId) return;
  router.push(`/platform/knowledge-bases/${kbId}`);
}

function handleOrgClick(orgName: string | undefined) {
  if (!orgName) return;
  // 目前组织详情页可能不存在，统一跳转到组织列表页
  router.push('/platform/organizations');
}

const onScroll = (e: Event) => {
  isScrolling.value = true;
  if (scrollTimer) clearTimeout(scrollTimer);
  scrollTimer = setTimeout(() => {
    isScrolling.value = false;
  }, 150);

  const target = e.target as HTMLElement;
  const { scrollTop, scrollHeight, clientHeight } = target;
  if (currentGroupType.value === 'file' && scrollHeight - scrollTop - clientHeight < 50 && props.hasMore && !props.loading) {
    emit('loadMore');
  }
};

watch(() => props.activeIndex, (newIndex) => {
  if (!isRootView.value) scrollToRow(newIndex);
});

watch(() => props.visible, (newVisible) => {
  browsingFiles.value = false;
  if (newVisible) {
    stableMinHeight.value = 0;
    currentGroupType.value = null;
    groupActiveIndex.value = 0;
    resetListScroll();
    focusSearch();
  }
});

const scrollToRow = (index: number) => {
  nextTick(() => {
    const list = listRef.value;
    if (!list) return;
    const rows = list.querySelectorAll<HTMLElement>(isRootView.value ? '.mention-group-entry' : '.mention-item');
    const row = rows[index];
    if (!row) return;
    const listRect = list.getBoundingClientRect();
    const rowRect = row.getBoundingClientRect();
    if (rowRect.top < listRect.top) {
      list.scrollTop -= (listRect.top - rowRect.top);
    } else if (rowRect.bottom > listRect.bottom) {
      list.scrollTop += (rowRect.bottom - listRect.bottom);
    }
  });
};
</script>

<style scoped>
.mention-browse-entry { flex-shrink: 0; display: flex; justify-content: space-between; border: 0; padding: 8px; background: transparent; color: var(--td-brand-color); cursor: pointer; font: inherit; }
.mention-browse-entry:focus-visible { outline: 2px solid var(--td-brand-color); }
/* 与输入框内其他下拉（推理强度、模型选择）保持同一套尺寸 */
.mention-menu {
  position: fixed;
  z-index: 10000;
  width: 300px;
  max-width: calc(100vw - 16px);
  max-height: 360px;
  padding: 4px;
  box-sizing: border-box;
  background: var(--td-bg-color-container);
  border: .5px solid var(--td-component-border);
  border-radius: var(--app-radius-lg);
  box-shadow: var(--td-shadow-2);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  font-family: var(--app-font-family);
  animation: mentionMenuFadeIn 0.15s ease-out;
}

@keyframes mentionMenuFadeIn {
  from {
    opacity: 0;
    transform: scale(0.98);
  }
  to {
    opacity: 1;
    transform: scale(1);
  }
}

.mention-search {
  flex-shrink: 0;
  margin-bottom: 2px;
  border-bottom: .5px solid var(--td-component-stroke);
}

.mention-search__box {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
}

.mention-search__icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

.mention-search__crumb {
  flex-shrink: 0;
  max-width: 88px;
  height: 20px;
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 0 6px 0 2px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  font-weight: 500;
  cursor: pointer;
  transition: background var(--app-motion-instant), color var(--app-motion-instant);
}

.mention-search__crumb:hover {
  background: var(--td-bg-color-secondarycontainer-hover);
  color: var(--td-text-color-primary);
}

.mention-search__crumb span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mention-search__input {
  flex: 1;
  min-width: 0;
  height: 20px;
  padding: 0;
  border: 0;
  outline: none;
  text-overflow: ellipsis;
  background: transparent;
  color: var(--td-text-color-primary);
  font: inherit;
  font-size: var(--app-text-md);
  line-height: 20px;
}

.mention-search__input::placeholder {
  color: var(--td-text-color-placeholder);
}

.mention-search__box.is-readonly .mention-search__input {
  cursor: default;
}

.mention-list {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.mention-group {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.mention-row {
  width: 100%;
  min-height: 30px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 8px;
  margin: 0;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  box-sizing: border-box;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-md);
  line-height: 20px;
  text-align: left;
  cursor: pointer;
  transition: background var(--app-motion-instant), color var(--app-motion-instant);
}

.mention-row:hover {
  background: var(--td-bg-color-secondarycontainer-hover);
}

.mention-row.active {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}

.mention-row__icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
  transition: color var(--app-motion-instant);
}

.mention-row.active .mention-row__icon {
  color: var(--td-brand-color);
}

.mention-row.active .mention-row__icon.faq-icon {
  color: var(--weknora-faq-color, #0052d9);
}

.mention-row__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mention-hit {
  padding: 0;
  background: transparent;
  color: var(--td-brand-color);
  font-weight: 500;
}

.mention-row__meta {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  font-variant-numeric: tabular-nums;
}

.mention-row__meta--text {
  max-width: 96px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mention-row__meta.is-stale {
  color: var(--td-warning-color);
}

.mention-row__arrow {
  flex-shrink: 0;
  margin-right: -4px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

/* 全局搜索结果中的分组标题 */
.mention-section-header {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 8px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  line-height: 18px;
}

.mention-group + .mention-section-header {
  margin-top: 2px;
  padding-top: 4px;
  height: 28px;
  border-top: .5px solid var(--td-component-stroke);
}

.mention-section-header__count {
  font-variant-numeric: tabular-nums;
}

.mention-section-header__more {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  padding: 0 0 0 4px;
  border: 0;
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  cursor: pointer;
  transition: color var(--app-motion-instant);
}

.mention-section-header__more:hover {
  color: var(--td-brand-color);
}

.empty {
  min-height: 52px;
  padding: 16px 8px;
  box-sizing: border-box;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);
  line-height: 20px;
}

.loading-more {
  display: flex;
  justify-content: center;
  padding: 4px 8px;
}
</style>

<style>
/* 详情浮层在 Teleport 中，需全局样式 */
.mention-detail-popup-wrap.t-popup__content {
  min-width: 220px;
  max-width: 280px;
  padding: 10px 12px;
  border: .5px solid var(--td-component-border);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
}
.mention-detail-content {
  font-size: var(--td-font-size-body-small);
  color: var(--td-text-color-primary);
  line-height: 1.5;
}
.mention-detail-content .detail-loading,
.mention-detail-content .detail-error {
  padding: 8px 0;
  color: var(--td-text-color-secondary);
  font-size: var(--td-font-size-body-small);
}
.mention-detail-content .detail-error {
  color: var(--td-error-color);
}
.mention-detail-content .detail-header {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.mention-detail-content .detail-name {
  font-weight: 600;
  font-size: var(--td-font-size-body-medium);
  line-height: 20px;
  word-break: break-word;
}
.mention-detail-content .detail-type-badge {
  flex-shrink: 0;
  padding: 1px 6px;
  border: .5px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xs);
  font-size: var(--td-font-size-mark-small);
  line-height: 18px;
}
.mention-detail-content .detail-type-badge.doc {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-brand-color);
}
.mention-detail-content .detail-type-badge.faq {
  border-color: rgba(0, 82, 217, 0.16);
  background: rgba(0, 82, 217, 0.08);
  color: var(--weknora-faq-color, #0052d9);
}
.mention-detail-content .detail-desc {
  margin: 0 0 8px;
  font-size: var(--td-font-size-body-small);
  color: var(--td-text-color-secondary);
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 4;
  line-clamp: 4;
  -webkit-box-orient: vertical;
  overflow: hidden;
  word-break: break-word;
}
.mention-detail-content .detail-meta {
  font-size: var(--td-font-size-mark-small);
  color: var(--td-text-color-placeholder);
  display: flex;
  flex-direction: column;
  gap: 5px;
  align-items: flex-start;
}
.mention-detail-content .detail-readonly-hint {
  display: block;
  margin-top: 6px;
  font-size: var(--td-font-size-mark-small);
  color: var(--td-text-color-placeholder);
  font-style: italic;
}

.mention-detail-content .detail-org,
.mention-detail-content .detail-kb {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  width: 100%;
  line-height: 1.5;
}
.mention-detail-content .detail-icon {
  flex-shrink: 0;
  font-size: var(--app-text-base);
  color: var(--td-text-color-placeholder);
  margin-right: 2px;
  display: inline-flex;
  align-items: center;
  vertical-align: middle;
}
.mention-detail-content .detail-kb .detail-icon {
  color: var(--td-brand-color);
  font-weight: 600;
}
.mention-detail-content .detail-icon-img {
  flex-shrink: 0;
  width: 14px;
  height: 14px;
  margin-right: 2px;
  color: var(--td-text-color-placeholder);
  opacity: 0.7;
  display: inline-block;
  vertical-align: middle;
  object-fit: contain;
}
.mention-detail-content .detail-label {
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
  line-height: 1.5;
  display: inline-flex;
  align-items: center;
}
.mention-detail-content .detail-value {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 160px;
  line-height: 1.5;
  display: inline-flex;
  align-items: center;
}
.mention-detail-content .detail-value.clickable {
  cursor: pointer;
  text-decoration: underline;
  text-decoration-color: var(--td-text-color-placeholder);
  transition: color var(--app-motion-base), text-decoration-color var(--app-motion-base);
}
.mention-detail-content .detail-value.clickable:hover {
  color: var(--td-brand-color);
  text-decoration-color: var(--td-brand-color);
}
.mention-detail-content .detail-stale {
  color: var(--td-warning-color);
}
</style>
