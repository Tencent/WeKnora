<template>
  <section class="scene-view" aria-label="场景视图">
    <t-alert v-if="sceneMode === 'training' && errorMessage" theme="error" :message="errorMessage" />
    <t-alert v-if="sceneMode === 'training' && warningMessage" theme="warning" :message="warningMessage" />
    <t-alert v-if="sceneMode === 'training' && refreshSummary" theme="info" :message="refreshSummary" />

    <nav class="scene-view__modes" role="tablist" aria-label="场景应用">
      <button type="button" role="tab" :aria-selected="sceneMode === 'training'" :class="{ 'is-active': sceneMode === 'training' }" @click="sceneMode = 'training'">培训</button>
      <button type="button" role="tab" :aria-selected="sceneMode === 'meeting'" :class="{ 'is-active': sceneMode === 'meeting' }" @click="sceneMode = 'meeting'">会议</button>
    </nav>

    <MeetingSceneView v-if="sceneMode === 'meeting'" ref="meetingSceneRef" @select-video="openMeetingVideo" @select-wiki="selectWiki" />
    <template v-else>

    <div v-if="loading && !projection" class="scene-view__state">
      <t-loading text="正在读取培训学习路径" />
    </div>
    <div v-else-if="!projection" class="scene-view__state scene-panel">
      <t-empty description="尚未生成培训学习路径" />
      <t-button :loading="generating" @click="refresh">根据当前视频生成</t-button>
    </div>

    <template v-else>
      <div v-if="generating" class="scene-generation" role="status">
        <t-loading size="small" />
        <span>正在根据当前视频刷新学习路径（{{ jobProgress }}%），完成前继续展示上一版。</span>
      </div>

      <div class="scene-view__overview" aria-label="场景概览">
        <div v-for="stat in overviewStats" :key="stat.label" class="scene-stat" :class="{ 'scene-stat--generated': stat.key === 'generatedAt' }">
          <span>{{ stat.label }}</span>
          <div v-if="stat.key === 'generatedAt'" class="scene-stat__value-row">
            <strong>{{ stat.value }}</strong>
            <t-button
              size="small"
              variant="text"
              shape="square"
              :loading="generating"
              aria-label="刷新培训学习路径"
              title="刷新培训学习路径"
              @click="refresh"
            >
              <RefreshIcon />
            </t-button>
          </div>
          <strong v-else>{{ stat.value }}</strong>
        </div>
      </div>

      <div v-if="!trainingClusters.length" class="scene-view__state scene-panel">
        <t-empty description="当前合格视频尚未形成可发布的学习主题" />
      </div>

      <div v-else class="training-layout">
        <section class="scene-panel topic-network-panel">
          <header class="scene-panel__head">
            <strong>培训主题簇</strong>
            <span>{{ trainingClusters.length }} 个主题簇 · {{ trainingEdges.length }} 条学习关系</span>
          </header>
          <div v-if="selectedClusterRelations.length" class="topic-relation-labels" aria-label="当前主题的学习关系">
            <button
              v-for="edge in selectedClusterRelations"
              :key="edge.key"
              type="button"
              :class="{ 'is-selected': selectedRelation === edge.key }"
              @click="selectRelation(edge.key)"
              @mouseenter="previewRelation(edge.key)"
              @mouseleave="clearRelationPreview(edge.key)"
              @focus="previewRelation(edge.key)"
              @blur="clearRelationPreview(edge.key)"
            >
              <span>{{ relationTypeLabels[edge.relationType] }}</span>
              <strong>{{ edge.otherClusterTitle }}</strong>
            </button>
          </div>
            <div class="topic-network" aria-label="培训主题簇原生力导图">
              <GraphCanvas
                :nodes="graphNodes"
                :edges="graphEdges"
                :selected-node-id="selectedCluster"
                :full-labels="true"
                label-position="left"
                :centered="true"
                :show-legend="false"
                :relation-labels="relationTypeLabels"
                :show-relation-labels="true"
                :show-relation-arrows="true"
                :relation-arrow-types="directedTrainingRelations"
                @node-click="selectGraphNode"
              />
            </div>
        </section>

        <aside v-if="activeCluster" class="scene-panel training-detail" aria-live="polite">
          <template v-if="activeRelation">
            <div class="scene-kicker">主题簇关系</div>
            <h2>{{ relationTypeLabels[activeRelation.relationType] }}</h2>
            <p class="training-detail__description relation-detail__summary">{{ activeRelation.summary }}</p>
          </template>

          <template v-else>
            <div class="scene-kicker">{{ contentTypeLabels[activeCluster.contentType] || '培训主题' }} · 学习路径</div>
            <h2>{{ activeCluster.title }}</h2>
            <p class="training-detail__description">{{ activeCluster.description }}</p>

            <div class="scene-section">
              <span class="scene-section__label">学习目标</span>
              <p class="training-detail__description">{{ activeCluster.learningGoal }}</p>
            </div>

            <div class="scene-section">
              <span class="scene-section__label">学习路径</span>
              <div class="training-path">
                <section v-for="stage in activeCluster.stages" :key="stage.stage_id" class="path-stage">
                  <button
                    type="button"
                    class="path-stage__toggle"
                    :aria-expanded="isStageExpanded(stage.stage_id)"
                    @click="toggleStage(stage.stage_id)"
                  >
                    <span>
                      <small>{{ String(stage.sequence).padStart(2, '0') }}</small>
                      <strong>{{ stage.title }}</strong>
                    </span>
                    <ChevronDownIcon :class="{ 'is-expanded': isStageExpanded(stage.stage_id) }" />
                  </button>
                  <p>{{ stage.summary }}</p>
                  <div v-if="isStageExpanded(stage.stage_id)" class="path-stage__units">
                    <div
                      v-for="unit in [...stage.units].sort((a, b) => a.sequence - b.sequence)"
                      :key="unit.unit_id"
                      class="path-step"
                      :class="{ 'is-active': selectedUnit === unit.unit_id }"
                    >
                      <button type="button" class="path-step__toggle" :aria-expanded="selectedUnit === unit.unit_id" @click="selectedUnit = selectedUnit === unit.unit_id ? null : unit.unit_id">
                        <small>{{ String(unit.sequence).padStart(2, '0') }}</small>
                        <strong>{{ unit.learning_title }}</strong>
                      </button>
                      <p class="path-step__question"><small>学习任务</small>{{ unit.learner_question }} 学完可以 {{ unit.learning_outcome }}</p>
                      <div v-if="unit.knowledge_refs.length" class="path-step__knowledge" aria-label="知识点">
                        <button v-for="ref in unit.knowledge_refs" :key="`${unit.unit_id}-${ref.knowledge_object_id}`" type="button" class="knowledge-tag" :title="ref.title" @click.stop="selectKnowledge(ref, unit)">{{ ref.title }}</button>
                      </div>
                    </div>
                  </div>
                </section>
              </div>

              <div v-if="activeUnit" class="path-detail">
                <div v-if="activeUnit.knowledge_refs.length" class="path-detail__group">
                  <span class="path-detail__label">知识入口</span>
                  <button
                    v-for="ref in activeUnit.knowledge_refs"
                    :key="`${ref.knowledge_object_id}-${ref.wiki_page_id}`"
                    type="button"
                    class="source-link"
                    @click="emit('selectWiki', { targetPageId: ref.wiki_page_id, title: ref.title })"
                  >
                    <FileIcon />
                    <span>
                      <strong>{{ ref.title }}</strong>
                      <small>{{ knowledgeTypeLabels[ref.knowledge_type] || ref.knowledge_type }}</small>
                    </span>
                  </button>
                </div>
                <div class="path-detail__group">
                  <span class="path-detail__label">视频证据</span>
                  <button
                    v-for="ref in activeUnit.evidence_refs"
                    :key="evidenceKey(ref)"
                    type="button"
                    class="source-link"
                    @click="emit('selectVideo', ref.video_id, ref.start_ms / 1000)"
                  >
                    <PlayCircleIcon />
                    <span>
                      <strong>{{ videoTitle(ref.video_id) }}</strong>
                      <small>{{ formatTimeRange(ref.start_ms, ref.end_ms) }}</small>
                    </span>
                  </button>
                </div>
              </div>

              <section v-if="activeCluster.gap_analysis" class="gap-analysis" :class="{ 'is-sufficient': activeCluster.gap_analysis.status === 'sufficient' }">
                <button type="button" class="gap-analysis__toggle" :aria-expanded="gapAnalysisExpanded" @click="gapAnalysisExpanded = !gapAnalysisExpanded">
                  <span>
                    <strong>缺口说明</strong>
                    <small>{{ activeCluster.gap_analysis.current_depth_summary }}</small>
                    <small>{{ activeCluster.gap_analysis.supplement_direction_summary }}</small>
                    <small class="gap-analysis__action">{{ gapAnalysisExpanded ? '收起缺口分析' : '查看缺口分析' }}</small>
                  </span>
                  <ChevronDownIcon :class="{ 'is-expanded': gapAnalysisExpanded }" />
                </button>
                <div v-if="gapAnalysisExpanded" class="gap-analysis__dimensions">
                  <article v-for="item in gapDimensions" :key="item.label" class="gap-analysis__dimension">
                    <strong>{{ item.label }}</strong>
                    <p>{{ item.value.gap }}</p>
                    <small>{{ item.value.recommendation }}</small>
                  </article>
                </div>
              </section>
            </div>
          </template>
        </aside>
      </div>
    </template>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ChevronDownIcon, FileIcon, PlayCircleIcon, RefreshIcon } from 'tdesign-icons-vue-next'
