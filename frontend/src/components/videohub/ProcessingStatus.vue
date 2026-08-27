<template>
  <t-alert class="processing-status" :theme="alertTheme" :message="message">
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
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { fetchVideoProcessingStatus, retryVideoProcessingStage } from '@/api/videohub'
import type { VideoProcessingStatus } from '@/types/videohub'

const props = defineProps<{ videoId: string }>()
const emit = defineEmits<{ retried: [] }>()

const status = ref<VideoProcessingStatus | null>(null)
const loadError = ref('')
const retrying = ref(false)
let requestSequence = 0
let pollTimer: number | undefined

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
    emit('retried')
    await refresh()
  } catch (reason: any) {
    loadError.value = reason?.message || '重试失败，请稍后再试'
  } finally {
    retrying.value = false
  }
}

watch(() => props.videoId, () => {
  status.value = null
  void refresh()
})
onMounted(() => void refresh())
onBeforeUnmount(() => window.clearTimeout(pollTimer))
</script>

<style scoped>
.processing-status { margin-bottom: var(--td-comp-margin-l); }
</style>
