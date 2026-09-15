import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

test('opens graph knowledge links in the current graph node detail panel', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, 'KnowledgeGraph.vue'), 'utf8')

  assert.match(source, /@select-graph-node="selectGraphNode"/)
  assert.match(source, /function selectGraphNode\(target: \{ targetPageId: string;/)
  assert.match(source, /fetchKnowledgeGraphDetail\(pageId\)/)
  assert.doesNotMatch(source, /name:\s*'knowledgeBaseDetail'/)
  assert.doesNotMatch(source, /openWikiPage/)
})

test('graph page exposes a URL-addressable scene view without blocking on graph data', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, 'KnowledgeGraph.vue'), 'utf8')

  assert.match(source, /import SceneView from '@\/components\/videohub\/SceneView\.vue'/)
  assert.match(source, /route\.query\.view === 'scene'/)
  assert.match(source, /<SceneView v-else-if="activeView === 'scene'" @select-wiki="selectGraphNode" @select-video="openVideo" \/>/)
  assert.match(source, /activeView === 'knowledge' && !payload/)
})

test('scene view keeps one refresh entry point and removes partial status banners', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, 'KnowledgeGraph.vue'), 'utf8')
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')

  assert.doesNotMatch(source, /class="knowledge-graph__status is-partial"/)
  assert.match(scene, /aria-label="刷新培训学习路径"/)
  assert.match(scene, /@click="refresh"/)
  assert.match(scene, /defineExpose\(\{ refresh \}\)/)
})

test('graph page shows actual canvas node counts only in filters', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, 'KnowledgeGraph.vue'), 'utf8')

  assert.doesNotMatch(source, /knowledge-graph__count|visibleCount|visibleRelationCount|counts\.scope_nodes/)
  assert.match(source, /catalogTotal\.value = next\.nodes\.length/)
  assert.match(source, /next\.nodes\.filter\(node => node\.attributes\.includes\(item\)\)\.length/)
  assert.match(source, /\[attribute\]: next\.nodes\.length/)
})

test('scene relation graph uses the shared canvas and opens relation summaries', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')

  assert.match(scene, /import GraphCanvas from '\.\/GraphCanvas\.vue'/)
  assert.match(scene, /<GraphCanvas/)
  assert.match(scene, /:relation-labels="relationTypeLabels"/)
  assert.match(scene, /:show-relation-labels="true"/)
  assert.match(scene, /:show-relation-arrows="true"/)
  assert.match(scene, /@node-click="selectGraphNode"/)
  assert.match(scene, /@click="selectRelation\(edge\.key\)"/)
  assert.match(scene, /v-if="activeRelation"/)
  assert.match(scene, /relationTypeLabels\[activeRelation\.relationType\]/)
  assert.match(scene, /activeRelation\.summary/)
  assert.match(scene, /function selectCluster\(key: string\) \{[\s\S]*?selectedRelation\.value = null/)
  assert.doesNotMatch(scene, /<svg/)
})

test('scene topic nodes use the shared graph canvas and keep full titles visible', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')
  const canvas = readFileSync(join(here, '../../components/videohub/GraphCanvas.vue'), 'utf8')

  assert.match(scene, /:full-labels="true"/)
  assert.match(scene, /label-position="left"/)
  assert.match(canvas, /overflow: props\.fullLabels \? 'break' : 'truncate'/)
  assert.match(canvas, /function labelLayout\(\)/)
})

