<template>
  <SettingDrawer
    v-model:visible="drawerVisible"
    :title="$t('knowledgeBase.tagManageTitle')"
    :description="$t('knowledgeBase.tagManageDescription')"
    icon="discount"
    width="480px"
    :min-width="420"
    :max-width="640"
    resizable
    storage-key="setting-drawer:width:kb-tag-manage"
    :hide-footer="true"
  >
    <section class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.tagManageListSection') }}</h4>

      <div class="tag-manage-toolbar">
        <div class="tag-manage-search-wrap">
          <t-input
            v-model.trim="searchQuery"
            size="small"
            :placeholder="$t('knowledgeBase.tagSearchPlaceholder')"
            clearable
            class="tag-manage-search"
          >
            <template #prefix-icon>
              <t-icon name="search" size="14px" />
            </template>
          </t-input>
        </div>
        <t-tooltip :content="$t('knowledgeBase.tagCreateAction')" placement="top">
          <t-button
            size="small"
            variant="text"
            class="tag-manage-create-btn"
            :disabled="creatingTag"
            :aria-label="$t('knowledgeBase.tagCreateAction')"
            @click="startCreateTag"
          >
            <template #icon><t-icon name="add" size="16px" /></template>
          </t-button>
        </t-tooltip>
      </div>

      <t-loading :loading="loading && !tags.length" size="small" class="tag-manage-loading">
        <div v-if="!loading && !tags.length && !creatingTag" class="tag-manage-empty">
          <t-empty :description="$t('knowledgeBase.tagEmptyResult')" />
        </div>

        <ul v-else class="tag-tile-grid">
          <template v-if="loading && !tags.length">
            <li v-for="n in 4" :key="'tag-skel-' + n" class="tag-tile tag-tile--skeleton">
              <t-skeleton animation="gradient" :row-col="[{ width: '100%', height: '44px', type: 'rect' }]" />
            </li>
          </template>

          <template v-else>
            <li v-if="creatingTag" class="tag-tile tag-tile--editing" @click.stop>
              <div class="tag-tile__main tag-tile__main--editing">
                <span class="tag-tile__badge" aria-hidden="true">
                  <t-icon name="discount" size="15px" />
                </span>
                <t-input
                  ref="newTagInputRef"
                  v-model="newTagName"
                  size="small"
                  :maxlength="40"
                  class="tag-tile__input"
                  :placeholder="$t('knowledgeBase.tagNamePlaceholder')"
                  @enter="submitCreateTag"
                  @keydown="(_v: string, ctx?: { e?: KeyboardEvent }) => onEditKeydown(ctx, cancelCreateTag)"
                />
              </div>
              <div class="tag-tile__actions">
                <t-button
                  variant="text"
                  shape="square"
                  size="small"
                  class="tag-tile__action-btn tag-tile__action-btn--confirm"
                  :loading="creatingTagLoading"
                  :title="$t('common.create')"
                  @click.stop="submitCreateTag"
                >
                  <template #icon><t-icon name="check" size="14px" /></template>
                </t-button>
                <t-button
                  variant="text"
                  shape="square"
                  size="small"
                  class="tag-tile__action-btn"
                  :title="$t('common.cancel')"
                  @click.stop="cancelCreateTag"
                >
                  <template #icon><t-icon name="close" size="14px" /></template>
                </t-button>
              </div>
            </li>

            <li
              v-for="tag in tags"
              :key="tag.id"
              class="tag-tile"
              :class="{ 'tag-tile--editing': editingTagId === tag.id }"
              @click.stop
            >
              <template v-if="editingTagId === tag.id">
                <div class="tag-tile__main tag-tile__main--editing">
                  <span class="tag-tile__badge" aria-hidden="true">
                    <t-icon name="discount" size="15px" />
                  </span>
                  <t-input
                    :ref="(el: any) => setEditingTagInputRef(el, tag.id)"
                    v-model="editingTagName"
                    size="small"
                    :maxlength="40"
                    class="tag-tile__input"
                    :placeholder="$t('knowledgeBase.tagNamePlaceholder')"
                    @enter="submitEditTag"
                    @keydown="(_v: string, ctx?: { e?: KeyboardEvent }) => onEditKeydown(ctx, cancelEditTag)"
                  />
                </div>
                <div class="tag-tile__actions">
                  <t-button
                    variant="text"
                    shape="square"
                    size="small"
                    class="tag-tile__action-btn tag-tile__action-btn--confirm"
                    :loading="editingTagSubmitting"
                    :title="$t('common.save')"
                    @click.stop="submitEditTag"
                  >
                    <template #icon><t-icon name="check" size="14px" /></template>
                  </t-button>
                  <t-button
                    variant="text"
                    shape="square"
                    size="small"
                    class="tag-tile__action-btn"
                    :title="$t('common.cancel')"
                    @click.stop="cancelEditTag"
                  >
                    <template #icon><t-icon name="close" size="14px" /></template>
                  </t-button>
                </div>
              </template>
              <template v-else>
                <div class="tag-tile__main">
                  <span class="tag-tile__badge" aria-hidden="true">
                    <t-icon name="discount" size="15px" />
                  </span>
                  <span class="tag-tile__text">
                    <span class="tag-tile__name" :title="tag.name">{{ tag.name }}</span>
                    <span class="tag-tile__count">
                      {{
                        isFaq
                          ? $t('knowledgeBase.tagManageFaqCount', { count: tag.chunk_count || 0 })
                          : $t('knowledgeBase.tagManageDocCount', { count: tag.knowledge_count || 0 })
                      }}
                    </span>
                  </span>
                </div>
                <div class="tag-tile__actions" @click.stop>
                  <t-button
                    variant="text"
                    shape="square"
                    size="small"
                    class="tag-tile__action-btn"
                    :title="$t('knowledgeBase.tagEditAction')"
                    @click="startEditTag(tag)"
                  >
                    <template #icon><t-icon name="edit" size="14px" /></template>
                  </t-button>
                  <t-popconfirm
                    :content="getDeleteConfirmContent(tag)"
                    :confirm-btn="{ content: $t('common.delete'), theme: 'danger' }"
                    :cancel-btn="{ content: $t('common.cancel') }"
                    placement="bottom-right"
                    @confirm="deleteTag(tag)"
                  >
                    <t-button
                      theme="danger"
                      shape="square"
                      variant="text"
                      size="small"
                      class="tag-tile__action-btn"
                      :title="$t('knowledgeBase.tagDeleteAction')"
                      @click.stop
                    >
                      <template #icon><t-icon name="delete" size="14px" /></template>
                    </t-button>
                  </t-popconfirm>
                </div>
              </template>
            </li>
          </template>
        </ul>

        <div v-if="hasMore && tags.length" class="tag-load-more">
          <t-button variant="text" size="small" :loading="loadingMore" @click="loadTags(false)">
            {{ $t('tenant.loadMore') }}
          </t-button>
        </div>
      </t-loading>
    </section>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { ref, watch, nextTick, computed, onBeforeUnmount, type ComponentPublicInstance } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import SettingDrawer from '@/components/settings/SettingDrawer.vue';
