<template>
  <div class="pd-timeline">
    <div v-for="stage in stages" :key="stage.stage" class="pd-timeline__row">
      <t-tag :theme="stateTone(stage.state)" variant="light" size="small">
        {{ t(`processingDashboard.state.${stage.state}`) }}
      </t-tag>
      <div class="pd-timeline__main">
        <strong>{{ t(`processingDashboard.stage.${stage.stage}.title`) }}</strong>
        <span>
          <ProgressValue :progress="stage.progress" :fallback="stage.phase || ''" />
          <template v-if="stage.failed_children"> · {{ stage.failed_children }} {{ t('processingDashboard.drawer.failedChildren') }}</template>
        </span>
        <small v-if="stage.error_message">{{ stage.error_message }}</small>
      </div>
      <div class="pd-timeline__time">
        <span>{{ stage.started_at ? new Date(stage.started_at).toLocaleTimeString() : '' }}</span>
        <small>{{ formatElapsed(stage.duration_ms || stage.elapsed_ms) }}</small>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ProcessingStageItem } from '@/types/processingDashboard'
import { formatElapsed, stateTone } from '../format'
import ProgressValue from './ProgressValue.vue'

defineProps<{ stages: ProcessingStageItem[] }>()
const { t } = useI18n()
</script>

<style scoped>
.pd-timeline {
  display: grid;
  gap: 10px;
}

.pd-timeline__row {
  display: grid;
  grid-template-columns: 96px minmax(0, 1fr) 88px;
  gap: 12px;
  align-items: start;
  padding: 10px 0;
  border-bottom: 1px solid #edf1ef;
}

.pd-timeline__main {
  min-width: 0;
  display: grid;
  gap: 4px;
}

.pd-timeline__main strong {
  color: #1f2d35;
}

.pd-timeline__main span,
.pd-timeline__main small,
.pd-timeline__time {
  color: #687883;
}

.pd-timeline__time {
  display: grid;
  gap: 4px;
  justify-items: end;
  font-variant-numeric: tabular-nums;
}

@media (max-width: 560px) {
  .pd-timeline__row {
    grid-template-columns: minmax(0, 1fr);
    gap: 8px;
  }

  .pd-timeline__time {
    justify-items: start;
  }
}
</style>
