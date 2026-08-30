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
      <t-alert v-if="hasUntranscribedTail" theme="warning" class="chapters__notice" :message="`章节导航覆盖至 ${lastChapterEndTime}；视频尾部暂无可用转写内容`" />
      <article v-for="chapter in chapters" :key="chapter.id" :ref="el => setChapterRef(chapter.id, el)" :class="['chapter', { 'chapter--active': chapter.id === activeChapterId, 'chapter--pending': chapter.alignment_status === 'pending_alignment' }]">
        <button class="chapter__main" type="button" @click="$emit('seek', chapter.start_seconds)">
          <span class="chapter__index">{{ chapter.chapter_index }}</span>
          <span class="chapter__content">
            <strong>{{ chapter.chapter_title }}</strong>
            <small>{{ chapter.start_time }} – {{ chapter.end_time }}</small>
            <span v-if="chapter.chapter_summary" class="chapter__summary">
              <em>本章核心内容</em>
              {{ chapter.chapter_summary }}
            </span>
          </span>
          <span v-if="chapter.alignment_status === 'pending_alignment'" class="chapter__alignment" title="章节时间待校准">待校准</span>
          <span v-if="chapter.id === activeChapterId" class="chapter__wave" aria-label="当前播放章节"><i /><i /><i /></span>
        </button>
        <div v-if="chapter.knowledge_points.length" class="chapter__points" aria-label="关键知识点">
          <button v-for="point in chapter.knowledge_points" :key="point.id" :class="['chapter__point', { 'chapter__point--active': point.id === activeKnowledgePointId }]" type="button" @click="$emit('seek', point.seconds)">
            <span class="chapter__point-title">{{ point.title }}</span>
            <span class="chapter__point-time">{{ point.timestamp }}</span>
          </button>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import { formatTimestamp } from '@/api/videohub/contentParsing'
import type { Chapter, ContentState, VideoData } from '@/types/videohub'

const props = defineProps<{ video: VideoData; currentSeconds: number; contentState: ContentState<Chapter[]> }>()
const emit = defineEmits<{ seek: [seconds: number]; reload: [] }>()
const chapters = computed(() => props.contentState.data)
const loading = computed(() => props.contentState.status === 'loading')
const error = computed(() => props.contentState.status === 'error' ? props.contentState.error || '章节加载失败' : '')
const notGenerated = computed(() => props.contentState.status === 'not_generated')
const lastChapterEndSeconds = computed(() => chapters.value[chapters.value.length - 1]?.end_seconds ?? 0)
const lastChapterEndTime = computed(() => formatTimestamp(lastChapterEndSeconds.value))
const hasUntranscribedTail = computed(() => props.video.durationSeconds > 0 && lastChapterEndSeconds.value < props.video.durationSeconds - 1)
const chapterRefs = new Map<string, HTMLElement>()
const activeChapterId = computed(() => chapters.value.find(chapter => props.currentSeconds >= chapter.start_seconds && props.currentSeconds < chapter.end_seconds)?.id)
const activeKnowledgePointId = computed(() => {
  const activeChapter = chapters.value.find(chapter => chapter.id === activeChapterId.value)
  if (!activeChapter) return undefined
  return activeChapter.knowledge_points.reduce<string | undefined>((activeId, point) => {
    if (point.seconds <= props.currentSeconds) return point.id
    return activeId
  }, undefined)
})

function load() { emit('reload') }

function setChapterRef(id: string, el: Element | ComponentPublicInstance | null) {
  if (el instanceof HTMLElement) chapterRefs.set(id, el)
}

watch(activeChapterId, async id => {
  await nextTick()
  if (id) chapterRefs.get(id)?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
})
watch(() => props.video.id, () => chapterRefs.clear())
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
.chapter__main { width: 100%; display: grid; grid-template-columns: 28px 1fr auto auto; gap: 8px; padding: 12px; border: 0; background: transparent; color: var(--td-text-color-primary); text-align: left; cursor: pointer; }
.chapter__index { color: var(--td-brand-color); font-weight: 600; }
.chapter__content { display: grid; gap: 4px; }
.chapter__content strong { font-size: 14px; font-weight: 600; line-height: 1.57; }
.chapter__content small, .chapter__content > span { color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.67; }
.chapter__summary { display: grid; gap: 2px; }
.chapter__summary em { color: var(--td-text-color-primary); font-size: 11px; font-style: normal; font-weight: 600; }
.chapter__alignment { align-self: start; color: var(--td-error-color); font-size: 11px; line-height: 1.67; white-space: nowrap; }
.chapter__points { display: grid; gap: 2px; padding: 0 12px 12px 48px; }
.chapter__point { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; padding: 6px 8px; border: 0; border-radius: var(--td-radius-medium); background: transparent; color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.67; text-align: left; cursor: pointer; }
.chapter__point:hover, .chapter__point--active { background: var(--td-bg-color-component-hover); color: var(--td-text-color-primary); }
.chapter__point--active { box-shadow: inset 2px 0 var(--td-brand-color); }
.chapter__point-title { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
.chapter__point-time { flex: 0 0 auto; color: var(--td-brand-color); font-size: 12px; font-weight: 600; }
.chapter__wave { height: 18px; display: flex; align-items: center; gap: 2px; }
.chapter__wave i { width: 2px; height: 7px; border-radius: var(--td-radius-round); background: var(--td-brand-color); animation: wave .8s ease-in-out infinite alternate; }
.chapter__wave i:nth-child(2) { animation-delay: .2s; }.chapter__wave i:nth-child(3) { animation-delay: .4s; }
@keyframes wave { to { height: 17px; } }
</style>
