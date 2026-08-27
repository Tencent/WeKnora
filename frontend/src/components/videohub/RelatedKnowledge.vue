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
import { computed, ref, watch } from 'vue'
import './filterTabs.css'
import { fetchRelatedKnowledge } from '@/api/videohub/relatedKnowledge'
import KnowledgeAnchorCard from './KnowledgeAnchorCard.vue'
import RelationOverviewCard from './RelationOverviewCard.vue'
import { KNOWLEDGE_TYPES, KNOWLEDGE_TYPE_STYLES } from './knowledgeTypeStyles'
import type { CrossVideoKnowledgeItem, CurrentKnowledgeAnchor, KnowledgeType, RelationOverview, VideoData } from '@/types/videohub'

const props = defineProps<{ video: VideoData }>()
const emit = defineEmits<{ seek: [seconds: number]; selectVideoById: [videoId: string, seconds: number] }>()
const selectedType = ref<KnowledgeType | 'all'>('all')
const loading = ref(true)
const error = ref('')
const notGenerated = ref(false)
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
  error.value = ''
  notGenerated.value = false
  selectedType.value = 'all'
  try {
    const payload = await fetchRelatedKnowledge(videoId)
    if (props.video.id !== videoId) return
    overview.value = payload.overview
    anchors.value = payload.anchors
    crossVideoItems.value = payload.crossVideoItems
  } catch (reason: any) {
    if (props.video.id !== videoId) return
    overview.value = null
    anchors.value = []
    crossVideoItems.value = []
    if (reason?.status === 404) notGenerated.value = true
    else error.value = reason?.message || '关联知识加载失败'
  } finally {
    if (props.video.id === videoId) loading.value = false
  }
}
watch(() => props.video.id, load, { immediate: true })
</script>

<style scoped>
.related-knowledge { display: grid; gap: calc(var(--td-comp-margin-s) * 2); padding: calc(var(--td-comp-margin-s) * 2) calc(var(--td-comp-margin-s) / 2); }
.related-knowledge__state, .related-knowledge > :deep(.t-empty) { min-height: 320px; display: grid; place-items: center; }
.related-knowledge__anchors { display: grid; gap: calc(var(--td-comp-margin-s) * 2); }
</style>
