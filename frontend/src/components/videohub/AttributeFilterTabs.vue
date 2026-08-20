<template>
  <nav class="videohub-filter-tabs" aria-label="知识图谱分类筛选">
    <button type="button" :class="{ 'is-active': modelValue === 'all' }" @click="emit('update:modelValue', 'all')">
      全部 <small>{{ total }}</small>
    </button>
    <button
      v-for="attribute in visibleAttributes"
      :key="attribute"
      type="button"
      :class="{ 'is-active': modelValue === attribute }"
      @click="emit('update:modelValue', attribute)"
    >
      {{ attribute }} <small>{{ counts[attribute] }}</small>
    </button>
  </nav>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import './filterTabs.css'

const props = defineProps<{ modelValue: string; attributes: string[]; counts: Record<string, number>; total: number }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const visibleAttributes = computed(() => props.attributes.filter(attribute => (props.counts[attribute] ?? 0) > 0))
</script>
