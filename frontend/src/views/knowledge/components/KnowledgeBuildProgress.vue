<script setup lang="ts">
import { useI18n } from 'vue-i18n';

import type { KnowledgeBuildProgress } from '@/api/knowledge-base';

defineProps<{
  progress: KnowledgeBuildProgress;
}>();

const { t } = useI18n();
</script>

<template>
  <section v-if="progress.total > 0" class="knowledge-build-progress" role="status"
    :aria-label="t('knowledgeBase.buildProgressAria', {
      settled: progress.settled,
      total: progress.total,
      percentage: progress.percentage,
    })">
    <div class="build-progress-heading">
      <div class="build-progress-title">
        <t-icon name="chart-bar" size="16px" />
        <span>{{ t('knowledgeBase.buildProgress') }}</span>
      </div>
      <span class="build-progress-total">
        {{ t('knowledgeBase.buildProgressTotal', { settled: progress.settled, total: progress.total }) }}
        · {{ progress.percentage }}%
      </span>
    </div>

    <div class="build-progress-track" aria-hidden="true">
      <div class="build-progress-fill" :style="{ width: `${progress.percentage}%` }" />
    </div>

    <div class="build-progress-statuses">
      <span v-if="progress.in_flight > 0" class="build-status is-in-flight">
        {{ t('knowledgeBase.batchStatusInFlight', { count: progress.in_flight }) }}
      </span>
      <span v-if="progress.completed > 0" class="build-status is-completed">
        {{ t('knowledgeBase.batchStatusCompleted', { count: progress.completed }) }}
      </span>
      <span v-if="progress.failed > 0" class="build-status is-failed">
        {{ t('knowledgeBase.batchStatusFailed', { count: progress.failed }) }}
      </span>
      <span v-if="progress.cancelled > 0" class="build-status is-cancelled">
        {{ t('knowledgeBase.batchStatusCancelled', { count: progress.cancelled }) }}
      </span>
      <span v-if="progress.draft > 0" class="build-status is-draft">
        {{ t('knowledgeBase.batchStatusDraft', { count: progress.draft }) }}
      </span>
    </div>
  </section>
</template>

<style scoped lang="less">
.knowledge-build-progress {
  margin: 0 20px 12px;
  padding: 12px 14px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
}

.build-progress-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}

.build-progress-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.build-progress-total {
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.build-progress-track {
  height: 6px;
  overflow: hidden;
  background: var(--td-bg-color-component);
  border-radius: 999px;
}

.build-progress-fill {
  height: 100%;
  background: var(--td-brand-color);
  border-radius: inherit;
  transition: width 0.25s ease;
}

.build-progress-statuses {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.build-status {
  padding: 2px 7px;
  border-radius: 999px;
  background: var(--td-bg-color-component);
  color: var(--td-text-color-secondary);
  font-size: 11px;
  line-height: 18px;

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
</style>
