<template>
  <div v-if="visible" class="mention-menu" :style="style" ref="menuRef" @click.stop>
    <div class="mention-list" ref="listRef" @scroll="onScroll">
      <template v-if="!currentGroupType && !isFlatMode">
        <button
          v-for="(group, index) in groupRows"
          :key="group.type"
          type="button"
          class="mention-group-entry"
          :class="{ active: index === groupActiveIndex }"
          @click.stop="enterGroup(group.type)"
          @mouseenter="groupActiveIndex = index"
        >
          <span class="mention-group-entry__icon">
            <t-icon :name="group.icon" />
          </span>
          <span class="mention-group-entry__label">{{ group.label }}</span>
          <span class="mention-group-entry__count">{{ formatGroupCount(group) }}</span>
          <t-icon class="mention-group-entry__arrow" name="chevron-right" />
        </button>
        <div v-if="groupRows.length === 0 && !loading" class="empty">
          {{ emptyHint || $t('common.noResult') }}
        </div>
      </template>

      <template v-else>
        <button v-if="!isFlatMode" type="button" class="mention-back-row" @click.stop="leaveGroup">
          <t-icon name="chevron-left" />
          <span>{{ currentGroup?.label }}</span>
        </button>

      <div
        v-if="isFlatMode && groupTabs.length > 1 && kbItems.length > 0"
        class="mention-group-header"
      >
        {{ $t('common.knowledgeBase') }}
      </div>
      <!-- Knowledge Bases Group -->
      <div v-if="(isFlatMode || currentGroupType === 'kb') && kbItems.length > 0" class="mention-group" data-group-type="kb">
        <t-popup
          v-for="(item, index) in kbItems"
          :key="item.id"
          placement="right-start"
          trigger="hover"
          :show-arrow="false"
          :delay="[320, 80]"
          :disabled="isScrolling"
          :overlay-class-name="'mention-detail-popup'"
          :overlay-inner-class-name="'mention-detail-popup-wrap'"
          @visible-change="(v: boolean) => v && fetchKbDetail(item)"
        >
          <div
            class="mention-item"
            :class="{ active: index === activeIndex }"
            @click="$emit('select', item)"
            @mouseenter="$emit('update:activeIndex', index)"
          >
            <div class="icon-wrap">
              <div class="icon" :class="item.kbType === 'faq' ? 'faq-icon' : 'kb-icon'">
                <t-icon :name="item.kbType === 'faq' ? 'chat-bubble-help' : 'folder'" />
              </div>
            </div>
            <div class="item-main">
              <span class="name">{{ item.name }}</span>
              <span class="count">{{ item.count || 0 }}</span>
            </div>
          </div>
          <template #content>
            <div class="mention-detail-content">
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
            </div>
          </template>
        </t-popup>
      </div>

      <template v-for="group in activeExtraGroups" :key="group.type">
        <div
          v-if="isFlatMode && groupTabs.length > 1"
          class="mention-group-header"
        >
          {{ group.label }}
        </div>
        <div class="mention-group" :data-group-type="group.type">
        <t-popup
          v-for="(item, index) in group.items"
          :key="`${item.type}:${item.id}`"
          placement="right-start"
          trigger="hover"
          :show-arrow="false"
          :delay="[320, 80]"
          :disabled="isScrolling"
          :overlay-class-name="'mention-detail-popup'"
          :overlay-inner-class-name="'mention-detail-popup-wrap'"
        >
          <div
            class="mention-item"
            :class="{ active: group.offset + index === activeIndex }"
            @click="$emit('select', item)"
            @mouseenter="$emit('update:activeIndex', group.offset + index)"
          >
            <div class="icon-wrap">
              <div class="icon" :class="`${item.type}-icon`">
                <t-icon :name="group.icon" />
              </div>
            </div>
            <div class="item-main">
              <span class="name">{{ item.name }}</span>
            </div>
          </div>
          <template #content>
            <div class="mention-detail-content">
              <div class="detail-header">
                <span class="detail-name">{{ item.name }}</span>
              </div>
              <p v-if="item.description" class="detail-desc">{{ item.description }}</p>
              <div class="detail-meta">
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
            </div>
          </template>
        </t-popup>
        </div>
      </template>

      <div
        v-if="isFlatMode && groupTabs.length > 1 && fileItems.length > 0"
        class="mention-group-header"
      >
        {{ $t('common.file') }}
      </div>
      <!-- Files Group -->
      <div v-if="(isFlatMode || currentGroupType === 'file') && fileItems.length > 0" class="mention-group" data-group-type="file">
        <t-popup
          v-for="(item, index) in fileItems"
          :key="item.id"
          placement="right-start"
          trigger="hover"
          :show-arrow="false"
          :delay="[320, 80]"
          :disabled="isScrolling"
          :overlay-class-name="'mention-detail-popup'"
          :overlay-inner-class-name="'mention-detail-popup-wrap'"
          @visible-change="(v: boolean) => v && fetchFileDetail(item)"
        >
          <div
            class="mention-item"
            :class="{ active: (fileGroupOffset + index) === activeIndex }"
            @click="$emit('select', item)"
            @mouseenter="$emit('update:activeIndex', fileGroupOffset + index)"
          >
            <div class="icon-wrap">
              <div class="icon file-icon">
                <t-icon name="file" />
              </div>
            </div>
            <span class="name">{{ item.name }}</span>
          </div>
          <template #content>
            <div class="mention-detail-content">
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
                      @click.stop="handleKbClick(detailCache[item.id].data.knowledge_base_id || (item as any).kbId)"
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
            </div>
          </template>
        </t-popup>
        <!-- Loading indicator -->
        <div v-if="loading" class="loading-more">
          <t-loading size="small" />
        </div>
      </div>

      <div v-if="items.length === 0 && !loading" class="empty">
        {{ emptyHint || $t('common.noResult') }}
      </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, watch, ref, nextTick, onBeforeUnmount } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { getKnowledgeBaseById } from '@/api/knowledge-base';
