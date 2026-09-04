<template>
  <main ref="page" class="video-detail-page">
    <div v-if="loading" class="video-detail-page__state"><t-loading text="正在加载视频" /></div>
    <div v-else-if="error" class="video-detail-page__state"><t-empty :description="error"><t-button @click="router.push('/platform/videos')">返回 Home</t-button></t-empty></div>
    <template v-else-if="video">
      <header class="video-detail-page__header">
        <t-button class="video-detail-page__back" variant="text" @click="router.push('/platform/videos')">
          <template #icon><t-icon name="chevron-left" /></template>
          返回列表
        </t-button>
        <div class="video-detail-page__title">
          <span class="video-detail-page__title-mark" aria-hidden="true"><t-icon name="play" /></span>
          <h1>{{ video.title }}</h1>
          <span class="video-detail-page__status">{{ statusLabel }}</span>
          <span v-if="video.categoryName" class="video-detail-page__category">{{ video.categoryName }}</span>
        </div>
        <t-select class="video-detail-page__switcher" v-model="selectedVideoId" :options="videoOptions" placeholder="切换视频" aria-label="切换视频" @change="switchVideo" />
      </header>
      <ProcessingStatus v-if="showProcessingStatus" :video-id="video.id" @retry-started="handleRetryStarted" @stage-completed="handleStageCompleted" />
      <div v-if="!isPlayable" class="video-detail-page__state">
        <t-empty :description="statusHint">
          <template #action>
            <t-button @click="router.push('/platform/videos')">返回 Home</t-button>
          </template>
        </t-empty>
        <t-alert v-if="video.status === 'failed' && video.processing_error_summary" class="video-detail-page__error" theme="error" :message="video.processing_error_summary" />
      </div>
      <template v-else>
        <div ref="layout" class="video-detail-page__layout">
          <section ref="left" class="video-detail-page__left">
            <VideoPlayer ref="player" :src="video.play_url || video.video_url" :poster="video.cover_url || video.poster_url" :title="video.title" :chapter-label="currentChapterLabel" :duration-hint="video.durationSeconds" :subtitles="video.subtitles" @timeupdate="currentSeconds = $event" />
            <ChapterNavigation :video="video" :current-seconds="currentSeconds" :content-state="content.outline" @reload="reloadOutline" @seek="seekTo" />
          </section>
          <aside ref="right" class="video-detail-page__right">
            <t-tabs v-model="activeTab">
              <t-tab-panel value="summary">
                <template #label><span class="video-detail-page__tab-label"><span>智能总结</span></span></template>
                <SmartSummary :key="video.id" :video="video" :content-state="content.summary" @reload="reloadSummary" @seek="seekTo" />
              </t-tab-panel>
              <t-tab-panel v-if="showRelatedKnowledgeTab" value="related">
                <template #label><span class="video-detail-page__tab-label"><span>关联知识</span></span></template>
                <RelatedKnowledge :key="video.id" :video="video" :content-state="content.relatedKnowledge" @reload="reloadRelatedKnowledge" @seek="seekTo" @select-video-by-id="onSelectVideoById" />
              </t-tab-panel>
            </t-tabs>
          </aside>
        </div>
      </template>
      <AiAssistant :current-video="video" :current-time="currentSeconds" @seek="seekTo" @navigate="navigateToEvidence" />
    </template>
  </main>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { contentModuleForStage, createLoadingContentModuleState, createLoadingContentState, fetchVideoContent, fetchVideoContentModule, fetchVideoDetail, fetchVideoOptions, fetchVideoSubtitles, isVideoInitiallyAvailable, shouldShowRelatedKnowledgeTab, type VideoContentModule, type VideoContentState } from '@/api/videohub'
import type { VideoData } from '@/types/videohub'
import VideoPlayer from '@/components/videohub/VideoPlayer.vue'
import ChapterNavigation from '@/components/videohub/ChapterNavigation.vue'
import AiAssistant from '@/components/videohub/AiAssistant.vue'
import SmartSummary from '@/components/videohub/SmartSummary.vue'
import RelatedKnowledge from '@/components/videohub/RelatedKnowledge.vue'
import ProcessingStatus from '@/components/videohub/ProcessingStatus.vue'

const route = useRoute()
const router = useRouter()
const player = ref<InstanceType<typeof VideoPlayer> | null>(null)
const page = ref<HTMLElement | null>(null)
const layout = ref<HTMLElement | null>(null)
const left = ref<HTMLElement | null>(null)
const right = ref<HTMLElement | null>(null)
const video = ref<VideoData | null>(null)
const videoOptions = ref<Array<{ label: string; value: string }>>([])
const selectedVideoId = ref('')
const currentSeconds = ref(0)
const activeTab = ref('related')
const loading = ref(true)
const error = ref('')
const content = ref<VideoContentState>(createLoadingContentState())
let heightObserver: ResizeObserver | null = null
let observedLeft: HTMLElement | null = null
let heightFrame = 0
let loadSequence = 0
let contentSequence = 0
const moduleSequences: Record<VideoContentModule, number> = { outline: 0, summary: 0, relatedKnowledge: 0, transcriptPage: 0 }
const showRelatedKnowledgeTab = computed(() => shouldShowRelatedKnowledgeTab(content.value.relatedKnowledge))
const showProcessingStatus = computed(() => Boolean(video.value?.status && !['completed', 'ready'].includes(video.value.status)))
const currentChapterLabel = computed(() => {
  const chapter = content.value.outline.data.find(item => currentSeconds.value >= item.start_seconds && currentSeconds.value < item.end_seconds)
  return chapter ? `正在播放：${chapter.chapter_index} ${chapter.chapter_title}` : ''
})
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