test('scene view consumes the current training projection and evidence fields', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')
  const api = readFileSync(join(here, '../../api/videohub/trainingOrchestration.ts'), 'utf8')

  for (const label of ['培训主题', '培训视频', '引用知识', '学习时长', '上次生成']) {
    assert.match(scene, new RegExp(label))
  }
  for (const field of ['selected_videos', 'selected_knowledge_count', 'learning_duration_seconds', 'generated_at', 'topic_clusters', 'topic_cluster_relations']) {
    assert.match(scene, new RegExp(field))
  }
  assert.match(scene, /units: stages\.reduce\(\(total, stage\) => total \+ stage\.units\.length, 0\)/)
  assert.match(scene, /filter\(cluster => cluster\.path\.stages\.some\(stage => stage\.units\.length > 0\)\)/)
  assert.match(scene, /String\(unit\.sequence\)\.padStart\(2, '0'\)/)
  assert.match(scene, /学习任务/)
  assert.match(scene, /学完可以/)
  assert.match(scene, /<strong>\{\{ ref\.title \}\}<\/strong>/)
  assert.match(api, /title: string/)
  assert.match(scene, /selectedClusterRelations/)
  assert.match(scene, /edge\.connectedToSelection/)
  assert.match(scene, /fetchVideoOptions/)
  assert.match(scene, /videoTitle\(ref\.video_id\)/)
  assert.match(scene, /formatTimeRange\(ref\.start_ms, ref\.end_ms\)/)
  assert.match(api, /selected_videos: number/)
  assert.match(api, /selected_knowledge_count: number/)
  assert.match(api, /learning_duration_seconds: number/)
  assert.match(api, /topic_clusters: TrainingTopicCluster\[\]/)
  assert.match(api, /topic_cluster_relations:/)
  assert.match(api, /assertSupportedTrainingProjection\(projection\)/)
})

test('scene view places gap analysis below the complete learning path block', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')
  const pathStart = scene.indexOf('<div class="training-path">')
  const detailStart = scene.indexOf('<div v-if="activeUnit" class="path-detail">', pathStart)
  const gapStart = scene.indexOf('<section v-if="activeCluster.gap_analysis"', pathStart)

  assert.ok(pathStart >= 0)
  assert.ok(detailStart > pathStart)
  assert.ok(gapStart > pathStart)
  assert.ok(gapStart > detailStart)
})

test('meeting todos expose source evidence and a timestamped video jump', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const meeting = readFileSync(join(here, '../../components/videohub/MeetingSceneView.vue'), 'utf8')

  assert.match(meeting, /todo\.evidence_refs/)
  assert.match(meeting, /@click="openEvidence\(todo\.video_id, todo\.evidence_refs\)"/)
  assert.match(meeting, /emit\('selectVideo', videoId, refs\[0\] \? refs\[0\]\.start_ms \/ 1000 : 0\)/)
  assert.match(meeting, /簇内事项分支/)
  assert.match(meeting, /workItemStatusLabel\(item\.status\)/)
  assert.match(meeting, /v-if="item\.evidence_refs\.length"/)
})