import { getKnowledgeDetails } from '@/api/knowledge-base';
import { useOrganizationStore } from '@/stores/organization';
import { useSettingsStore } from '@/stores/settings';
import type { MentionItem, MentionItemType } from '@/types/mention';

type DetailState = { loading: boolean; error?: string; data?: any };

const props = defineProps<{
  visible: boolean;
  style: any;
  items: MentionItem[];
  activeIndex: number;
  hasMore?: boolean;
  loading?: boolean;
  // 空态下替换默认 "无结果" 文案，用于给上游（如"被智能体工具兼容性过滤掉了"）透传具体原因
  emptyHint?: string;
  // 输入 @ 后的筛选关键词；非空时平铺展示匹配项，不再要求进入二级目录
  query?: string;
  // 分组入口展示用的总数（如文件搜索的 total），避免仅用首屏已加载条数
  groupCounts?: Partial<Record<MentionItemType, number>>;
}>();

const emit = defineEmits(['select', 'update:activeIndex', 'loadMore']);

const router = useRouter();
const { t } = useI18n();
const orgStore = useOrganizationStore();
const settingsStore = useSettingsStore();
const menuRef = ref<HTMLElement | null>(null);
const listRef = ref<HTMLElement | null>(null);
const detailCache = ref<Record<string, DetailState>>({});
const isScrolling = ref(false);
const currentGroupType = ref<MentionItemType | null>(null);
const groupActiveIndex = ref(0);
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

const kbItems = computed(() => props.items.filter(item => item.type === 'kb'));
const fileItems = computed(() => props.items.filter(item => item.type === 'file'));

const mentionGroupDefs = computed<Array<{ type: MentionItemType; label: string; icon: string }>>(() => [
  { type: 'kb', label: t('common.knowledgeBase'), icon: 'folder' },
  { type: 'tag', label: '标签', icon: 'tag' },
  { type: 'mcp', label: 'MCP', icon: 'tools' },
  { type: 'skill', label: 'Skills', icon: 'bookmark' },
  { type: 'file', label: t('common.file'), icon: 'file' },
]);

