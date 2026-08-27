<template>
  <section class="chapters" aria-label="视频章节">
    <div class="chapters__heading"><h2>章节导航</h2><span>{{ chapters.length }} 章</span></div>
    <div v-if="loading" class="chapters__state"><t-loading text="正在加载章节" /></div>
    <t-alert v-else-if="error" class="chapters__state" theme="error" :message="error">
      <template #operation><t-button size="small" variant="outline" @click="load">刷新</t-button></template>
    </t-alert>
    <t-empty v-else-if="chapters.length === 0" :description="notGenerated ? '章节尚未生成' : '暂无章节'">
      <template #action><t-button size="small" variant="outline" @click="load">刷新</t-button></template>
    </t-empty>
    <div v-else class="chapters__list">
      <article v-for="chapter in chapters" :key="chapter.id" :ref="el => setChapterRef(chapter.id, el)" :class="['chapter', { 'chapter--active': chapter.id === activeChapterId }]">
        <button class="chapter__main" type="button" @click="$emit('seek', chapter.start_seconds)">
          <span class="chapter__index">{{ chapter.chapter_index }}</span>
          <span class="chapter__content">
            <strong>{{ chapter.chapter_title }}</strong>
            <small>{{ chapter.start_time }} – {{ chapter.end_time }}</small>
            <span>{{ chapter.chapter_summary }}</span>
          </span>
          <span v-if="chapter.id === activeChapterId" class="chapter__wave" aria-label="当前播放章节"><i /><i /><i /></span>
        </button>
        <div class="chapter__points">
          <button v-for="point in chapter.knowledge_points" :key="point.id" type="button" @click="$emit('seek', point.seconds)">
            <span>{{ point.timestamp }}</span>{{ point.title }}
          </button>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import { fetchOutline } from '@/api/videohub/outline'
import type { Chapter, VideoData } from '@/types/videohub'

const props = defineProps<{ video: VideoData; currentSeconds: number }>()
defineEmits<{ seek: [seconds: number] }>()
const chapters = ref<Chapter[]>([])
const loading = ref(true)
const error = ref('')
const notGenerated = ref(false)
const chapterRefs = new Map<string, HTMLElement>()
const activeChapterId = computed(() => chapters.value.find(chapter => props.currentSeconds >= chapter.start_seconds && props.currentSeconds < chapter.end_seconds)?.id)

async function load() {
  const videoId = props.video.id
  loading.value = true
  error.value = ''
  notGenerated.value = false
  chapterRefs.clear()
  try {
    const next = await fetchOutline(videoId, props.video.durationSeconds)
    if (props.video.id !== videoId) return
    chapters.value = next
  } catch (reason: any) {
    if (props.video.id !== videoId) return
    chapters.value = []
    if (reason?.status === 404) notGenerated.value = true
    else error.value = reason?.message || '章节加载失败'
  } finally {
    if (props.video.id === videoId) loading.value = false
  }
}

function setChapterRef(id: string, el: Element | ComponentPublicInstance | null) {
  if (el instanceof HTMLElement) chapterRefs.set(id, el)
}

watch(activeChapterId, async id => {
  await nextTick()
  if (id) chapterRefs.get(id)?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
})
watch(() => props.video.id, () => void load(), { immediate: true })
</script>

<style scoped>
.chapters { overflow: hidden; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); }
.chapters__heading { display: flex; align-items: center; justify-content: space-between; min-height: 48px; padding: 12px 16px; border-bottom: 1px solid var(--td-border-level-1-color); }
.chapters__heading h2 { margin: 0; color: var(--td-text-color-primary); font-size: 16px; font-weight: 400; line-height: 1.5; }
.chapters__heading span { color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.67; }
.chapters__state, .chapters > :deep(.t-empty) { min-height: 180px; display: grid; place-items: center; }
.chapters__list { max-height: 430px; overflow: auto; padding: 8px; }
.chapter { position: relative; margin-bottom: 4px; border-radius: var(--td-radius-medium); background: var(--td-bg-color-container); transition: background-color .15s ease; }
.chapter:hover { background: var(--td-bg-color-container-hover); }
.chapter::before { position: absolute; inset: 0 auto 0 0; width: 3px; border-radius: var(--td-radius-round); background: transparent; content: ''; }
.chapter--active { background: var(--td-brand-color-light); }
.chapter--active::before { background: var(--td-brand-color); }
.chapter__main { width: 100%; display: grid; grid-template-columns: 28px 1fr auto; gap: 8px; padding: 12px; border: 0; background: transparent; color: var(--td-text-color-primary); text-align: left; cursor: pointer; }
.chapter__index { color: var(--td-brand-color); font-weight: 600; }
.chapter__content { display: grid; gap: 4px; }
.chapter__content strong { font-size: 14px; font-weight: 600; line-height: 1.57; }
.chapter__content small, .chapter__content > span { color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.67; }
.chapter__points { display: grid; gap: 2px; padding: 0 12px 12px 48px; }
.chapter__points button { padding: 6px 8px; border: 0; border-radius: var(--td-radius-medium); background: transparent; color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.67; text-align: left; cursor: pointer; }
.chapter__points button:hover { background: var(--td-bg-color-component-hover); color: var(--td-text-color-primary); }
.chapter__points span { display: inline-block; margin-right: 8px; color: var(--td-brand-color); font-size: 12px; font-weight: 600; }
.chapter__wave { height: 18px; display: flex; align-items: center; gap: 2px; }
.chapter__wave i { width: 2px; height: 7px; border-radius: var(--td-radius-round); background: var(--td-brand-color); animation: wave .8s ease-in-out infinite alternate; }
.chapter__wave i:nth-child(2) { animation-delay: .2s; }.chapter__wave i:nth-child(3) { animation-delay: .4s; }
@keyframes wave { to { height: 17px; } }
</style>