test('meeting topic network uses an isolated native ECharts force renderer', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const meeting = readFileSync(join(here, '../../components/videohub/MeetingTopicGraph.vue'), 'utf8')
  const meetingScene = readFileSync(join(here, '../../components/videohub/MeetingSceneView.vue'), 'utf8')
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')

  assert.match(meetingScene, /import MeetingTopicGraph/)
  assert.match(scene, /import GraphCanvas/)
  assert.match(meeting, /echarts\/core/)
  assert.match(meeting, /GraphChart/)
  assert.match(meeting, /layout: 'force'/)
  assert.match(meeting, /scaleLimit: \{ min: 0\.4, max: 3 \}/)
  assert.match(meeting, /force:\s*\{/)
  assert.match(meeting, /roam: true/)
  assert.match(meeting, /edgeLabel:\s*\{/)
  assert.match(meeting, /symbolSize: Math\.min\(42, 10 \+ Math\.sqrt\(/)
  assert.match(meeting, /borderColor: 'rgba\(255, 255, 255, \.86\)'/)
  assert.match(meeting, /emphasis: \{[\s\S]*focus: 'adjacency'/)
  assert.match(meeting, /edgeSymbolSize/)
  assert.match(meeting, /resizeObserver = new ResizeObserver\(\(\) => \{ chart\?\.resize\(\); render\(\) \}\)/)
  assert.doesNotMatch(meeting, /<svg/)
})

test('scene evidence jumps preserve sub-second evidence offsets', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, 'KnowledgeGraph.vue'), 'utf8')

  assert.match(source, /function openVideo\(videoId: string, seconds: number\).*query\.t = Math\.max\(0, seconds\)/)
  assert.doesNotMatch(source, /function openVideo\(videoId: string, seconds: number\).*Math\.floor\(seconds\)/)
})

test('scene view loads and refreshes the real training orchestration API without fixtures', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')
  const api = readFileSync(join(here, '../../api/videohub/trainingOrchestration.ts'), 'utf8')

  assert.match(scene, /fetchCurrentTrainingProjection/)
  assert.match(scene, /generateTrainingProjection/)
  assert.match(scene, /fetchTrainingJob/)
  assert.match(scene, /while \(job\.status === 'queued' \|\| job\.status === 'running'\)/)
  assert.match(scene, /刷新培训学习路径/)
	assert.match(scene, /generateTrainingProjection\(\)/)
  assert.match(scene, /@click="emit\('selectWiki'/)
  assert.match(scene, /@click="emit\('selectVideo'/)
  assert.doesNotMatch(scene, /const trainingClusters = \[/)
  assert.doesNotMatch(scene, /ai-learning/)
  assert.match(api, /\/api\/custom\/training-orchestration\/current/)
  assert.match(api, /\/api\/custom\/training-orchestration\/generate/)
})

test('local video pages never switch to offline fixture data', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const sources = [
    readFileSync(join(here, '../../api/videohub/knowledgeGraph.ts'), 'utf8'),
    readFileSync(join(here, '../../api/videohub/relatedKnowledge.ts'), 'utf8'),
    readFileSync(join(here, 'VideoDetail.vue'), 'utf8'),
    readFileSync(join(here, '../../components/videohub/RelatedKnowledge.vue'), 'utf8'),
  ]
  for (const source of sources) {
    assert.doesNotMatch(source, /fixtures\/aiLearningEval|isAiLearningEvalFixtureEnabled|fixture=ai-learning/)
  }
})

test('graph page uses backend filtering and page-id detail/cross-video contracts', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, 'KnowledgeGraph.vue'), 'utf8')

  assert.match(source, /fetchKnowledgeGraph\(\{ mode: 'overview', limit, types: graphTypesForAttribute\(attribute\)/)
  assert.match(source, /fetchKnowledgeGraphDetail\(pageId\)/)
  assert.match(source, /fetchCrossVideoAssociations\(videoId, pageId\)/)
  assert.match(source, /cross-video-status="crossVideoStatus"/)
  assert.match(source, /@retry-detail="retryDetail"/)
  assert.match(source, /@retry-cross-video="retryCrossVideo"/)
  assert.match(source, /retryDetail\(\).*loadDetail\(selectedNode\.value, false\)/)
  assert.match(source, /const graphGate = createRequestGate\(\); const detailGate = createRequestGate\(\); const crossVideoGate = createRequestGate\(\)/)
  assert.match(source, /:related-edges="detailLoaded \? detailEdges : \[\]"/)
  assert.match(source, /watch\(selectedAttribute, value => \{ if \(payload\.value\) void loadGraph\(value\) \}\)/)
  assert.doesNotMatch(source, /!payload \|\| \(!payload\.nodes\.length/)
  assert.doesNotMatch(source, /node\.id\.replace/)
})

test('detail panel hides overview detail until the page-id request succeeds', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, '../../components/videohub/NodeDetailPanel.vue'), 'utf8')

  assert.match(source, /v-if="detailLoaded && detail"/)
  assert.match(source, /props\.detail \|\| null/)
  assert.match(source, /evidenceCount = computed\(\(\) => props\.evidence\?\.length \|\| 0\)/)
  assert.match(source, /validReading\.value\.filter\(item => item\.target_title\)/)
  assert.match(source, /v-if="crossVideoLoading \|\| crossVideoStatus !== 'idle'"/)
  assert.match(source, /detailStatus !== 'ready'/)
  assert.doesNotMatch(source, /props\.node\.knowledge_detail/)
})

test('detail panel keeps incoming graph relations when Wiki detail has outgoing relations', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, '../../components/videohub/NodeDetailPanel.vue'), 'utf8')

  assert.doesNotMatch(source, /if \(detail\.value\?\.relations\?\.length\)/)
  assert.match(source, /props\.relatedEdges\.map/)
  assert.match(source, /edge\.source === props\.node\.id/)
  assert.match(source, /edge\.target_title/)
  assert.match(source, /edge\.source_title/)
})

