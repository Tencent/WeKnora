<template>
  <span v-if="status && (status === 'succeeded' || iconOnly)" class="automation-status-icon" :class="`is-${status}`" role="img"
    :aria-label="t(`automation.status.${status}`)" :title="t(`automation.status.${status}`)">
    <t-icon :name="ICONS[status] || 'time'" size="16px" />
  </span>
  <t-tag v-else-if="status" size="small" variant="light" :theme="theme">{{ t(`automation.status.${status}`) }}</t-tag>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AutomationRunStatus } from '@/api/automation'

const props = defineProps<{ status: AutomationRunStatus | 'paused' | ''; iconOnly?: boolean }>()
const { t } = useI18n()

const ICONS: Record<string, string> = {
  succeeded: 'check-circle', failed: 'error-circle', running: 'loading', queued: 'time',
  awaiting_approval: 'secured', needs_attention: 'error-circle', cancelled: 'close-circle',
  skipped: 'jump', paused: 'pause-circle',
}

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

<style scoped lang="less">
.automation-status-icon {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  color: var(--td-text-color-secondary);

  &.is-succeeded { color: var(--td-success-color); }
  &.is-running { color: var(--td-brand-color); }
  &.is-failed { color: var(--td-error-color); }
  &.is-awaiting_approval, &.is-needs_attention { color: var(--td-warning-color); }
}
</style>
