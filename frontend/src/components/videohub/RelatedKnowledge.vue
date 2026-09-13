<template>
  <div class="related-knowledge">
    <t-alert v-if="processingFailure" class="related-knowledge__failure" theme="error" message="关联知识生成失败">
      <template #operation><t-button size="small" variant="outline" :loading="props.isRetrying" @click="emit('retry')">重试</t-button></template>
    </t-alert>
    <div v-if="showSkeleton" class="related-knowledge__skeleton" aria-busy="true" aria-label="正在生成关联知识">
      <t-skeleton v-for="n in 4" :key="`related-skeleton-${n}`" animation="gradient" :row-col="[{ width: '44%', height: '18px' }, { width: '90%', height: '14px' }, { width: '72%', height: '14px' }]" />
    </div>
    <t-alert v-else-if="error && !processingFailure" class="related-knowledge__state" theme="error" :message="error">
      <template #operation><t-button size="small" variant="outline" @click="load(video.id)">刷新</t-button></template>
    </t-alert>
    <t-empty v-else-if="!overview && !processingFailure" :description="notGenerated ? '关联知识尚未生成' : '暂无关联知识'">
      <template #action><t-button size="small" variant="outline" @click="load(video.id)">刷新</t-button></template>
    </t-empty>
    <template v-else-if="overview">
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
import { computed, nextTick, ref } from 'vue'
import './filterTabs.css'
import KnowledgeAnchorCard from './KnowledgeAnchorCard.vue'
import RelationOverviewCard from './RelationOverviewCard.vue'
import { KNOWLEDGE_TYPES, KNOWLEDGE_TYPE_STYLES } from './knowledgeTypeStyles'
import type { ContentState, CrossVideoKnowledgeItem, CurrentKnowledgeAnchor, KnowledgeType, RelationOverview, VideoData } from '@/types/videohub'

const props = withDefaults(defineProps<{ video: VideoData; contentState: ContentState<{ videoId: string; overview: RelationOverview | null; anchors: CurrentKnowledgeAnchor[]; crossVideoItems: CrossVideoKnowledgeItem[] }>; isGenerating?: boolean; isProcessingFailed?: boolean; isRetrying?: boolean }>(), { isGenerating: false, isProcessingFailed: false, isRetrying: false })
const emit = defineEmits<{ seek: [seconds: number]; reload: []; retry: []; selectVideoById: [videoId: string, seconds: number] }>()
const selectedType = ref<KnowledgeType | 'all'>('all')
const expandedAnchorId = ref<string | null>(null)
const anchorList = ref<HTMLElement | null>(null)
const loading = computed(() => props.contentState.status === 'loading')
const error = computed(() => props.contentState.status === 'error' ? props.contentState.error || '关联知识加载失败' : '')
const notGenerated = computed(() => props.contentState.status === 'not_generated')
const processingFailure = computed(() => props.isProcessingFailed)
const overview = computed(() => props.contentState.data.overview)
const anchors = computed(() => props.contentState.data.anchors)
const crossVideoItems = computed(() => props.contentState.data.crossVideoItems)
const hasContent = computed(() => Boolean(overview.value || anchors.value.length || crossVideoItems.value.length))
const showSkeleton = computed(() => !processingFailure.value && !hasContent.value && (loading.value || props.isGenerating))

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
</script>

<style scoped>
.related-knowledge { display: grid; gap: 14px; height: min(760px, calc(100vh - 150px)); min-height: 0; padding: 16px 4px 96px 0; overflow: hidden; }
.related-knowledge__state, .related-knowledge > :deep(.t-empty) { min-height: 320px; display: grid; place-items: center; }
.related-knowledge__skeleton { display: grid; gap: 18px; min-height: 320px; padding: 16px 8px; }
.related-knowledge__skeleton :deep(.t-skeleton) { display: grid; gap: 8px; }
.related-knowledge__anchors { min-height: 0; overflow-y: auto; padding-right: 10px; scroll-behavior: smooth; scrollbar-width: thin; scrollbar-color: color-mix(in srgb, var(--td-text-color-secondary) 28%, transparent) transparent; }
.related-knowledge__relation-empty { margin: 0; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
</style>