import { fetchVideoOptions } from '@/api/videohub'
import MeetingSceneView from './MeetingSceneView.vue'
import GraphCanvas from './GraphCanvas.vue'
import type { GraphEdge, GraphNode } from '@/types/videohub'
import { createTrainingNetworkLayout, positionTrainingCluster, routeTrainingEdge } from './trainingOrchestrationView'
import {
  fetchCurrentTrainingProjection,
  fetchTrainingJob,
  generateTrainingProjection,
  trainingJobErrorMessage,
  trainingJobWarningMessage,
  type TrainingEvidenceRef,
  type TrainingGapAnalysis,
  type TrainingJob,
  type TrainingLearningUnit,
  type TrainingProjection,
  type TrainingRelationType,
} from '@/api/videohub/trainingOrchestration'

const emit = defineEmits<{
  selectWiki: [target: { targetPageId: string; title: string }]
  selectVideo: [videoId: string, seconds: number]
}>()

const sceneMode = ref<'training' | 'meeting'>('training')
const meetingSceneRef = ref<{ refresh: () => Promise<void> } | null>(null)
const projection = ref<TrainingProjection | null>(null)
const loading = ref(true)
const generating = ref(false)
const jobProgress = ref(0)
const errorMessage = ref('')
const warningMessage = ref('')
const refreshSummary = ref('')
const selectedCluster = ref('')
const selectedUnit = ref<string | null>(null)
const selectedRelation = ref<string | null>(null)
const previewedRelation = ref<string | null>(null)
const expandedStages = ref<string[]>([])
const gapAnalysisExpanded = ref(false)
const videoTitles = ref<Record<string, string>>({})
const networkElement = ref<HTMLElement | null>(null)
const networkWidth = ref(720)
let networkResizeObserver: ResizeObserver | undefined

