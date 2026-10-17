<template>
  <div class="progress-updates">
    <details v-if="history.length" class="progress-disclosure">
      <summary>
        <span class="progress-icon" :style="iconStyle" aria-hidden="true" />
        <span>{{ content }}</span>
        <t-icon class="progress-chevron" name="chevron-right" />
      </summary>
      <div class="progress-history">
        <p v-for="(update, index) in history" :key="index">{{ update }}</p>
      </div>
    </details>
    <div v-else class="progress-summary">
      <span class="progress-icon" :style="iconStyle" aria-hidden="true" />
      <span>{{ content }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import thinkingIcon from '@/assets/img/Frame3718.svg'
const props = defineProps<{ content: string; history?: string[] }>()
const history = computed(() => props.history || [])
const iconStyle = { maskImage: `url("${thinkingIcon}")`, WebkitMaskImage: `url("${thinkingIcon}")` }
</script>

<style scoped lang="less">
.progress-updates {
  position: relative;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--agent-step-summary-size, 13px);
  line-height: 1.55;
  overflow-wrap: anywhere;
}
.progress-summary, summary {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-height: 24px;
  padding: 0;
  list-style: none;
  &::-webkit-details-marker { display: none; }
}
summary {
  cursor: pointer;
  &:hover { color: var(--td-text-color-primary); }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 3px; border-radius: var(--app-radius-xs); }
}
.progress-icon {
  position: absolute;
  left: -42px;
  top: 3px;
  width: 18px;
  height: 18px;
  background: var(--agent-step-icon-color, var(--td-text-color-placeholder));
  mask-size: contain;
  mask-repeat: no-repeat;
  mask-position: center;
}
.progress-chevron { flex-shrink: 0; color: var(--td-text-color-placeholder); }
details[open] .progress-chevron { transform: rotate(90deg); }
.progress-history {
  margin-top: 8px;
  padding-left: 12px;
  border-left: 1px solid var(--agent-step-line-color, var(--td-component-border));
  color: var(--td-text-color-placeholder);
  white-space: pre-wrap;
  p { margin: 0 0 8px; }
  p:last-child { margin-bottom: 0; }
}
</style>
