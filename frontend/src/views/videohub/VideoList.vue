<template>
  <main class="video-list-page">
    <header class="video-list-page__header">
      <div><h1>Home</h1><p>浏览组织内的视频知识</p></div>
      <div class="video-list-page__actions">
        <t-input v-model="query" clearable placeholder="搜索视频">
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-select v-model="sortBy" :options="sortOptions" />
        <t-button theme="primary" @click="openUpload">上传视频</t-button>
      </div>
    </header>
    <t-alert v-if="refreshError" theme="warning" :message="refreshError" class="video-list-page__alert" />
    <div v-if="loading" class="video-list-page__state"><t-loading text="正在加载视频" /></div>
    <t-empty v-else-if="filteredVideos.length === 0" :description="query ? '没有匹配的视频' : '还没有视频，上传第一个视频吧'">
      <template #action><t-button v-if="!query" @click="openUpload">上传视频</t-button></template>
    </t-empty>
    <section v-else class="video-list-page__grid">
      <VideoCard v-for="video in filteredVideos" :key="video.id" :video="video" @select="router.push(`/platform/videos/${video.id}`)" />
    </section>
    <UploadModal v-model:visible="uploadVisible" :after-upload="refreshVideos" />
    <AiAssistant v-if="playableVideos.length" :current-video="globalScopeVideo" :global-mode="true" @navigate="navigateToEvidence" />
  </main>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { fetchVideoDetail, fetchVideoList, isVideoInitiallyAvailable } from '@/api/videohub'
import type { VideoData } from '@/types/videohub'
import VideoCard from '@/components/videohub/VideoCard.vue'
import AiAssistant from '@/components/videohub/AiAssistant.vue'
import UploadModal from '@/components/videohub/UploadModal.vue'

const router = useRouter()
const videos = ref<VideoData[]>([])
const loading = ref(true)
const refreshError = ref('')
const uploadVisible = ref(false)
const query = ref('')
const sortBy = ref<'latest' | 'duration'>('latest')
const sortOptions = [{ label: '最新上传', value: 'latest' }, { label: '时长最长', value: 'duration' }]
const playableVideos = computed(() => videos.value.filter(video => isCoreAvailable(video)))
const filteredVideos = computed(() => videos.value
  .filter(video => video.title.toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase()))
  .sort((a, b) => sortBy.value === 'duration' ? b.durationSeconds - a.durationSeconds : b.created_at.localeCompare(a.created_at)))

const globalScopeVideo = computed<VideoData>(() => ({
  id: '__global__',
  title: '全部视频',
  category: 'general',
  categoryName: '通用分享',
  duration: '',
  durationSeconds: 0,
  created_at: '',
  video_url: '',
  overview: '全局问答上下文：包含知识库全部视频',
  chapters: [],
  subtitles: [],
}))

const ENHANCEMENT_POLL_INTERVAL_MS = 1500
const ENHANCEMENT_POLL_TIMEOUT_MS = 10 * 60 * 1000
let enhancementPollTimer: number | undefined
let enhancementPollDeadline = 0

function openUpload() { uploadVisible.value = true }

function isCoreAvailable(video: VideoData): boolean {
  return isVideoInitiallyAvailable({
    status: video.status,
    file_url: video.video_url,
    thumbnail_url: video.poster_url,
    initially_available: video.initiallyAvailable,
  })
}

function needsEnhancement(video: VideoData): boolean {
  if (video.status === 'failed' || !isCoreAvailable(video)) return false
  return video.status === 'uploaded'
    || video.status === 'initializing'
    || !video.poster_url
    || video.durationSeconds <= 0
}

function mergeVideo(video: VideoData) {
  const index = videos.value.findIndex(item => item.id === video.id)
  if (index < 0) {
    videos.value = [video, ...videos.value]
    return
  }
  videos.value = videos.value.map(item => item.id === video.id ? video : item)
}

function replaceVideos(next: VideoData[], fallback?: VideoData) {
  const byId = new Map(next.map(video => [video.id, video]))
  if (fallback && isCoreAvailable(fallback) && !byId.has(fallback.id)) byId.set(fallback.id, fallback)
  videos.value = Array.from(byId.values()).sort((left, right) => right.created_at.localeCompare(left.created_at))
}

