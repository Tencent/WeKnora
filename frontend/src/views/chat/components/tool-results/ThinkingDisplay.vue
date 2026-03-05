<template>
  <div class="thinking-display">
    <div class="thinking-content">
      <div class="thinking-icon" aria-hidden="true">💭</div>
      <div class="thinking-text">{{ data.thought }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { defineProps } from 'vue';
import type { ThinkingData } from '@/types/tool-results';
import { useI18n } from 'vue-i18n';

const props = defineProps<{
  data: ThinkingData;
}>();

useI18n(); // ensure component reacts to locale changes if needed
</script>

<style lang="less" scoped>
@import './tool-results.less';

.thinking-display {
  padding: 0;
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
  font-size: 16px;
  flex-shrink: 0;
  line-height: 1.5;
}

.thinking-text {
  font-size: 15px;
  color: var(--td-text-color-primary);
  line-height: 1.65;
  white-space: pre-wrap;
  word-break: break-word;
  flex: 1;
  font-weight: 400;
}
</style>

