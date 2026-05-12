<script setup lang="ts">
import { useI18n } from 'vue-i18n';

defineProps<{
  count: number;
  loading?: boolean;
  // When true the bar stays visible even with 0 selections, so users can exit
  // batch mode from here without selecting anything first.
  visible?: boolean;
}>();

const emit = defineEmits<{
  (e: 'cancel'): void;
  (e: 'delete'): void;
}>();

const { t } = useI18n();
</script>

<template>
  <transition name="batch-bar-fade">
    <div
      v-if="visible || count > 0"
      class="doc-batch-bar"
      role="region"
      :aria-label="t('knowledgeBase.selectedCount', { count })"
    >
      <div class="batch-bar-inner">
        <div class="batch-bar-left">
          <span class="batch-bar-count">{{ t('knowledgeBase.selectedCount', { count }) }}</span>
          <t-button
            variant="text"
            theme="default"
            size="small"
            class="batch-bar-clear"
            @click="emit('cancel')"
          >
            {{ t('knowledgeBase.clearSelection') }}
          </t-button>
        </div>
        <div class="batch-bar-actions">
          <t-button
            theme="danger"
            variant="outline"
            size="small"
            :disabled="count === 0"
            :loading="loading"
            @click="emit('delete')"
          >
            <template #icon><t-icon name="delete" size="14px" /></template>
            {{ t('knowledgeBase.batchDelete') }}
          </t-button>
        </div>
      </div>
    </div>
  </transition>
</template>

<style scoped lang="less">
.doc-batch-bar {
  position: relative;
  z-index: 5;
  width: 100%;
  max-width: 560px;
  margin: 0 auto;
  padding: 0 4px;
  box-sizing: border-box;
}

.batch-bar-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  box-shadow: 0 6px 16px rgba(0, 0, 0, 0.08);
}

.batch-bar-left {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  flex: 1;
}

.batch-bar-count {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  white-space: nowrap;
}

.batch-bar-clear {
  flex-shrink: 0;
  padding: 0 6px !important;
  height: 28px !important;
  font-size: 12px;
  color: var(--td-text-color-secondary) !important;

  &:hover {
    color: var(--td-brand-color) !important;
  }
}

.batch-bar-actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}

.batch-bar-fade-enter-active,
.batch-bar-fade-leave-active {
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.batch-bar-fade-enter-from,
.batch-bar-fade-leave-to {
  opacity: 0;
  transform: translateY(6px);
}
</style>
