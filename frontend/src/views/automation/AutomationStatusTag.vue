<template>
  <t-tag v-if="status" size="small" variant="light" :theme="theme">{{ t(`automation.status.${status}`) }}</t-tag>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AutomationRunStatus } from '@/api/automation'

const props = defineProps<{ status: AutomationRunStatus | 'paused' | '' }>()
const { t } = useI18n()

const THEMES: Record<string, 'default' | 'primary' | 'success' | 'warning' | 'danger'> = {
  queued: 'default',
  running: 'primary',
  awaiting_approval: 'warning',
  succeeded: 'success',
  failed: 'danger',
  cancelled: 'default',
  skipped: 'default',
  needs_attention: 'warning',
  paused: 'default',
}

const theme = computed(() => THEMES[props.status] ?? 'default')
</script>
