<template>
  <section class="chapters" aria-label="视频章节">
    <div class="chapters__heading">
      <div>
        <h2>章节大纲</h2>
        <p>全课共 {{ chapters.length }} 个关键章节</p>
      </div>
      <button v-if="chapters.length" class="chapters__collapse-all" type="button" @click="toggleAll">
        {{ allExpanded ? '全部折叠' : '全部展开' }}
      </button>
    </div>
    <div v-if="loading" class="chapters__state"><t-loading text="正在加载章节" /></div>
    <t-alert v-else-if="error" class="chapters__state" theme="error" :message="error">
      <template #operation><t-button size="small" variant="outline" @click="load">刷新</t-button></template>
    </t-alert>
    <t-empty v-else-if="chapters.length === 0" :description="notGenerated ? '章节尚未生成' : '暂无章节'">
      <template #action><t-button size="small" variant="outline" @click="load">刷新</t-button></template>
    </t-empty>
    <div v-else class="chapters__list">
      <article v-for="chapter in chapters" :key="chapter.id" :ref="el => setChapterRef(chapter.id, el)" :class="['chapter', { 'chapter--active': chapter.id === activeChapterId, 'chapter--collapsed': !isExpanded(chapter.id) }]">
        <div class="chapter__header">
          <button class="chapter__main" type="button" @click="$emit('seek', chapter.start_seconds)">
            <span class="chapter__index">{{ chapter.chapter_index }}</span>
            <span class="chapter__title">{{ chapter.chapter_title }}</span>
            <span class="chapter__time">{{ chapter.start_time }}–{{ chapter.end_time }}</span>
          </button>
          <button class="chapter__toggle" type="button" :aria-label="isExpanded(chapter.id) ? '折叠章节' : '展开章节'" :aria-expanded="isExpanded(chapter.id)" @click="toggleChapter(chapter.id)">
            <span :class="['chapter__chevron', { 'chapter__chevron--collapsed': !isExpanded(chapter.id) }]" aria-hidden="true" />
          </button>
        </div>
        <div v-if="isExpanded(chapter.id)" class="chapter__body">
          <button v-if="chapter.chapter_summary" class="chapter__summary" type="button" @click="$emit('seek', chapter.start_seconds)">
            <strong>本章核心内容</strong>
            <span>{{ chapter.chapter_summary }}</span>
          </button>
          <div v-if="chapter.knowledge_points.length" class="chapter__points" aria-label="关键知识点">
            <span class="chapter__points-label">关键知识点</span>
            <div class="chapter__point-list">
              <button v-for="point in chapter.knowledge_points" :key="point.id" :class="['chapter__point', { 'chapter__point--active': point.id === activeKnowledgePointId }]" type="button" @click="$emit('seek', point.seconds)">
                <span class="chapter__point-title">{{ point.title }}</span>
                <span class="chapter__point-time">{{ point.timestamp }}</span>
              </button>
            </div>
          </div>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import type { Chapter, ContentState, VideoData } from '@/types/videohub'

const props = defineProps<{ video: VideoData; currentSeconds: number; contentState: ContentState<Chapter[]> }>()
const emit = defineEmits<{ seek: [seconds: number]; reload: [] }>()
const chapters = computed(() => props.contentState.data)
const loading = computed(() => props.contentState.status === 'loading')
const error = computed(() => props.contentState.status === 'error' ? props.contentState.error || '章节加载失败' : '')
const notGenerated = computed(() => props.contentState.status === 'not_generated')
const chapterRefs = new Map<string, HTMLElement>()
const expandedChapterIds = ref(new Set<string>())
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

const allExpanded = computed(() => chapters.value.length > 0 && chapters.value.every(chapter => expandedChapterIds.value.has(chapter.id)))

function isExpanded(id: string) { return expandedChapterIds.value.has(id) }

