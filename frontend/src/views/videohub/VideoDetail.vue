<template>
  <main ref="page" class="video-detail-page">
    <div v-if="loading" class="video-detail-page__state"><t-loading text="正在加载视频" /></div>
    <div v-else-if="error" class="video-detail-page__state"><t-empty :description="error"><t-button @click="router.push('/platform/videos')">返回 Home</t-button></t-empty></div>
    <template v-else-if="video">
      <header class="video-detail-page__header">
        <t-button variant="text" @click="router.push('/platform/videos')">← 返回</t-button>
        <h1>{{ video.title }}</h1>
        <span class="video-detail-page__status">{{ statusLabel }}</span>
        <t-select v-model="selectedVideoId" :options="videoOptions" placeholder="切换视频" @change="switchVideo" />
      </header>
      <ProcessingStatus :video-id="video.id" @retry-started="handleRetryStarted" @stage-completed="handleStageCompleted" />
      <div v-if="!isPlayable" class="video-detail-page__state">
        <t-empty :description="statusHint">
          <template #action>
            <t-button @click="router.push('/platform/videos')">返回 Home</t-button>
          </template>
        </t-empty>
        <t-alert v-if="video.status === 'failed' && video.processing_error_summary" class="video-detail-page__error" theme="error" :message="video.processing_error_summary" />
      </div>
      <template v-else>
        <div class="video-detail-page__layout">
          <section class="video-detail-page__left">
            <VideoPlayer ref="player" :src="video.play_url || video.video_url" :poster="video.cover_url || video.poster_url" :duration-hint="video.durationSeconds" :subtitles="video.subtitles" @timeupdate="currentSeconds = $event" />
            <OverviewContent :content-state="content.overview" @reload="reloadOverview" />
            <ChapterNavigation :video="video" :current-seconds="currentSeconds" :content-state="content.outline" @reload="reloadOutline" @seek="seekTo" />
          </section>
          <aside class="video-detail-page__right">
            <t-tabs v-model="activeTab">
              <t-tab-panel value="summary" label="智能总结"><SmartSummary :key="video.id" :video="video" :content-state="content.summary" @reload="reloadSummary" @seek="seekTo" /></t-tab-panel>
              <t-tab-panel value="related" label="关联知识"><RelatedKnowledge :key="video.id" :video="video" :content-state="content.relatedKnowledge" @reload="reloadRelatedKnowledge" @seek="seekTo" @select-video-by-id="onSelectVideoById" /></t-tab-panel>
              <t-tab-panel value="transcript" label="完整文字稿"><TranscriptPageContent :key="video.id" :content-state="content.transcriptPage" @reload="reloadTranscriptPage" /></t-tab-panel>
            </t-tabs>
          </aside>
        </div>
      </template>
      <AiAssistant :current-video="video" :current-time="currentSeconds" @seek="seekTo" @navigate="navigateToEvidence" />
    </template>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { contentModuleForStage, createLoadingContentModuleState, createLoadingContentState, fetchVideoContent, fetchVideoContentModule, fetchVideoDetail, fetchVideoOptions, fetchVideoSubtitles, isVideoInitiallyAvailable, type VideoContentModule, type VideoContentState } from '@/api/videohub'
import type { VideoData } from '@/types/videohub'
import VideoPlayer from '@/components/videohub/VideoPlayer.vue'
import ChapterNavigation from '@/components/videohub/ChapterNavigation.vue'
import AiAssistant from '@/components/videohub/AiAssistant.vue'
import SmartSummary from '@/components/videohub/SmartSummary.vue'
import RelatedKnowledge from '@/components/videohub/RelatedKnowledge.vue'
import ProcessingStatus from '@/components/videohub/ProcessingStatus.vue'
import OverviewContent from '@/components/videohub/OverviewContent.vue'
import TranscriptPageContent from '@/components/videohub/TranscriptPageContent.vue'

