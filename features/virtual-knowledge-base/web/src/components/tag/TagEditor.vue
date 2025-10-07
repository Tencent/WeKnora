<template>
  <t-card title="标签列表" class="tag-editor">
    <template #actions>
      <t-button v-if="category" variant="outline" @click="startCreate">新建标签</t-button>
    </template>

    <LoadingState v-if="loading" label="加载标签中" />
    <ErrorState v-else-if="error" :message="error" :onRetry="loadTags" />
    <div v-else>
      <t-empty v-if="!category" description="请选择左侧分类查看标签" />
      <t-empty v-else-if="!tags.length" description="当前分类暂无标签" />
      <t-table v-else :data="tags" row-key="id" size="small">
        <t-table-column col-key="name" title="名称" />
        <t-table-column col-key="value" title="值" />
        <t-table-column col-key="weight" title="权重" />
        <t-table-column col-key="description" title="描述" />
        <t-table-column col-key="actions" title="操作">
          <template #cell="{ row }">
            <t-space>
              <t-button size="small" variant="outline" @click="startEdit(row)">编辑</t-button>
              <t-popconfirm content="确认删除该标签？" theme="danger" @confirm="handleDelete(row.id)">
                <t-button size="small" theme="danger" variant="outline">删除</t-button>
              </t-popconfirm>
            </t-space>
          </template>
        </t-table-column>
      </t-table>
    </div>

    <t-dialog v-model:visible="dialogVisible" :header="dialogTitle" width="520px" @confirm="handleSubmit">
      <t-form ref="formRef" :data="form" :rules="rules" label-width="90px">
        <t-form-item label="名称" name="name">
          <t-input v-model="form.name" placeholder="标签名称" />
        </t-form-item>
        <t-form-item label="值" name="value">
          <t-input v-model="form.value" placeholder="唯一标识值" />
        </t-form-item>
        <t-form-item label="权重" name="weight">
          <t-input-number v-model="form.weight" :min="0" :step="0.1" theme="column" />
        </t-form-item>
        <t-form-item label="描述" name="description">
          <t-textarea v-model="form.description" :maxlength="200" placeholder="标签描述" />
        </t-form-item>
      </t-form>
    </t-dialog>
  </t-card>
</template>

<script setup lang="ts">
import { computed, watch, ref, reactive } from "vue";
import type { FormInstanceFunctions, FormRule } from "tdesign-vue-next";

import LoadingState from "@components/common/LoadingState.vue";
import ErrorState from "@components/common/ErrorState.vue";
import type { Tag, TagCategory } from "@api/tag";
import { createTag, deleteTag, fetchTagsByCategory, updateTag } from "@api/tag";

const props = defineProps<{ category: TagCategory | null }>();
const emit = defineEmits<{ (e: "changed", tags: Tag[]): void }>();

const tags = ref<Tag[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const dialogVisible = ref(false);
const editing = ref<Tag | null>(null);
const form = reactive({ name: "", value: "", weight: 1, description: "" });
const formRef = ref<FormInstanceFunctions>();

const rules: Record<string, FormRule[]> = {
  name: [{ required: true, message: "请输入标签名称", type: "error" }],
  value: [{ required: true, message: "请输入标签值", type: "error" }],
};

const dialogTitle = computed(() => (editing.value ? "编辑标签" : "新建标签"));

const resetForm = () => {
  form.name = "";
  form.value = "";
  form.weight = 1;
  form.description = "";
  editing.value = null;
};

const loadTags = async () => {
  if (!props.category) {
    tags.value = [];
    return;
  }
  try {
    loading.value = true;
    const data = await fetchTagsByCategory(props.category.id);
    tags.value = data;
    emit("changed", data);
    error.value = null;
  } catch (err) {
    error.value = (err as Error).message ?? "加载标签失败";
  } finally {
    loading.value = false;
  }
};

const startCreate = () => {
  resetForm();
  dialogVisible.value = true;
};

const startEdit = (tag: Tag) => {
  editing.value = tag;
  form.name = tag.name;
  form.value = tag.value;
  form.weight = tag.weight;
  form.description = tag.description ?? "";
  dialogVisible.value = true;
};

const handleSubmit = async () => {
  if (!props.category || !formRef.value) return;
  const result = await formRef.value.validate();
  if (result !== true) return;

  try {
    if (editing.value) {
      const updated = await updateTag(editing.value.id, {
        name: form.name,
        value: form.value,
        weight: form.weight,
        description: form.description,
        category_id: props.category.id,
      });
      tags.value = tags.value.map((item) => (item.id === updated.id ? updated : item));
    } else {
      const created = await createTag({
        name: form.name,
        value: form.value,
        weight: form.weight,
        description: form.description,
        category_id: props.category.id,
      });
      tags.value = [...tags.value, created];
    }
    emit("changed", tags.value);
    dialogVisible.value = false;
    resetForm();
  } catch (err) {
    error.value = (err as Error).message ?? "保存标签失败";
  }
};

const handleDelete = async (id: number) => {
  try {
    await deleteTag(id);
    tags.value = tags.value.filter((item) => item.id !== id);
    emit("changed", tags.value);
  } catch (err) {
    error.value = (err as Error).message ?? "删除标签失败";
  }
};

watch(
  () => props.category?.id,
  () => {
    resetForm();
    loadTags();
  },
  { immediate: true }
);
</script>

<style scoped>
.tag-editor {
  min-height: 420px;
}
</style>
