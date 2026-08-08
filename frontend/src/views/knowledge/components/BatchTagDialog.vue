<template>
  <t-dialog :visible="visible" :footer="false" width="420px" dialog-class-name="batch-tag-dialog"
    :close-on-overlay-click="false" destroy-on-close @close="handleClose">
    <template #header>
      <div class="batch-tag-heading">
        <div class="batch-tag-heading-row">
          <t-icon name="discount" size="16px" class="batch-tag-heading-icon" aria-hidden="true" />
          <span class="batch-tag-title">{{ $t('knowledgeBase.batchTagDialogHeading') }}</span>
        </div>
        <p class="batch-tag-subtitle">{{ $t('knowledgeBase.batchTagSubtitle', { count }) }}</p>
      </div>
    </template>

    <div class="batch-tag-body">
      <section class="setting-drawer__section">
        <div class="batch-tag-section-head">
          <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.batchTagSelectedSection') }}</h4>
          <t-button v-if="selectedSet.size > 0" variant="text" size="small" theme="default" @click="clearAll">
            {{ $t('knowledgeBase.tagClearAction') }}
          </t-button>
        </div>
        <div v-if="selectedTagsList.length > 0" class="batch-tag-chips">
          <button v-for="tag in selectedTagsList" :key="tag.id" type="button" class="batch-tag-chip is-selected"
            :title="tag.name" @click="toggleTag(tag.id)">
            {{ tag.name }}
          </button>
        </div>
        <p v-else class="batch-tag-section-empty">{{ $t('knowledgeBase.batchTagNoSelected') }}</p>
      </section>

      <section class="setting-drawer__section">
        <div class="batch-tag-section-head">
          <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.batchTagAvailableSection') }}</h4>
          <t-button
            v-if="canManage"
            variant="text"
            size="small"
            theme="default"
            class="batch-tag-manage-link"
            @click="handleOpenManage"
          >
            {{ $t('knowledgeBase.tagManageLink') }}
          </t-button>
        </div>
        <div class="batch-tag-search-bar">
          <t-input v-model="searchQuery" :placeholder="$t('knowledgeBase.tagEditSearch')" clearable size="small">
            <template #prefix-icon>
              <t-icon name="search" size="14px" />
            </template>
          </t-input>
        </div>
        <div v-if="availableTagsList.length > 0" class="batch-tag-chips">
          <button v-for="tag in availableTagsList" :key="tag.id" type="button" class="batch-tag-chip"
            :title="tag.knowledge_count !== undefined ? `${tag.name} (${tag.knowledge_count})` : tag.name"
            @click="toggleTag(tag.id)">
            {{ tag.name }}
          </button>
        </div>
        <div v-else class="batch-tag-section-empty batch-tag-section-empty--row">
          <span>{{ searchQuery.trim() ? $t('knowledgeBase.tagEmptyResult') : $t('knowledgeBase.noTags') }}</span>
          <t-button v-if="searchQuery.trim()" variant="text" theme="default" size="small" :loading="creatingTag"
            @click="handleCreateTag">
            {{ $t('knowledgeBase.tagCreateAction') }} "{{ searchQuery.trim() }}"
          </t-button>
        </div>
        <div class="batch-tag-create-row">
          <t-input v-model="newTagName" :placeholder="$t('knowledgeBase.tagNewPlaceholder')" size="small"
            :maxlength="40" :disabled="creatingTag" @enter="handleAddNewTag" />
        </div>
      </section>
    </div>

    <div class="batch-tag-footer">
      <span class="batch-tag-selected-count">
        {{ $t('knowledgeBase.tagSelectedCount', { count: selectedSet.size }) }}
      </span>
      <div class="batch-tag-footer-right">
        <t-button variant="outline" size="small" :disabled="confirmLoading" @click="handleClose">
          {{ $t('common.cancel') }}
        </t-button>
        <t-button theme="primary" size="small" :loading="confirmLoading" @click="handleConfirm">
          {{ $t('common.confirm') }}
        </t-button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { createKnowledgeBaseTag } from '@/api/knowledge-base';

interface Tag {
  id: string;
  name: string;
  color?: string;
  knowledge_count?: number;
}

