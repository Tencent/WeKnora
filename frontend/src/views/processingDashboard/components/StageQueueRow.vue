<template>
  <section class="pd-stage-row">
    <div class="pd-stage-row__head">
      <div class="pd-stage-row__icon">
        <t-icon :name="iconName" size="18px" />
      </div>
      <div class="pd-stage-row__title">
        <h3>{{ title }}</h3>
        <p>{{ description }}</p>
      </div>
    </div>

    <div class="pd-stage-row__metrics">
      <div>
        <strong>{{ formatStageCount(stage.running_count, stage.counts_reliable) }}</strong>
        <span>{{ t('processingDashboard.running') }}</span>
      </div>
      <div>
        <strong>{{ formatStageCount(stage.queued_count, stage.counts_reliable) }}</strong>
        <span>{{ t('processingDashboard.queued') }}</span>
      </div>
      <div>
        <strong>{{ stage.retrying_observable ? stage.retrying_count : '-' }}</strong>
        <span>{{ t('processingDashboard.retrying') }}</span>
      </div>
    </div>

    <div class="pd-stage-row__preview">
      <template v-if="runningItems.length">
        <StageItemPreview
          v-for="item in previewItems(runningItems, 5)"
          :key="`${item.knowledge_id}:${item.attempt}:${item.stage}`"
          :item="item"
          @open="$emit('openKnowledge', $event)"
        />
      </template>
      <div v-else class="pd-stage-row__empty">{{ t('processingDashboard.noRunning') }}</div>
    </div>

    <div class="pd-stage-row__action">
      <t-button size="small" variant="outline" @click="$emit('openQueue', stage.key)">
        <template #icon><t-icon name="queue" /></template>
        {{ t('processingDashboard.viewQueue') }}
      </t-button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ProcessingLogicalStage, ProcessingStageItem, ProcessingStageSummary } from '@/types/processingDashboard'
import { formatStageCount, previewItems } from '../format'
import StageItemPreview from './StageItemPreview.vue'

const props = defineProps<{ stage: ProcessingStageSummary }>()
defineEmits<{
  openQueue: [stage: ProcessingLogicalStage]
  openKnowledge: [item: ProcessingStageItem]
}>()

const { t } = useI18n()
const title = computed(() => t(`processingDashboard.stage.${props.stage.key}.title`))
const description = computed(() => t(`processingDashboard.stage.${props.stage.key}.description`))
const runningItems = computed(() => Array.isArray(props.stage.running_items) ? props.stage.running_items : [])
const iconName = computed(() => {
  switch (props.stage.key) {
    case 'docreader': return 'file'
    case 'chunking': return 'fork'
    case 'embedding': return 'data'
    case 'multimodal': return 'image'
    case 'postprocess': return 'flow'
    case 'summary': return 'text'
    case 'question': return 'help-circle'
    case 'graph': return 'share'
    case 'wiki': return 'book'
    default: return 'queue'
  }
})
</script>

<style scoped>
.pd-stage-row {
  display: grid;
  grid-template-columns: minmax(220px, 1.2fr) 280px minmax(260px, 1.5fr) auto;
  gap: 20px;
  align-items: center;
  padding: 18px 24px;
  border-bottom: 1px solid #e7ece9;
}

.pd-stage-row__head {
  display: grid;
  grid-template-columns: 40px minmax(0, 1fr);
  gap: 12px;
  align-items: center;
}

.pd-stage-row__icon {
  width: 40px;
  height: 40px;
  border-radius: 8px;
  background: #e9f6ef;
  color: #009966;
  display: grid;
  place-items: center;
}

.pd-stage-row__title h3 {
  margin: 0 0 4px;
  font-size: 15px;
  line-height: 1.25;
}

.pd-stage-row__title p {
  margin: 0;
  color: #60717d;
  font-size: 12px;
  line-height: 1.35;
}

.pd-stage-row__metrics {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
}

.pd-stage-row__metrics div {
  min-width: 0;
  padding: 8px 10px;
  border: 1px solid #e3eae6;
  border-radius: 8px;
  background: #fbfcfb;
}

.pd-stage-row__metrics strong {
  display: block;
  font-size: 20px;
  line-height: 1.1;
  font-variant-numeric: tabular-nums;
}

.pd-stage-row__metrics span {
  color: #60717d;
  font-size: 12px;
}

.pd-stage-row__preview {
  min-width: 0;
}

.pd-stage-row__empty {
  color: #8b99a3;
  font-size: 13px;
}

.pd-stage-row__action {
  justify-self: end;
}

@media (max-width: 1180px) {
  .pd-stage-row {
    grid-template-columns: minmax(0, 1fr);
    align-items: stretch;
  }
  .pd-stage-row__action {
    justify-self: start;
  }
}
</style>
