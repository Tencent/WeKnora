<template>
  <div class="background-work-results">
    <div v-if="loading" class="results-loading">{{ $t('chat.backgroundWork.loading') }}</div>
    <ShellExecResult v-else-if="detail?.result" :data="detail.result" />
    <div v-else class="results-error">{{ $t('chat.backgroundWork.unavailable') }}</div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { getBackgroundWork, type BackgroundWorkDetail } from '@/api/chat/background-work'
import ShellExecResult from './tool-results/ShellExecResult.vue'

const props = defineProps<{ sessionId: string; workId: string }>()
const detail = ref<BackgroundWorkDetail | null>(null)
const loading = ref(false)

let request = 0
watch(() => [props.sessionId, props.workId] as const, async ([sessionId, id]) => {
  if (!sessionId || !id) return
  const current = ++request
  loading.value = true
  detail.value = null
  try {
    const next = await getBackgroundWork(sessionId, id)
    if (current !== request) return
    detail.value = next
  } catch {
    if (current !== request) return
    detail.value = null
  } finally {
    if (current === request) loading.value = false
  }
}, { immediate: true })
</script>

<style lang="less" scoped>
.background-work-results {
  min-width: 0;
}

.results-loading,
.results-error {
  font-size: var(--app-text-sm);
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
}
</style>
