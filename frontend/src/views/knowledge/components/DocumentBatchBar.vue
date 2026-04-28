<script setup lang="ts">
import { useI18n } from 'vue-i18n';

defineProps<{
  count: number;
  loading?: boolean;
}>();

const emit = defineEmits<{
  (e: 'clear'): void;
  (e: 'delete'): void;
}>();

const { t } = useI18n();
</script>

<template>
  <transition name="batch-bar-fade">
    <div v-if="count > 0" class="doc-batch-bar" role="region" :aria-label="t('knowledgeBase.selectedCount', { count })">
      <div class="batch-bar-info">
        <span class="batch-bar-count">{{ t('knowledgeBase.selectedCount', { count }) }}</span>
        <button class="batch-bar-link" type="button" @click="emit('clear')">
          {{ t('knowledgeBase.clearSelection') }}
        </button>
      </div>
      <div class="batch-bar-actions">
        <t-button
          theme="danger"
          variant="base"
          size="small"
          :loading="loading"
          @click="emit('delete')"
        >
          <template #icon><t-icon name="delete" size="14px" /></template>
          {{ t('knowledgeBase.batchDelete') }}
        </t-button>
      </div>
    </div>
  </transition>
</template>

<style scoped lang="less">
.doc-batch-bar {
  position: sticky;
  bottom: 12px;
  align-self: center;
  display: flex;
  align-items: center;
  gap: 24px;
  padding: 8px 12px 8px 16px;
  margin: 12px auto 4px;
  min-width: 320px;
  max-width: 720px;
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 999px;
  box-shadow:
    0 6px 20px rgba(0, 0, 0, 0.08),
    0 2px 6px rgba(0, 0, 0, 0.06);
  z-index: 5;
}

.batch-bar-info {
  display: flex;
  align-items: center;
  gap: 12px;
}

.batch-bar-count {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary, #232323);
}

.batch-bar-link {
  background: transparent;
  border: 0;
  padding: 2px 4px;
  font-size: 12px;
  color: var(--td-brand-color, #0052d9);
  cursor: pointer;
  border-radius: 4px;

  &:hover { background: var(--td-brand-color-1, #f0f6ff); }
}

.batch-bar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
}

.batch-bar-fade-enter-active,
.batch-bar-fade-leave-active {
  transition: transform 0.18s ease, opacity 0.18s ease;
}
.batch-bar-fade-enter-from,
.batch-bar-fade-leave-to {
  opacity: 0;
  transform: translateY(8px);
}
</style>
