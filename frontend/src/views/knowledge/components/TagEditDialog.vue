<template>
  <t-dialog :visible="visible" :footer="false" width="400px" dialog-class-name="tag-edit-dialog"
    :close-on-overlay-click="false" destroy-on-close @close="handleClose">
    <template #header>
      <div class="tag-edit-heading">
        <div class="tag-edit-heading-row">
          <t-icon name="discount" size="16px" class="tag-edit-heading-icon" aria-hidden="true" />
          <span class="tag-edit-title">{{ $t('knowledgeBase.tagEditDialogHeading') }}</span>
        </div>
        <p class="tag-edit-document-name" :title="knowledgeName">{{ knowledgeName }}</p>
      </div>
    </template>

    <div class="tag-edit-body">
      <section class="setting-drawer__section">
        <div class="tag-edit-section-head">
          <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.tagEditSelectedSection') }}</h4>
          <t-button v-if="selectedSet.size > 0" variant="text" size="small" theme="default" @click="clearAll">
            {{ $t('knowledgeBase.tagClearAction') }}
          </t-button>
        </div>
        <div v-if="selectedTagsList.length > 0" class="tag-edit-chips">
          <button v-for="tag in selectedTagsList" :key="tag.id" type="button" class="tag-edit-chip is-selected"
            :title="tag.name" @click="toggleTag(tag.id)">
            {{ tag.name }}
          </button>
        </div>
        <p v-else class="tag-edit-section-empty">{{ $t('knowledgeBase.tagEditNoSelected') }}</p>
      </section>

      <section class="setting-drawer__section">
        <div class="tag-edit-section-head">
          <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.tagEditAvailableSection') }}</h4>
          <t-button
            v-if="canManage"
            variant="text"
            size="small"
            theme="default"
            class="tag-edit-manage-link"
            @click="handleOpenManage"
          >
            {{ $t('knowledgeBase.tagManageLink') }}
          </t-button>
        </div>
        <div class="tag-edit-search-bar">
          <t-input v-model="searchQuery" :placeholder="$t('knowledgeBase.tagEditSearch')" clearable size="small">
            <template #prefix-icon>
              <t-icon name="search" size="14px" />
            </template>
          </t-input>
        </div>
        <div v-if="availableTagsList.length > 0" class="tag-edit-chips">
          <button v-for="tag in availableTagsList" :key="tag.id" type="button" class="tag-edit-chip"
            :title="tag.knowledge_count !== undefined ? `${tag.name} (${tag.knowledge_count})` : tag.name"
            @click="toggleTag(tag.id)">
            {{ tag.name }}
          </button>
        </div>
        <div v-else class="tag-edit-section-empty tag-edit-section-empty--row">
          <span>{{ searchQuery.trim() ? $t('knowledgeBase.tagEmptyResult') : $t('knowledgeBase.noTags') }}</span>
          <t-button v-if="searchQuery.trim()" variant="text" theme="default" size="small" :loading="creatingTag"
            @click="handleCreateTag">
            {{ $t('knowledgeBase.tagCreateAction') }} “{{ searchQuery.trim() }}”
          </t-button>
        </div>
        <div class="tag-edit-create-row">
          <t-input v-model="newTagName" :placeholder="$t('knowledgeBase.tagNewPlaceholder')" size="small"
            :maxlength="40" :disabled="creatingTag" @enter="handleAddNewTag" />
        </div>
      </section>
    </div>

    <div class="tag-edit-footer">
      <span class="tag-edit-selected-count">
        {{ $t('knowledgeBase.tagSelectedCount', { count: selectedSet.size }) }}
      </span>
      <div class="tag-edit-footer-right">
        <t-button variant="outline" size="small" @click="handleClose">
          {{ $t('common.cancel') }}
        </t-button>
        <t-button theme="primary" size="small" :loading="saving" @click="handleConfirm">
          {{ $t('common.confirm') }}
        </t-button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { createKnowledgeBaseTag } from '@/api/knowledge-base';

import { useKnowledgeTagSelection, type KnowledgeTag as Tag } from '@/composables/useKnowledgeTagSelection';

const props = defineProps<{
  visible: boolean;
  knowledgeName: string;
  kbId: string;
  tagList: Tag[];
  selectedTags: Tag[];
  canManage?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void;
  (e: 'confirm', tagIds: string[]): void;
  (e: 'tag-created'): void;
  (e: 'open-manage'): void;
}>();

const { t } = useI18n();

const {
  searchQuery, newTagName, selectedSet, creatingTag, selectedTagsList, availableTagsList,
  toggleTag, clearAll, handleCreateTag, handleAddNewTag,
} = useKnowledgeTagSelection({
  visible: () => props.visible,
  kbId: () => props.kbId,
  tags: () => props.tagList,
  selectedIds: () => props.selectedTags.map(tag => tag.id),
  createTag: createKnowledgeBaseTag,
  onCreated: () => {
    emit('tag-created');
    MessagePlugin.success(t('knowledgeBase.tagCreateSuccess'));
  },
  onError: (error: any) => MessagePlugin.error(error?.message || t('common.operationFailed')),
});

const saving = ref(false);

