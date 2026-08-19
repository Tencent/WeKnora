<template>
  <main class="video-detail-page">
    <div v-if="loading" class="video-detail-page__state"><t-loading text="正在加载视频" /></div>
    <div v-else-if="error" class="video-detail-page__state"><t-empty :description="error"><t-button @click="router.push('/platform/videos')">返回 Home</t-button></t-empty></div>
    <template v-else-if="video">
      <header class="video-detail-page__header">
        <t-button variant="text" @click="router.push('/platform/videos')">← 返回</t-button>
        <h1>{{ video.title }}</h1>
        <t-select v-model="selectedVideoId" :options="videoOptions" placeholder="切换视频" @change="switchVideo" />
      </header>
      <div class="video-detail-page__layout">
        <section class="video-detail-page__left">
          <VideoPlayer ref="player" :src="video.video_url" :poster="video.poster_url" :duration-hint="video.durationSeconds" :subtitles="video.subtitles" @timeupdate="currentSeconds = $event" />
          <ChapterNavigation :chapters="video.chapters" :current-seconds="currentSeconds" @seek="seekTo" />
        </section>
        <aside class="video-detail-page__right">
          <t-tabs v-model="activeTab">
            <t-tab-panel value="summary" label="智能总结"><PlaceholderView title="智能总结" /></t-tab-panel>
            <t-tab-panel value="related" label="关联知识"><PlaceholderView title="关联知识" /></t-tab-panel>
          </t-tabs>
        </aside>
      </div>
    </template>
  </main>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { fetchVideoDetail, fetchVideoOptions } from '@/api/videohub'
import type { VideoData } from '@/types/videohub'
import VideoPlayer from '@/components/videohub/VideoPlayer.vue'
import ChapterNavigation from '@/components/videohub/ChapterNavigation.vue'
import PlaceholderView from '@/components/videohub/PlaceholderView.vue'

const route = useRoute()
const router = useRouter()
const player = ref<InstanceType<typeof VideoPlayer> | null>(null)
const video = ref<VideoData | null>(null)
const videoOptions = ref<Array<{ label: string; value: string }>>([])
const selectedVideoId = ref('')
const currentSeconds = ref(0)
const activeTab = ref('summary')
const loading = ref(true)
const error = ref('')

async function loadVideo(id: string) {
  loading.value = true; error.value = ''; currentSeconds.value = 0
  try { video.value = await fetchVideoDetail(id); selectedVideoId.value = id }
  catch (reason) { video.value = null; error.value = reason instanceof Error ? reason.message : '视频加载失败' }
  finally { loading.value = false }
}
function seekTo(seconds: number) { player.value?.seekTo(seconds) }
function switchVideo(value: string | number | Array<string | number>) { if (typeof value === 'string') router.push(`/platform/videos/${value}`) }
watch(() => route.params.videoId, value => { if (typeof value === 'string') loadVideo(value) })
onMounted(async () => {
  videoOptions.value = (await fetchVideoOptions()).map(item => ({ label: item.title, value: item.id }))
  if (typeof route.params.videoId === 'string') await loadVideo(route.params.videoId)
})
</script>

<style scoped>
.video-detail-page { height: 100%; overflow-y: auto; padding: 0 32px 40px; background: var(--td-bg-color-container); color: var(--td-text-color-primary); }
.video-detail-page__header { position: sticky; top: 0; z-index: 4; display: grid; grid-template-columns: auto 1fr minmax(220px, 280px); align-items: center; gap: 12px; margin: 0 -32px 24px; padding: 12px 32px; border-bottom: 1px solid var(--td-border-level-1-color); background: color-mix(in srgb, var(--td-bg-color-container) 92%, transparent); backdrop-filter: blur(8px); }
.video-detail-page__header h1 { margin: 0; overflow: hidden; color: var(--td-text-color-primary); font-size: 20px; font-weight: 400; line-height: 1.2; text-overflow: ellipsis; white-space: nowrap; }
.video-detail-page__layout { display: grid; grid-template-columns: minmax(0, 3fr) minmax(320px, 2fr); gap: 24px; max-width: 1440px; margin: 0 auto; }
.video-detail-page__left { display: grid; align-content: start; gap: 16px; }
.video-detail-page__right { min-height: 480px; padding: 0 16px; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); }
.video-detail-page__state { min-height: 420px; display: grid; place-items: center; }
@media (max-width: 1050px) { .video-detail-page { padding: 0 20px 40px; }.video-detail-page__layout { grid-template-columns: 1fr; }.video-detail-page__header { grid-template-columns: auto 1fr; margin-right: -20px; margin-left: -20px; padding: 12px 20px; }.video-detail-page__header :deep(.t-select) { grid-column: 1 / -1; } }
</style>