const route = useRoute()
const router = useRouter()
const player = ref<InstanceType<typeof VideoPlayer> | null>(null)
const page = ref<HTMLElement | null>(null)
const video = ref<VideoData | null>(null)
const videoOptions = ref<Array<{ label: string; value: string }>>([])
const selectedVideoId = ref('')
const currentSeconds = ref(0)
const activeTab = ref('summary')
const loading = ref(true)
const error = ref('')
const content = ref<VideoContentState>(createLoadingContentState())
let loadSequence = 0
let contentSequence = 0
const moduleSequences: Record<VideoContentModule, number> = { outline: 0, overview: 0, summary: 0, relatedKnowledge: 0, transcriptPage: 0 }
const isPlayable = computed(() => Boolean(video.value && isVideoInitiallyAvailable({
  status: video.value.status,
  file_url: video.value.video_url,
  thumbnail_url: video.value.poster_url,
  initially_available: video.value.initiallyAvailable,
})))
const statusLabel = computed(() => {
  if (!video.value?.status) return '状态未知'
  const map: Record<string, string> = {
    uploading: '上传中',
    uploaded: '处理中',
    initializing: '处理中',
    ready: '可播放',
    processing: '处理中',
    completed: '可播放',
    failed: '处理失败',
  }
  return map[video.value.status] || video.value.status
})
const statusHint = computed(() => {
  if (!video.value) return ''
  if (video.value.status === 'failed') return video.value.processing_error_summary || '视频初始处理失败'
  if (video.value.status === 'uploaded' || video.value.status === 'initializing') return '视频正在生成封面和时长，播放入口已保留'
  return '视频尚未进入可播放状态'
})

async function loadVideo(id: string) {
  const sequence = ++loadSequence
  contentSequence++
  loading.value = true; error.value = ''; currentSeconds.value = 0
  try {
    const nextVideo = await fetchVideoDetail(id)
    if (sequence !== loadSequence) return
    video.value = nextVideo; selectedVideoId.value = id
    loading.value = false
    void loadSubtitles(nextVideo)
    content.value = createLoadingContentState()
    void loadContent(nextVideo)
    await nextTick()
    page.value?.scrollTo({ top: 0 })
    const querySeconds = Number(route.query.t)
    if (route.query.t !== undefined && Number.isFinite(querySeconds)) seekTo(Math.min(Math.max(querySeconds, 0), video.value.durationSeconds))
  }
  catch (reason) {
    if (sequence !== loadSequence) return
    video.value = null
    error.value = reason instanceof Error ? reason.message : '视频加载失败'
  }
  finally {
    if (sequence === loadSequence) loading.value = false
  }
}
async function loadSubtitles(videoData: VideoData) {
  if (!videoData.subtitle_file_url || video.value?.id !== videoData.id) return
  const subtitles = await fetchVideoSubtitles(videoData.subtitle_file_url)
  if (video.value?.id === videoData.id) video.value = { ...video.value, subtitles }
}
async function loadContent(videoData: VideoData) {
  const sequence = ++contentSequence
  for (const module of Object.keys(moduleSequences) as VideoContentModule[]) moduleSequences[module]++
  content.value = createLoadingContentState()
  const nextContent = await fetchVideoContent(videoData.id, videoData.durationSeconds, videoData.category)
  if (sequence === contentSequence && video.value?.id === videoData.id) content.value = nextContent
}
function markContentModuleLoading(module: VideoContentModule) {
  moduleSequences[module]++
  content.value = { ...content.value, [module]: createLoadingContentModuleState(module) } as VideoContentState
}
async function refreshContentModule(module: VideoContentModule, videoData: VideoData) {
  const sequence = ++moduleSequences[module]
  content.value = { ...content.value, [module]: createLoadingContentModuleState(module) } as VideoContentState
  const nextState = await fetchVideoContentModule(videoData.id, videoData.durationSeconds, videoData.category, module)
  if (sequence === moduleSequences[module] && video.value?.id === videoData.id) {
    content.value = { ...content.value, [module]: nextState } as VideoContentState
  }
}
function handleRetryStarted(stage: string) {
  const module = contentModuleForStage(stage)
  if (module === 'all') {
    for (const contentModule of Object.keys(moduleSequences) as VideoContentModule[]) moduleSequences[contentModule]++
    content.value = createLoadingContentState()
    contentSequence++
  } else if (module && video.value) {
    markContentModuleLoading(module)
  }
}
function handleStageCompleted(stage: string) {
  if (!video.value) return
  const module = contentModuleForStage(stage)
  if (module === 'all') void loadContent(video.value)
  else if (module) void refreshContentModule(module, video.value)
}
function reloadContentModule(module: VideoContentModule) {
  if (video.value) void refreshContentModule(module, video.value)
}
function reloadOutline() { reloadContentModule('outline') }
function reloadOverview() { reloadContentModule('overview') }
function reloadSummary() { reloadContentModule('summary') }
function reloadRelatedKnowledge() { reloadContentModule('relatedKnowledge') }
function reloadTranscriptPage() { reloadContentModule('transcriptPage') }
async function loadVideoOptions() {
  try {
    videoOptions.value = (await fetchVideoOptions()).map(item => ({ label: item.title, value: item.id }))
  } catch {
    // The detail view remains usable when the optional switcher list is unavailable.
    videoOptions.value = []
  }
}
function seekTo(seconds: number) { player.value?.seekTo(seconds) }
function navigateToEvidence(videoId: string, seconds: number) {
  if (videoId === video.value?.id) seekTo(seconds)
  else router.push(`/platform/videos/${videoId}?t=${seconds}`)
}
function onSelectVideoById(videoId: string, seconds: number) {
  if (videoId === video.value?.id) seekTo(seconds)
  else router.push(`/platform/videos/${videoId}?t=${seconds}`)
}
function switchVideo(value: string | number | Array<string | number>) { if (typeof value === 'string') router.push(`/platform/videos/${value}`) }
watch(() => route.params.videoId, value => { if (typeof value === 'string') loadVideo(value) })
onMounted(() => {
  void loadVideoOptions()
  if (typeof route.params.videoId === 'string') void loadVideo(route.params.videoId)
})
</script>

