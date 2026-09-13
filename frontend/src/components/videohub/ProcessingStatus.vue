<template>
  <section v-if="message" class="processing-status" aria-label="内容处理状态">
    <t-alert v-if="message" :theme="alertTheme" :message="message">
    <template #operation>
      <t-button
        v-if="canRetryTranscription"
        size="small"
        theme="danger"
        variant="outline"
        :loading="retrying"
        @click="retryFailedStage"
      >
        重试
      </t-button>
      <t-button v-else-if="loadError" size="small" variant="outline" @click="refresh">刷新状态</t-button>
    </template>
    </t-alert>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { fetchVideoProcessingStatus, retryVideoProcessingStage } from '@/api/videohub'
import type { VideoProcessingStatus } from '@/types/videohub'
import { getFailedProcessingStages, getNewlyCompletedStages, getProcessingBannerState, getProcessingStages, getTranscriptionProgress, shouldPollProcessingStatus } from './processingStatusState'

const props = defineProps<{ videoId: string }>()
const emit = defineEmits<{
  retryStarted: [jobType: string]
  stageCompleted: [stage: string]
  processingStages: [stages: string[]]
  processingFailures: [stages: string[]]
}>()

const status = ref<VideoProcessingStatus | null>(null)
const loadError = ref('')
const retrying = ref(false)
let requestSequence = 0
let pollTimer: number | undefined
const observedJobStates = new Map<string, string>()
const transcriptionStages = new Set(['transcription', 'subtitle_generate', 'index'])

const alertTheme = computed<'info' | 'success' | 'warning' | 'error'>(() => {
  if (loadError.value || transcriptionFailed.value) return 'error'
  return 'info'
})

const bannerState = computed(() => getProcessingBannerState(status.value?.jobs || []))
const transcriptionProgress = computed(() => getTranscriptionProgress(status.value?.jobs || []))
const transcriptionFailed = computed(() => bannerState.value === 'transcription_failed')
const canRetryTranscription = computed(() => status.value?.retryable_job?.job_type ? transcriptionStages.has(status.value.retryable_job.job_type) && transcriptionFailed.value : false)

const message = computed(() => {
  if (loadError.value) return '内容处理状态加载失败'
  if (!status.value) return ''
  if (bannerState.value === 'transcription_failed') return '视频转写失败'
  if (bannerState.value === 'transcribing') return transcriptionProgress.value === undefined ? '视频转写中' : `视频转写中 ${transcriptionProgress.value}%`
  if (bannerState.value === 'ai_generating') return 'AI内容生成中'
  return ''
})

function shouldPoll(value: VideoProcessingStatus | null) {
  return shouldPollProcessingStatus(value)
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
    emit('processingStages', getProcessingStages(next.jobs))
    emit('processingFailures', getFailedProcessingStages(next.jobs))
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

defineExpose({ refresh })

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
  emit('processingStages', [])
  emit('processingFailures', [])
  void refresh()
})
onMounted(() => void refresh())
onBeforeUnmount(() => window.clearTimeout(pollTimer))
</script>

<style scoped>
.processing-status { margin-bottom: var(--td-comp-margin-l); }
</style>