import {
  listKnowledgeTags,
  createKnowledgeBaseTag,
  updateKnowledgeBaseTag,
  deleteKnowledgeBaseTag,
} from '@/api/knowledge-base/index';

type TagRow = {
  id: string;
  seq_id: number;
  name: string;
  knowledge_count?: number;
  chunk_count?: number;
};

type TagInputInstance = ComponentPublicInstance<{ focus: () => void; select: () => void }>;

const TAG_PAGE_SIZE = 50;
const TAG_SEARCH_DEBOUNCE_MS = 300;
const TAG_DELETE_REFRESH_DELAY_MS = 800;

const props = defineProps<{
  visible: boolean;
  kbId: string;
  isFaq?: boolean;
}>();

const emit = defineEmits<{
  'update:visible': [boolean];
  changed: [payload?: { deletedTagId?: string }];
}>();

const { t } = useI18n();

const drawerVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
});

const tags = ref<TagRow[]>([]);
const loading = ref(false);
const loadingMore = ref(false);
const page = ref(1);
const hasMore = ref(false);
const total = ref(0);
const searchQuery = ref('');
let searchDebounce: ReturnType<typeof setTimeout> | null = null;
let disposed = false;
// 知识库归属与抽屉会话分开：关闭后写成功仍要通知同一知识库的父列表。
let contextEpoch = 0;
let drawerEpoch = 0;
let requestEpoch = 0;
let resettingSearch = false;
let createEpoch = 0;
let editEpoch = 0;
const deleteRefreshTimers = new Set<ReturnType<typeof setTimeout>>();