async function handleConfirm() {
  saving.value = true;
  try {
    emit('confirm', Array.from(selectedSet.value));
    emit('update:visible', false);
  } finally {
    saving.value = false;
  }
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
.tag-edit-dialog {
  overflow: hidden;
  padding: 0;
  border-radius: var(--app-radius-xs);
}

.tag-edit-dialog .t-dialog__header {
  min-height: auto;
  padding: 20px 20px 0;
}

.tag-edit-dialog .t-dialog__body {
  padding: 0 20px 20px;
}

.tag-edit-dialog .t-dialog__close {
  top: 16px;
  right: 16px;
  width: 28px;
  height: 28px;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-secondary);
  transition: background 0.18s ease;
}

.tag-edit-dialog .t-dialog__close:hover {
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container-hover);
}

@media (max-width: 480px) {
  .tag-edit-dialog {
    width: calc(100vw - 24px) !important;
  }
}
</style>

<style scoped>
.tag-edit-heading {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  padding-right: 28px;
}

.tag-edit-heading-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.tag-edit-heading-icon {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.tag-edit-title {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-lg);
  font-weight: 600;
  line-height: 22px;
  letter-spacing: 0.2px;
}

.tag-edit-document-name {
  margin: 0;
  min-width: 0;
  overflow: hidden;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  font-weight: 400;
  line-height: 18px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tag-edit-body {
  display: flex;
  flex-direction: column;
  margin-top: 16px;
}

.tag-edit-body .setting-drawer__section {
  padding: 12px 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.tag-edit-body .setting-drawer__section:first-child {
  padding-top: 0;
}

.tag-edit-body .setting-drawer__section:last-child {
  border-bottom: none;
  padding-bottom: 0;
}

.tag-edit-body .setting-drawer__section-title {
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 4px;
  user-select: none;
  display: flex;
  align-items: center;
  gap: 8px;
}

.tag-edit-body .setting-drawer__section-title::before {
  content: '';
  width: 3px;
  height: 14px;
  background: var(--td-brand-color);
  border-radius: 2px;
  flex-shrink: 0;
}

.tag-edit-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.tag-edit-section-head .setting-drawer__section-title {
  margin-bottom: 0;
  flex: 1;
  min-width: 0;
}

.tag-edit-section-head :deep(.t-button) {
  height: auto;
  padding: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
  border: none !important;
  background: transparent !important;
  box-shadow: none !important;
  transition: color var(--app-motion-fast) ease;
}

.tag-edit-section-head :deep(.tag-edit-manage-link.t-button:hover),
.tag-edit-section-head :deep(.tag-edit-manage-link.t-button:focus-visible) {
  color: var(--td-brand-color) !important;
  background: transparent !important;
  border-color: transparent !important;
  text-decoration: none;
}

.tag-edit-search-bar {
  margin: 0;
}

.tag-edit-search-bar :deep(.t-input) {
  font-size: var(--app-text-sm);
  background-color: var(--td-bg-color-secondarycontainer);
  border-color: transparent;
  border-radius: var(--app-radius-xs);
  box-shadow: none !important;
}

.tag-edit-search-bar :deep(.t-input:hover),
.tag-edit-search-bar :deep(.t-input.t-is-focused) {
  border-color: var(--td-component-border);
  background-color: var(--td-bg-color-container);
  box-shadow: none !important;
}

.tag-edit-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  max-height: min(120px, 24vh);
  overflow-y: auto;
  scrollbar-width: thin;
}

.tag-edit-chips::-webkit-scrollbar {
  width: 4px;
}

.tag-edit-chips::-webkit-scrollbar-thumb {
  border-radius: 2px;
  background: var(--td-scrollbar-color);
}

.tag-edit-chip {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  max-width: 100%;
  height: 22px;
  padding: 0 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xs);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  font-size: var(--app-text-xs);
  line-height: 22px;
  text-align: center;
  cursor: pointer;
  outline: none;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: border-color var(--app-motion-fast) ease, background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;
  -webkit-font-smoothing: antialiased;
}

.tag-edit-chip:hover {
  border-color: var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}

.tag-edit-chip:focus-visible {
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-component-stroke) 60%, transparent);
}

.tag-edit-chip.is-selected {
  border-color: transparent;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  font-weight: 500;
}

.tag-edit-chip.is-selected:hover {
  background: color-mix(in srgb, var(--td-bg-color-secondarycontainer) 70%, var(--td-bg-color-container));
}

.tag-edit-section-empty {
  margin: 0;
  min-height: 22px;
  font-size: var(--app-text-sm);
  line-height: 22px;
  color: var(--td-text-color-placeholder);
}

.tag-edit-section-empty--row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.tag-edit-create-row {
  margin-top: 0;
}

.tag-edit-create-row :deep(.t-input) {
  font-size: var(--app-text-sm);
  background-color: transparent;
  border-style: dashed;
  border-color: var(--td-component-stroke);
  border-radius: var(--app-radius-xs);
  box-shadow: none !important;
}

.tag-edit-create-row :deep(.t-input:hover),
.tag-edit-create-row :deep(.t-input.t-is-focused) {
  border-color: var(--td-component-border);
  border-style: dashed;
  background-color: var(--td-bg-color-secondarycontainer);
  box-shadow: none !important;
}

.tag-edit-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--td-component-stroke);
}

.tag-edit-selected-count {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.tag-edit-footer-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
</style>