<style scoped>
.video-detail-page { height: 100%; overflow-y: auto; padding: 0 32px 40px; background: var(--td-bg-color-container); color: var(--td-text-color-primary); }
.video-detail-page__header { position: sticky; top: 0; z-index: 4; display: grid; grid-template-columns: auto 1fr auto minmax(220px, 280px); align-items: center; gap: 12px; margin: 0 -32px 24px; padding: 12px 32px; border-bottom: 1px solid var(--td-border-level-1-color); background: color-mix(in srgb, var(--td-bg-color-container) 92%, transparent); backdrop-filter: blur(8px); }
.video-detail-page__header h1 { margin: 0; overflow: hidden; color: var(--td-text-color-primary); font-size: 20px; font-weight: 400; line-height: 1.2; text-overflow: ellipsis; white-space: nowrap; }
.video-detail-page__status { justify-self: start; padding: 2px 8px; border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.5; white-space: nowrap; }
.video-detail-page__layout { display: grid; grid-template-columns: minmax(0, 3fr) minmax(320px, 2fr); gap: 24px; max-width: 1440px; margin: 0 auto; }
.video-detail-page__left { display: grid; align-content: start; gap: 16px; }
.video-detail-page__right { min-height: 480px; padding: 0 16px; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); }
.video-detail-page__state { min-height: 420px; display: grid; place-items: center; }
.video-detail-page__error { max-width: 640px; margin: 0 auto 24px; }
@media (max-width: 1050px) { .video-detail-page { padding: 0 20px 40px; }.video-detail-page__layout { grid-template-columns: 1fr; }.video-detail-page__header { grid-template-columns: auto 1fr auto; margin-right: -20px; margin-left: -20px; padding: 12px 20px; }.video-detail-page__header :deep(.t-select) { grid-column: 1 / -1; } }
</style>