const mentionGroups = computed(() => {
  let offset = 0;
  return mentionGroupDefs.value.map(def => {
    const items = props.items.filter(item => item.type === def.type);
    const loadedCount = items.length;
    const count = props.groupCounts?.[def.type] ?? loadedCount;
    const group = { ...def, items, offset, count, loadedCount };
    offset += items.length;
    return group;
  });
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

const groupTabs = computed(() => mentionGroups.value.filter(group => group.count > 0));
const groupRows = computed(() => groupTabs.value);
const isFlatMode = computed(() => (props.query ?? '').trim().length > 0);
const currentGroup = computed(() => mentionGroups.value.find(group => group.type === currentGroupType.value));
const extraGroups = computed(() => mentionGroups.value.filter(group =>
  group.type !== 'kb' && group.type !== 'file' && group.count > 0
));
const activeExtraGroups = computed(() => {
  if (isFlatMode.value) return extraGroups.value;
  return extraGroups.value.filter(group => group.type === currentGroupType.value);
});
const fileGroupOffset = computed(() => mentionGroups.value.find(group => group.type === 'file')?.offset || 0);

const enterGroup = (type: MentionItemType) => {
  const group = mentionGroups.value.find(item => item.type === type && item.count > 0);
  if (!group || !listRef.value) return;

  currentGroupType.value = type;
  emit('update:activeIndex', group.offset);

  nextTick(() => {
    if (!listRef.value) return;
    listRef.value.scrollTo({
      top: 0,
    });
  });
};

const leaveGroup = () => {
  if (isFlatMode.value) return false;
  if (!currentGroupType.value) return false;
  const rowIndex = groupRows.value.findIndex(group => group.type === currentGroupType.value);
  groupActiveIndex.value = Math.max(0, rowIndex);
  currentGroupType.value = null;
  nextTick(() => {
    if (listRef.value) listRef.value.scrollTop = 0;
  });
  return true;
};

const updateActiveGroupFromIndex = (index: number) => {
  const group = groupTabs.value.find(item => index >= item.offset && index < item.offset + item.count);
  if (group) currentGroupType.value = group.type;
};

watch(groupTabs, (groups) => {
  if (groupActiveIndex.value >= groups.length) {
    groupActiveIndex.value = Math.max(0, groups.length - 1);
  }
});

const moveActive = (delta: number) => {
  if (isFlatMode.value) {
    const next = Math.min(props.items.length - 1, Math.max(0, props.activeIndex + delta));
    emit('update:activeIndex', next);
    scrollToItem(next);
    return;
  }

  if (!currentGroupType.value) {
    const maxIndex = Math.max(0, groupRows.value.length - 1);
    groupActiveIndex.value = Math.min(maxIndex, Math.max(0, groupActiveIndex.value + delta));
    return;
  }

  const group = currentGroup.value;
  if (!group) return;
  const currentLocalIndex = props.activeIndex - group.offset;
  const nextLocalIndex = Math.min(group.count - 1, Math.max(0, currentLocalIndex + delta));
  emit('update:activeIndex', group.offset + nextLocalIndex);
  scrollToItem(nextLocalIndex);
};

const confirmActive = () => {
  if (isFlatMode.value) {
    const item = props.items[props.activeIndex];
    if (item) emit('select', item);
    return;
  }

  if (!currentGroupType.value) {
    const group = groupRows.value[groupActiveIndex.value];
    if (group) enterGroup(group.type);
    return;
  }

  const group = currentGroup.value;
  if (!group) return;
  const localIndex = props.activeIndex - group.offset;
  const item = group.items[localIndex];
  if (item) emit('select', item);
};

defineExpose({
  moveActive,
  confirmActive,
  leaveGroup,
});

async function fetchKbDetail(item: { id: string }) {
  if (detailCache.value[item.id]?.data || detailCache.value[item.id]?.loading) return;
  detailCache.value = { ...detailCache.value, [item.id]: { loading: true } };
  try {
    const opts = agentIdForDetail.value ? { agent_id: agentIdForDetail.value } : undefined;
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
    const opts = agentIdForDetail.value ? { agent_id: agentIdForDetail.value } : undefined;
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

function handleOrgClick(orgName: string) {
  if (!orgName) return;
  // 从共享知识库列表中找到对应的组织 ID
  const sharedKb = orgStore.sharedKnowledgeBases.find(
    (s: any) => s.org_name === orgName
  );
  if (sharedKb?.organization_id) {
    // 跳转到组织列表页（目前组织详情页可能不存在，先跳转到列表页）
    router.push('/platform/organizations');
  } else {
    // 如果找不到组织 ID，也跳转到组织列表页
    router.push('/platform/organizations');
  }
}

const onScroll = (e: Event) => {
  isScrolling.value = true;
  if (scrollTimer) clearTimeout(scrollTimer);
  scrollTimer = setTimeout(() => {
    isScrolling.value = false;
  }, 150);

  const target = e.target as HTMLElement;
  const { scrollTop, scrollHeight, clientHeight } = target;
  if ((currentGroupType.value === 'file' || isFlatMode.value) && scrollHeight - scrollTop - clientHeight < 50 && props.hasMore && !props.loading) {
    emit('loadMore');
  }
};

watch(() => props.activeIndex, (newIndex) => {
  if (isFlatMode.value) {
    scrollToItem(newIndex);
    return;
  }
  if (currentGroupType.value) {
    updateActiveGroupFromIndex(newIndex);
    const group = currentGroup.value;
    if (group) scrollToItem(newIndex - group.offset);
  }
});

watch(isFlatMode, (flat) => {
  if (flat) {
    currentGroupType.value = null;
    nextTick(() => {
      if (listRef.value) listRef.value.scrollTop = 0;
    });
  }
});

watch(() => props.visible, (newVisible) => {
  if (newVisible) {
    nextTick(() => {
      if (listRef.value) listRef.value.scrollTop = 0;
      currentGroupType.value = null;
      groupActiveIndex.value = 0;
    });
  }
});

const scrollToItem = (index: number) => {
  nextTick(() => {
    if (!listRef.value) return;
    
    const items = listRef.value.querySelectorAll('.mention-item');
    if (!items || items.length <= index) return;
    
    const activeItem = items[index] as HTMLElement;
    const menu = listRef.value;
    
    if (activeItem) {
      const menuRect = menu.getBoundingClientRect();
      const itemRect = activeItem.getBoundingClientRect();
      
      // 检查是否在上方被遮挡
      if (itemRect.top < menuRect.top) {
        menu.scrollTop -= (menuRect.top - itemRect.top);
      }
      // 检查是否在下方被遮挡
      else if (itemRect.bottom > menuRect.bottom) {
        menu.scrollTop += (itemRect.bottom - menuRect.bottom);
      }
    }
  });
};
</script>

<style scoped>
.mention-menu {
  position: fixed;
  z-index: 10000;
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-stroke, #e7e9eb);
  border-radius: var(--td-radius-extraLarge, 12px);
  box-shadow: 0 10px 30px rgba(0, 0, 0, 0.1), 0 2px 8px rgba(0, 0, 0, 0.04);
  width: 220px;
  max-height: 388px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.mention-list {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding: 4px 0;
}

.mention-group-entry,
.mention-back-row {
  width: calc(100% - 12px);
  min-height: 32px;
  margin: 1px 6px;
  padding: 0 8px;
  border: 0;
  border-radius: var(--td-radius-medium, 6px);
  background: transparent;
  color: var(--td-text-color-primary, #333);
  font-family: var(--app-font-family);
  font-size: var(--td-font-size-body-medium, 14px);
  font-weight: 400;
  line-height: 20px;
  cursor: pointer;
  box-sizing: border-box;
  display: flex;
  align-items: center;
  gap: 8px;
  transition: background 0.15s ease;
}

.mention-group-entry:hover,
.mention-group-entry.active,
.mention-back-row:hover {
  background: var(--td-bg-color-secondarycontainer, #f3f3f3);
}

.mention-group-entry__icon {
  width: 18px;
  height: 18px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary, #666);
  font-size: 16px;
}

.mention-group-entry__label,
.mention-back-row span {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: inherit;
  font-weight: inherit;
}

.mention-group-entry__count {
  flex-shrink: 0;
  min-width: 18px;
  padding: 0 6px;
  border-radius: 999px;
  background: var(--td-bg-color-secondarycontainer, #f3f3f3);
  color: var(--td-text-color-placeholder, #999);
  font-size: var(--td-font-size-mark-small, 12px);
  line-height: 18px;
  text-align: center;
  font-variant-numeric: tabular-nums;
}

.mention-group-entry__arrow {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder, #999);
  font-size: 16px;
}

.mention-back-row {
  min-height: 30px;
  margin-bottom: 4px;
  color: var(--td-text-color-secondary, #666);
  border-bottom: 1px solid var(--td-component-stroke, #f0f0f0);
  border-radius: 0;
}

.mention-back-row span {
  font-size: var(--td-font-size-body-medium, 14px);
}

.mention-group {
  padding: 2px 0 5px;
}

.mention-group:not(:last-child) {
  border-bottom: 1px solid var(--td-component-stroke, #f0f0f0);
}

.mention-group-header {
  padding: 7px 14px 5px;
  font-size: var(--td-font-size-mark-small, 12px);
  font-weight: 600;
  line-height: 18px;
  color: var(--td-text-color-placeholder, #999);
}

.mention-item {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 32px;
  padding: 4px 8px;
  margin: 1px 6px;
  box-sizing: border-box;
  cursor: pointer;
  border-radius: var(--td-radius-medium, 6px);
  color: var(--td-text-color-primary, #333);
  font-size: var(--td-font-size-body-medium, 14px);
  font-family: var(--app-font-family);
  transition: background 0.15s ease;
}

.mention-item:hover {
  background: var(--td-bg-color-secondarycontainer, #f3f3f3);
}

.mention-item.active {
  background: var(--td-bg-color-secondarycontainer, #f3f3f3);
  color: var(--td-text-color-primary, #333);
}

.icon-wrap {
  position: relative;
  width: 18px;
  height: 18px;
  flex-shrink: 0;
}

.icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  flex-shrink: 0;
  font-size: 16px;
}

/* 右下角组织角标：柔和小圆 + 绿色/灰色 icon，不刺眼 */
.org-badge-wrap {
  position: absolute;
  right: 0;
  bottom: 0;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer, #f0f2f5);
  box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.05);
  display: flex;
  align-items: center;
  justify-content: center;
  pointer-events: none;
}

.org-badge-wrap .org-badge {
  width: 6px;
  height: 6px;
  object-fit: contain;
}

/* 知识库 / 文件 - 无背景，与整体一致 */
.kb-icon,
.faq-icon,
.file-icon {
  background: transparent;
  color: var(--td-text-color-secondary, #666);
}

.mention-item.active .icon {
  color: var(--td-brand-color);
}

.mention-item.active .faq-icon {
  color: var(--weknora-faq-color, #0052d9);
}

.item-main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 4px;
}

.name {
  font-weight: 400;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 文件项中的 name 需要占据剩余空间，将 kb-name 推到右边 */
.mention-item > .name {
  flex: 1;
  min-width: 0;
}

.count {
  margin-left: auto;
  flex-shrink: 0;
  font-size: var(--td-font-size-mark-small, 12px);
  font-variant-numeric: tabular-nums;
  color: var(--td-text-color-placeholder, #999);
}

.org-name {
  flex-shrink: 0;
  max-width: 72px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--td-font-size-mark-small, 12px);
  color: var(--td-text-color-placeholder, #999);
}

.kb-name {
  flex-shrink: 0;
  max-width: 80px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--td-font-size-mark-small, 12px);
  color: var(--td-text-color-secondary, #999);
}

.empty {
  padding: 28px 16px;
  text-align: center;
  color: var(--td-text-color-placeholder, #999);
  font-size: var(--td-font-size-body-medium, 14px);
}

.loading-more {
  display: flex;
  justify-content: center;
  padding: 8px 12px;
}
</style>

<style>
/* 详情浮层在 Teleport 中，需全局样式 */
.mention-detail-popup-wrap.t-popup__content {
  min-width: 220px;
  max-width: 280px;
  padding: 13px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-large, 9px);
  background: var(--td-bg-color-container);
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.1), 0 2px 8px rgba(0, 0, 0, 0.04);
}
.mention-detail-content {
  font-size: var(--td-font-size-body-small, 12px);
  color: var(--td-text-color-primary, #333);
  line-height: 1.5;
}
.mention-detail-content .detail-loading,
.mention-detail-content .detail-error {
  padding: 8px 0;
  color: var(--td-text-color-secondary, #999);
  font-size: var(--td-font-size-body-small, 12px);
}
.mention-detail-content .detail-error {
  color: var(--td-error-color, #e34d59);
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
  font-size: var(--td-font-size-body-medium, 14px);
  line-height: 20px;
  word-break: break-word;
}
.mention-detail-content .detail-type-badge {
  flex-shrink: 0;
  padding: 1px 6px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium, 6px);
  font-size: var(--td-font-size-mark-small, 12px);
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
  font-size: var(--td-font-size-body-small, 12px);
  color: var(--td-text-color-secondary, #666);
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 4;
  line-clamp: 4;
  -webkit-box-orient: vertical;
  overflow: hidden;
  word-break: break-word;
}
.mention-detail-content .detail-meta {
  font-size: var(--td-font-size-mark-small, 12px);
  color: var(--td-text-color-placeholder, #999);
  display: flex;
  flex-direction: column;
  gap: 5px;
  align-items: flex-start;
}
.mention-detail-content .detail-readonly-hint {
  display: block;
  margin-top: 6px;
  font-size: var(--td-font-size-mark-small, 12px);
  color: var(--td-text-color-placeholder, #999);
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
  font-size: 14px;
  color: var(--td-text-color-placeholder, #999);
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
  color: var(--td-text-color-placeholder, #000000);
  opacity: 0.7;
  display: inline-block;
  vertical-align: middle;
  object-fit: contain;
}
.mention-detail-content .detail-label {
  color: var(--td-text-color-placeholder, #999);
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
  text-decoration-color: var(--td-text-color-placeholder, #999);
  transition: color 0.2s, text-decoration-color 0.2s;
}
.mention-detail-content .detail-value.clickable:hover {
  color: var(--td-brand-color, #07c05f);
  text-decoration-color: var(--td-brand-color, #07c05f);
}
</style>
