<template>
  <button class="child-task-row" :class="{ 'is-running': task.status === 'running' }" type="button" :disabled="!task.sessionId || disabled" @click="$emit('open', task)">
    <span class="child-task-icon" :style="iconStyle" aria-hidden="true" />
    <span class="child-task-name" :class="{ 'is-running': task.status === 'running' }">{{ task.displayName || t('agentStream.collaboration.unnamed') }}</span>
    <span class="child-task-activity" :class="task.status">{{ activity }}</span>
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
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  min-height: 24px;
  padding: 0;
  border: 0;
  background: transparent;
  text-align: left;
  font: inherit;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  &:hover:not(:disabled) {
    .child-task-name { color: var(--td-text-color-primary); }
    .child-task-chevron { opacity: 1; transform: translateX(1px); }
  }
  &:disabled { cursor: default; }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; border-radius: var(--app-radius-sm, 4px); }
}
.child-task-icon {
  width: 18px;
  height: 18px;
  flex-shrink: 0;
  background: var(--agent-step-icon-color, var(--td-text-color-secondary));
  mask-size: contain;
  mask-repeat: no-repeat;
  mask-position: center;
}
.child-task-row.is-running {
  .child-task-icon { animation: child-task-breathe 2s ease-in-out infinite; }
  &::before {
    content: '';
    position: absolute;
    inset: -2px -6px;
    z-index: -1;
    border-radius: var(--app-radius-sm, 6px);
    pointer-events: none;
    background: linear-gradient(100deg,
      transparent 30%,
      color-mix(in srgb, var(--td-text-color-primary) 5%, transparent) 50%,
      transparent 70%) 0 0 / 250% 100%;
    animation: child-task-sweep 2.8s ease-in-out infinite;
  }
}
.child-task-name {
  flex: 0 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--agent-step-text-size, 14px);
  line-height: 1.55;
  transition: color var(--app-motion-fast);
}
.child-task-name.is-running {
  background: linear-gradient(90deg,
    var(--td-text-color-secondary) 30%,
    var(--td-text-color-primary) 50%,
    var(--td-text-color-secondary) 70%) 0 0 / 300% 100%;
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  animation: child-task-shimmer 2.4s linear infinite;
}
.child-task-activity {
  flex-shrink: 0;
  max-width: 50%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-placeholder);
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.55;
}
.child-task-activity.waiting, .child-task-activity.needs_attention { color: var(--td-warning-color); }
.child-task-activity.failed { color: var(--td-error-color); }
.child-task-chevron {
  flex-shrink: 0;
  width: 14px;
  margin-left: -6px;
  color: var(--td-text-color-placeholder);
  opacity: 0;
  transition: opacity var(--app-motion-fast), transform var(--app-motion-fast);
}
.child-task-row:disabled .child-task-chevron { display: none; }
@keyframes child-task-shimmer {
  from { background-position: 100% 0; }
  to { background-position: 0 0; }
}
@keyframes child-task-sweep {
  from { background-position: 100% 0; }
  to { background-position: -50% 0; }
}
@keyframes child-task-breathe {
  50% { opacity: 0.5; }
}
@media (prefers-reduced-motion: reduce) {
  .child-task-name.is-running { animation: none; color: var(--td-text-color-secondary); background: none; }
  .child-task-row.is-running .child-task-icon { animation: none; }
  .child-task-row.is-running::before { display: none; }
  .child-task-chevron { transition: none; }
}
</style>
