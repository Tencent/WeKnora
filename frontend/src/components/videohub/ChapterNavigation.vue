<template>
  <section class="chapters" aria-label="视频章节">
    <div class="chapters__heading">
      <div class="chapters__heading-copy">
        <h2>快速导航</h2>
        <p>共 {{ chapters.length }} 章节</p>
      </div>
      <button v-if="chapters.length" class="chapters__collapse-all" type="button" :aria-label="allExpanded ? '全部折叠' : '全部展开'" :title="allExpanded ? '全部折叠' : '全部展开'" @click="toggleAll">
        <t-icon :name="allExpanded ? 'chevron-up' : 'chevron-down'" />
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
          <button class="chapter__toggle" type="button" :aria-label="isExpanded(chapter.id) ? '折叠章节' : '展开章节'" :aria-expanded="isExpanded(chapter.id)" :title="isExpanded(chapter.id) ? '折叠章节' : '展开章节'" @click.stop="toggleChapter(chapter.id)">
            <t-icon :name="isExpanded(chapter.id) ? 'chevron-up' : 'chevron-down'" />
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
.chapters { min-width: 0; height: auto; max-height: none; overflow: hidden; border: 1px solid rgba(255,255,255,.88); border-radius: var(--td-radius-extraLarge); background: transparent; }
.chapters__heading { display: flex; align-items: center; justify-content: space-between; min-height: 34px; margin: 0 0 12px; padding: 12px 16px; }
.chapters__heading-copy { display: flex; align-items: baseline; gap: 8px; min-width: 0; white-space: nowrap; }
.chapters__heading h2 { margin: 0; color: var(--td-text-color-primary); font-size: 16px; font-weight: 600; line-height: 1.5; }
.chapters__heading p { margin: 0; color: var(--td-text-color-placeholder); font-size: var(--td-font-size-body-small); line-height: 1.67; }
.chapters__collapse-all, .chapter__toggle { display: grid; place-items: center; width: 28px; height: 28px; padding: 0; border: 0; border-radius: var(--td-radius-medium); background: transparent; color: var(--td-text-color-secondary); cursor: pointer; }
.chapters__collapse-all:hover, .chapter__toggle:hover { background: var(--td-bg-color-container-hover); color: var(--td-brand-color); }
.chapters__state, .chapters > :deep(.t-empty) { min-height: 180px; display: grid; place-items: center; }
.chapters__list { height: auto; max-height: none; overflow: visible; padding: 0 4px 96px 0; }
.chapter { margin: 0; padding: 13px 12px; border: 1px solid transparent; border-bottom-color: color-mix(in srgb, var(--td-component-stroke) 72%, transparent); background: transparent; transition: border-color .15s ease, background-color .15s ease; }
.chapter:first-child { border-top-color: color-mix(in srgb, var(--td-component-stroke) 72%, transparent); }
.chapter:last-child { margin-bottom: 0; }
.chapter--collapsed + .chapter--collapsed { margin-top: 8px; }
.chapter--active { border-color: color-mix(in srgb, var(--td-brand-color) 14%, transparent); border-radius: var(--td-radius-large); background: color-mix(in srgb, var(--td-brand-color-light) 60%, transparent); }
.chapter:not(.chapter--active):hover { border-color: rgba(255,255,255,.82); border-radius: var(--td-radius-large); background: rgba(255,255,255,.34); }
.chapter__header { display: flex; align-items: stretch; gap: 6px; }
.chapter__main { min-width: 0; flex: 1; display: grid; grid-template-columns: 27px minmax(0, 1fr) auto; align-items: center; gap: 9px; padding: 0; border: 0; background: transparent; color: var(--td-text-color-primary); text-align: left; cursor: pointer; }
.chapter__index { display: grid; width: 27px; height: 23px; place-items: center; border-radius: var(--td-radius-medium); background: color-mix(in srgb, var(--td-bg-color-container) 72%, transparent); color: var(--td-text-color-secondary); font-family: var(--app-font-family-mono, monospace); font-size: 11px; font-weight: 600; }
.chapter--active .chapter__index { background: var(--td-brand-color); color: var(--td-text-color-anti); }
.chapter__title { min-width: 0; overflow: hidden; font-size: 14px; font-weight: 500; line-height: 1.57; text-overflow: ellipsis; white-space: nowrap; }
.chapter__time { color: var(--td-text-color-placeholder); font-family: var(--app-font-family-mono, monospace); font-size: var(--td-font-size-body-small); font-weight: 400; white-space: nowrap; }
.chapter__body { padding: 9px 0 0 36px; }
.chapter__summary { width: 100%; display: grid; gap: 5px; margin: 0 0 8px; padding: 0; border: 0; background: transparent; color: var(--td-text-color-secondary); font-size: 14px; text-align: left; cursor: pointer; }
.chapter__summary strong { color: inherit; font-size: 0; line-height: 0; }
.chapter__summary span { color: var(--td-text-color-secondary); font-size: 14px; line-height: 1.55; }
.chapter__points { display: grid; gap: 7px; }
.chapter__points-label { display: none; }
.chapter__point-list { display: grid; gap: 7px; }
.chapter__point { display: flex; align-items: center; gap: 8px; width: 100%; padding: 3px 6px; border: 0; border-radius: var(--td-radius-medium); background: transparent; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); line-height: 1.55; text-align: left; cursor: pointer; transition: background-color .15s ease, color .15s ease; }
.chapter__point::before { content: ''; width: 6px; height: 6px; flex: none; border-radius: 50%; background: color-mix(in srgb, var(--td-brand-color) 52%, transparent); }
.chapter__point:hover { background: color-mix(in srgb, var(--td-brand-color-light) 64%, transparent); color: var(--td-brand-color); }
.chapter__point--active { color: var(--td-text-color-primary); }
.chapter__point:hover .chapter__point-time { color: var(--td-brand-color); }
.chapter__point--active::before { background: var(--td-brand-color); }
.chapter__point-title { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.chapter__point-time { margin-left: auto; color: var(--td-text-color-secondary); font-family: var(--app-font-family-mono, monospace); font-size: 11px; }
@media (max-width: 680px) { .chapters__list { padding-right: 0; }.chapter__main { grid-template-columns: 27px minmax(0, 1fr); }.chapter__time { grid-column: 2; justify-self: start; }.chapter__body { padding-left: 36px; } }
</style>
