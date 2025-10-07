<template>
  <t-card title="文档标签管理" class="document-tagging">
    <div class="document-input">
      <t-input v-model="documentID" placeholder="输入文档 ID" clearable />
      <t-button theme="primary" @click="loadDocumentTags">载入标签</t-button>
    </div>

    <t-divider />

    <template v-if="!category">
      <t-empty description="请选择标签分类后再进行文档标签操作" />
    </template>
    <template v-else>
      <t-space direction="vertical" size="large" class="full-width">
        <div>
          <p class="section-title">可用标签</p>
          <LoadingState v-if="loadingAvailable" label="加载标签中" />
          <ErrorState
            v-else-if="errorAvailable"
            :message="errorAvailable"
            :onRetry="loadAvailableTags"
          />
          <t-tag-input
            v-else
            v-model="selectedTagIds"
            :value-display="renderAvailableTags"
            :options="availableTagOptions"
            placeholder="选择或搜索标签，回车确认"
            clearable
            filterable
            @change="handleAssign"
          />
        </div>

        <div>
          <p class="section-title">文档已有标签</p>
          <LoadingState v-if="loadingDocument" label="加载文档标签中" />
          <ErrorState
            v-else-if="errorDocument"
            :message="errorDocument"
            :onRetry="loadDocumentTags"
          />
          <t-space v-else-if="documentTags.length" size="small" wrap>
            <t-tag
              v-for="tag in documentTags"
              :key="tag.id"
              theme="primary"
              closable
              @close="handleRemove(tag.id)"
            >
              {{ tag.name }} ({{ tag.value }})
            </t-tag>
          </t-space>
          <t-empty v-else description="该文档暂无标签" />
        </div>
      </t-space>
    </template>
  </t-card>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import LoadingState from "@components/common/LoadingState.vue";
import ErrorState from "@components/common/ErrorState.vue";

import type { Tag, TagCategory } from "@api/tag";
import { fetchTagsByCategory } from "@api/tag";
import { assignTagToDocument, fetchDocumentTags, removeTagFromDocument } from "@api/documentTag";

type TagOption = { label: string; value: number };

const props = defineProps<{ category: TagCategory | null }>();

const documentID = ref("");
const availableTags = ref<Tag[]>([]);
const documentTags = ref<Tag[]>([]);

const loadingAvailable = ref(false);
const errorAvailable = ref<string | null>(null);
const loadingDocument = ref(false);
const errorDocument = ref<string | null>(null);

const selectedTagIds = ref<number[]>([]);

const availableTagOptions = computed<TagOption[]>(() =>
  availableTags.value.map((tag) => ({ label: `${tag.name} (${tag.value})`, value: tag.id }))
);

const renderAvailableTags = (value: number[]) =>
  value
    .map((id) => availableTags.value.find((tag) => tag.id === id))
    .filter(Boolean)
    .map((tag) => `${tag!.name} (${tag!.value})`)
    .join(", ");

const loadAvailableTags = async () => {
  if (!props.category) {
    availableTags.value = [];
    return;
  }
  try {
    loadingAvailable.value = true;
    const tags = await fetchTagsByCategory(props.category.id);
    availableTags.value = tags;
    selectedTagIds.value = documentTags.value.map((tag) => tag.id);
    errorAvailable.value = null;
  } catch (err) {
    errorAvailable.value = (err as Error).message ?? "加载可用标签失败";
  } finally {
    loadingAvailable.value = false;
  }
};

const loadDocumentTags = async () => {
  if (!documentID.value.trim()) {
    errorDocument.value = "请先输入文档 ID";
    return;
  }
  try {
    loadingDocument.value = true;
    const tags = await fetchDocumentTags(documentID.value.trim());
    documentTags.value = tags;
    selectedTagIds.value = tags.map((tag) => tag.id);
    errorDocument.value = null;
  } catch (err) {
    errorDocument.value = (err as Error).message ?? "加载文档标签失败";
  } finally {
    loadingDocument.value = false;
  }
};

const handleAssign = async (values: number[]) => {
  if (!documentID.value.trim()) {
    errorDocument.value = "请先输入文档 ID";
    selectedTagIds.value = documentTags.value.map((tag) => tag.id);
    return;
  }

  const newTagIds = values.filter((id) => !documentTags.value.some((tag) => tag.id === id));
  try {
    await Promise.all(
      newTagIds.map((id) =>
        assignTagToDocument(documentID.value.trim(), {
          tag_id: id,
        })
      )
    );
    await loadDocumentTags();
  } catch (err) {
    errorDocument.value = (err as Error).message ?? "添加标签失败";
  }
};

const handleRemove = async (tagID: number) => {
  if (!documentID.value.trim()) {
    errorDocument.value = "请先输入文档 ID";
    return;
  }
  try {
    await removeTagFromDocument(documentID.value.trim(), tagID);
    await loadDocumentTags();
  } catch (err) {
    errorDocument.value = (err as Error).message ?? "移除标签失败";
  }
};

watch(
  () => props.category?.id,
  () => {
    selectedTagIds.value = [];
    availableTags.value = [];
    loadAvailableTags();
  }
);
</script>

<style scoped>
.document-tagging {
  min-height: 400px;
}

.document-input {
  display: flex;
  gap: 12px;
  align-items: center;
}

.section-title {
  margin-bottom: 8px;
  font-weight: 600;
}

.full-width {
  width: 100%;
}
</style>