const creatingTag = ref(false);
const creatingTagLoading = ref(false);
const newTagName = ref('');
const newTagInputRef = ref<TagInputInstance | null>(null);

const editingTagId = ref<string | null>(null);
const editingTagName = ref('');
const editingTagSubmitting = ref(false);
const editingTagInputRefs = new Map<string, TagInputInstance | null>();

const setEditingTagInputRef = (el: TagInputInstance | null, tagId: string) => {
  if (el) {
    editingTagInputRefs.set(tagId, el);
  } else {
    editingTagInputRefs.delete(tagId);
  }
};

const getDeleteConfirmContent = (tag: { name: string }) =>
  t(props.isFaq ? 'knowledgeBase.tagDeleteDesc' : 'knowledgeBase.tagDeleteDescDoc', { name: tag.name });

const onEditKeydown = (ctx: { e?: KeyboardEvent } | undefined, cancel: () => void) => {
  if (ctx?.e?.key === 'Escape') {
    ctx.e.stopPropagation();
    ctx.e.preventDefault();
    cancel();
  }
};

const isCurrentContext = (epoch: number) => !disposed && contextEpoch === epoch;
const isCurrentDrawer = (epoch: number) =>
  !disposed && props.visible && !!props.kbId && drawerEpoch === epoch;

const cancelSearchDebounce = () => {
  if (searchDebounce !== null) {
    clearTimeout(searchDebounce);
    searchDebounce = null;
  }
};

const invalidateTagRequests = () => {
  requestEpoch += 1;
  cancelSearchDebounce();
  loading.value = false;
  loadingMore.value = false;
};

const resetTagList = () => {
  tags.value = [];
  total.value = 0;
  hasMore.value = false;
  page.value = 1;
};

const clearDeleteRefreshTimers = () => {
  for (const timer of deleteRefreshTimers) clearTimeout(timer);
  deleteRefreshTimers.clear();
};

const loadTags = async (reset = false) => {
  if (!isCurrentDrawer(drawerEpoch)) return;
  if (reset) {
    invalidateTagRequests();
    resetTagList();
  } else if (loading.value || loadingMore.value || searchDebounce !== null || !hasMore.value) {
    return;
  }

  const currentPage = page.value;
  const kbId = props.kbId;
  const keyword = searchQuery.value || undefined;
  const drawer = drawerEpoch;
  const request = ++requestEpoch;
  const isCurrent = () => isCurrentDrawer(drawer) && requestEpoch === request;
  loading.value = currentPage === 1;
  loadingMore.value = currentPage > 1;

  try {
    const res: any = await listKnowledgeTags(kbId, {
      page: currentPage,
      page_size: TAG_PAGE_SIZE,
      keyword,
    });
    if (!isCurrent()) return;
    const pageData = (res?.data || {}) as { data?: TagRow[]; total?: number };
    const pageTags = (pageData.data || []).map((tag) => ({
      ...tag,
      id: String(tag.id),
    }));

    if (currentPage === 1) {
      tags.value = pageTags;
    } else {
      tags.value = [...tags.value, ...pageTags];
    }

    total.value = pageData.total || tags.value.length;
    hasMore.value = tags.value.length < total.value;
    if (hasMore.value) {
      page.value = currentPage + 1;
    }
  } catch (error) {
    if (isCurrent()) console.error('Failed to load tags', error);
  } finally {
    // 旧分页或旧关键词的收尾不能提前结束新请求的加载状态。
    if (isCurrent()) {
      loading.value = false;
      loadingMore.value = false;
    }
  }
};

