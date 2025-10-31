<template>
  <t-card :title="title" class="virtual-kb-editor">
    <t-form ref="formRef" :data="form" :rules="rules" label-width="110px" layout="vertical">
      <t-form-item label="名称" name="name">
        <t-input v-model="form.name" placeholder="输入虚拟知识库名称" />
      </t-form-item>
      <t-form-item label="描述" name="description">
        <t-textarea v-model="form.description" :maxlength="200" placeholder="描述信息" />
      </t-form-item>

      <t-form-item label="过滤规则" name="filters">
        <t-space direction="vertical" size="large" class="filters-block">
          <t-card
            v-for="(filter, index) in form.filters"
            :key="index"
            theme="borderless"
            class="filter-card"
          >
            <t-row :gutter="16">
              <t-col :xs="12" :md="6">
                <t-form-item label="标签分类" :name="`filters.${index}.tag_category_id`" :rules="rules.filterCategory">
                  <t-select
                    v-model="filter.tag_category_id"
                    :options="categoryOptions"
                    placeholder="选择分类"
                    @change="(val) => handleCategoryChange(index, val as number)"
                  />
                </t-form-item>
              </t-col>
              <t-col :xs="12" :md="6">
                <t-form-item label="标签" :name="`filters.${index}.tag_ids`" :rules="rules.filterTags">
                  <t-select
                    v-model="filter.tag_ids"
                    multiple
                    filterable
                    :options="filter.tagOptions"
                    :disabled="!filter.tag_category_id"
                    placeholder="选择标签"
                  />
                </t-form-item>
              </t-col>
              <t-col :xs="12" :md="6">
                <t-form-item label="运算符" :name="`filters.${index}.operator`">
                  <t-radio-group v-model="filter.operator">
                    <t-radio value="AND">AND</t-radio>
                    <t-radio value="OR">OR</t-radio>
                    <t-radio value="NOT">NOT</t-radio>
                  </t-radio-group>
                </t-form-item>
              </t-col>
              <t-col :xs="12" :md="6">
                <t-form-item label="权重" :name="`filters.${index}.weight`">
                  <t-input-number v-model="filter.weight" :step="0.1" :min="0" theme="column" />
                </t-form-item>
              </t-col>
            </t-row>
            <t-button theme="danger" variant="outline" size="small" @click="removeFilter(index)">
              删除过滤器
            </t-button>
          </t-card>

          <t-button variant="outline" theme="primary" @click="addFilter">添加过滤器</t-button>
        </t-space>
      </t-form-item>

      <t-form-item label="配置(JSON)" name="config">
        <t-textarea
          v-model="configText"
          placeholder="例如：{\n  \"boost\": 1.5\n}"
          :autosize="{ minRows: 4, maxRows: 12 }"
        />
      </t-form-item>

      <div class="actions">
        <t-space>
          <t-button variant="outline" @click="handleReset">重置</t-button>
          <t-button theme="primary" @click="handleSubmit">{{ submitText }}</t-button>
        </t-space>
      </div>
    </t-form>
  </t-card>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import type { FormInstanceFunctions, FormRule } from "tdesign-vue-next";

import type { TagCategory, Tag } from "@api/tag";
import { fetchTagCategories, fetchTagsByCategory } from "@api/tag";
import type { VirtualKB, VirtualKBCreateRequest, VirtualKBFilter } from "@api/virtualKB";

interface FilterDraft extends VirtualKBFilter {
  tagOptions?: { label: string; value: number }[];
}

const props = defineProps<{ initialValue?: VirtualKB | null }>();
const emit = defineEmits<{ (e: "submit", payload: VirtualKBCreateRequest): void; (e: "reset"): void }>();

const formRef = ref<FormInstanceFunctions>();

const form = reactive<VirtualKBCreateRequest>({
  name: "",
  description: "",
  filters: [],
  config: {},
});

const filterDrafts = ref<FilterDraft[]>([]);
const categories = ref<TagCategory[]>([]);
const configText = ref("{}");

