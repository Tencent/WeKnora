<template>
  <main class="video-list-page">
    <header class="video-list-page__header">
      <div><h1>Home</h1><p class="video-list-page__description">Explore the knowledge within your videos.</p></div>
      <div class="video-list-page__actions">
        <t-input v-model="query" class="video-list-page__search" clearable placeholder="搜索视频">
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-button class="video-list-page__upload" @click="openUpload">
          <template #icon><t-icon name="upload" /></template>
          上传视频
        </t-button>
      </div>
    </header>
    <t-alert v-if="refreshError" theme="warning" :message="refreshError" class="video-list-page__alert" />
    <div v-if="loading" class="video-list-page__state"><t-loading text="正在加载视频" /></div>
    <t-empty v-else-if="filteredVideos.length === 0" :description="query ? '没有匹配的视频' : '还没有视频，上传第一个视频吧'">
      <template #action><t-button v-if="!query" class="video-list-page__upload" @click="openUpload"><template #icon><t-icon name="upload" /></template>上传视频</t-button></template>
    </t-empty>
    <template v-else>
      <div class="video-list-page__summary">
        <span><strong>全部视频</strong><span aria-hidden="true"> · </span>{{ filteredVideos.length }} 个</span>
      </div>
      <section class="video-list-page__grid">
      <VideoCard
        v-for="video in filteredVideos"
        :key="video.id"
        :video="video"
        @select="router.push(`/platform/videos/${video.id}`)"
        @delete="confirmDelete(video)"
      />
      </section>
      <div class="video-list-page__footer">已展示全部视频</div>
    </template>
    <UploadModal v-model:visible="uploadVisible" :after-upload="refreshVideos" />
    <AiAssistant v-if="playableVideos.length" :current-video="globalScopeVideo" :global-mode="true" @navigate="navigateToEvidence" />
  </main>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { deleteVideo, fetchVideoDetail, fetchVideoList, isVideoInitiallyAvailable } from '@/api/videohub'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
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
const playableVideos = computed(() => videos.value.filter(video => isCoreAvailable(video)))
const filteredVideos = computed(() => videos.value
  .filter(video => video.title.toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase()))
  .sort((a, b) => b.created_at.localeCompare(a.created_at)))

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
function confirmDelete(video: VideoData) {
  const dialog = DialogPlugin.confirm({
    header: '删除视频',
    body: `确定删除“${video.title}”吗？删除后将从视频列表中移除。`,
    theme: 'warning',
    confirmBtn: { content: '删除', theme: 'danger' },
    cancelBtn: '取消',
    onConfirm: async () => {
      try {
        await deleteVideo(video.id)
        videos.value = videos.value.filter(item => item.id !== video.id)
        MessagePlugin.success('视频已删除')
      } catch (error: any) {
        MessagePlugin.error(error?.message || '视频删除失败，请重试')
      } finally {
        dialog.destroy()
      }
    },
  })
}

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
.video-list-page { position: relative; isolation: isolate; box-sizing: border-box; height: 100%; min-height: 0; overflow-y: auto; padding: 18px 42px 52px; color: var(--td-text-color-primary); background: linear-gradient(116deg, rgba(213,224,226,.28) 0%, rgba(255,255,255,.1) 34%, rgba(215,231,219,.06) 68%, rgba(255,255,255,.12) 100%), repeating-linear-gradient(90deg, rgba(255,255,255,.06) 0, rgba(255,255,255,.06) 1px, transparent 1px, transparent 92px), linear-gradient(118deg, #edf1f1 0%, #f3f5f4 37%, #edf1ef 68%, #f0f3f1 100%); background-attachment: local, local, fixed; font-family: var(--app-font-family); }
.video-list-page::before { content: none; }
.video-list-page__header { display: flex; align-items: flex-end; justify-content: space-between; gap: 24px; margin: 0 auto; padding: 8px 0 16px; max-width: 1400px; }
.video-list-page__alert { max-width: 1400px; margin: 0 auto 16px; }
.video-list-page h1 { margin: 0; color: var(--td-text-color-primary); font-size: 32px; font-weight: 400; line-height: 1.2; }
.video-list-page p { margin: 8px 0 0; color: var(--td-text-color-secondary); font-size: 14px; line-height: 1.57; }
.video-list-page__description { font-family: "Arial Narrow", Arial, sans-serif; }
.video-list-page__actions { display: flex; align-items: center; gap: 10px; }
.video-list-page__search { width: 260px; }
.video-list-page__actions :deep(.t-input) { height: 42px; border: 1px solid rgba(255, 255, 255, .82); border-radius: var(--td-radius-large); background: rgba(255,255,255,.28); backdrop-filter: blur(20px) saturate(180%); -webkit-backdrop-filter: blur(20px) saturate(180%); }
.video-list-page__actions :deep(.t-input:hover), .video-list-page__actions :deep(.t-input.t-is-focused) { border-color: color-mix(in srgb, var(--td-brand-color) 42%, var(--td-component-stroke)); }
.video-list-page__upload { height: 42px; padding: 0 17px; border: 1px solid var(--td-brand-color); border-radius: var(--td-radius-large); color: var(--td-text-color-anti); background: var(--td-brand-color); }
.video-list-page__upload:hover { border-color: var(--td-brand-color-hover); background: var(--td-brand-color-hover); }
.video-list-page__summary { display: flex; align-items: center; justify-content: space-between; gap: 18px; min-height: 42px; margin: 0 auto 12px; max-width: 1400px; padding: 0 2px; color: var(--td-text-color-secondary); font-size: 12px; }
.video-list-page__summary strong { color: var(--td-text-color-primary); font-weight: 600; }
.video-list-page__grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 18px; max-width: 1400px; margin: 0 auto; }
.video-list-page__footer { display: flex; align-items: center; justify-content: center; gap: 8px; margin: 30px auto 0; color: var(--td-text-color-placeholder); font-size: 11px; }
.video-list-page__footer::before, .video-list-page__footer::after { content: ""; width: 42px; height: 1px; background: color-mix(in srgb, var(--td-text-color-secondary) 18%, transparent); }
.video-list-page__state { min-height: 320px; display: grid; place-items: center; }
@media (max-width: 980px) { .video-list-page { padding: 8px 26px 44px; }.video-list-page__grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 680px) { .video-list-page { padding: 4px 16px 44px; }.video-list-page__header { align-items: stretch; flex-direction: column; padding: 26px 0 16px; gap: 20px; }.video-list-page__actions { width: 100%; }.video-list-page__search { flex: 1; width: auto; }.video-list-page__grid { grid-template-columns: 1fr; gap: 14px; }.video-list-page__summary { min-height: 38px; margin-bottom: 8px; } }
</style>
