<template>
  <button class="pd-preview" type="button" @click="$emit('open', item)">
    <span class="pd-preview__title">{{ item.title }}</span>
    <ProgressValue :progress="item.progress" :fallback="item.phase || ''" />
    <span class="pd-preview__time">{{ formatElapsed(item.elapsed_ms) }}</span>
  </button>
</template>

<script setup lang="ts">
import type { ProcessingStageItem } from '@/types/processingDashboard'
import { formatElapsed } from '../format'
import ProgressValue from './ProgressValue.vue'

defineProps<{ item: ProcessingStageItem }>()
defineEmits<{ open: [item: ProcessingStageItem] }>()
</script>

<style scoped>
.pd-preview {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  gap: 12px;
  align-items: center;
  width: 100%;
  border: 0;
  background: transparent;
  padding: 4px 0;
  color: #23313a;
  cursor: pointer;
  text-align: left;
}

.pd-preview:hover .pd-preview__title {
  color: #009966;
}

.pd-preview__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pd-preview__time {
  color: #6b7a86;
  font-variant-numeric: tabular-nums;
  min-width: 56px;
  text-align: right;
}

@media (max-width: 560px) {
  .pd-preview {
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 6px 10px;
  }

  .pd-preview__time {
    grid-column: 1 / -1;
    min-width: 0;
    text-align: left;
  }
}
</style>
