<template>
  <div class="related-knowledge">
    <div v-if="loading" class="related-knowledge__state"><t-loading text="正在加载关联知识" /></div>
    <t-alert v-else-if="error" class="related-knowledge__state" theme="error" :message="error">
      <template #operation><t-button size="small" variant="outline" @click="load(video.id)">刷新</t-button></template>
    </t-alert>
    <t-empty v-else-if="!overview" :description="notGenerated ? '关联知识尚未生成' : '暂无关联知识'">
      <template #action><t-button size="small" variant="outline" @click="load(video.id)">刷新</t-button></template>
    </t-empty>
    <template v-else>
      <RelationOverviewCard :overview="overview" />
      <nav class="videohub-filter-tabs" aria-label="关联知识类型筛选">
        <button v-for="tab in visibleTabs" :key="tab.value" type="button" :class="{ 'is-active': selectedType === tab.value }" @click="selectedType = tab.value">
          <span>{{ tab.label }}</span><small>{{ tab.count }}</small>
        </button>
      </nav>
      <div v-if="filteredAnchors.length" class="related-knowledge__anchors">
        <KnowledgeAnchorCard
          v-for="anchor in filteredAnchors"
          :key="anchor.id"
          :anchor="anchor"
          :items="filteredItems(anchor.id)"
          @seek="emit('seek', $event)"
          @select-video-by-id="forwardSelection"
        />
      </div>
      <t-empty v-else :description="anchors.length ? '当前类型暂无锚点' : '暂无锚点'" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import './filterTabs.css'
import KnowledgeAnchorCard from './KnowledgeAnchorCard.vue'
import RelationOverviewCard from './RelationOverviewCard.vue'
import { KNOWLEDGE_TYPES, KNOWLEDGE_TYPE_STYLES } from './knowledgeTypeStyles'
import type { ContentState, CrossVideoKnowledgeItem, CurrentKnowledgeAnchor, KnowledgeType, RelationOverview, VideoData } from '@/types/videohub'

const props = defineProps<{ video: VideoData; contentState: ContentState<{ videoId: string; overview: RelationOverview | null; anchors: CurrentKnowledgeAnchor[]; crossVideoItems: CrossVideoKnowledgeItem[] }> }>()
const emit = defineEmits<{ seek: [seconds: number]; reload: []; selectVideoById: [videoId: string, seconds: number] }>()
const selectedType = ref<KnowledgeType | 'all'>('all')
const loading = computed(() => props.contentState.status === 'loading')
const error = computed(() => props.contentState.status === 'error' ? props.contentState.error || '关联知识加载失败' : '')
const notGenerated = computed(() => props.contentState.status === 'not_generated')
const overview = computed(() => props.contentState.data.overview)
const anchors = computed(() => props.contentState.data.anchors)
const crossVideoItems = computed(() => props.contentState.data.crossVideoItems)

const typeCounts = computed(() => Object.fromEntries(KNOWLEDGE_TYPES.map(type => [type,
  anchors.value.filter(anchor => anchor.knowledge_type === type).length
  + crossVideoItems.value.filter(item => item.knowledge_type === type).length,
])) as Record<KnowledgeType, number>)
const visibleTabs = computed(() => [
  { value: 'all' as const, label: '全部', count: anchors.value.length + crossVideoItems.value.length },
  ...KNOWLEDGE_TYPES.filter(type => typeCounts.value[type] > 0).map(type => ({ value: type, label: KNOWLEDGE_TYPE_STYLES[type].label, count: typeCounts.value[type] })),
])
const filteredAnchors = computed(() => selectedType.value === 'all' ? anchors.value : anchors.value.filter(anchor => anchor.knowledge_type === selectedType.value))
function filteredItems(anchorId: string) {
  return crossVideoItems.value.filter(item => item.anchorId === anchorId && (selectedType.value === 'all' || item.knowledge_type === selectedType.value))
}
function forwardSelection(videoId: string, seconds: number) { emit('selectVideoById', videoId, seconds) }
function load(_videoId?: string) {
  selectedType.value = 'all'
  emit('reload')
}
</script>

<style scoped>
.related-knowledge { display: grid; gap: calc(var(--td-comp-margin-s) * 2); padding: calc(var(--td-comp-margin-s) * 2) calc(var(--td-comp-margin-s) / 2); }
.related-knowledge__state, .related-knowledge > :deep(.t-empty) { min-height: 320px; display: grid; place-items: center; }
.related-knowledge__anchors { display: grid; gap: calc(var(--td-comp-margin-s) * 2); }
</style>