function syncRightHeight() {
  const layoutElement = layout.value
  const leftElement = left.value
  const rightElement = right.value
  if (!layoutElement || !leftElement || !rightElement) return

  if (window.matchMedia('(max-width: 1050px)').matches) {
    rightElement.style.removeProperty('height')
    return
  }

  const leftHeight = Math.ceil(leftElement.getBoundingClientRect().height)
  rightElement.style.height = `${leftHeight}px`
}

function scheduleRightHeightSync() {
  if (heightFrame) cancelAnimationFrame(heightFrame)
  heightFrame = requestAnimationFrame(() => {
    heightFrame = 0
    syncRightHeight()
  })
}

function observeLeftElement(element: HTMLElement | null) {
  if (!heightObserver || element === observedLeft) return
  if (observedLeft) heightObserver.unobserve(observedLeft)
  observedLeft = element
  if (observedLeft) heightObserver.observe(observedLeft)
  scheduleRightHeightSync()
}

async function loadVideo(id: string) {
  const sequence = ++loadSequence
  contentSequence++
  loading.value = true; error.value = ''; currentSeconds.value = 0; activeTab.value = 'related'
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
function reloadSummary() { reloadContentModule('summary') }
function reloadRelatedKnowledge() { reloadContentModule('relatedKnowledge') }
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
watch(showRelatedKnowledgeTab, visible => {
  if (!visible && activeTab.value === 'related') activeTab.value = 'summary'
  else if (visible && activeTab.value === 'summary') activeTab.value = 'related'
})
watch(left, observeLeftElement, { flush: 'post' })
watch([isPlayable, () => content.value.outline.data.length], scheduleRightHeightSync, { flush: 'post' })
onMounted(() => {
  heightObserver = new ResizeObserver(scheduleRightHeightSync)
  observeLeftElement(left.value)
  window.addEventListener('resize', scheduleRightHeightSync)
  scheduleRightHeightSync()
  void loadVideoOptions()
  if (typeof route.params.videoId === 'string') void loadVideo(route.params.videoId)
})
onBeforeUnmount(() => {
  if (heightFrame) cancelAnimationFrame(heightFrame)
  heightObserver?.disconnect()
  window.removeEventListener('resize', scheduleRightHeightSync)
})
</script>

<style scoped>
.video-detail-page { position: relative; isolation: isolate; box-sizing: border-box; height: 100%; min-height: 0; overflow-y: auto; padding: 0 30px 104px; background: linear-gradient(116deg, rgba(213,224,226,.28) 0%, rgba(255,255,255,.1) 34%, rgba(215,231,219,.06) 68%, rgba(255,255,255,.12) 100%), repeating-linear-gradient(90deg, rgba(255,255,255,.06) 0, rgba(255,255,255,.06) 1px, transparent 1px, transparent 92px), linear-gradient(118deg, #edf1f1 0%, #f3f5f4 37%, #edf1ef 68%, #f0f3f1 100%); background-attachment: local, local, fixed; color: var(--td-text-color-primary); }
.video-detail-page__header { position: sticky; top: 0; z-index: 4; display: flex; align-items: center; gap: 16px; min-height: 72px; margin: 0 -30px 24px; padding: 0 30px; border-bottom: 1px solid rgba(48,59,65,.12); background: rgba(255,255,255,.28); backdrop-filter: blur(20px) saturate(180%); }
.video-detail-page__back { flex: none; padding: 0 !important; color: var(--td-text-color-secondary) !important; font-size: var(--td-font-size-body-small) !important; }
.video-detail-page__title { display: flex; align-items: center; gap: 10px; min-width: 0; padding-left: 16px; border-left: 1px solid var(--td-component-stroke); }
.video-detail-page__title-mark { display: grid; flex: none; width: 24px; height: 24px; place-items: center; border-radius: var(--td-radius-medium); background: var(--td-brand-color); color: var(--td-text-color-anti); font-size: 14px; }
.video-detail-page__header h1 { min-width: 0; margin: 0; overflow: hidden; color: var(--td-text-color-primary); font-size: var(--td-font-size-title-medium); font-weight: 400; line-height: 1.5; text-overflow: ellipsis; white-space: nowrap; }
.video-detail-page__status { flex: none; padding: 3px 8px; border: 1px solid color-mix(in srgb, var(--td-brand-color) 20%, transparent); border-radius: var(--td-radius-large); background: var(--td-brand-color-light); color: var(--td-brand-color); font-size: var(--td-font-size-body-small); line-height: 1.5; white-space: nowrap; }
.video-detail-page__category { flex: none; overflow: hidden; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); text-overflow: ellipsis; white-space: nowrap; }
.video-detail-page__switcher { width: 26px; margin-left: auto; overflow: hidden; }
.video-detail-page__switcher :deep(.t-input) { min-width: 26px; padding: 0; border: 0; background: transparent; }
.video-detail-page__switcher :deep(.t-input__inner) { display: none; }
.video-detail-page__switcher :deep(.t-input__suffix) { margin: 0; color: var(--td-text-color-secondary); }
.video-detail-page__layout { display: grid; grid-template-columns: minmax(0, 54fr) minmax(340px, 46fr); align-items: start; gap: 28px; max-width: 1440px; margin: 0 auto; overflow: visible; }
.video-detail-page__left { display: block; min-width: 0; align-self: start; }
.video-detail-page__left > :deep(.chapters) { margin-top: 22px; }
.video-detail-page__right { display: flex; min-width: 0; min-height: 0; align-self: start; flex-direction: column; box-sizing: border-box; overflow: hidden; padding: 0 16px 16px 20px; border: 1px solid rgba(255,255,255,.56); border-radius: var(--td-radius-extraLarge); background: rgba(255,255,255,.12); backdrop-filter: blur(14px) saturate(112%); }
.video-detail-page__right :deep(.t-tabs) { display: flex; height: 100%; min-height: 0; flex: 1 1 auto; flex-direction: column; background: transparent; }
.video-detail-page__right :deep(.t-tabs__nav) { margin: 0; border-bottom: 1px solid color-mix(in srgb, var(--td-component-stroke) 58%, transparent); }
.video-detail-page__right :deep(.t-tabs__bar) { display: none; }
.video-detail-page__right :deep(.t-tabs__nav-item) { position: relative; height: 42px; padding: 0 14px; color: var(--td-text-color-secondary); font-size: 16px; transition: color .15s ease, background-color .15s ease; }
.video-detail-page__right :deep(.t-tabs__nav-item:hover) { background: color-mix(in srgb, var(--td-bg-color-container) 34%, transparent); color: var(--td-text-color-primary); }
.video-detail-page__right :deep(.t-tabs__nav-item.t-is-active) { color: var(--td-brand-color); font-weight: 600; }
.video-detail-page__right :deep(.t-tabs__nav-item.t-is-active)::after { position: absolute; right: auto; bottom: 0; left: 50%; width: calc(100% - 28px); height: 2px; border-radius: var(--td-radius-small); background: var(--td-brand-color); content: ''; transform: translateX(-50%); }
.video-detail-page__tab-label { display: inline-flex; align-items: center; gap: 0; font-size: 16px; }
.video-detail-page__right :deep(.t-tabs__operations) { display: none; }
.video-detail-page__right :deep(.t-tabs__content) { min-height: 0; flex: 1 1 auto; overflow-y: auto; overflow-x: hidden; background: transparent; }
.video-detail-page__state { min-height: 420px; display: grid; place-items: center; }
.video-detail-page__error { max-width: 640px; margin: 0 auto 24px; }
@media (max-width: 1050px) { .video-detail-page { padding: 0 20px 104px; }.video-detail-page__header { margin-right: -20px; margin-left: -20px; padding: 0 20px; }.video-detail-page__layout { display: block; }.video-detail-page__right { height: auto !important; min-height: 520px; margin-top: 28px; padding-left: 0; overflow: visible; }.video-detail-page__right :deep(.t-tabs), .video-detail-page__right :deep(.t-tabs__content) { height: auto; overflow: visible; }.video-detail-page__switcher { margin-left: 0; } }
@media (max-width: 680px) { .video-detail-page__header { gap: 10px; }.video-detail-page__back { width: 32px; overflow: hidden; white-space: nowrap; }.video-detail-page__back :deep(.t-button__text) { display: none; }.video-detail-page__title { gap: 7px; padding-left: 10px; }.video-detail-page__category { display: none; }.video-detail-page__status { padding: 2px 6px; font-size: 11px; }.video-detail-page__switcher { flex: none; } }
@media (max-width: 420px) { .video-detail-page { padding-right: 12px; padding-left: 12px; }.video-detail-page__header { margin-right: -12px; margin-left: -12px; padding-right: 12px; padding-left: 12px; }.video-detail-page__title-mark { width: 22px; height: 22px; }.video-detail-page__title { gap: 6px; padding-left: 8px; }.video-detail-page__header h1 { font-size: var(--td-font-size-body-large); }.video-detail-page__status { padding: 1px 5px; font-size: 10px; }.video-detail-page__right { border-right: 0; border-left: 0; border-radius: var(--td-radius-large); padding-right: 8px; } }
</style>