function scheduleEnhancementPolling() {
  if (enhancementPollTimer !== undefined) return
  const candidates = videos.value.filter(needsEnhancement)
  if (!candidates.length) return
  if (!enhancementPollDeadline || enhancementPollDeadline < Date.now()) {
    enhancementPollDeadline = Date.now() + ENHANCEMENT_POLL_TIMEOUT_MS
  }
  enhancementPollTimer = window.setTimeout(async () => {
    enhancementPollTimer = undefined
    if (Date.now() >= enhancementPollDeadline) return
    await Promise.all(candidates.map(async video => {
      try {
        mergeVideo(await fetchVideoDetail(video.id))
      } catch {
        // Keep the core list item visible and retry on the next poll.
      }
    }))
    scheduleEnhancementPolling()
  }, ENHANCEMENT_POLL_INTERVAL_MS)
}

async function refreshVideos(uploaded?: VideoData) {
  if (uploaded && isCoreAvailable(uploaded)) mergeVideo(uploaded)
  try {
    const next = await fetchVideoList()
    replaceVideos(next, uploaded)
    refreshError.value = ''
    scheduleEnhancementPolling()
  } catch (error) {
    refreshError.value = uploaded
      ? '视频已上传，但列表刷新失败；已保留可播放条目，请重试同步或刷新页面'
      : '视频列表加载失败，请刷新页面重试'
    if (uploaded && isCoreAvailable(uploaded)) {
      mergeVideo(uploaded)
      scheduleEnhancementPolling()
    }
    throw error
  }
}

function navigateToEvidence(videoId: string, seconds: number) { router.push(`/platform/videos/${videoId}?t=${seconds}`) }
onMounted(async () => {
  try {
    await refreshVideos()
  } catch {
    // The alert explains the failure; keep the page usable for a manual retry/upload.
  } finally {
    loading.value = false
  }
})
onBeforeUnmount(() => {
  if (enhancementPollTimer !== undefined) window.clearTimeout(enhancementPollTimer)
})
</script>

<style scoped>
.video-list-page { height: 100%; overflow-y: auto; padding: calc(var(--td-comp-margin-s) * 3) calc(var(--td-comp-margin-s) * 3) calc(var(--td-comp-margin-s) * 14); background: var(--td-bg-color-container); color: var(--td-text-color-primary); }
.video-list-page__header { display: flex; align-items: flex-end; justify-content: space-between; gap: calc(var(--td-comp-margin-s) * 3); margin: 0 auto calc(var(--td-comp-margin-s) * 3); max-width: 1440px; }
.video-list-page__alert { max-width: 1440px; margin: 0 auto calc(var(--td-comp-margin-s) * 2); }
.video-list-page h1 { margin: 0; color: var(--td-text-color-primary); font-family: -apple-system, "system-ui", BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif; font-size: var(--td-font-size-headline-medium); font-weight: 400; line-height: var(--td-line-height-headline-medium); }
.video-list-page p { margin: calc(var(--td-comp-margin-s) / 2) 0 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-medium); line-height: var(--td-line-height-body-medium); }
.video-list-page__actions { display: grid; grid-template-columns: minmax(220px, 300px) 144px auto; gap: var(--td-comp-margin-s); align-items: center; }
.video-list-page__actions :deep(.t-input), .video-list-page__actions :deep(.t-select-input) { border-radius: var(--td-radius-medium); }
.video-list-page__grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: calc(var(--td-comp-margin-s) * 2); max-width: 1440px; margin: 0 auto; }
.video-list-page__state { min-height: 320px; display: grid; place-items: center; }
@media (max-width: 900px) { .video-list-page { padding: calc(var(--td-comp-margin-s) * 3) calc(var(--td-comp-margin-s) * 2.5) calc(var(--td-comp-margin-s) * 5); }.video-list-page__header { align-items: stretch; flex-direction: column; }.video-list-page__actions { grid-template-columns: 1fr; } }
</style>
