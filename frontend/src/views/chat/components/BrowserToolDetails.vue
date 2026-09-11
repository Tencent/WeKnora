<template>
  <div class="browser-tool-details">
    <p :class="{ failed: event.success === false }">{{ browserToolSummary(t, event) }}</p>
    <details v-if="event.output || event.error"><summary>{{ t('localBrowser.technicalDetails') }}</summary><pre>{{ event.error || event.output }}</pre></details>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { browserToolSummary, type BrowserToolEvent } from '@/utils/browserToolDisplay'
defineProps<{ event: BrowserToolEvent }>()
const { t } = useI18n()
</script>
<style scoped>
.browser-tool-details { font-size: 13px; color: var(--td-text-color-secondary); }
p { margin: 4px 0 10px; line-height: 1.6; }.failed { color: var(--td-error-color); }
summary { cursor: pointer; width: fit-content; font-size: 12px; }
pre { margin: 8px 0 0; max-height: 240px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 12px; padding: 12px; border-radius: 6px; background: var(--td-bg-color-secondarycontainer); }
</style>
