<template>
  <div class="related-knowledge">
    <div v-if="isOfflineEval" class="related-knowledge__fixture" role="status"><t-icon name="file-paste" /><span><strong>离线测评数据</strong> · 14 个 Wiki 页面，未写入 WeKnora</span></div>
    <div v-if="loading" class="related-knowledge__state"><t-loading text="正在加载关联知识" /></div>
    <t-alert v-else-if="error" class="related-knowledge__state" theme="error" :message="error">
      <template #operation><t-button size="small" variant="outline" @click="load(video.id)">刷新</t-button></template>
    </t-alert>
    <t-empty v-else-if="!overview" :description="notGenerated ? '关联知识尚未生成' : '暂无关联知识'">
      <template #action><t-button size="small" variant="outline" @click="load(video.id)">刷新</t-button></template>
    </t-empty>
    <template v-else>
      <RelationOverviewCard :overview="overview" :knowledge-count="anchors.length" />
      <nav class="videohub-filter-tabs" aria-label="关联知识类型筛选">
        <button v-for="tab in visibleTabs" :key="tab.value" type="button" :class="{ 'is-active': selectedType === tab.value }" @click="selectedType = tab.value">
          <span>{{ tab.label }}</span><small>{{ tab.count }}</small>
        </button>
      </nav>
      <p v-if="anchors.length && !crossVideoItems.length" class="related-knowledge__relation-empty">当前暂无跨视频关联</p>
      <div v-if="filteredAnchors.length" ref="anchorList" class="related-knowledge__anchors">
        <KnowledgeAnchorCard
          v-for="anchor in filteredAnchors"
          :key="anchor.id"
          :anchor="anchor"
          :expanded="expandedAnchorId === anchor.id"
          @seek="emit('seek', $event)"
          @toggle="toggleAnchor(anchor.id)"
          @select-knowledge="selectKnowledge"
        />
      </div>
      <t-empty v-else :description="anchors.length ? '当前类型暂无锚点' : '暂无锚点'" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import './filterTabs.css'
import KnowledgeAnchorCard from './KnowledgeAnchorCard.vue'
import RelationOverviewCard from './RelationOverviewCard.vue'
import { KNOWLEDGE_TYPES, KNOWLEDGE_TYPE_STYLES } from './knowledgeTypeStyles'
import type { ContentState, CrossVideoKnowledgeItem, CurrentKnowledgeAnchor, KnowledgeType, RelationOverview, VideoData } from '@/types/videohub'
import { isAiLearningEvalFixtureEnabled } from '@/api/videohub/fixtures/aiLearningEval'

const props = defineProps<{ video: VideoData; contentState: ContentState<{ videoId: string; overview: RelationOverview | null; anchors: CurrentKnowledgeAnchor[]; crossVideoItems: CrossVideoKnowledgeItem[] }> }>()
const emit = defineEmits<{ seek: [seconds: number]; reload: []; selectVideoById: [videoId: string, seconds: number] }>()
const selectedType = ref<KnowledgeType | 'all'>('all')
const expandedAnchorId = ref<string | null>(null)
const anchorList = ref<HTMLElement | null>(null)
const isOfflineEval = isAiLearningEvalFixtureEnabled()
const loading = computed(() => props.contentState.status === 'loading')
const error = computed(() => props.contentState.status === 'error' ? props.contentState.error || '关联知识加载失败' : '')
const notGenerated = computed(() => props.contentState.status === 'not_generated')
const overview = computed(() => props.contentState.data.overview)
const anchors = computed(() => props.contentState.data.anchors)
const crossVideoItems = computed(() => props.contentState.data.crossVideoItems)

const typeCounts = computed(() => Object.fromEntries(KNOWLEDGE_TYPES.map(type => [type,
  anchors.value.filter(anchor => anchor.knowledge_type === type).length,
])) as Record<KnowledgeType, number>)
const visibleTabs = computed(() => [
  { value: 'all' as const, label: '全部', count: anchors.value.length },
  ...KNOWLEDGE_TYPES.filter(type => typeCounts.value[type] > 0).map(type => ({ value: type, label: KNOWLEDGE_TYPE_STYLES[type].label, count: typeCounts.value[type] })),
])
const filteredAnchors = computed(() => selectedType.value === 'all' ? anchors.value : anchors.value.filter(anchor => anchor.knowledge_type === selectedType.value))
function toggleAnchor(anchorId: string) { expandedAnchorId.value = expandedAnchorId.value === anchorId ? null : anchorId }
async function selectKnowledge(anchorId: string) {
  selectedType.value = 'all'
  expandedAnchorId.value = anchorId
  await nextTick()
  anchorList.value?.querySelector<HTMLElement>(`#knowledge-anchor-${anchorId}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}
function load(_videoId?: string) {
  selectedType.value = 'all'
  emit('reload')
}
watch(anchors, value => {
  if (isOfflineEval && value.length && !expandedAnchorId.value) expandedAnchorId.value = value[0].id
}, { immediate: true })
</script>

<style scoped>
.related-knowledge { display: grid; gap: 14px; height: min(760px, calc(100vh - 150px)); min-height: 0; padding: 16px 4px 96px 0; overflow: hidden; }
.related-knowledge__fixture { display: flex; align-items: center; gap: 8px; padding: 9px 11px; border: 1px solid color-mix(in srgb, var(--td-brand-color) 18%, transparent); border-radius: var(--td-radius-medium); background: color-mix(in srgb, var(--td-brand-color-light) 36%, transparent); color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.related-knowledge__fixture strong { color: var(--td-brand-color); font-weight: 600; }
.related-knowledge__state, .related-knowledge > :deep(.t-empty) { min-height: 320px; display: grid; place-items: center; }
.related-knowledge__anchors { min-height: 0; overflow-y: auto; padding-right: 10px; scroll-behavior: smooth; scrollbar-width: thin; scrollbar-color: color-mix(in srgb, var(--td-text-color-secondary) 28%, transparent) transparent; }
.related-knowledge__relation-empty { margin: 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
</style>