const props = defineProps<{
  visible: boolean;
  count: number;
  kbId: string;
  tagList: Tag[];
  preSelectedTagIds?: string[];
  canManage?: boolean;
  confirmLoading?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void;
  (e: 'confirm', tagIds: string[]): void;
  (e: 'tag-created'): void;
  (e: 'open-manage'): void;
}>();

const { t } = useI18n();

const searchQuery = ref('');
const selectedSet = ref<Set<string>>(new Set());
const creatingTag = ref(false);
const newTagName = ref('');

watch(
  () => props.visible,
  (val) => {
    if (val) {
      selectedSet.value = new Set(props.preSelectedTagIds ?? []);
      searchQuery.value = '';
      newTagName.value = '';
    }
  },
);

const tagMap = computed(() => new Map(props.tagList.map((tag) => [tag.id, tag])));

const selectedTagsList = computed(() => {
  return Array.from(selectedSet.value)
    .map((id) => tagMap.value.get(id))
    .filter((tag): tag is Tag => Boolean(tag));
});

const availableTagsList = computed(() => {
  const query = searchQuery.value.trim().toLowerCase();
  return props.tagList.filter((tag) => {
    if (selectedSet.value.has(tag.id)) return false;
    if (query && !(tag.name || '').toLowerCase().includes(query)) return false;
    return true;
  });
});

function toggleTag(tagId: string) {
  const next = new Set(selectedSet.value);
  if (next.has(tagId)) {
    next.delete(tagId);
  } else {
    next.add(tagId);
  }
  selectedSet.value = next;
}

function clearAll() {
  selectedSet.value = new Set();
}

