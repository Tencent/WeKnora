<template>
  <div class="tool-result-renderer">
    <!-- Views come from the tool result registry: builtins plus plugin views. -->
    <component :is="view.component" v-if="view" v-bind="viewProps" />

    <!-- Fallback: Display raw output -->
    <div v-else class="fallback-output">
      <div class="fallback-header">
        <span class="fallback-label">{{ $t('chat.rawOutputLabel') }}</span>
      </div>
      <div class="detail-output-wrapper">
        <div class="detail-output">{{ output }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { DisplayType } from '@/types/tool-results';
import { registerBuiltinToolResultViews } from '@/extensions/builtin/toolResultViews';
import { toolResultProps, toolResultViews } from '@/extensions/toolResultViews';

registerBuiltinToolResultViews();

interface Props {
  success?: boolean;
  displayType?: DisplayType;
  toolData?: Record<string, any>;
  output?: string;
  arguments?: Record<string, any>;
}

const props = withDefaults(defineProps<Props>(), { success: undefined });

const output = computed(() => props.output || '');

const view = computed(() => (props.displayType ? toolResultViews.get(props.displayType) : undefined));
const viewProps = computed(() =>
  view.value
    ? toolResultProps(view.value, {
      displayType: props.displayType ?? '',
      data: props.toolData || {},
      output: output.value,
      arguments: props.arguments || {},
      success: props.success,
    })
    : {},
);
</script>

<style lang="less" scoped>
.tool-result-renderer {
  margin: 0;
}

.fallback-output {
  margin: 12px 0;
  padding: 0;

  .fallback-header {
    display: flex;
    align-items: center;
    margin-bottom: 10px;
    padding: 0 4px;

    .fallback-label {
      font-size: var(--app-text-sm);
      color: var(--td-text-color-secondary);
      font-weight: 500;
      line-height: 1.5;
    }
  }

  .detail-output-wrapper {
    position: relative;
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-sm);
    overflow: hidden;
    margin: 0;
    padding: 0;

    .detail-output {
      font-family: var(--app-font-family-mono);
      font-size: var(--app-text-sm);
      color: var(--td-text-color-primary);
      padding: 16px;
      margin: 0;
      white-space: pre-wrap;
      word-break: break-word;
      line-height: 1.6;
      max-height: 400px;
      overflow-y: auto;
      overflow-x: auto;
      background: var(--td-bg-color-container);
      display: block;

      // 滚动条样式
      &::-webkit-scrollbar {
        width: 8px;
        height: 8px;
      }

      &::-webkit-scrollbar-track {
        background: var(--td-bg-color-secondarycontainer);
        border-radius: var(--app-radius-xs);
      }

      &::-webkit-scrollbar-thumb {
        background: var(--td-component-border);
        border-radius: var(--app-radius-xs);

        &:hover {
          background: var(--td-text-color-placeholder);
        }
      }
    }
  }
}
</style>
