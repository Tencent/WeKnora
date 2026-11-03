<template>
  <div class="host-review" :class="stateClass">
    <t-icon class="host-review__icon" :name="icon" />
    <div class="host-review__body">
      <div class="host-review__line">{{ $t(labelKey, { reason: resolveReason || '' }) }}</div>
      <code v-if="target" class="host-review__target">{{ target }}</code>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { hostReviewLabelKey, type HostApprovalPayload } from './hostApproval'

const props = defineProps<{
  host: HostApprovalPayload
  resolved?: boolean
  approved?: boolean
  resolveReason?: string
}>()

const labelKey = computed(() => hostReviewLabelKey({ resolved: props.resolved, approved: props.approved }))
const target = computed(() => props.host.file_path || props.host.command || props.host.grant_path || '')
const icon = computed(() => {
  if (!props.resolved) return 'loading'
  return props.approved ? 'check-circle' : 'close-circle'
})
const stateClass = computed(() => {
  if (!props.resolved) return 'is-pending'
  return props.approved ? 'is-approved' : 'is-denied'
})
</script>

<style scoped lang="less">
.host-review {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 8px 10px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);

  &.is-approved .host-review__icon {
    color: var(--td-success-color);
  }

  &.is-denied .host-review__icon {
    color: var(--td-error-color);
  }
}

.host-review__body {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.host-review__target {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  word-break: break-all;
  color: var(--td-text-color-primary);
}
</style>