const cancelCreateTag = () => {
  createEpoch += 1;
  creatingTag.value = false;
  creatingTagLoading.value = false;
  newTagName.value = '';
};

const cancelEditTag = () => {
  editEpoch += 1;
  editingTagId.value = null;
  editingTagName.value = '';
  editingTagSubmitting.value = false;
};

// 服务端写入已成功时保留父列表通知；抽屉自身只刷新当前会话的查询。
const refreshAfterTagChange = (context: number, payload?: { deletedTagId?: string }) => {
  if (!isCurrentContext(context)) return;
  emit('changed', payload);
  if (isCurrentContext(context) && isCurrentDrawer(drawerEpoch)) void loadTags(true);
};

const startCreateTag = () => {
  if (!isCurrentDrawer(drawerEpoch) || creatingTag.value) return;
  cancelEditTag();
  creatingTag.value = true;
  const drawer = drawerEpoch;
  const form = ++createEpoch;
  nextTick(() => {
    if (!isCurrentDrawer(drawer) || createEpoch !== form || !creatingTag.value) return;
    newTagInputRef.value?.focus?.();
    newTagInputRef.value?.select?.();
  });
};

const submitCreateTag = async () => {
  if (!isCurrentDrawer(drawerEpoch) || !creatingTag.value || creatingTagLoading.value) return;
  const name = newTagName.value.trim();
  if (!name) {
    MessagePlugin.warning(t('knowledgeBase.tagNameRequired'));
    return;
  }
  const kbId = props.kbId;
  const context = contextEpoch;
  const drawer = drawerEpoch;
  const operation = ++createEpoch;
  const isCurrent = () => isCurrentDrawer(drawer) && createEpoch === operation;
  creatingTagLoading.value = true;
  try {
    await createKnowledgeBaseTag(kbId, { name });
    if (!isCurrentContext(context)) return;
    if (isCurrent()) {
      MessagePlugin.success(t('knowledgeBase.tagCreateSuccess'));
      cancelCreateTag();
    }
    refreshAfterTagChange(context);
  } catch (error: any) {
    if (isCurrent()) MessagePlugin.error(error?.message || t('common.operationFailed'));
  } finally {
    if (isCurrent()) creatingTagLoading.value = false;
  }
};

const startEditTag = (tag: TagRow) => {
  if (!isCurrentDrawer(drawerEpoch)) return;
  cancelCreateTag();
  cancelEditTag();
  editingTagId.value = tag.id;
  editingTagName.value = tag.name;
  const drawer = drawerEpoch;
  const form = editEpoch;
  nextTick(() => {
    if (!isCurrentDrawer(drawer) || editEpoch !== form || editingTagId.value !== tag.id) return;
    editingTagInputRefs.get(tag.id)?.focus?.();
    editingTagInputRefs.get(tag.id)?.select?.();
  });
};

