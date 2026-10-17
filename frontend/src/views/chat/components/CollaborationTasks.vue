<template>
  <button class="child-task-row" :class="{ 'is-running': task.status === 'running' }" type="button" :disabled="!task.sessionId || disabled" @click="$emit('open', task)">
    <span class="child-task-icon" :style="iconStyle" aria-hidden="true" />
    <span class="child-task-body">
      <span class="child-task-name">{{ task.displayName || t('agentStream.collaboration.unnamed') }}</span>
      <span class="child-task-activity" :class="task.status">{{ activity }}</span>
    </span>
    <ChevronRightIcon class="child-task-chevron" aria-hidden="true" />
  </button>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronRightIcon } from 'tdesign-icons-vue-next'
import agentIcon from '@/assets/img/agent.svg'
import type { CollaborationTaskView } from '@/utils/collaborationDisplay'
const iconStyle = { maskImage: `url("${agentIcon}")`, WebkitMaskImage: `url("${agentIcon}")` }
const props = defineProps<{ task: CollaborationTaskView; disabled?: boolean; toolLabel: (name: string) => string }>()
defineEmits<{ open: [task: CollaborationTaskView] }>()
const { t } = useI18n()
const activity = computed(() => {
  if (props.task.status === 'running' && props.task.progress?.tool_name) {
    const phase = props.task.progress.phase
    const key = ['tool_running', 'tool_succeeded', 'tool_failed'].includes(phase || '') ? phase : 'tool_running'
    return `${t(`agentStream.collaboration.phase.${key}`)} · ${props.toolLabel(props.task.progress.tool_name)}`
  }
  return t(`agentStream.collaboration.status.${props.task.status}`)
})
</script>
<style scoped lang="less">
.child-task-row {
  position: relative;
  isolation: isolate;
  overflow: hidden;
  display: flex;
  align-items: flex-start;
  gap: 8px;
  width: 100%;
  max-width: 480px;
  padding: 8px 10px;
  border: 1px solid color-mix(in srgb, var(--td-text-color-primary) 8%, transparent);
  border-radius: var(--app-radius-md, 8px);
  background: transparent;
  text-align: left;
  font: inherit;
  color: var(--td-text-color-primary);
  cursor: pointer;
  transition: background var(--app-motion-fast), border-color var(--app-motion-fast);
  &:hover:not(:disabled) {
    background: color-mix(in srgb, var(--td-text-color-primary) 3%, transparent);
    border-color: color-mix(in srgb, var(--td-text-color-primary) 14%, transparent);
    .child-task-chevron { color: var(--td-text-color-secondary); transform: translateX(1px); }
  }
  &:active:not(:disabled) { background: color-mix(in srgb, var(--td-text-color-primary) 5%, transparent); }
}
.child-task-row:disabled { cursor: default; }
.child-task-row:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 3px; }
.child-task-body { display: flex; flex: 1; min-width: 0; flex-direction: column; gap: 2px; }
.child-task-name { font-size: var(--agent-step-summary-size, 13px); line-height: 1.55; overflow-wrap: anywhere; }
.child-task-activity { width: fit-content; max-width: 100%; color: var(--td-text-color-placeholder); font-size: var(--app-text-xs, 12px); line-height: 1.5; }
.child-task-chevron { transition: color var(--app-motion-fast), transform var(--app-motion-fast); flex-shrink: 0; margin-top: 4px; color: var(--td-text-color-placeholder); width: 14px; }
.child-task-icon {
  width: 18px;
  height: 18px;
  margin-top: 1px;
  flex-shrink: 0;
  background: var(--td-text-color-secondary);
  mask-size: contain;
  mask-repeat: no-repeat;
  mask-position: center;
}
.child-task-row.is-running {
  background-color: color-mix(in srgb, var(--td-text-color-primary) 2%, transparent);
  &::before {
    content: '';
    position: absolute;
    inset: 0;
    z-index: -1;
    border-radius: inherit;
    pointer-events: none;
    background: linear-gradient(105deg,
      transparent 20%,
      color-mix(in srgb, var(--td-text-color-primary) 3%, transparent) 40%,
      color-mix(in srgb, var(--td-text-color-primary) 8%, transparent) 50%,
      color-mix(in srgb, var(--td-text-color-primary) 3%, transparent) 60%,
      transparent 80%);
    animation: child-card-sweep 2.6s ease-in-out infinite;
  }
}
.child-task-activity.running { color: var(--td-text-color-secondary); }
@keyframes child-card-sweep {
  0% { transform: translateX(-100%); }
  85%, 100% { transform: translateX(100%); }
}
.child-task-activity.waiting { color: var(--td-warning-color); }
@media (prefers-reduced-motion: reduce) {
  .child-task-row, .child-task-chevron { transition: none; }
  .child-task-row:hover:not(:disabled) .child-task-chevron { transform: none; }
  .child-task-row.is-running::before { display: none; animation: none; }
}
</style>
