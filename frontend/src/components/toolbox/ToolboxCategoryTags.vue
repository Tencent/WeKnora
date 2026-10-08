<template>
  <div class="toolbox-category-tags">
    <t-tag v-for="category in categories.slice(0, 2)" :key="category.id"
      class="toolbox-category-tags__preview" size="small" variant="light" :title="category.name">
      {{ category.name }}
    </t-tag>
    <t-popup v-if="categories.length > 2" v-model="expanded" trigger="click" placement="bottom-left"
      destroy-on-close>
      <button type="button" class="toolbox-category-tags__more" :aria-label="$t('toolboxCategories.all')"
        :aria-expanded="expanded" :title="$t('toolboxCategories.all')" @keydown.esc="expanded = false">…</button>
      <template #content>
        <div class="toolbox-category-tags__all" role="group" :aria-label="$t('toolboxCategories.all')"
          tabindex="0" @keydown.esc.stop="expanded = false">
          <t-tag v-for="category in categories" :key="category.id" size="small" variant="light">
            {{ category.name }}
          </t-tag>
        </div>
      </template>
    </t-popup>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { ToolboxCategory } from '@/api/toolbox-category'

defineProps<{ categories: ToolboxCategory[] }>()
const expanded = ref(false)
</script>

<style scoped>
.toolbox-category-tags {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.toolbox-category-tags__preview {
  min-width: 0;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.toolbox-category-tags :deep(.t-popup) { flex-shrink: 0; }
.toolbox-category-tags__more {
  border: 0;
  border-radius: 3px;
  padding: 0 7px;
  height: 22px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  font: inherit;
}
.toolbox-category-tags__more:hover,
.toolbox-category-tags__more:focus-visible { color: var(--td-brand-color); }
.toolbox-category-tags__all {
  display: flex;
  flex-wrap: wrap;
  align-content: flex-start;
  gap: 6px;
  width: min(360px, calc(100vw - 64px));
  max-height: min(320px, 60vh);
  overflow-y: auto;
}
.toolbox-category-tags__all :deep(.t-tag) {
  max-width: 100%;
  height: auto;
  white-space: normal;
  overflow-wrap: anywhere;
}
</style>
