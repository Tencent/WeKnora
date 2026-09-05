<template>
  <div class="thinking-display">
    <div class="thinking-content">
      <t-icon name="lightbulb" class="thinking-icon" aria-hidden="true" />
      <div class="thinking-text">{{ thinkingText }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { ThinkingData } from '@/types/tool-results';
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { sanitizeThinkingText } from '@/utils/agent-tool-display';

const props = defineProps<{
  data: ThinkingData;
}>();

useI18n(); // ensure component reacts to locale changes if needed

const thinkingText = computed(() => sanitizeThinkingText(String(props.data.thought || '')));
</script>

<style lang="less" scoped>
@import './tool-results.less';

.thinking-display {
  padding: 0;
  height: auto;
  max-height: none;
  overflow: visible;
  font-size: 14px;
}

.thinking-content {
  display: flex;
  gap: 10px;
  padding: 12px 14px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  border-left: 3px solid var(--td-text-color-placeholder);
}

.thinking-icon {
  width: 18px;
  height: 18px;
  min-width: 18px;
  min-height: 18px;
  font-size: 18px;
  flex-shrink: 0;
  line-height: 18px;
  flex: 0 0 18px;
}

.thinking-text {
  font-size: 14px;
  color: var(--td-text-color-primary);
  line-height: 1.57;
  white-space: pre-wrap;
  word-break: break-word;
  flex: 1;
  font-weight: 400;
}
</style>
