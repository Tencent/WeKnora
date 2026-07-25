<script setup lang="ts">
import { useI18n } from 'vue-i18n';

defineProps<{
  count: number;
  inFlightCount?: number;
  rebuildableCount?: number;
  completedCount?: number;
  failedCount?: number;
  cancelledCount?: number;
  draftCount?: number;
  deleteLoading?: boolean;
  reparseLoading?: boolean;
  cancelParseLoading?: boolean;
  // When true the bar stays visible even with 0 selections, so users can exit
  // batch mode from here without selecting anything first.
  visible?: boolean;
}>();

const emit = defineEmits<{
  (e: 'cancel'): void;
  (e: 'delete'): void;
  (e: 'reparse'): void;
  (e: 'cancel-parse'): void;
}>();

const { t } = useI18n();
</script>

<template>
  <transition name="batch-bar-fade">
    <div v-if="visible || count > 0" class="doc-batch-bar" role="region"
      :aria-label="t('knowledgeBase.selectedCount', { count })">
      <div class="batch-bar-inner">
        <div class="batch-bar-left">
          <div class="batch-bar-selection">
            <span class="batch-bar-count">{{ t('knowledgeBase.selectedCount', { count }) }}</span>
            <div v-if="count > 0" class="batch-bar-statuses">
              <span v-if="inFlightCount" class="batch-status is-in-flight">
                {{ t('knowledgeBase.batchStatusInFlight', { count: inFlightCount }) }}
              </span>
              <span v-if="failedCount" class="batch-status is-failed">
                {{ t('knowledgeBase.batchStatusFailed', { count: failedCount }) }}
              </span>
              <span v-if="completedCount" class="batch-status is-completed">
                {{ t('knowledgeBase.batchStatusCompleted', { count: completedCount }) }}
              </span>
              <span v-if="cancelledCount" class="batch-status is-cancelled">
                {{ t('knowledgeBase.batchStatusCancelled', { count: cancelledCount }) }}
              </span>
              <span v-if="draftCount" class="batch-status is-draft">
                {{ t('knowledgeBase.batchStatusDraft', { count: draftCount }) }}
              </span>
            </div>
          </div>
          <t-button variant="text" theme="default" size="small" class="batch-bar-clear" @click="emit('cancel')">
            {{ t('knowledgeBase.clearSelection') }}
          </t-button>
        </div>
        <div class="batch-bar-actions">
          <t-popconfirm theme="warning"
            :content="t('knowledgeBase.confirmBatchCancelParse', { count: inFlightCount || 0 })"
            :confirm-btn="{ content: t('knowledgeBase.batchCancelParse'), theme: 'warning' }"
            :cancel-btn="{ content: t('common.cancel') }" placement="top" @confirm="emit('cancel-parse')">
            <t-button theme="warning" variant="outline" size="small"
              :disabled="!inFlightCount || deleteLoading || reparseLoading || cancelParseLoading"
              :loading="cancelParseLoading" @click.stop>
              <template #icon><t-icon name="stop-circle" size="14px" /></template>
              {{ t('knowledgeBase.batchCancelParse') }}
            </t-button>
          </t-popconfirm>

          <t-popconfirm theme="warning"
            :content="t('knowledgeBase.confirmBatchReparseDocument', { count: rebuildableCount || 0 })"
            :confirm-btn="{ content: t('knowledgeBase.confirmBatchReparse'), theme: 'warning' }"
            :cancel-btn="{ content: t('common.cancel') }" placement="top" @confirm="emit('reparse')">
            <t-button theme="default" variant="outline" size="small"
              :disabled="!rebuildableCount || deleteLoading || reparseLoading || cancelParseLoading"
              :loading="reparseLoading" @click.stop>
              <template #icon><t-icon name="refresh" size="14px" /></template>
              {{ t('knowledgeBase.rebuildDocument') }}
            </t-button>
          </t-popconfirm>

          <t-popconfirm theme="warning" :content="t('knowledgeBase.confirmBatchDeleteDocument', { count })"
            :confirm-btn="{ content: t('knowledgeBase.confirmDelete'), theme: 'danger' }"
            :cancel-btn="{ content: t('common.cancel') }" placement="top" @confirm="emit('delete')">
            <t-button theme="danger" variant="outline" size="small"
              :disabled="count === 0 || deleteLoading || reparseLoading || cancelParseLoading"
              :loading="deleteLoading" @click.stop>
              <template #icon><t-icon name="delete" size="14px" /></template>
              {{ t('knowledgeBase.batchDelete') }}
            </t-button>
          </t-popconfirm>
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
  max-width: 820px;
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

.batch-bar-selection {
  min-width: 0;
}

.batch-bar-statuses {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 3px;
}

.batch-status {
  padding: 0 5px;
  border-radius: 999px;
  background: var(--td-bg-color-component);
  color: var(--td-text-color-secondary);
  font-size: 10px;

  &.is-in-flight {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
  }

  &.is-failed {
    background: var(--td-error-color-light);
    color: var(--td-error-color);
  }

  &.is-completed {
    background: var(--td-success-color-light);
    color: var(--td-success-color);
  }
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

@media (max-width: 960px) {
  .batch-bar-inner {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .batch-bar-actions {
    width: 100%;
    justify-content: flex-end;
  }
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
