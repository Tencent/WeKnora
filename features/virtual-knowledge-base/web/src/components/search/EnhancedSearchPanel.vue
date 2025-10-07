<template>
  <t-card title="增强搜索" class="enhanced-search-panel">
    <t-form :data="form" label-width="120px">
      <t-form-item label="虚拟知识库">
        <t-select
          v-model="form.virtual_kb_id"
          :options="virtualKBOptions"
          placeholder="可选"
          clearable
        />
      </t-form-item>
      <t-form-item label="搜索限制">
        <t-input-number v-model="form.limit" :min="1" :max="100" theme="column" />
      </t-form-item>

      <t-divider>自定义标签过滤</t-divider>
      <t-space direction="vertical" size="large">
        <t-card
          v-for="(filter, index) in form.tag_filters"
          :key="`filter-${index}`"
          theme="borderless"
          class="filter-card"
        >
          <t-row :gutter="16">
            <t-col :xs="12" :md="6">
              <t-select
                v-model="filter.tag_category_id"
                :options="categoryOptions"
                placeholder="选择标签分类"
                @change="(val) => handleCategoryChange(index, val as number)"
              />
            </t-col>
            <t-col :xs="12" :md="6">
              <t-select
                v-model="filter.tag_ids"
                multiple
                :disabled="!filter.tag_category_id"
                :options="filter.tagOptions"
                placeholder="选择标签"
              />
            </t-col>
            <t-col :xs="12" :md="6">
              <t-radio-group v-model="filter.operator">
                <t-radio value="AND">AND</t-radio>
                <t-radio value="OR">OR</t-radio>
                <t-radio value="NOT">NOT</t-radio>
              </t-radio-group>
            </t-col>
            <t-col :xs="12" :md="6">
              <t-input-number v-model="filter.weight" :step="0.1" :min="0" theme="column" />
            </t-col>
          </t-row>
          <t-button theme="danger" variant="outline" size="small" @click="removeFilter(index)">
            删除
          </t-button>
        </t-card>

        <t-button variant="outline" theme="primary" @click="addFilter">添加过滤器</t-button>
      </t-space>

      <t-form-item>
        <t-space>
          <t-button theme="primary" @click="handleSearch">执行搜索</t-button>
          <t-button variant="outline" @click="reset">重置</t-button>
        </t-space>
      </t-form-item>
    </t-form>
  </t-card>
</template>

<script setup lang="ts">
import { computed, reactive } from "vue";
import type { VirtualKB } from "@api/virtualKB";
import { fetchTagsByCategory, type TagCategory, fetchTagCategories } from "@api/tag";
import type { EnhancedSearchRequest } from "@api/search";

interface FilterDraft {
  tag_category_id: number;
  tag_ids: number[];
  operator: "AND" | "OR" | "NOT";
  weight: number;
  tagOptions?: { label: string; value: number }[];
}

const props = defineProps<{ virtualKBs: VirtualKB[] }>();
const emit = defineEmits<{ (e: "search", payload: EnhancedSearchRequest): void }>();

const categories = reactive<{ list: TagCategory[] }>({ list: [] });

const form = reactive<EnhancedSearchRequest & { tag_filters: FilterDraft[] }>({
  virtual_kb_id: undefined,
  limit: 20,
  tag_filters: [],
});

const virtualKBOptions = computed(() => props.virtualKBs.map((item) => ({ label: item.name, value: item.id })));
const categoryOptions = computed(() => categories.list.map((item) => ({ label: item.name, value: item.id })));

const loadCategories = async () => {
  categories.list = await fetchTagCategories();
};

const handleCategoryChange = async (index: number, categoryID: number) => {
  form.tag_filters[index].tag_ids = [];
  const tags = await fetchTagsByCategory(categoryID);
  form.tag_filters[index].tagOptions = tags.map((tag) => ({ label: `${tag.name} (${tag.value})`, value: tag.id }));
};

const addFilter = () => {
  form.tag_filters.push({ tag_category_id: 0, tag_ids: [], operator: "OR", weight: 1, tagOptions: [] });
};

const removeFilter = (index: number) => {
  form.tag_filters.splice(index, 1);
};

const handleSearch = () => {
  const payload: EnhancedSearchRequest = {
    virtual_kb_id: form.virtual_kb_id,
    limit: form.limit,
    tag_filters: form.tag_filters
      .filter((filter) => filter.tag_category_id && filter.tag_ids.length)
      .map(({ tagOptions, ...filter }) => ({ ...filter })),
  };
  emit("search", payload);
};

const reset = () => {
  form.virtual_kb_id = undefined;
  form.limit = 20;
  form.tag_filters = [];
};

loadCategories();
</script>

<style scoped>
.enhanced-search-panel {
  min-height: 420px;
}

.filter-card {
  border: 1px solid var(--td-component-border);
  border-radius: 8px;
  padding: 16px;
}
</style>
