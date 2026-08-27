<template>
  <section class="processing-status" aria-label="内容处理状态">
    <t-alert :theme="alertTheme" :message="message">
    <template #operation>
      <t-button
        v-if="status?.retryable_job"
        size="small"
        theme="danger"
        variant="outline"
        :loading="retrying"
        @click="retryFailedStage"
      >
        重试此阶段
      </t-button>
      <t-button v-else-if="loadError" size="small" variant="outline" @click="refresh">刷新状态</t-button>
    </template>
    </t-alert>
    <ol v-if="status" class="processing-status__stages">
      <li v-for="stage in stages" :key="stage.name" :class="`is-${stage.state}`">
        <span>{{ stageLabels[stage.name] }}</span>
        <small>{{ stageStateLabels[stage.state] }}</small>
      </li>
    </ol>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { fetchVideoProcessingStatus, retryVideoProcessingStage } from '@/api/videohub'
import type { VideoProcessingStatus } from '@/types/videohub'
import { getNewlyCompletedStages } from './processingStatusState'

const props = defineProps<{ videoId: string }>()
const emit = defineEmits<{
  retryStarted: [jobType: string]
  stageCompleted: [stage: string]
}>()

const status = ref<VideoProcessingStatus | null>(null)
const loadError = ref('')
const retrying = ref(false)
let requestSequence = 0
let pollTimer: number | undefined
const observedJobStates = new Map<string, string>()

const stageLabels: Record<string, string> = {
  transcription: '视频转写',
  subtitle_generate: '字幕生成',
  index: '内容入库',
  graph: '知识提取',
  outline: '章节生成',
  overview: '概要生成',
  summary: '智能总结',
  assemble: '内容组装',
}
const stageOrder = ['transcription', 'subtitle_generate', 'index', 'graph', 'outline', 'overview', 'summary', 'assemble']
const stageStateLabels: Record<string, string> = {
  waiting: '等待',
  pending: '排队中',
  running: '处理中',
  succeeded: '已完成',
  failed: '失败',
}

const alertTheme = computed<'info' | 'success' | 'warning' | 'error'>(() => {
  if (loadError.value || status.value?.status === 'failed') return 'error'
  if (status.value?.status === 'completed') return 'success'
  if (status.value?.status === 'partial_completed') return 'warning'
  return 'info'
})

const message = computed(() => {
  if (loadError.value) return `解析状态加载失败：${loadError.value}`
  if (!status.value) return '正在读取内容解析状态'
  const stage = stageLabels[status.value.current_stage || ''] || status.value.current_stage || '等待开始'
  switch (status.value.status) {
    case 'completed': return '内容解析已完成，章节、总结和关联内容均可使用'
    case 'failed': return `${stage}失败：${status.value.failure?.message || '可重试当前阶段'}`
    case 'partial_completed': return `部分内容已生成，当前阶段：${stage}`
    case 'processing': return `内容解析中，当前阶段：${stage}`
    default: return '视频已可播放，内容解析即将开始'
  }
})

const stages = computed(() => stageOrder.map(name => {
  const job = status.value?.jobs.find(item => item.job_type === name)
  const state = job?.status === 'succeeded'
    ? 'succeeded'
    : job?.status === 'failed'
      ? 'failed'
      : job?.status === 'running'
        ? 'running'
        : job?.status === 'pending'
          ? 'pending'
          : status.value?.completed_stages.includes(name) ? 'succeeded' : 'waiting'
  return { name, state }
}))

function shouldPoll(value: VideoProcessingStatus | null) {
  return !value || value.status === 'ready' || value.status === 'processing' || value.status === 'partial_completed'
}

function schedulePoll() {
  window.clearTimeout(pollTimer)
  if (!shouldPoll(status.value)) return
  pollTimer = window.setTimeout(() => void refresh(), 3000)
}

async function refresh() {
  const sequence = ++requestSequence
  loadError.value = ''
  try {
    const next = await fetchVideoProcessingStatus(props.videoId)
    if (sequence !== requestSequence) return
    const hasObservedState = observedJobStates.size > 0
    if (hasObservedState) {
      for (const stage of getNewlyCompletedStages(observedJobStates, next.jobs)) emit('stageCompleted', stage)
    }
    for (const job of next.jobs) {
      const previousState = observedJobStates.get(job.job_id)
      observedJobStates.set(job.job_id, job.status)
    }
    status.value = next
  } catch (reason: any) {
    if (sequence !== requestSequence) return
    loadError.value = reason?.message || '请稍后重试'
  } finally {
    if (sequence === requestSequence) schedulePoll()
  }
}

async function retryFailedStage() {
  const jobType = status.value?.retryable_job?.job_type
  if (!jobType || retrying.value) return
  retrying.value = true
  loadError.value = ''
  try {
    await retryVideoProcessingStage(props.videoId, jobType)
    emit('retryStarted', jobType)
    await refresh()
  } catch (reason: any) {
    loadError.value = reason?.message || '重试失败，请稍后再试'
  } finally {
    retrying.value = false
  }
}

watch(() => props.videoId, () => {
  status.value = null
  observedJobStates.clear()
  void refresh()
})
onMounted(() => void refresh())
onBeforeUnmount(() => window.clearTimeout(pollTimer))
</script>

<style scoped>
.processing-status { margin-bottom: var(--td-comp-margin-l); }
.processing-status__stages { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--td-comp-margin-s); margin: var(--td-comp-margin-s) 0 0; padding: 0; list-style: none; }
.processing-status__stages li { display: grid; gap: 2px; min-width: 0; padding: var(--td-comp-margin-s); border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-medium); color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.processing-status__stages li small { color: var(--td-text-color-placeholder); }
.processing-status__stages li.is-running { border-color: var(--td-brand-color); color: var(--td-brand-color); }
.processing-status__stages li.is-succeeded { border-color: var(--td-success-color); }
.processing-status__stages li.is-failed { border-color: var(--td-error-color); color: var(--td-error-color); }
@media (max-width: 760px) { .processing-status__stages { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
