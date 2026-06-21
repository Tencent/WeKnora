<template>
  <main class="pd-page">
    <header class="pd-toolbar">
      <div>
        <h1>{{ t('processingDashboard.title') }}</h1>
        <p>{{ t('processingDashboard.subtitle') }}</p>
      </div>
      <div class="pd-toolbar__controls">
        <t-select v-model="kbId" clearable filterable :placeholder="t('processingDashboard.filterKnowledgeBase')" class="pd-kb-select">
          <t-option v-for="kb in kbOptions" :key="kb.value" :value="kb.value" :label="kb.label" />
        </t-select>
        <t-input :placeholder="t('processingDashboard.searchPlaceholder')" clearable @change="setKeywordDebounced">
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-select v-model="refreshSeconds" class="pd-refresh-select">
          <t-option :value="0" :label="t('processingDashboard.refresh.off')" />
          <t-option :value="5" label="5s" />
          <t-option :value="10" label="10s" />
          <t-option :value="30" label="30s" />
          <t-option :value="60" label="60s" />
        </t-select>
        <t-button :loading="loading" @click="refresh">
          <template #icon><t-icon name="refresh" /></template>
          {{ t('processingDashboard.manualRefresh') }}
        </t-button>
      </div>
    </header>

    <t-alert
      v-if="queueUnavailable"
      theme="warning"
      :message="queueMessage"
      class="pd-alert"
    />
    <t-alert v-if="error" theme="error" :message="error" class="pd-alert" />

    <section v-if="data" class="pd-statusbar">
      <span>{{ t('processingDashboard.updatedAt') }} {{ new Date(data.generated_at).toLocaleString() }}</span>
      <span>{{ data.source.executor_mode }}</span>
      <span>{{ data.source.queue_snapshot }}</span>
    </section>

    <section class="pd-board" :class="{ 'pd-board--loading': loading && !data }">
      <template v-if="data">
        <div v-for="group in groupedStages" :key="group.key" class="pd-group">
          <h2>{{ group.label }}</h2>
          <div class="pd-group__rows">
            <StageQueueRow
              v-for="stage in group.stages"
              :key="stage.key"
              :stage="stage"
              @open-queue="openQueue"
              @open-knowledge="openKnowledge"
            />
          </div>
        </div>
        <div v-if="!hasActiveTasks" class="pd-empty">{{ t('processingDashboard.noActiveTasks') }}</div>
      </template>
      <t-loading v-else />
    </section>

    <StageQueueDrawer
      v-model:visible="queueDrawerVisible"
      :stage="selectedStage"
      :kb-id="kbId"
      :keyword="keyword"
      @open-knowledge="openKnowledge"
    />
    <KnowledgePipelineDrawer
      v-model:visible="knowledgeDrawerVisible"
      :selected="selectedKnowledge"
    />
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useProcessingDashboard } from '@/hooks/useProcessingDashboard'
import type { ProcessingLogicalStage, ProcessingStageItem } from '@/types/processingDashboard'
import { sortStages } from './format'
import StageQueueRow from './components/StageQueueRow.vue'
import StageQueueDrawer from './components/StageQueueDrawer.vue'
import KnowledgePipelineDrawer from './components/KnowledgePipelineDrawer.vue'
import { ref } from 'vue'

const { t } = useI18n()
const authStore = useAuthStore()
const {
  data,
  loading,
  error,
  kbId,
  keyword,
  refreshSeconds,
  queueUnavailable,
  refresh,
  setKeywordDebounced,
} = useProcessingDashboard()

const queueDrawerVisible = ref(false)
const knowledgeDrawerVisible = ref(false)
const selectedStage = ref<ProcessingLogicalStage | ''>('')
const selectedKnowledge = ref<ProcessingStageItem | null>(null)

const kbOptions = computed(() => (authStore.knowledgeBases || [])
  .filter((kb: any) => kb?.type === 'document' || !kb?.type)
  .map((kb: any) => ({ value: String(kb.id), label: kb.name || kb.id })))

const groupedStages = computed(() => {
  const stages = sortStages(data.value?.stages || [])
  return [
    { key: 'primary', label: t('processingDashboard.primaryGroup'), stages: stages.filter(s => s.group === 'primary') },
    { key: 'enrichment', label: t('processingDashboard.enrichmentGroup'), stages: stages.filter(s => s.group === 'enrichment') },
  ]
})

const hasActiveTasks = computed(() => (data.value?.stages || []).some(stage =>
  stage.running_count > 0 || stage.queued_count > 0 || stage.retrying_count > 0))

const queueMessage = computed(() => {
  if (data.value?.source.queue_snapshot === 'partial') return t('processingDashboard.queueDataPartial')
  return t('processingDashboard.queueDataUnavailable')
})

const openQueue = (stage: ProcessingLogicalStage) => {
  selectedStage.value = stage
  queueDrawerVisible.value = true
}

const openKnowledge = (item: ProcessingStageItem) => {
  selectedKnowledge.value = item
  knowledgeDrawerVisible.value = true
}

onMounted(() => {
  void refresh()
})
</script>

<style scoped>
.pd-page {
  min-height: 100%;
  padding: 24px;
  background: #f5f7f6;
  color: #1f2d35;
}

.pd-toolbar {
  display: flex;
  justify-content: space-between;
  gap: 20px;
  align-items: flex-start;
  margin-bottom: 16px;
}

.pd-toolbar h1 {
  margin: 0 0 6px;
  font-size: 24px;
  line-height: 1.2;
}

.pd-toolbar p {
  margin: 0;
  color: #62727d;
}

.pd-toolbar__controls {
  display: grid;
  grid-template-columns: 220px 240px 110px auto;
  gap: 10px;
  align-items: center;
}

.pd-statusbar {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  color: #687883;
  font-size: 12px;
  margin-bottom: 12px;
}

.pd-statusbar span {
  border: 1px solid #e0e8e4;
  border-radius: 8px;
  padding: 4px 8px;
  background: #fff;
}

.pd-alert {
  margin-bottom: 12px;
}

.pd-board {
  background: #fff;
  border: 1px solid #dfe8e3;
  border-radius: 8px;
  overflow: hidden;
}

.pd-group h2 {
  margin: 0;
  padding: 14px 24px;
  background: #f8faf9;
  border-bottom: 1px solid #e3ebe6;
  font-size: 14px;
}

.pd-empty {
  padding: 18px 24px;
  color: #71808a;
  text-align: center;
}

@media (max-width: 980px) {
  .pd-toolbar {
    display: grid;
  }
  .pd-toolbar__controls {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