function observeNetworkElement(element: HTMLElement | null) {
  networkResizeObserver?.disconnect()
  networkResizeObserver = undefined
  if (!element || typeof ResizeObserver === 'undefined') return
  networkWidth.value = element.clientWidth || networkWidth.value
  networkResizeObserver = new ResizeObserver(entries => {
    const width = entries[0]?.contentRect.width
    if (width) networkWidth.value = width
  })
  networkResizeObserver.observe(element)
}

const relationTypeLabels: Record<TrainingRelationType, string> = {
  required_before: '必须先学',
  recommended_before: '推荐先学',
  application: '用于实践',
  complementary: '补充理解',
  contrast: '对照理解',
}
const directedTrainingRelations = ['required_before', 'recommended_before', 'application']
const canvasContentTypeMap: Record<string, string> = {
  skill_method: 'methodology',
  tool_operation: 'entity',
  concept_cognition: 'concept',
  case_analysis: 'case',
  humanities_reflection: 'insight',
  process_standard: 'concept',
}
const contentTypeLabels: Record<string, string> = {
  skill_method: '技能方法',
  tool_operation: '工具操作',
  concept_cognition: '概念认知',
  case_analysis: '案例分析',
  humanities_reflection: '人文反思',
  process_standard: '流程规范',
}
const contentTypeColors: Record<string, string> = {
  skill_method: '--color-data-4',
  tool_operation: '--color-data-1',
  concept_cognition: '--color-data-2',
  case_analysis: '--color-data-3',
  humanities_reflection: '--color-data-5',
  process_standard: '--color-data-2',
}
const knowledgeTypeLabels: Record<string, string> = {
  entity: '实体',
  concept: '概念',
  case: '案例',
  methodology: '方法论',
  insight: '洞察',
}
const visibleTopicClusters = computed(() => (projection.value?.topic_clusters || [])
  .filter(cluster => cluster.path.stages.some(stage => stage.units.length > 0)))