const submitEditTag = async () => {
  if (!isCurrentDrawer(drawerEpoch) || !editingTagId.value || editingTagSubmitting.value) return;
  const name = editingTagName.value.trim();
  if (!name) {
    MessagePlugin.warning(t('knowledgeBase.tagNameRequired'));
    return;
  }
  const tagId = editingTagId.value;
  const current = tags.value.find((tag) => tag.id === tagId);
  if (current && name === current.name) {
    cancelEditTag();
    return;
  }
  const kbId = props.kbId;
  const context = contextEpoch;
  const drawer = drawerEpoch;
  const operation = ++editEpoch;
  const isCurrent = () => isCurrentDrawer(drawer) && editEpoch === operation;
  editingTagSubmitting.value = true;
  try {
    await updateKnowledgeBaseTag(kbId, tagId, { name });
    if (!isCurrentContext(context)) return;
    if (isCurrent()) {
      MessagePlugin.success(t('knowledgeBase.tagEditSuccess'));
      cancelEditTag();
    }
    refreshAfterTagChange(context);
  } catch (error: any) {
    if (isCurrent()) MessagePlugin.error(error?.message || t('common.operationFailed'));
  } finally {
    if (isCurrent()) editingTagSubmitting.value = false;
  }
};

const deleteTag = async (tag: TagRow) => {
  if (!isCurrentDrawer(drawerEpoch)) return;
  const kbId = props.kbId;
  const context = contextEpoch;
  const drawer = drawerEpoch;
  const tagId = tag.id;
  const seqId = tag.seq_id;
  cancelCreateTag();
  cancelEditTag();
  try {
    await deleteKnowledgeBaseTag(kbId, seqId, { force: true });
    if (!isCurrentContext(context)) return;
    if (isCurrentDrawer(drawer)) MessagePlugin.success(t('knowledgeBase.tagDeleteSuccess'));
    refreshAfterTagChange(context, { deletedTagId: tagId });
    if (!isCurrentContext(context)) return;
    // 删除会异步清理关联条目。关闭抽屉仍需补刷新，切知识库或卸载则取消。
    const timer = setTimeout(() => {
      deleteRefreshTimers.delete(timer);
      if (isCurrentContext(context)) emit('changed', { deletedTagId: tagId });
    }, TAG_DELETE_REFRESH_DELAY_MS);
    deleteRefreshTimers.add(timer);
  } catch (error: any) {
    if (isCurrentDrawer(drawer)) MessagePlugin.error(error?.message || t('common.operationFailed'));
  }
};

watch(
  () => [props.visible, props.kbId, props.isFaq] as const,
  ([open, kbId], previous) => {
    if (kbId !== previous?.[1]) {
      contextEpoch += 1;
      clearDeleteRefreshTimers();
    }
    drawerEpoch += 1;
    invalidateTagRequests();
    resetTagList();
    cancelCreateTag();
    cancelEditTag();
    resettingSearch = true;
    searchQuery.value = '';
    resettingSearch = false;
    if (open && kbId) {
      const drawer = drawerEpoch;
      const request = requestEpoch;
      // 同一轮 props 更新可能同时改变可见性和知识库，只为最后的会话发首屏请求。
      void nextTick(() => {
        if (isCurrentDrawer(drawer) && requestEpoch === request) void loadTags(true);
      });
    }
  },
  { immediate: true, flush: 'sync' },
);

watch(searchQuery, () => {
  if (resettingSearch) return;
  invalidateTagRequests();
  resetTagList();
  if (!isCurrentDrawer(drawerEpoch)) return;
  // 输入变化就作废旧结果，不能等防抖结束才隔离在途请求和旧分页。
  loading.value = true;
  const drawer = drawerEpoch;
  const request = requestEpoch;
  searchDebounce = setTimeout(() => {
    searchDebounce = null;
    if (isCurrentDrawer(drawer) && requestEpoch === request) void loadTags(true);
  }, TAG_SEARCH_DEBOUNCE_MS);
}, { flush: 'sync' });

onBeforeUnmount(() => {
  disposed = true;
  contextEpoch += 1;
  drawerEpoch += 1;
  invalidateTagRequests();
  clearDeleteRefreshTimers();
});
</script>

<style scoped lang="less">
.tag-manage-toolbar {
  display: flex;
  align-items: center;
  gap: 6px;
}

.tag-manage-search-wrap {
  flex: 1;
  min-width: 0;
}

