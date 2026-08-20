<template>
  <main class="video-list-page">
    <header class="video-list-page__header">
      <div><h1>Home</h1><p>浏览组织内的视频知识</p></div>
      <div class="video-list-page__actions">
        <t-input v-model="query" clearable placeholder="搜索视频" />
        <t-select v-model="sortBy" :options="sortOptions" />
        <t-button @click="openUpload">上传视频</t-button>
      </div>
    </header>
    <div v-if="loading" class="video-list-page__state"><t-loading text="正在加载视频" /></div>
    <t-empty v-else-if="filteredVideos.length === 0" :description="query ? '没有匹配的视频' : '还没有视频，上传第一个视频吧'">
      <template #action><t-button v-if="!query" @click="openUpload">上传视频</t-button></template>
    </t-empty>
    <section v-else class="video-list-page__grid">
      <VideoCard v-for="video in filteredVideos" :key="video.id" :video="video" @select="router.push(`/platform/videos/${video.id}`)" />
    </section>
    <UploadModal v-model:visible="uploadVisible" :after-upload="refreshVideos" />
    <AiAssistant v-if="videos.length" :current-video="globalScopeVideo" :global-mode="true" @navigate="navigateToEvidence" />
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { fetchVideoList } from '@/api/videohub'
import type { VideoData } from '@/types/videohub'
import VideoCard from '@/components/videohub/VideoCard.vue'
import AiAssistant from '@/components/videohub/AiAssistant.vue'
import UploadModal from '@/components/videohub/UploadModal.vue'

const router = useRouter()
const videos = ref<VideoData[]>([])
const loading = ref(true)
const uploadVisible = ref(false)
const query = ref('')
const sortBy = ref<'latest' | 'duration'>('latest')
const sortOptions = [{ label: '最新上传', value: 'latest' }, { label: '时长最长', value: 'duration' }]
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

function openUpload() { uploadVisible.value = true }
async function refreshVideos() { videos.value = await fetchVideoList() }
function navigateToEvidence(videoId: string, seconds: number) { router.push(`/platform/videos/${videoId}?t=${seconds}`) }
onMounted(async () => { try { await refreshVideos() } finally { loading.value = false } })
</script>

<style scoped>
.video-list-page { height: 100%; overflow-y: auto; padding: 32px 32px 112px; background: var(--td-bg-color-container); color: var(--td-text-color-primary); }
.video-list-page__header { display: flex; align-items: flex-end; justify-content: space-between; gap: 24px; margin: 0 auto 24px; max-width: 1440px; }
.video-list-page h1 { margin: 0; color: var(--td-text-color-primary); font-size: 28px; font-weight: 400; line-height: 1.29; }
.video-list-page p { margin: 8px 0 0; color: var(--td-text-color-secondary); font-size: 14px; line-height: 1.57; }
.video-list-page__actions { display: grid; grid-template-columns: minmax(220px, 300px) 144px auto; gap: 8px; align-items: center; }
.video-list-page__actions :deep(.t-input), .video-list-page__actions :deep(.t-select-input) { border-radius: var(--td-radius-medium); }
.video-list-page__grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 16px; max-width: 1440px; margin: 0 auto; }
.video-list-page__state { min-height: 320px; display: grid; place-items: center; }
@media (max-width: 900px) { .video-list-page { padding: 24px 20px 40px; }.video-list-page__header { align-items: stretch; flex-direction: column; }.video-list-page__actions { grid-template-columns: 1fr; } }
</style>
