<template>
  <t-drawer
    :visible="visible"
    :header="detail?.knowledge?.title || selected?.title || t('processingDashboard.detail.title')"
    size="min(720px, 100vw)"
    :footer="false"
    @close="$emit('update:visible', false)"
  >
    <div class="pd-detail">
      <t-loading v-if="loading" />
      <template v-else-if="detail">
        <div class="pd-detail__meta">
          <span>{{ detail.knowledge?.knowledge_base_name }}</span>
          <span>attempt {{ detail.current_attempt }}</span>
          <span>{{ detail.parse_status }}</span>
          <span>{{ t('processingDashboard.detail.pendingSubtasks') }} {{ detail.pending_subtasks_count }}</span>
        </div>
        <LogicalStageTimeline :stages="detail.stages" />
        <t-collapse class="pd-detail__raw" expand-icon-placement="right">
          <t-collapse-panel value="raw" :header="t('processingDashboard.detail.rawTrace')">
            <pre>{{ compactTrace(detail.raw_trace) }}</pre>
          </t-collapse-panel>
        </t-collapse>
      </template>
      <div v-else class="pd-detail__empty">{{ t('processingDashboard.detail.empty') }}</div>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getProcessingKnowledgeDetail } from '@/api/processing-dashboard'
import type { ProcessingKnowledgeDetailResponse, ProcessingStageItem } from '@/types/processingDashboard'
import LogicalStageTimeline from './LogicalStageTimeline.vue'

const props = defineProps<{
  visible: boolean
  selected: ProcessingStageItem | null
}>()

defineEmits<{ 'update:visible': [value: boolean] }>()

const { t } = useI18n()
const loading = ref(false)
const detail = ref<ProcessingKnowledgeDetailResponse | null>(null)

const load = async () => {
  if (!props.selected) return
  loading.value = true
  try {
    const res = await getProcessingKnowledgeDetail(props.selected.knowledge_id, props.selected.attempt)
    detail.value = res.data
  } finally {
    loading.value = false
  }
}

watch(() => [props.visible, props.selected?.knowledge_id, props.selected?.attempt], () => {
  if (props.visible && props.selected) void load()
})

const compactTrace = (trace: any[]) => JSON.stringify(trace || [], (key, value) => {
  if (key === 'input' || key === 'output' || key === 'metadata') return value
  if (Array.isArray(value) && value.length > 20) return value.slice(0, 20)
  return value
}, 2)
</script>

<style scoped>
.pd-detail {
  min-height: 100%;
}

.pd-detail__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 16px;
}

.pd-detail__meta span {
  border: 1px solid #e2e9e5;
  border-radius: 8px;
  padding: 5px 8px;
  color: #52636f;
  background: #fbfcfb;
  font-size: 12px;
}

.pd-detail__raw {
  margin-top: 18px;
}

.pd-detail__raw pre {
  max-height: 360px;
  overflow: auto;
  max-width: 100%;
  background: #f7f9f8;
  border-radius: 8px;
  padding: 12px;
  font-size: 12px;
}

.pd-detail__empty {
  padding: 48px 0;
  text-align: center;
  color: #8b99a3;
}
</style>
