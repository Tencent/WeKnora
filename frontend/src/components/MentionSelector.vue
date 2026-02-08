<template>
  <div v-if="visible" class="mention-menu" :style="style" ref="menuRef" @click.stop @scroll="onScroll">
    <!-- Knowledge Bases Group -->
    <div v-if="kbItems.length > 0" class="mention-group">
      <div class="mention-group-header">{{ $t('common.knowledgeBase') }}</div>
      <t-popup
        v-for="(item, index) in kbItems"
        :key="item.id"
        placement="right"
        trigger="hover"
        :show-arrow="true"
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
            <span class="count">({{ item.count || 0 }})</span>
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
    
    <!-- Files Group -->
    <div v-if="fileItems.length > 0" class="mention-group">
      <div class="mention-group-header">{{ $t('common.file') }}</div>
      <t-popup
        v-for="(item, index) in fileItems"
        :key="item.id"
        placement="right"
        trigger="hover"
        :show-arrow="true"
        :overlay-inner-class-name="'mention-detail-popup-wrap'"
        @visible-change="(v: boolean) => v && fetchFileDetail(item)"
      >
        <div
          class="mention-item"
          :class="{ active: (kbItems.length + index) === activeIndex }"
          @click="$emit('select', item)"
          @mouseenter="$emit('update:activeIndex', kbItems.length + index)"
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
      {{ $t('common.noResult') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, watch, ref, nextTick } from 'vue';
import { useRouter } from 'vue-router';
import { getKnowledgeBaseById } from '@/api/knowledge-base';
import { getKnowledgeDetails } from '@/api/knowledge-base';
import { useOrganizationStore } from '@/stores/organization';
import { useSettingsStore } from '@/stores/settings';

type DetailState = { loading: boolean; error?: string; data?: any };

const props = defineProps<{
  visible: boolean;
  style: any;
  items: Array<{ id: string; name: string; type: 'kb' | 'file'; kbType?: 'document' | 'faq'; count?: number; kbName?: string; orgName?: string; kbId?: string }>;
  activeIndex: number;
  hasMore?: boolean;
  loading?: boolean;
}>();

const emit = defineEmits(['select', 'update:activeIndex', 'loadMore']);

const router = useRouter();
const orgStore = useOrganizationStore();
const settingsStore = useSettingsStore();
const menuRef = ref<HTMLElement | null>(null);
const detailCache = ref<Record<string, DetailState>>({});

// 共享智能体上下文：用于请求知识库/知识详情时带 agent_id，后端据此校验权限
const agentIdForDetail = computed(() => {
  const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
  const agentId = settingsStore.selectedAgentId;
  return sourceTenantId && agentId ? agentId : undefined;
});

const kbItems = computed(() => props.items.filter(item => item.type === 'kb'));
const fileItems = computed(() => props.items.filter(item => item.type === 'file'));

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
  const target = e.target as HTMLElement;
  const { scrollTop, scrollHeight, clientHeight } = target;
  // Load more when scrolled to bottom (with 50px threshold)
  if (scrollHeight - scrollTop - clientHeight < 50 && props.hasMore && !props.loading) {
    emit('loadMore');
  }
};

watch(() => props.activeIndex, (newIndex) => {
  scrollToItem(newIndex);
});

watch(() => props.visible, (newVisible) => {
  if (newVisible) {
    nextTick(() => {
      if (menuRef.value) menuRef.value.scrollTop = 0;
      scrollToItem(props.activeIndex);
    });
  }
});

const scrollToItem = (index: number) => {
  nextTick(() => {
    if (!menuRef.value) return;
    
    const items = menuRef.value.querySelectorAll('.mention-item');
    if (!items || items.length <= index) return;
    
    const activeItem = items[index] as HTMLElement;
    const menu = menuRef.value;
    
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
  border: 1px solid var(--td-component-border, #e7e9eb);
  border-radius: var(--td-radius-medium, 6px);
  box-shadow: var(--td-shadow-2, 0 3px 14px 2px rgba(0, 0, 0, 0.05));
  width: 300px;
  max-height: 360px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  padding: 4px 0;
}

.mention-group {
  padding: 4px 0;
}

.mention-group:not(:last-child) {
  border-bottom: 1px solid var(--td-component-border, #f0f0f0);
}

.mention-group-header {
  padding: 8px 12px 4px;
  font-size: var(--td-font-size-mark-small, 12px);
  font-weight: 600;
  color: var(--td-text-color-secondary, #999);
}

.mention-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  margin: 0 4px;
  cursor: pointer;
  border-radius: var(--td-radius-default, 3px);
  color: var(--td-text-color-primary, #333);
  font-size: var(--td-font-size-body-medium, 14px);
  font-family: var(--td-font-family, "PingFang SC");
  transition: background 0.2s cubic-bezier(0.38, 0, 0.24, 1);
}

.mention-item:hover {
  background: var(--td-bg-color-container-hover, #f3f3f3);
}

.mention-item.active {
  background: var(--td-brand-color-light, #e9f8ec);
  color: var(--td-brand-color, #07c05f);
}

.icon-wrap {
  position: relative;
  width: 20px;
  height: 20px;
  flex-shrink: 0;
}

.icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: var(--td-radius-small, 2px);
  flex-shrink: 0;
  /* background: var(--td-bg-color-secondarycontainer, #f3f3f3); */
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
  /* Active state keeps the colored icon but maybe adjusts background or just inherits */
  background: transparent;
  color: inherit;
}

.item-main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 4px;
}

.name {
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
  flex-shrink: 0;
  font-size: var(--td-font-size-mark-small, 12px);
  color: var(--td-text-color-secondary, #999);
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
  padding: 24px 12px;
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
  padding: 12px;
  max-width: 320px;
  min-width: 240px;
}
.mention-detail-content {
  font-size: var(--td-font-size-body-medium, 14px);
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
  margin-bottom: 6px;
}
.mention-detail-content .detail-name {
  font-weight: 600;
  font-size: var(--td-font-size-body-large, 14px);
  word-break: break-word;
}
.mention-detail-content .detail-type-badge {
  flex-shrink: 0;
  padding: 2px 6px;
  border-radius: var(--td-radius-small, 2px);
  font-size: var(--td-font-size-mark-small, 12px);
}
.mention-detail-content .detail-type-badge.doc {
  background: rgba(16, 185, 129, 0.1);
  color: #10b981;
}
.mention-detail-content .detail-type-badge.faq {
  background: rgba(0, 82, 217, 0.1);
  color: #0052d9;
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
  gap: 6px;
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
  color: #17b03a;
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