function toggleChapter(id: string) {
  const next = new Set(expandedChapterIds.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expandedChapterIds.value = next
}

function toggleAll() {
  expandedChapterIds.value = allExpanded.value ? new Set() : new Set(chapters.value.map(chapter => chapter.id))
}

function setChapterRef(id: string, el: Element | ComponentPublicInstance | null) {
  if (el instanceof HTMLElement) chapterRefs.set(id, el)
}

watch(activeChapterId, async id => {
  await nextTick()
  if (id) chapterRefs.get(id)?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
})
watch(chapters, value => {
  const ids = new Set(value.map(chapter => chapter.id))
  expandedChapterIds.value = expandedChapterIds.value.size ? new Set([...expandedChapterIds.value].filter(id => ids.has(id))) : ids
}, { immediate: true })
watch(() => props.video.id, () => chapterRefs.clear())
</script>

<style scoped>
.chapters { overflow: hidden; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); }
.chapters__heading { display: flex; align-items: center; justify-content: space-between; min-height: 68px; padding: 14px 20px; border-bottom: 1px solid var(--td-border-level-1-color); }
.chapters__heading h2 { margin: 0; color: var(--td-text-color-primary); font-size: 18px; font-weight: 600; line-height: 1.5; }
.chapters__heading p { margin: 2px 0 0; color: var(--td-text-color-secondary); font-size: 12px; line-height: 1.67; }
.chapters__collapse-all { padding: 4px 0; border: 0; background: transparent; color: var(--td-brand-color); font-size: 12px; cursor: pointer; }
.chapters__collapse-all:hover { color: var(--td-brand-color-hover); }
.chapters__state, .chapters > :deep(.t-empty) { min-height: 180px; display: grid; place-items: center; }
.chapters__list { max-height: 600px; overflow: auto; padding: 16px 20px 20px; }
.chapter { margin-bottom: 12px; overflow: hidden; border: 1px solid var(--td-border-level-1-color); border-radius: var(--td-radius-extraLarge); background: var(--td-bg-color-container); transition: border-color .15s ease, box-shadow .15s ease; }
.chapter:last-child { margin-bottom: 0; }
.chapter:hover, .chapter--active { border-color: var(--td-brand-color-light); box-shadow: 0 2px 8px color-mix(in srgb, var(--td-brand-color) 8%, transparent); }
.chapter__header { display: flex; align-items: stretch; min-height: 62px; background: var(--td-bg-color-secondarycontainer); }
.chapter--active .chapter__header { background: var(--td-brand-color-light); }
.chapter__main { min-width: 0; flex: 1; display: grid; grid-template-columns: 36px minmax(0, 1fr) auto; align-items: center; gap: 12px; padding: 14px 12px 14px 16px; border: 0; background: transparent; color: var(--td-text-color-primary); text-align: left; cursor: pointer; }
.chapter__index { display: grid; width: 30px; height: 30px; place-items: center; border-radius: var(--td-radius-medium); background: var(--td-brand-color-light); color: var(--td-brand-color); font-size: 14px; font-weight: 600; }
.chapter__title { min-width: 0; overflow: hidden; font-size: 16px; font-weight: 600; line-height: 1.5; text-overflow: ellipsis; white-space: nowrap; }
.chapter__time { padding: 4px 8px; border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); font-size: 12px; font-weight: 600; line-height: 1.5; white-space: nowrap; }
.chapter__toggle { width: 48px; border: 0; border-left: 1px solid var(--td-border-level-1-color); background: transparent; cursor: pointer; }
.chapter__chevron { display: inline-block; width: 9px; height: 9px; border-top: 1.5px solid var(--td-text-color-secondary); border-left: 1.5px solid var(--td-text-color-secondary); transform: rotate(45deg); transition: transform .15s ease; }
.chapter__chevron--collapsed { transform: rotate(225deg); }
.chapter__body { padding: 12px 20px 18px; }
.chapter__summary { width: 100%; display: grid; gap: 6px; margin: 0 0 14px; padding: 14px 16px; border: 1px solid var(--td-warning-color-3); border-radius: var(--td-radius-large); background: var(--td-warning-color-1); color: var(--td-text-color-primary); text-align: left; cursor: pointer; }
.chapter__summary strong { color: var(--td-warning-color-7); font-size: 13px; line-height: 1.5; }
.chapter__summary span { color: var(--td-text-color-secondary); font-size: 14px; line-height: 1.6; }
.chapter__points { display: grid; gap: 8px; }
.chapter__points-label { color: var(--td-text-color-secondary); font-size: 13px; font-weight: 600; line-height: 1.5; }
.chapter__point-list { display: flex; flex-wrap: wrap; gap: 8px; }
.chapter__point { display: inline-flex; align-items: center; gap: 8px; padding: 6px 10px; border: 1px solid var(--td-brand-color-light); border-radius: var(--td-radius-medium); background: var(--td-brand-color-light); color: var(--td-brand-color); font-size: 13px; line-height: 1.5; text-align: left; cursor: pointer; }
.chapter__point:hover, .chapter__point--active { border-color: var(--td-brand-color); background: var(--td-brand-color); color: var(--td-text-color-anti); }
.chapter__point-title { min-width: 0; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.chapter__point-time { color: inherit; font-size: 12px; font-weight: 600; opacity: .85; }
@media (max-width: 680px) { .chapters__heading { padding: 12px 16px; }.chapters__list { padding: 12px; }.chapter__main { grid-template-columns: 30px minmax(0, 1fr); gap: 8px; padding-left: 12px; }.chapter__time { grid-column: 2; justify-self: start; }.chapter__body { padding: 10px 12px 14px; }.chapter__title { font-size: 14px; } }
</style>