.tag-manage-search {
  width: 100%;

  :deep(.t-input) {
    font-size: var(--app-text-md);
    background-color: var(--td-bg-color-secondarycontainer);
    border-color: transparent;
    border-radius: var(--app-radius-sm);
    box-shadow: none !important;

    &:hover,
    &:focus,
    &.t-is-focused {
      border-color: var(--td-component-border);
      background-color: var(--td-bg-color-container);
      box-shadow: none !important;
    }
  }

  :deep(.t-input__inner) {
    font-size: var(--app-text-md);
  }

  :deep(.t-input__prefix-icon) {
    margin-right: 0;
  }
}

.tag-manage-create-btn {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  padding: 0;
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-secondary);

  :deep(.t-icon) {
    font-size: var(--app-text-xl);
  }

  &:hover:not(:disabled) {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }

  &:disabled {
    opacity: 0.45;
  }
}

.tag-manage-loading {
  min-height: 80px;
}

.tag-manage-empty {
  padding: 24px 0;
}

.tag-tile-grid {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px;
}

.tag-tile {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  min-height: 44px;
  padding: 5px 6px 5px 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  box-sizing: border-box;
  transition: border-color var(--app-motion-fast) ease, background var(--app-motion-fast) ease;

  &:hover:not(.tag-tile--editing):not(.tag-tile--skeleton) {
    border-color: var(--td-component-border);
    background: color-mix(in srgb, var(--td-bg-color-secondarycontainer) 40%, var(--td-bg-color-container));
  }

  &--skeleton {
    padding: 0;
    border: none;
    background: transparent;
  }

  &--editing {
    border-color: var(--td-component-border);
    background: var(--td-bg-color-secondarycontainer);
    box-shadow: none;

    .tag-tile__actions {
      opacity: 1;
    }
  }
}

.tag-tile__main {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
}

.tag-tile__input {
  flex: 1;
  min-width: 0;

  :deep(.t-input) {
    background: transparent;
    border-color: transparent;
    box-shadow: none;
    padding-left: 0;
    padding-right: 0;
  }

  :deep(.t-input__wrap) {
    background: transparent;
    border-color: transparent;
    box-shadow: none;
  }

  :deep(.t-input__inner) {
    padding: 0;
    font-size: var(--app-text-md);
    font-weight: 500;
  }

  :deep(.t-input:hover),
  :deep(.t-input.t-is-focused),
  :deep(.t-input__wrap:hover),
  :deep(.t-input__wrap.t-is-focused) {
    border-color: transparent !important;
    box-shadow: none !important;
    outline: none;
  }

  :deep(.t-input.t-is-focused .t-input__suffix),
  :deep(.t-input.t-is-focused .t-input__prefix) {
    box-shadow: none;
  }
}

.tag-tile__badge {
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  border-radius: var(--app-radius-sm);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-placeholder);
}

.tag-tile__text {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
  flex: 1;
}

.tag-tile__name {
  font-size: var(--app-text-md);
  font-weight: 500;
  line-height: 1.3;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tag-tile__count {
  font-size: var(--app-text-xs);
  line-height: 1.3;
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tag-tile__actions {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  opacity: 0;
  transition: opacity var(--app-motion-instant) ease;
}

.tag-tile:hover .tag-tile__actions,
.tag-tile:focus-within .tag-tile__actions,
.tag-tile__actions:focus-within {
  opacity: 1;
}

@media (hover: none) {
  .tag-tile__actions {
    opacity: 1;
  }
}

.tag-tile__action-btn {
  padding: 0 2px;

  &--confirm {
    color: var(--td-text-color-secondary);

    &:hover,
    &:focus-visible {
      color: var(--td-text-color-primary);
      background: var(--td-bg-color-container);
    }
  }
}

.tag-load-more {
  display: flex;
  justify-content: center;
  padding-top: 8px;

  :deep(.t-button) {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
  }
}
</style>
