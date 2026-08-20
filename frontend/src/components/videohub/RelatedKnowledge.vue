<template>
  <div class="related-knowledge">
    <div v-if="loading" class="related-knowledge__state"><t-loading text="正在加载关联知识" /></div>
    <t-empty v-else-if="!overview" description="暂无关联知识" />
    <template v-else>
      <RelationOverviewCard :overview="overview" />
      <nav class="related-knowledge__tabs" aria-label="关联知识类型筛选">
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
import { computed, ref, watch } from 'vue'
import { fetchRelatedKnowledge } from '@/api/videohub/relatedKnowledge'
import KnowledgeAnchorCard from './KnowledgeAnchorCard.vue'
import RelationOverviewCard from './RelationOverviewCard.vue'
import { KNOWLEDGE_TYPES, KNOWLEDGE_TYPE_STYLES } from './knowledgeTypeStyles'
import type { CrossVideoKnowledgeItem, CurrentKnowledgeAnchor, KnowledgeType, RelationOverview, VideoData } from '@/types/videohub'

const props = defineProps<{ video: VideoData }>()
const emit = defineEmits<{ seek: [seconds: number]; selectVideoById: [videoId: string, seconds: number] }>()
const selectedType = ref<KnowledgeType | 'all'>('all')
const loading = ref(true)
const overview = ref<RelationOverview | null>(null)
const anchors = ref<CurrentKnowledgeAnchor[]>([])
const crossVideoItems = ref<CrossVideoKnowledgeItem[]>([])

const typeCounts = computed(() => Object.fromEntries(KNOWLEDGE_TYPES.map(type => [type, crossVideoItems.value.filter(item => item.knowledge_type === type).length])) as Record<KnowledgeType, number>)
const visibleTabs = computed(() => [
  { value: 'all' as const, label: '全部', count: crossVideoItems.value.length },
  ...KNOWLEDGE_TYPES.filter(type => typeCounts.value[type] > 0).map(type => ({ value: type, label: KNOWLEDGE_TYPE_STYLES[type].label, count: typeCounts.value[type] })),
])
const filteredAnchors = computed(() => selectedType.value === 'all' ? anchors.value : anchors.value.filter(anchor => anchor.knowledge_type === selectedType.value))
function filteredItems(anchorId: string) {
  return crossVideoItems.value.filter(item => item.anchorId === anchorId && (selectedType.value === 'all' || item.knowledge_type === selectedType.value))
}
function forwardSelection(videoId: string, seconds: number) { emit('selectVideoById', videoId, seconds) }
async function load(videoId: string) {
  loading.value = true
  selectedType.value = 'all'
  const payload = await fetchRelatedKnowledge(videoId)
  overview.value = payload.overview
  anchors.value = payload.anchors
  crossVideoItems.value = payload.crossVideoItems
  loading.value = false
}
watch(() => props.video.id, load, { immediate: true })
</script>

<style scoped>
.related-knowledge { display: grid; gap: calc(var(--td-comp-margin-s) * 2); padding: calc(var(--td-comp-margin-s) * 2) calc(var(--td-comp-margin-s) / 2); }
.related-knowledge__state, .related-knowledge > :deep(.t-empty) { min-height: 320px; display: grid; place-items: center; }
.related-knowledge__tabs { display: flex; gap: calc(var(--td-comp-margin-s) * .75); overflow-x: auto; padding-bottom: calc(var(--td-comp-margin-s) / 2); }
.related-knowledge__tabs button { display: inline-flex; flex: none; align-items: center; gap: calc(var(--td-comp-margin-s) / 2); padding: calc(var(--td-comp-margin-s) / 2) calc(var(--td-comp-margin-s) * 1.5); border: 0; border-radius: var(--td-radius-medium); background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); font: inherit; font-size: var(--td-font-size-body-small); cursor: pointer; }
.related-knowledge__tabs button:hover { color: var(--td-text-color-primary); }
.related-knowledge__tabs button.is-active { background: var(--td-brand-color); color: var(--td-text-color-anti); box-shadow: var(--td-shadow-1); font-weight: 600; }
.related-knowledge__tabs small { font-family: var(--app-font-family-mono, monospace); font-size: 11px; opacity: .8; }
.related-knowledge__anchors { display: grid; gap: calc(var(--td-comp-margin-s) * 2); }
</style>
