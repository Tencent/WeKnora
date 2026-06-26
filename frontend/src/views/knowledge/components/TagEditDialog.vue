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

const searchQuery = ref('');
const selectedSet = ref<Set<string>>(new Set());
const creatingTag = ref(false);
const saving = ref(false);
const newTagName = ref('');

watch(
  () => props.visible,
  (val) => {
    if (val) {
      selectedSet.value = new Set(props.selectedTags.map((t) => t.id));
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
  border-radius: 4px;
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
  border-radius: 4px;
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
  font-size: 15px;
  font-weight: 600;
  line-height: 22px;
  letter-spacing: 0.2px;
}

.tag-edit-document-name {
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
  font-size: 13px;
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
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
  border: none !important;
  background: transparent !important;
  box-shadow: none !important;
  transition: color 0.15s ease;
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
  font-size: 12px;
  background-color: var(--td-bg-color-secondarycontainer);
  border-color: transparent;
  border-radius: 4px;
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
  font-size: 12px;
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
  font-size: 12px;
  background-color: transparent;
  border-style: dashed;
  border-color: var(--td-component-stroke);
  border-radius: 4px;
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
  font-size: 12px;
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