const rules = {
  name: [{ required: true, message: "请输入名称", type: "error" }],
  filterCategory: [{ required: true, message: "请选择标签分类", type: "error", typeName: "number" }],
  filterTags: [{ required: true, message: "请选择至少一个标签", type: "error" }],
} satisfies Record<string, FormRule[]>;

const title = computed(() => (props.initialValue ? "编辑虚拟知识库" : "新建虚拟知识库"));
const submitText = computed(() => (props.initialValue ? "更新" : "创建"));

const categoryOptions = computed(() => categories.value.map((item) => ({ label: item.name, value: item.id })));

const syncForm = () => {
  if (!props.initialValue) {
    form.name = "";
    form.description = "";
    form.filters = [];
    filterDrafts.value = [];
    configText.value = "{}";
    if (!filterDrafts.value.length) {
      addFilter();
    }
    return;
  }
  form.name = props.initialValue.name;
  form.description = props.initialValue.description ?? "";
  form.filters = props.initialValue.filters.map((filter) => ({ ...filter }));
  filterDrafts.value = props.initialValue.filters.map((filter) => ({ ...filter }));
  configText.value = props.initialValue.config ? JSON.stringify(props.initialValue.config, null, 2) : "{}";
};

const loadCategories = async () => {
  categories.value = await fetchTagCategories();
};

const loadTagsForFilter = async (index: number) => {
  const filter = filterDrafts.value[index];
  if (!filter?.tag_category_id) return;
  const tags = await fetchTagsByCategory(filter.tag_category_id);
  const options = tags.map((tag: Tag) => ({ label: `${tag.name} (${tag.value})`, value: tag.id }));
  filterDrafts.value[index].tagOptions = options;
};

const handleCategoryChange = async (index: number, categoryID: number) => {
  filterDrafts.value[index].tag_ids = [];
  filterDrafts.value[index].tag_category_id = categoryID;
  await loadTagsForFilter(index);
};

const addFilter = () => {
  const draft: FilterDraft = {
    tag_category_id: 0,
    tag_ids: [],
    operator: "OR",
    weight: 1,
    tagOptions: [],
  };
  filterDrafts.value.push(draft);
};

const removeFilter = (index: number) => {
  filterDrafts.value.splice(index, 1);
};

const parseConfig = () => {
  const trimmed = configText.value?.trim();
  if (!trimmed) return undefined;
  try {
    return JSON.parse(trimmed);
  } catch (err) {
    throw new Error("配置 JSON 格式错误");
  }
};

const handleSubmit = async () => {
  if (!formRef.value) return;
  const validation = await formRef.value.validate();
  if (validation !== true) return;

  let config: Record<string, unknown> | undefined;
  try {
    config = parseConfig();
  } catch (err) {
    // eslint-disable-next-line no-alert
    alert((err as Error).message);
    return;
  }

  const payload: VirtualKBCreateRequest = {
    name: form.name,
    description: form.description ?? "",
    filters: filterDrafts.value
      .filter((filter) => filter.tag_category_id && filter.tag_ids.length > 0)
      .map(({ tagOptions, ...filter }) => ({ ...filter })),
    config,
  };

  emit("submit", payload);
};

const handleReset = () => {
  syncForm();
  emit("reset");
};

watch(
  () => props.initialValue,
  () => {
    syncForm();
    filterDrafts.value.forEach((_, index) => {
      loadTagsForFilter(index);
    });
  },
  { immediate: true }
);

watch(
  filterDrafts,
  (drafts) => {
    form.filters = drafts.map(({ tagOptions, ...filter }) => ({ ...filter }));
  },
  { deep: true }
);

onMounted(() => {
  loadCategories();
  if (!props.initialValue && !filterDrafts.value.length) {
    addFilter();
  }
});
</script>

<style scoped>
.virtual-kb-editor {
  min-height: 420px;
}

.filters-block {
  width: 100%;
}

.filter-card {
  border: 1px solid var(--td-component-border);
  border-radius: 8px;
  padding: 16px;
}

.actions {
  display: flex;
  justify-content: flex-end;
}
</style>