async function handleCreateTag() {
  const name = searchQuery.value.trim();
  if (!name) return;
  creatingTag.value = true;
  try {
    const res: any = await createKnowledgeBaseTag(props.kbId, { name });
    const newTag = res?.data || res;
    const next = new Set(selectedSet.value);
    next.add(newTag.id);
    selectedSet.value = next;
    searchQuery.value = '';
    emit('tag-created');
    MessagePlugin.success(t('knowledgeBase.tagCreateSuccess'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'));
  } finally {
    creatingTag.value = false;
  }
}

async function handleAddNewTag() {
  const name = newTagName.value.trim();
  if (!name) return;
  const exists = props.tagList.find((t) => t.name === name);
  if (exists) {
    const next = new Set(selectedSet.value);
    next.add(exists.id);
    selectedSet.value = next;
    newTagName.value = '';
    return;
  }
  creatingTag.value = true;
  try {
    const res: any = await createKnowledgeBaseTag(props.kbId, { name });
    const newTag = res?.data || res;
    const next = new Set(selectedSet.value);
    next.add(newTag.id);
    selectedSet.value = next;
    newTagName.value = '';
    emit('tag-created');
    MessagePlugin.success(t('knowledgeBase.tagCreateSuccess'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'));
  } finally {
    creatingTag.value = false;
  }
}

function handleConfirm() {
  if (props.confirmLoading) return;
  emit('confirm', Array.from(selectedSet.value));
}

function handleClose() {
  emit('update:visible', false);
}

function handleOpenManage() {
  emit('update:visible', false);
  emit('open-manage');
}
</script>

<style>
.batch-tag-dialog {
  overflow: hidden;
  padding: 0;
  border-radius: 4px;
}

.batch-tag-dialog .t-dialog__header {
  min-height: auto;
  padding: 20px 20px 0;
}

.batch-tag-dialog .t-dialog__body {
  padding: 0 20px 20px;
}

.batch-tag-dialog .t-dialog__close {
  top: 16px;
  right: 16px;
  width: 28px;
  height: 28px;
  border-radius: 4px;
  color: var(--td-text-color-secondary);
  transition: background 0.18s ease;
}

.batch-tag-dialog .t-dialog__close:hover {
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container-hover);
}

@media (max-width: 480px) {
  .batch-tag-dialog {
    width: calc(100vw - 24px) !important;
  }
}
</style>

<style scoped>
.batch-tag-heading {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  padding-right: 28px;
}

.batch-tag-heading-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.batch-tag-heading-icon {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.batch-tag-title {
  color: var(--td-text-color-primary);
  font-size: 15px;
  font-weight: 600;
  line-height: 22px;
  letter-spacing: 0.2px;
}

.batch-tag-subtitle {
  margin: 0;
  min-width: 0;
  overflow: hidden;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-weight: 400;
  line-height: 18px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.batch-tag-body {
  display: flex;
  flex-direction: column;
  margin-top: 16px;
}

.batch-tag-body .setting-drawer__section {
  padding: 12px 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.batch-tag-body .setting-drawer__section:first-child {
  padding-top: 0;
}

.batch-tag-body .setting-drawer__section:last-child {
  border-bottom: none;
  padding-bottom: 0;
}

.batch-tag-body .setting-drawer__section-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 4px;
  user-select: none;
  display: flex;
  align-items: center;
  gap: 8px;
}

.batch-tag-body .setting-drawer__section-title::before {
  content: '';
  width: 3px;
  height: 14px;
  background: var(--td-brand-color);
  border-radius: 2px;
  flex-shrink: 0;
}

.batch-tag-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.batch-tag-section-head .setting-drawer__section-title {
  margin-bottom: 0;
  flex: 1;
  min-width: 0;
}

.batch-tag-section-head :deep(.t-button) {
  height: auto;
  padding: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
  border: none !important;
  background: transparent !important;
  box-shadow: none !important;
  transition: color 0.15s ease;
}

.batch-tag-section-head :deep(.batch-tag-manage-link.t-button:hover),
.batch-tag-section-head :deep(.batch-tag-manage-link.t-button:focus-visible) {
  color: var(--td-brand-color) !important;
  background: transparent !important;
  border-color: transparent !important;
  text-decoration: none;
}

.batch-tag-search-bar {
  margin: 0;
}

.batch-tag-search-bar :deep(.t-input) {
  font-size: 12px;
  background-color: var(--td-bg-color-secondarycontainer);
  border-color: transparent;
  border-radius: 4px;
  box-shadow: none !important;
}

.batch-tag-search-bar :deep(.t-input:hover),
.batch-tag-search-bar :deep(.t-input.t-is-focused) {
  border-color: var(--td-component-border);
  background-color: var(--td-bg-color-container);
  box-shadow: none !important;
}

.batch-tag-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  max-height: min(120px, 24vh);
  overflow-y: auto;
  scrollbar-width: thin;
}

.batch-tag-chips::-webkit-scrollbar {
  width: 4px;
}

.batch-tag-chips::-webkit-scrollbar-thumb {
  border-radius: 2px;
  background: var(--td-scrollbar-color);
}

.batch-tag-chip {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  max-width: 100%;
  height: 22px;
  padding: 0 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: 11px;
  line-height: 22px;
  text-align: center;
  cursor: pointer;
  outline: none;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: border-color 0.15s ease, background 0.15s ease, color 0.15s ease;
  -webkit-font-smoothing: antialiased;
}

.batch-tag-chip:hover {
  border-color: var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}

.batch-tag-chip:focus-visible {
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-component-stroke) 60%, transparent);
}

.batch-tag-chip.is-selected {
  border-color: transparent;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  font-weight: 500;
}

.batch-tag-chip.is-selected:hover {
  background: color-mix(in srgb, var(--td-bg-color-secondarycontainer) 70%, var(--td-bg-color-container));
}

.batch-tag-section-empty {
  margin: 0;
  min-height: 22px;
  font-size: 12px;
  line-height: 22px;
  color: var(--td-text-color-placeholder);
}

.batch-tag-section-empty--row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.batch-tag-create-row {
  margin-top: 0;
}

.batch-tag-create-row :deep(.t-input) {
  font-size: 12px;
  background-color: transparent;
  border-style: dashed;
  border-color: var(--td-component-stroke);
  border-radius: 4px;
  box-shadow: none !important;
}

.batch-tag-create-row :deep(.t-input:hover),
.batch-tag-create-row :deep(.t-input.t-is-focused) {
  border-color: var(--td-component-border);
  border-style: dashed;
  background-color: var(--td-bg-color-secondarycontainer);
  box-shadow: none !important;
}

.batch-tag-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--td-component-stroke);
}

.batch-tag-selected-count {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.batch-tag-footer-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
</style>
