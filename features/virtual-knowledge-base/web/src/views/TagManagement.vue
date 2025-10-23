<template>
  <div class="tag-management">
    <t-row :gutter="24">
      <t-col :xs="12" :md="5" :lg="4">
        <TagCategoryManager
          :selected-category-id="selectedCategory?.id ?? null"
          @select="handleSelectCategory"
          @changed="handleCategoriesChanged"
        />
      </t-col>
      <t-col :xs="12" :md="7" :lg="8">
        <TagEditor :category="selectedCategory" @changed="handleTagsChanged" />
        <t-card title="文档标签" class="mt-24">
          <DocumentTagging :category="selectedCategory" />
        </t-card>
      </t-col>
    </t-row>
  </div>
</template>

<script setup lang="ts">
import { ref } from "vue";
import TagCategoryManager from "@components/tag/TagCategoryManager.vue";
import TagEditor from "@components/tag/TagEditor.vue";
import DocumentTagging from "@components/tag/DocumentTagging.vue";
import type { TagCategory, Tag } from "@api/tag";

const selectedCategory = ref<TagCategory | null>(null);
const tags = ref<Tag[]>([]);
const categories = ref<TagCategory[]>([]);

const handleSelectCategory = (category: TagCategory | null) => {
  selectedCategory.value = category;
};

const handleTagsChanged = (newTags: Tag[]) => {
  tags.value = newTags;
};

const handleCategoriesChanged = (newCategories: TagCategory[]) => {
  categories.value = newCategories;
  if (selectedCategory.value) {
    const match = newCategories.find((item) => item.id === selectedCategory.value?.id);
    selectedCategory.value = match ?? null;
  }
};
</script>

<style scoped>
.tag-management {
  padding: 24px;
}

.mt-24 {
  margin-top: 24px;
}
</style>
