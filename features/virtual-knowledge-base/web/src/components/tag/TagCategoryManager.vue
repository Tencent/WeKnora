<template>
  <div class="tag-category-manager">
    <t-card title="标签分类">
      <t-button block theme="primary" @click="startCreate">新建分类</t-button>
      <LoadingState v-if="loading" label="加载分类中" class="mt-16" />
      <ErrorState
        v-else-if="error"
        class="mt-16"
        :message="error"
        :onRetry="loadCategories"
      />
      <t-list v-else-if="categories.length" class="mt-16">
        <t-list-item
          v-for="category in categories"
          :key="category.id"
          :class="{ active: category.id === selectedCategoryId }"
          @click="emit('select', category)"
        >
          <div class="category-item">
            <div class="category-info">
              <span class="name">{{ category.name }}</span>
              <t-tag v-if="category.color" :color="category.color" size="small">{{ category.color }}</t-tag>
              <p v-if="category.description" class="description">{{ category.description }}</p>
            </div>
            <div class="actions" @click.stop>
              <t-button size="small" variant="outline" @click="startEdit(category)">编辑</t-button>
              <t-popconfirm content="确认删除该分类？" theme="danger" @confirm="handleDelete(category.id)">
                <t-button size="small" theme="danger" variant="outline">删除</t-button>
              </t-popconfirm>
            </div>
          </div>
        </t-list-item>
      </t-list>
      <t-empty v-else description="暂无分类，点击上方按钮创建" />
    </t-card>

    <t-dialog v-model:visible="dialogVisible" :header="dialogTitle" width="480px" @confirm="handleSubmit">
      <t-form ref="formRef" :data="form" :rules="rules" label-width="90px">
        <t-form-item label="名称" name="name">
          <t-input v-model="form.name" placeholder="输入分类名称" />
        </t-form-item>
        <t-form-item label="描述" name="description">
          <t-textarea v-model="form.description" :maxlength="200" placeholder="分类简介" />
        </t-form-item>
        <t-form-item label="颜色" name="color">
          <t-input v-model="form.color" placeholder="#1f7aec" />
        </t-form-item>
      </t-form>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import type { FormRule, FormInstanceFunctions } from "tdesign-vue-next";

import type { TagCategory } from "@api/tag";
import { createTagCategory, deleteTagCategory, fetchTagCategories, updateTagCategory } from "@api/tag";

const props = defineProps<{ selectedCategoryId?: number | null }>();
const emit = defineEmits<{ (e: "select", category: TagCategory | null): void; (e: "changed", categories: TagCategory[]): void }>();

const categories = ref<TagCategory[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const dialogVisible = ref(false);
const editing = ref<TagCategory | null>(null);
const form = reactive({ name: "", description: "", color: "" });
const formRef = ref<FormInstanceFunctions>();

const rules: Record<string, FormRule[]> = {
  name: [{ required: true, message: "请输入分类名称", type: "error" }],
};

const dialogTitle = computed(() => (editing.value ? "编辑标签分类" : "新建标签分类"));

const resetForm = () => {
  form.name = "";
  form.description = "";
  form.color = "";
  editing.value = null;
};

const startCreate = () => {
  resetForm();
  dialogVisible.value = true;
};

const startEdit = (category: TagCategory) => {
  editing.value = category;
  form.name = category.name;
  form.description = category.description ?? "";
  form.color = category.color ?? "";
  dialogVisible.value = true;
};

const loadCategories = async () => {
  try {
    loading.value = true;
    const data = await fetchTagCategories();
    categories.value = data;
    emit("changed", data);
    if (props.selectedCategoryId) {
      const match = data.find((item) => item.id === props.selectedCategoryId);
      emit("select", match ?? null);
    }
    error.value = null;
  } catch (err) {
    error.value = (err as Error).message ?? "加载分类失败";
  } finally {
    loading.value = false;
  }
};

const handleSubmit = async () => {
  if (!formRef.value) return;
  const result = await formRef.value.validate();
  if (result !== true) return;

  try {
    if (editing.value) {
      const updated = await updateTagCategory(editing.value.id, { ...form });
      categories.value = categories.value.map((item) => (item.id === updated.id ? updated : item));
    } else {
      const created = await createTagCategory({ ...form });
      categories.value = [...categories.value, created];
    }
    emit("changed", categories.value);
    dialogVisible.value = false;
    resetForm();
  } catch (err) {
    error.value = (err as Error).message ?? "保存分类失败";
  }
};

const handleDelete = async (id: number) => {
  try {
    await deleteTagCategory(id);
    categories.value = categories.value.filter((item) => item.id !== id);
    emit("changed", categories.value);
    if (props.selectedCategoryId === id) {
      emit("select", null);
    }
  } catch (err) {
    error.value = (err as Error).message ?? "删除分类失败";
  }
};

onMounted(() => {
  loadCategories();
});
</script>

<style scoped>
.tag-category-manager {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.mt-16 {
  margin-top: 16px;
}

.category-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}

.category-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.description {
  color: var(--td-text-color-secondary);
  margin: 0;
}

.actions {
  display: flex;
  gap: 8px;
}

.active {
  background: var(--td-bg-color-component-hover);
}
</style>