const trainingClusterCount = computed(() => visibleTopicClusters.value.length)
const networkLayout = computed(() => createTrainingNetworkLayout(trainingClusterCount.value, networkWidth.value))
const networkHeight = computed(() => networkLayout.value.height)
const trainingClusters = computed(() => visibleTopicClusters.value.map((cluster, index, all) => {
  const position = positionTrainingCluster(index, all.length, networkLayout.value)
  const stages = [...cluster.path.stages].sort((a, b) => a.sequence - b.sequence)
  return {
    key: cluster.cluster_id,
    title: cluster.title,
    units: stages.reduce((total, stage) => total + stage.units.length, 0),
    videos: cluster.source_video_ids.length,
    x: position.x,
    y: position.y,
    description: cluster.summary,
    learningGoal: cluster.learning_goal,
    contentType: cluster.learning_content_type,
    nodeColor: `var(${contentTypeColors[cluster.learning_content_type] || '--color-data-2'})`,
    gap_analysis: cluster.gap_analysis,
    stages,
  }
}))
const clusterByID = computed(() => new Map(trainingClusters.value.map(cluster => [cluster.key, cluster])))
const trainingEdges = computed(() => (projection.value?.topic_cluster_relations || []).flatMap(relation => {
  const source = clusterByID.value.get(relation.source_cluster_id)
  const target = clusterByID.value.get(relation.target_cluster_id)
  if (!source || !target) return []
    const sourceCenter = { x: source.x, y: source.y * networkHeight.value / 100 }
    const targetCenter = { x: target.x, y: target.y * networkHeight.value / 100 }
  const obstacleCenters = trainingClusters.value
    .filter(cluster => cluster.key !== source.key && cluster.key !== target.key)
    .map(cluster => ({ x: cluster.x, y: cluster.y * networkHeight.value / 100 }))
  const route = routeTrainingEdge(sourceCenter, targetCenter, obstacleCenters, { width: 100, height: networkHeight.value })
  return [{
    key: relation.relation_id,
    ...route,
    sourceClusterID: relation.source_cluster_id,
    targetClusterID: relation.target_cluster_id,
    relationType: relation.relation_type,
    summary: relation.summary,
    connectedToSelection: selectedCluster.value === relation.source_cluster_id
      || selectedCluster.value === relation.target_cluster_id,
  }]
}))
const graphNodes = computed<GraphNode[]>(() => trainingClusters.value.map(cluster => {
  const graphType = canvasContentTypeMap[cluster.contentType] || 'concept'
  return {
    id: cluster.key,
    name: cluster.title,
    label: cluster.title,
    attributes: [graphType],
    type: graphType,
    link_count: cluster.videos,
  }
}))
const graphEdges = computed<GraphEdge[]>(() => trainingEdges.value.map(edge => ({
  id: edge.key,
  source: edge.sourceClusterID,
  target: edge.targetClusterID,
  type: edge.relationType,
  source_title: clusterByID.value.get(edge.sourceClusterID)?.title,
  target_title: clusterByID.value.get(edge.targetClusterID)?.title,
})))
const activeCluster = computed(() => clusterByID.value.get(selectedCluster.value) || trainingClusters.value[0] || null)
const activeRelation = computed(() => trainingEdges.value.find(edge => edge.key === selectedRelation.value) || null)
const relationPreview = computed(() => trainingEdges.value.find(edge => edge.key === (previewedRelation.value || selectedRelation.value)) || null)
const selectedClusterRelations = computed(() => trainingEdges.value
  .filter(edge => edge.connectedToSelection)
  .map(edge => {
    const otherClusterID = edge.sourceClusterID === selectedCluster.value ? edge.targetClusterID : edge.sourceClusterID
    return { ...edge, otherClusterTitle: clusterByID.value.get(otherClusterID)?.title || '关联主题' }
  }))
const activeUnit = computed(() => activeCluster.value?.stages
  .flatMap(stage => stage.units)
  .find(unit => unit.unit_id === selectedUnit.value) || null)