test('detail panel groups relations by target knowledge type', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, '../../components/videohub/NodeDetailPanel.vue'), 'utf8')

  assert.match(source, /<h3>知识关系/)
  assert.match(source, /关联实体/)
  assert.match(source, /关联概念/)
  assert.match(source, /关联方法论/)
  assert.match(source, /关联案例/)
  assert.match(source, /关联洞察/)
  assert.match(source, /v-if="relationGroups\.length"/)
  assert.match(source, /links: links\.filter\(link => link\.knowledgeType === group\.key\)/)
  assert.match(source, /在图谱中查看\$\{link\.title\}/)
  assert.match(source, /@click="selectGraphNode\(link\)"/)
  assert.match(source, /targetPageId/)
  assert.doesNotMatch(source, /正式关系/)
  assert.doesNotMatch(source, /阅读关联/)
  assert.doesNotMatch(source, />延伸阅读</)
  assert.doesNotMatch(source, /type: '延伸关系'/)
  assert.match(source, /\.node-panel__relation-group \{ display: grid; grid-template-columns: max-content minmax\(0, 1fr\);/)
  assert.match(source, /\.node-panel__relation-group h4 \{[^}]*white-space: nowrap;/)
  assert.match(source, /\.node-panel__relation-group \.node-panel__links button,[^}]*text-overflow: ellipsis; white-space: nowrap;/)
})

test('graph controls keep stable height and dense labels do not overlap', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const canvas = readFileSync(join(here, '../../components/videohub/GraphCanvas.vue'), 'utf8')
  const graphStyles = readFileSync(join(here, '../../components/videohub/graphStyles.ts'), 'utf8')
  const filters = readFileSync(join(here, '../../components/videohub/filterTabs.css'), 'utf8')
  const theme = readFileSync(join(here, '../../assets/theme/theme.css'), 'utf8')

  assert.match(canvas, /label:\s*\{\s*show:\s*true[^}]*overflow:\s*props\.fullLabels \? 'break' : 'truncate'/)
  assert.match(canvas, /function labelLayout\(\)[\s\S]*?moveOverlap: 'shiftY'/)
  assert.doesNotMatch(canvas, /link_count[^\n]*>=\s*3/)
  assert.match(canvas, /graph-canvas__legend/)
  assert.doesNotMatch(canvas, /graph-canvas__legend-dot[^}]*box-shadow/s)
  assert.doesNotMatch(canvas, /shadowBlur:\s*14/)
  assert.match(canvas, /focus:\s*'adjacency'/)
  assert.match(canvas, /blurScope:\s*'coordinateSystem'/)
  assert.match(canvas, /TooltipComponent/)
  assert.match(canvas, /const links = new Map<string, /)
  assert.match(canvas, /const key = \[source, target\]\.sort\(\)\.join/)
  assert.match(canvas, /if \(existing\.lineStyle\.type === 'dashed' && lineStyle\.type !== 'dashed'\)/)
  assert.match(canvas, /tooltip:\s*\{\s*show:\s*true,\s*formatter:\s*node\.label/)
  assert.match(canvas, /color:\s*color\('--td-text-color-secondary'\)/)
  assert.match(canvas, /background:\s*rgba\(232,239,236,\.42\)/)
  assert.doesNotMatch(canvas, /background:\s*rgba\(7,15,14/)
  assert.match(graphStyles, /'实体': '--color-data-1'/)
  assert.match(graphStyles, /'概念': '--color-data-2'/)
  assert.match(graphStyles, /'案例': '--color-data-3'/)
  assert.match(graphStyles, /'方法论': '--color-data-4'/)
  assert.match(graphStyles, /'洞察': '--color-data-5'/)
  assert.equal(theme.match(/--color-data-1: #7BC27C;/g)?.length, 2)
  assert.equal(theme.match(/--color-data-2: #6581C0;/g)?.length, 2)
  assert.equal(theme.match(/--color-data-3: #F08B67;/g)?.length, 2)
  assert.equal(theme.match(/--color-data-4: #C9CBC9;/g)?.length, 2)
  assert.equal(theme.match(/--color-data-5: #5F58A1;/g)?.length, 2)
  assert.match(filters, /\.videohub-filter-tabs\s*\{[^}]*min-height:\s*32px/s)
  assert.match(filters, /\.videohub-filter-tabs button\s*\{[^}]*min-height:\s*28px/s)
})
