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
                  @keydown="(_v, ctx) => onEditKeydown(ctx, cancelCreateTag)"
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
                    @keydown="(_v, ctx) => onEditKeydown(ctx, cancelEditTag)"
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
                          ? $t('knowledgeBase.tagManageFaqCount', { count: tag.knowledge_count || 0 })
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
import { ref, watch, nextTick, computed, type ComponentPublicInstance } from 'vue';
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
};

type TagInputInstance = ComponentPublicInstance<{ focus: () => void; select: () => void }>;

const TAG_PAGE_SIZE = 50;

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

const onEditKeydown = (ctx: { e?: KeyboardEvent }, cancel: () => void) => {
  if (ctx?.e?.key === 'Escape') {
    ctx.e.stopPropagation();
    ctx.e.preventDefault();
    cancel();
  }
};

const resetLocalState = () => {
  cancelCreateTag();
  cancelEditTag();
  searchQuery.value = '';
};

const loadTags = async (reset = false) => {
  if (!props.kbId) {
    tags.value = [];
    total.value = 0;
    hasMore.value = false;
    page.value = 1;
    return;
  }
  if (reset) {
    page.value = 1;
    tags.value = [];
    total.value = 0;
    hasMore.value = false;
  } else if (loading.value || loadingMore.value) {
    return;
  }

  const currentPage = page.value || 1;
  loading.value = currentPage === 1;
  loadingMore.value = currentPage > 1;

  try {
    const res: any = await listKnowledgeTags(props.kbId, {
      page: currentPage,
      page_size: TAG_PAGE_SIZE,
      keyword: searchQuery.value || undefined,
    });
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
    console.error('Failed to load tags', error);
  } finally {
    loading.value = false;
    loadingMore.value = false;
  }
};

const startCreateTag = () => {
  if (!props.kbId || creatingTag.value) return;
  cancelEditTag();
  creatingTag.value = true;
  nextTick(() => {
    newTagInputRef.value?.focus?.();
    newTagInputRef.value?.select?.();
  });
};

const cancelCreateTag = () => {
  creatingTag.value = false;
  newTagName.value = '';
};

const submitCreateTag = async () => {
  if (!props.kbId) return;
  const name = newTagName.value.trim();
  if (!name) {
    MessagePlugin.warning(t('knowledgeBase.tagNameRequired'));
    return;
  }
  creatingTagLoading.value = true;
  try {
    await createKnowledgeBaseTag(props.kbId, { name });
    MessagePlugin.success(t('knowledgeBase.tagCreateSuccess'));
    cancelCreateTag();
    await loadTags(true);
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'));
  } finally {
    creatingTagLoading.value = false;
  }
};

const startEditTag = (tag: TagRow) => {
  cancelCreateTag();
  editingTagId.value = tag.id;
  editingTagName.value = tag.name;
  nextTick(() => {
    editingTagInputRefs.get(tag.id)?.focus?.();
    editingTagInputRefs.get(tag.id)?.select?.();
  });
};

const cancelEditTag = () => {
  editingTagId.value = null;
  editingTagName.value = '';
};

const submitEditTag = async () => {
  if (!props.kbId || !editingTagId.value) return;
  const name = editingTagName.value.trim();
  if (!name) {
    MessagePlugin.warning(t('knowledgeBase.tagNameRequired'));
    return;
  }
  const current = tags.value.find((tag) => tag.id === editingTagId.value);
  if (current && name === current.name) {
    cancelEditTag();
    return;
  }
  editingTagSubmitting.value = true;
  try {
    await updateKnowledgeBaseTag(props.kbId, editingTagId.value, { name });
    MessagePlugin.success(t('knowledgeBase.tagEditSuccess'));
    cancelEditTag();
    await loadTags(true);
    emit('changed');
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'));
  } finally {
    editingTagSubmitting.value = false;
  }
};

const deleteTag = async (tag: TagRow) => {
  if (!props.kbId) return;
  cancelCreateTag();
  cancelEditTag();
  try {
    await deleteKnowledgeBaseTag(props.kbId, tag.seq_id, { force: true });
    MessagePlugin.success(t('knowledgeBase.tagDeleteSuccess'));
    await loadTags(true);
    emit('changed', { deletedTagId: tag.id });
    void (async () => {
      await new Promise((resolve) => setTimeout(resolve, 800));
      emit('changed', { deletedTagId: tag.id });
    })();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'));
  }
};

watch(
  () => props.visible,
  (open) => {
    if (open && props.kbId) {
      void loadTags(true);
    } else if (!open) {
      resetLocalState();
    }
  },
);

watch(searchQuery, (newVal, oldVal) => {
  if (newVal === oldVal || !props.visible || !props.kbId) return;
  if (searchDebounce) clearTimeout(searchDebounce);
  searchDebounce = setTimeout(() => {
    void loadTags(true);
  }, 300);
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
    font-size: 13px;
    background-color: var(--td-bg-color-secondarycontainer);
    border-color: transparent;
    border-radius: 6px;
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
    font-size: 13px;
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
  border-radius: 6px;
  color: var(--td-text-color-secondary);

  :deep(.t-icon) {
    font-size: 16px;
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
  border-radius: 6px;
  background: var(--td-bg-color-container);
  box-sizing: border-box;
  transition: border-color 0.15s ease, background 0.15s ease;

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
    font-size: 13px;
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
  border-radius: 6px;
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
  font-size: 13px;
  font-weight: 500;
  line-height: 1.3;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tag-tile__count {
  font-size: 11px;
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
  transition: opacity 0.12s ease;
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
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }
}
</style>