const gapDimensions = computed(() => {
  const analysis = activeCluster.value?.gap_analysis as TrainingGapAnalysis | undefined
  if (!analysis) return []
  return [
    { label: '知识覆盖缺口', value: analysis.dimensions.knowledge_coverage },
    { label: '工作场景运用缺口', value: analysis.dimensions.workplace_application },
    { label: '独立任务完成缺口', value: analysis.dimensions.independent_task_completion },
  ]
})
const overviewStats = computed(() => {
  const stats = projection.value?.statistics
  return [
    { key: 'topicCount', label: '培训主题', value: String(stats?.topic_cluster_count || 0) },
    { key: 'videoCount', label: '培训视频', value: String(stats?.selected_videos || 0) },
    { key: 'knowledgeCount', label: '引用知识', value: String(stats?.selected_knowledge_count || 0) },
    { key: 'duration', label: '学习时长', value: formatDuration(stats?.learning_duration_seconds || 0) },
    { key: 'generatedAt', label: '上次生成', value: projection.value ? formatGeneratedAt(projection.value.generated_at) : '-' },
  ]
})
function selectCluster(key: string) {
  selectedCluster.value = key
  selectedRelation.value = null
  selectedUnit.value = null
  gapAnalysisExpanded.value = false
  const firstStage = clusterByID.value.get(key)?.stages[0]
  expandedStages.value = firstStage ? [firstStage.stage_id] : []
}
function selectGraphNode(node: GraphNode) {
  selectCluster(node.id)
}
function openMeetingVideo(videoID: string, seconds: number) {
  emit('selectVideo', videoID, seconds)
}
function selectWiki(target: { targetPageId: string; title: string }) {
  emit('selectWiki', target)
}
function selectRelation(key: string) {
  selectedRelation.value = key
  selectedUnit.value = null
}
function selectKnowledge(ref: { wiki_page_id: string; title: string; locator_evidence?: TrainingEvidenceRef }, unit: TrainingLearningUnit) {
  const evidence = ref.locator_evidence || unit.evidence_refs.find(item => item.video_id === ref.locator_evidence?.video_id && item.evidence_id === ref.locator_evidence?.evidence_id) || unit.evidence_refs[0]
  if (evidence) {
    emit('selectVideo', evidence.video_id, evidence.start_ms / 1000)
    return
  }
  emit('selectWiki', { targetPageId: ref.wiki_page_id, title: ref.title })
}
function previewRelation(key: string) {
  previewedRelation.value = key
}
function clearRelationPreview(key: string) {
  if (previewedRelation.value === key) previewedRelation.value = null
}
function isDirectedRelation(type: TrainingRelationType) {
  return type === 'required_before' || type === 'recommended_before' || type === 'application'
}
function isStageExpanded(stageID: string) {
  return expandedStages.value.includes(stageID)
}
function toggleStage(stageID: string) {
  if (isStageExpanded(stageID)) {
    expandedStages.value = expandedStages.value.filter(id => id !== stageID)
    if (activeCluster.value?.stages.find(stage => stage.stage_id === stageID)?.units.some(unit => unit.unit_id === selectedUnit.value)) {
      selectedUnit.value = null
    }
    return
  }
  expandedStages.value = [...expandedStages.value, stageID]
}
async function loadCurrent() {
  loading.value = true
  errorMessage.value = ''
  warningMessage.value = ''
  try {
    projection.value = await fetchCurrentTrainingProjection()
    if (projection.value?.retrieval_degraded) {
      warningMessage.value = '证据召回降级，已使用规划证据完成生成。'
    }
    const firstCluster = trainingClusters.value[0]
    selectedCluster.value = firstCluster?.key || ''
    selectedRelation.value = null
    selectedUnit.value = null
    gapAnalysisExpanded.value = false
    expandedStages.value = firstCluster?.stages[0]?.stage_id ? [firstCluster.stages[0].stage_id] : []
    await loadVideoTitles()
  } catch (cause) {
    errorMessage.value = errorText(cause, '读取培训学习路径失败')
  } finally {
    loading.value = false
  }
}
async function loadVideoTitles() {
  if (!trainingClusters.value.length) {
    videoTitles.value = {}
    return
  }
  try {
    const options = await fetchVideoOptions()
    videoTitles.value = Object.fromEntries(options.map(video => [video.id, video.title]))
  } catch {
    videoTitles.value = {}
    warningMessage.value = '部分视频标题暂时无法读取，视频时间点仍可打开。'
  }
}
async function refresh() {
  if (sceneMode.value === 'meeting') {
    await meetingSceneRef.value?.refresh()
    return
  }
  if (generating.value) return
  generating.value = true
  errorMessage.value = ''
  refreshSummary.value = ''
  try {
    let job = await generateTrainingProjection()
    jobProgress.value = job.progress
    while (job.status === 'queued' || job.status === 'running') {
      await delay(1500)
      job = await fetchTrainingJob(job.id)
      jobProgress.value = job.progress
    }
    if (job.status === 'failed') throw new Error(trainingJobErrorMessage(job))
    await loadCurrent()
    refreshSummary.value = trainingRefreshMessage(job)
    if (!warningMessage.value) {
      warningMessage.value = trainingJobWarningMessage(job)
    }
  } catch (cause) {
    errorMessage.value = errorText(cause, '学习路径生成失败')
  } finally {
    generating.value = false
  }
}
function trainingRefreshMessage(job: TrainingJob) {
  const labels: Partial<Record<NonNullable<TrainingJob['refresh_mode']>, string>> = {
    reused: '当前内容没有变化，已保留现有学习路径。',
    metadata_patch: '标题或定位信息已更新，学习路径内容保持不变。',
    availability_filter: '已按视频可用状态更新学习路径。',
    incremental: '已更新受影响的培训主题。',
    full: '培训学习路径已重新生成。',
  }
  const base = job.refresh_mode ? labels[job.refresh_mode] || '' : ''
  const counts = [
    job.changed_video_count ? `${job.changed_video_count} 个视频` : '',
    job.changed_knowledge_count ? `${job.changed_knowledge_count} 个知识点` : '',
    job.changed_evidence_count ? `${job.changed_evidence_count} 条证据` : '',
    job.changed_cluster_count ? `${job.changed_cluster_count} 个培训主题` : '',
  ].filter(Boolean).join('、')
  return base && counts ? `${base} 涉及${counts}。` : base
}
function delay(ms: number) {
  return new Promise(resolve => window.setTimeout(resolve, ms))
}
function errorText(cause: unknown, fallback: string) {
  if (cause instanceof Error) return cause.message
  if (cause && typeof cause === 'object' && 'message' in cause) return String((cause as { message?: unknown }).message || fallback)
  return fallback
}
function evidenceKey(ref: TrainingEvidenceRef) {
  return `${ref.video_id}:${ref.transcript_generation}:${ref.evidence_id}`
}
function videoTitle(videoID: string) {
  return videoTitles.value[videoID] || '来源视频'
}
function formatTimestamp(ms: number) {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const remainder = seconds % 60
  return hours > 0
    ? `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
    : `${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
}
function formatTimeRange(startMs: number, endMs: number) {
  return `${formatTimestamp(startMs)}–${formatTimestamp(endMs)}`
}
function formatDuration(seconds: number) {
  const totalMinutes = Math.max(0, Math.ceil(seconds / 60))
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  if (!hours) return `${minutes} 分钟`
  return minutes ? `${hours} 小时 ${minutes} 分钟` : `${hours} 小时`
}
function formatGeneratedAt(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(date)
}

onMounted(() => {
  loadCurrent()
})
watch(networkElement, element => observeNetworkElement(element), { flush: 'post' })
onBeforeUnmount(() => networkResizeObserver?.disconnect())
defineExpose({ refresh })
</script>

<style scoped>
.scene-view { display: grid; gap: calc(var(--td-comp-margin-s) * 1.5); min-width: 0; }
.scene-view__modes { display: inline-flex; width: max-content; gap: 3px; padding: 3px; border: 1px solid rgba(255,255,255,.86); border-radius: var(--td-radius-medium); background: rgba(226,233,229,.54); }
.scene-view__modes button { min-width: 72px; min-height: 32px; padding: 4px 14px; border: 0; border-radius: var(--td-radius-small); color: var(--td-text-color-secondary); background: transparent; cursor: pointer; font-size: var(--td-font-size-body-small); }
.scene-view__modes button:hover, .scene-view__modes button.is-active { color: var(--td-brand-color); background: rgba(255,255,255,.9); font-weight: 600; }
.scene-view__overview { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: var(--td-comp-margin-s); }
.scene-view__state { display: grid; min-height: 360px; place-items: center; align-content: center; gap: 12px; }
.scene-generation { display: flex; align-items: center; gap: 8px; min-height: 36px; padding: 0 12px; border: 1px solid rgba(255,255,255,.84); border-radius: var(--td-radius-medium); color: var(--td-text-color-secondary); background: rgba(255,255,255,.45); font-size: var(--td-font-size-body-small); }
.scene-stat, .scene-panel { border: 1px solid rgba(255,255,255,.84); background: rgba(255,255,255,.45); backdrop-filter: blur(24px) saturate(112%); -webkit-backdrop-filter: blur(24px) saturate(112%); }
.scene-stat { min-width: 0; padding: 14px 16px; border-radius: var(--td-radius-medium); }
.scene-stat span { display: block; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); }
.scene-stat strong { display: block; margin-top: 7px; overflow-wrap: anywhere; font-size: 22px; font-weight: 500; line-height: 28px; }
.scene-stat__value-row { display: flex; align-items: center; justify-content: space-between; gap: 8px; min-width: 0; }
.scene-stat__value-row strong { min-width: 0; }
.scene-stat__value-row .t-button { flex: none; margin-top: 5px; }
.training-layout { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: calc(var(--td-comp-margin-s) * 2); align-items: start; }
.scene-panel { min-width: 0; border-radius: var(--td-radius-extraLarge); }
.topic-network-panel { overflow: hidden; }
.scene-panel__head { display: flex; align-items: center; justify-content: space-between; gap: 12px; min-height: 52px; padding: 0 18px; border-bottom: 1px solid color-mix(in srgb, var(--td-component-stroke) 72%, transparent); }
.scene-panel__head strong { font-size: var(--td-font-size-title-small); }
.scene-panel__head span { color: var(--td-text-color-secondary); font-size: 11px; text-align: right; }
.topic-relation-labels { display: flex; gap: 0; overflow-x: auto; border-bottom: 1px solid color-mix(in srgb, var(--td-component-stroke) 72%, transparent); }
.topic-relation-labels button { display: grid; grid-template-columns: max-content minmax(0, 1fr); align-items: center; gap: 7px; min-width: 0; padding: 9px 12px; border: 0; border-right: 1px solid color-mix(in srgb, var(--td-component-stroke) 72%, transparent); color: var(--td-text-color-secondary); background: rgba(255,255,255,.2); cursor: pointer; text-align: left; }
.topic-relation-labels button:hover, .topic-relation-labels button:focus-visible, .topic-relation-labels button.is-selected { color: var(--td-brand-color); background: color-mix(in srgb, var(--td-brand-color-light) 44%, rgba(255,255,255,.54)); }
.topic-relation-labels span { font-size: 10px; font-weight: 600; white-space: nowrap; }
.topic-relation-labels strong { max-width: 128px; overflow: hidden; color: var(--td-text-color-primary); font-size: 11px; font-weight: 500; text-overflow: ellipsis; white-space: nowrap; }
.topic-network { position: relative; width: 100%; min-width: 0; /* legacy min-width: 720px; retained only as a regression marker; the 5:5 panel must stay fluid. */ min-height: 540px; overflow: hidden; background: radial-gradient(circle, rgba(52,79,65,.11) 1px, transparent 1.5px), rgba(255,255,255,.24); background-size: 28px 28px; }
.topic-network svg { position: absolute; inset: 0; width: 100%; height: 100%; }
.topic-edge { cursor: pointer; outline: none; }
.topic-edge__hit { fill: none; stroke: transparent; stroke-width: 18; pointer-events: stroke; }
.topic-edge__line { fill: none; stroke: color-mix(in srgb, var(--td-text-color-secondary) 38%, transparent); stroke-width: 1.5; pointer-events: none; transition: stroke .16s ease, stroke-width .16s ease; }
.topic-edge.is-required_before .topic-edge__line, .topic-edge.is-application .topic-edge__line { stroke-width: 2; }
.topic-edge.is-complementary .topic-edge__line { stroke-dasharray: 8 6; }
.topic-edge.is-contrast .topic-edge__line { stroke-dasharray: 2 5; }
.topic-edge.is-connected .topic-edge__line { stroke: color-mix(in srgb, var(--td-brand-color) 72%, var(--td-text-color-secondary)); stroke-width: 2.25; }
.topic-edge:hover .topic-edge__line, .topic-edge:focus-visible .topic-edge__line, .topic-edge.is-selected .topic-edge__line { stroke: var(--td-brand-color); stroke-width: 3; }
.cluster-node { position: absolute; z-index: 1; display: inline-flex; width: max-content; max-width: min(270px, calc(100% - 18px)); align-items: flex-start; gap: 10px; padding: 0; transform: translate(-50%, -50%); overflow: visible; border: 0; color: var(--td-text-color-primary); background: transparent; cursor: pointer; text-align: left; transition: transform .16s ease, filter .16s ease; }
.cluster-node__marker { width: 24px; height: 24px; flex: none; margin-top: 2px; border: 2px solid rgba(255,255,255,.92); border-radius: var(--td-radius-circle); background: var(--node-color, var(--td-brand-color)); box-shadow: 0 0 0 1px rgba(0,0,0,.12); transition: transform .16s ease, box-shadow .16s ease; }
.cluster-node__label { display: grid; min-width: 0; max-width: 228px; gap: 2px; }
.cluster-node:hover, .cluster-node.is-selected { transform: translate(-50%, -50%) scale(1.03); filter: drop-shadow(0 2px 4px rgba(0,0,0,.12)); }
.cluster-node.is-selected .cluster-node__marker, .cluster-node:hover .cluster-node__marker { transform: scale(1.16); box-shadow: 0 0 0 4px color-mix(in srgb, var(--node-color, var(--td-brand-color)) 22%, transparent); }
.cluster-node strong { display: block; overflow-wrap: anywhere; font-size: var(--td-font-size-body-medium); font-weight: 500; line-height: 20px; }
.cluster-node__meta { display: block; overflow-wrap: anywhere; color: var(--td-text-color-secondary); font-size: 10px; line-height: 15px; }
.topic-network__relation-preview { position: absolute; z-index: 2; right: 12px; bottom: 12px; left: 12px; display: grid; grid-template-columns: max-content minmax(0, 1fr); align-items: start; gap: 10px; padding: 9px 11px; border: 1px solid rgba(255,255,255,.9); border-radius: var(--td-radius-medium); color: var(--td-text-color-secondary); background: rgba(255,255,255,.88); backdrop-filter: blur(12px); font-size: 11px; line-height: 16px; }
.topic-network__relation-preview strong { color: var(--td-brand-color); }
.topic-network__relation-preview span { display: -webkit-box; overflow: hidden; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.training-detail { padding: 18px; }
.scene-kicker { color: var(--td-brand-color); font-size: 11px; font-weight: 600; }
.training-detail h2 { margin: 5px 0 8px; overflow-wrap: anywhere; font-size: var(--td-font-size-title-large); font-weight: 600; line-height: 26px; }
.training-detail__description { margin: 0; overflow-wrap: anywhere; color: var(--td-text-color-secondary); font-size: var(--td-font-size-body-small); line-height: 19px; }
.relation-detail__summary { margin-top: 14px; padding-top: 14px; border-top: 1px solid color-mix(in srgb, var(--td-component-stroke) 72%, transparent); }
.scene-section { margin-top: 18px; padding-top: 16px; border-top: 1px solid color-mix(in srgb, var(--td-component-stroke) 72%, transparent); }
.scene-section__label { display: block; margin-bottom: 8px; color: var(--td-text-color-secondary); font-size: 11px; }
.training-path { display: grid; gap: 8px; }
.path-stage { display: grid; gap: 7px; padding: 10px; border-top: 1px solid rgba(255,255,255,.84); background: rgba(255,255,255,.22); }
.path-stage:first-child { border-top: 0; }
.path-stage__toggle { display: flex; align-items: center; justify-content: space-between; gap: 10px; width: 100%; padding: 0; border: 0; color: inherit; background: transparent; cursor: pointer; text-align: left; }
.path-stage__toggle > span { display: grid; grid-template-columns: 24px minmax(0, 1fr); align-items: center; gap: 7px; min-width: 0; }
.path-stage__toggle small { color: var(--td-brand-color); font-size: 10px; font-weight: 600; }
.path-stage__toggle strong { overflow-wrap: anywhere; font-size: 12px; line-height: 18px; }
.path-stage__toggle svg { flex: none; transition: transform .16s ease; }
.path-stage__toggle svg.is-expanded { transform: rotate(180deg); }
.path-stage > p { margin: 0; color: var(--td-text-color-secondary); font-size: 11px; line-height: 17px; }
.path-stage__units { display: grid; gap: 7px; }
.path-step { display: grid; gap: 7px; width: 100%; padding: 12px; border: 1px solid rgba(255,255,255,.88); border-radius: var(--td-radius-medium); color: inherit; background: rgba(255,255,255,.62); }
.path-step:hover, .path-step.is-active { border-color: color-mix(in srgb, var(--td-brand-color) 36%, transparent); background: color-mix(in srgb, var(--td-brand-color-light) 48%, rgba(255,255,255,.8)); }
.path-step__toggle { display: grid; grid-template-columns: 22px minmax(0, 1fr); gap: 5px; align-items: start; width: 100%; padding: 0; border: 0; color: var(--td-text-color-primary); background: transparent; cursor: pointer; text-align: left; }
.path-step__toggle strong { overflow-wrap: anywhere; font-size: 12px; line-height: 17px; }
.path-step__question { display: grid; grid-template-columns: 58px minmax(0, 1fr); gap: 5px; margin: 0; overflow-wrap: anywhere; color: var(--td-text-color-secondary); font-size: 10px; line-height: 15px; }
.path-step small { color: var(--td-text-color-placeholder); font-size: 10px; }
.path-step__knowledge { display: flex; flex-wrap: wrap; gap: 5px; }
.knowledge-tag { max-width: 100%; overflow: hidden; padding: 3px 8px; border: 1px solid color-mix(in srgb, var(--td-brand-color) 26%, transparent); border-radius: var(--td-radius-round); color: var(--td-brand-color); background: color-mix(in srgb, var(--td-brand-color-light) 60%, rgba(255,255,255,.74)); cursor: pointer; font-size: 10px; text-overflow: ellipsis; white-space: nowrap; }
.knowledge-tag:hover, .knowledge-tag:focus-visible { border-color: var(--td-brand-color); background: var(--td-brand-color-light); }
.gap-analysis { margin: 8px 0 14px; border: 1px solid color-mix(in srgb, var(--td-warning-color) 36%, rgba(255,255,255,.82)); border-radius: var(--td-radius-large); background: color-mix(in srgb, var(--td-warning-color) 8%, rgba(255,255,255,.48)); backdrop-filter: blur(18px) saturate(112%); -webkit-backdrop-filter: blur(18px) saturate(112%); }
.gap-analysis.is-sufficient { border-color: color-mix(in srgb, var(--td-component-stroke) 72%, rgba(255,255,255,.82)); background: rgba(255,255,255,.38); }
.gap-analysis__toggle { display: flex; align-items: center; justify-content: space-between; gap: 10px; width: 100%; padding: 11px 12px; border: 0; color: inherit; background: transparent; cursor: pointer; text-align: left; }
.gap-analysis__toggle > span { display: grid; gap: 3px; min-width: 0; }
.gap-analysis__toggle strong { color: var(--td-warning-color); font-size: 12px; }
.gap-analysis.is-sufficient .gap-analysis__toggle strong { color: var(--td-text-color-primary); }
.gap-analysis__toggle small { overflow-wrap: anywhere; color: var(--td-text-color-secondary); font-size: 10px; line-height: 15px; }
.gap-analysis__toggle .gap-analysis__action { margin-top: 2px; color: var(--td-brand-color); font-weight: 500; }
.gap-analysis__toggle svg { flex: none; transition: transform .16s ease; }
.gap-analysis__toggle svg.is-expanded { transform: rotate(180deg); }
.gap-analysis__dimensions { display: grid; gap: 8px; padding: 0 12px 12px; }
.gap-analysis__dimension { padding-top: 8px; border-top: 1px solid color-mix(in srgb, var(--td-component-stroke) 65%, transparent); }
.gap-analysis__dimension strong { font-size: 11px; }
.gap-analysis__dimension p { margin: 4px 0 2px; color: var(--td-text-color-secondary); font-size: 10px; line-height: 15px; }
.gap-analysis__dimension small { color: var(--td-text-color-placeholder); font-size: 10px; line-height: 15px; }
.path-detail { display: grid; gap: 12px; margin-top: 10px; padding: 13px; border-top: 1px solid color-mix(in srgb, var(--td-brand-color) 24%, transparent); background: color-mix(in srgb, var(--td-brand-color-light) 34%, rgba(255,255,255,.82)); }
.path-detail__group { display: grid; gap: 7px; }
.path-detail__label { color: var(--td-text-color-secondary); font-size: 10px; }
.source-link { display: grid; grid-template-columns: 18px minmax(0, 1fr); align-items: center; gap: 8px; width: 100%; padding: 8px 0; border: 0; color: var(--td-brand-color); background: transparent; cursor: pointer; text-align: left; }
.source-link > span { display: grid; gap: 2px; min-width: 0; }
.source-link strong { overflow: hidden; color: var(--td-text-color-primary); font-size: 11px; font-weight: 500; text-overflow: ellipsis; white-space: nowrap; }
.source-link small { color: var(--td-text-color-secondary); font-size: 10px; }
@media (max-width: 980px) { .scene-view__overview { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (max-width: 1050px) { .training-layout { grid-template-columns: 1fr; } }
@media (max-width: 820px) { .scene-view__overview { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 520px) { .scene-stat { padding: 12px; }.scene-stat strong { font-size: 20px; }.training-detail { padding: 14px; } }
</style>
