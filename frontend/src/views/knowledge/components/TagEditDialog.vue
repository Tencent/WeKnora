<template>
  <t-dialog
    :visible="visible"
    :header="title"
    :footer="false"
    width="480px"
    :close-on-overlay-click="false"
    destroy-on-close
    @close="handleClose"
  >
    <!-- 搜索框 -->
    <div class="tag-edit-search">
      <t-input
        v-model="searchQuery"
        :placeholder="$t('knowledgeBase.tagEditSearch')"
        clearable
        size="medium"
      >
        <template #prefix-icon>
          <t-icon name="search" />
        </template>
      </t-input>
    </div>

    <!-- 标签列表 -->
    <div class="tag-edit-list">
      <template v-if="filteredTags.length > 0">
        <div
          v-for="tag in filteredTags"
          :key="tag.id"
          class="tag-edit-item"
          :class="{ 'is-selected': selectedSet.has(tag.id) }"
          @click="toggleTag(tag.id)"
        >
          <t-checkbox
            :checked="selectedSet.has(tag.id)"
            :value="tag.id"
            @change="toggleTag(tag.id)"
            @click.stop
          />
          <span class="tag-edit-name">{{ tag.name }}</span>
          <span v-if="tag.knowledge_count !== undefined" class="tag-edit-count">{{ tag.knowledge_count }}</span>
        </div>
      </template>
      <template v-else>
        <div class="tag-edit-empty">
          <template v-if="searchQuery.trim()">
            <p class="tag-edit-empty-text">{{ $t('knowledgeBase.tagEmptyResult') }}</p>
            <t-button
              variant="text"
              theme="primary"
              size="small"
              :loading="creatingTag"
              @click="handleCreateTag"
            >
              <t-icon name="add" />
              {{ $t('knowledgeBase.tagCreateAction') }} "{{ searchQuery.trim() }}"
            </t-button>
          </template>
          <template v-else>
            <p class="tag-edit-empty-text">{{ $t('knowledgeBase.noTags') }}</p>
          </template>
        </div>
      </template>
    </div>

    <!-- 新增标签栏 -->
    <div class="tag-edit-new-bar">
      <t-input
        v-model="newTagName"
        :placeholder="$t('knowledgeBase.tagNewPlaceholder')"
        size="small"
        clearable
        :disabled="creatingTag"
        @keydown.enter.prevent="handleAddNewTag"
      >
        <template #suffix>
          <t-button
            theme="primary"
            variant="text"
            size="small"
            :loading="creatingTag"
            :disabled="!newTagName.trim()"
            @click="handleAddNewTag"
          >
            <t-icon name="add" />
          </t-button>
        </template>
      </t-input>
    </div>

    <!-- 底部操作栏 -->
    <div class="tag-edit-footer">
      <div class="tag-edit-footer-left">
        <span class="tag-edit-selected-count">
          {{ $t('knowledgeBase.tagSelectedCount', { count: selectedSet.size }) }}
        </span>
        <t-button
          v-if="selectedSet.size > 0"
          variant="text"
          size="small"
          theme="default"
          @click="clearAll"
        >
          {{ $t('knowledgeBase.tagClearAction') }}
        </t-button>
      </div>
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
}>();

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void;
  (e: 'confirm', tagIds: string[]): void;
  (e: 'tag-created'): void;
}>();

const { t } = useI18n();

const searchQuery = ref('');
const selectedSet = ref<Set<string>>(new Set());
const creatingTag = ref(false);
const saving = ref(false);
const newTagName = ref('');

const title = computed(() => {
  return t('knowledgeBase.tagEditDialogTitle', { name: props.knowledgeName });
});

// 初始化已选标签
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

// 过滤标签列表，按照关键字匹配，已选的排前面
const filteredTags = computed(() => {
  const query = searchQuery.value.trim().toLowerCase();
  let list = props.tagList;
  if (query) {
    list = list.filter((tag) => (tag.name || '').toLowerCase().includes(query));
  }
  // 已选的排前面
  const selected: Tag[] = [];
  const unselected: Tag[] = [];
  for (const tag of list) {
    if (selectedSet.value.has(tag.id)) {
      selected.push(tag);
    } else {
      unselected.push(tag);
    }
  }
  return [...selected, ...unselected];
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
  // 检查是否已存在同名标签
  const exists = props.tagList.find((t) => t.name === name);
  if (exists) {
    // 已存在则直接选中
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
</script>

<style scoped>
.tag-edit-search {
  margin-bottom: 12px;
}

.tag-edit-list {
  max-height: 320px;
  overflow-y: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  padding: 4px 0;
  margin-bottom: 12px;
}

.tag-edit-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  cursor: pointer;
  transition: background 0.15s;
}

.tag-edit-item:hover {
  background: var(--td-bg-color-container-hover);
}

.tag-edit-item.is-selected {
  background: var(--td-brand-color-light);
}

.tag-edit-name {
  flex: 1;
  font-size: 14px;
  color: var(--td-text-color-primary);
}

.tag-edit-count {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.tag-edit-empty {
  padding: 24px 12px;
  text-align: center;
}

.tag-edit-empty-text {
  margin: 0 0 8px;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.tag-edit-new-bar {
  margin-bottom: 12px;
}

.tag-edit-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.tag-edit-footer-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.tag-edit-selected-count {
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.tag-edit-footer-right {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
}
</style>
