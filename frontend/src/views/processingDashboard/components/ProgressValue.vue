<template>
  <span class="pd-progress">{{ display }}</span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ProcessingStageProgress } from '@/types/processingDashboard'
import { formatProgress } from '../format'

const props = defineProps<{
  progress?: ProcessingStageProgress | null
  fallback?: string
}>()

const display = computed(() => formatProgress(props.progress, props.fallback || ''))
</script>

<style scoped>
.pd-progress {
  color: #2b3a42;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
</style>
