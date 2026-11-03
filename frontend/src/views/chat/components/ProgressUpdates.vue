<template>
  <div class="progress-updates">
    <details v-if="earlier.length" class="progress-disclosure" @toggle="open = ($event.target as HTMLDetailsElement).open">
      <summary>
        <ChatTimelineIcon class="progress-icon" name="lightbulb" />
        <span class="progress-text" :class="{ 'is-clamped': !open }">{{ open ? earlier[0] : content }}</span>
        <span class="progress-count">{{ t('agentStream.collaboration.updates', { count: earlier.length + 1 }) }}</span>
        <ChatTimelineIcon width="14" height="14" class="progress-chevron" name="chevron-right" />
      </summary>
      <ol class="progress-history">
        <li v-for="(update, index) in later" :key="index">{{ update }}</li>
      </ol>
    </details>
    <div v-else class="progress-summary">
      <ChatTimelineIcon class="progress-icon" name="lightbulb" />
      <span class="progress-text">{{ content }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import ChatTimelineIcon from '@/components/ChatTimelineIcon.vue'
const props = defineProps<{ content: string; history?: string[] }>()
const { t } = useI18n()
const open = ref(false)
const earlier = computed(() => props.history || [])
// Open, the updates read in time order like the rest of the timeline: the
// summary takes the first one and the list runs down to the latest.
const later = computed(() => [...earlier.value.slice(1), props.content])
</script>

<style scoped lang="less">
.progress-updates {
  position: relative;
  color: var(--agent-step-color, var(--td-text-color-secondary));
  font: inherit;
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.55;
  overflow-wrap: anywhere;
}
.progress-summary, summary {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  min-height: 24px;
  padding: 0;
  list-style: none;
  &::-webkit-details-marker { display: none; }
}
.progress-text {
  flex: 1;
  min-width: 0;
  &.is-clamped {
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
}
summary {
  cursor: pointer;
  border-radius: var(--app-radius-xs);
  &:hover .progress-count, &:hover .progress-chevron { color: var(--agent-step-color, var(--td-text-color-secondary)); }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 3px; }
}
.progress-count {
  flex-shrink: 0;
  color: var(--agent-step-color, var(--td-text-color-secondary));
  font-size: var(--app-text-xs, 12px);
  line-height: 20px;
  white-space: nowrap;
  transition: color var(--app-motion-fast);
}
.progress-chevron {
  flex-shrink: 0;
  margin-top: 3px;
  color: var(--agent-step-color, var(--td-text-color-secondary));
  transition: color var(--app-motion-fast), transform var(--app-motion-fast);
}
details[open] .progress-chevron { transform: rotate(90deg); }
.progress-icon {
  position: absolute;
  left: -42px;
  top: 3px;
  width: 18px;
  height: 18px;
  color: var(--agent-step-color, var(--td-text-color-secondary));
}
.progress-history {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 4px 0 0;
  padding: 0;
  list-style: none;
  white-space: pre-wrap;
}
@media (prefers-reduced-motion: reduce) {
  .progress-count, .progress-chevron { transition: none; }
}
</style>
