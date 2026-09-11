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
  assert.match(source, /<SceneView v-else-if="activeView === 'scene'" ref="sceneViewRef" @select-wiki="selectGraphNode" @select-video="openVideo" \/>/)
  assert.match(source, /activeView === 'knowledge' && !payload/)
})

test('graph page keeps one refresh action and removes partial status banners', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, 'KnowledgeGraph.vue'), 'utf8')
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')

  assert.equal(source.match(/>刷新<\/t-button>/g)?.length, 1)
  assert.doesNotMatch(source, /class="knowledge-graph__status is-partial"/)
  assert.match(source, /refreshCurrentView/)
  assert.match(source, /await loadGraph\(\)/)
  assert.match(source, /sceneViewRef\.value\?\.refresh\(\)/)
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

test('scene relation lines open only the relation type and summary', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')

  assert.match(scene, /class="topic-edge"/)
  assert.match(scene, /@click="selectRelation\(edge\.key\)"/)
  assert.match(scene, /@keydown\.enter="selectRelation\(edge\.key\)"/)
  assert.match(scene, /@keydown\.space\.prevent="selectRelation\(edge\.key\)"/)
  assert.match(scene, /v-if="activeRelation"/)
  assert.match(scene, /relationTypeLabels\[activeRelation\.relationType\]/)
  assert.match(scene, /activeRelation\.summary/)
  assert.match(scene, /function selectCluster\(key: string\) \{[\s\S]*?selectedCluster\.value = key[\s\S]*?selectedRelation\.value = null/)
  assert.doesNotMatch(scene, /关系依据|起点主题簇|终点主题簇/)
  assert.doesNotMatch(scene, /项目会议|meetingTopics|meeting-layout/)
  assert.match(scene, /id="topic-edge-arrow"/)
  assert.match(scene, /isDirectedRelation\(edge\.relationType\)/)
  assert.match(scene, /\.topic-edge\.is-complementary \.topic-edge__line \{ stroke-dasharray: 8 6; \}/)
  assert.match(scene, /\.topic-edge\.is-contrast \.topic-edge__line \{ stroke-dasharray: 2 5; \}/)
  assert.match(scene, /\.topic-network \{[^}]*min-width: 720px;/)
  assert.match(scene, /@media \(max-width: 1050px\) \{ \.training-layout \{ grid-template-columns: 1fr; \}/)
})

test('scene view consumes every user-facing training orchestration field group', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')
  const api = readFileSync(join(here, '../../api/videohub/trainingOrchestration.ts'), 'utf8')

  for (const label of ['培训主题', '培训视频', '引用知识', '学习时长', '上次生成']) {
    assert.match(scene, new RegExp(label))
  }
  for (const field of ['scanned_videos', 'qualified_videos', 'selected_videos', 'not_selected_videos', 'skipped_videos']) {
    assert.match(scene, new RegExp(field))
  }
  assert.match(scene, /topic_source_counts/)
  assert.match(scene, /not_selected_reason_counts/)
  assert.match(scene, /skipped_reason_counts/)
  assert.match(scene, /units: cluster\.member_topics\.length/)
  assert.match(scene, /cluster\.units \}\} 个单元 · \{\{ cluster\.videos \}\} 个视频/)
  assert.match(scene, /String\(unit\.sequence\)\.padStart\(2, '0'\)/)
  assert.match(scene, /你将解决/)
  assert.match(scene, /学完可以/)
  assert.match(scene, /selectedClusterRelations/)
  assert.match(scene, /edge\.connectedToSelection/)
  assert.match(scene, /routeTrainingEdge\(sourceCenter, targetCenter, obstacleCenters/)
  assert.match(scene, /@focus="previewRelation\(edge\.key\)"/)
  assert.match(scene, /relationPreview\.summary/)
  assert.match(scene, /<title>\{\{ relationTypeLabels\[edge\.relationType\] \}\}：\{\{ edge\.summary \}\}<\/title>/)
  assert.match(scene, /fetchVideoOptions/)
  assert.match(scene, /videoTitle\(ref\.video_id\)/)
  assert.match(scene, /formatTimeRange\(ref\.start_ms, ref\.end_ms\)/)
  assert.match(api, /not_selected_reason_counts: Record<TrainingNotSelectedReason, number>/)
  assert.match(api, /skipped_reason_counts: Record<TrainingSkipReason, number>/)
  assert.match(api, /topic_source_counts: Record<TrainingTopicSource, number>/)
  assert.match(api, /assertSupportedTrainingProjection\(projection\)/)
})

test('meeting todos expose source evidence and a timestamped video jump', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const meeting = readFileSync(join(here, '../../components/videohub/MeetingSceneView.vue'), 'utf8')

  assert.match(meeting, /原文证据 · \{\{ todo\.timeRange \}\}/)
  assert.match(meeting, /todo\.evidenceQuote/)
  assert.match(meeting, /@click="openTodoEvidence\(todo\)"/)
  assert.match(meeting, /emit\('selectVideo', todo\.videoId, todo\.seconds\)/)
})

test('scene view loads and refreshes the real training orchestration API without fixtures', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const scene = readFileSync(join(here, '../../components/videohub/SceneView.vue'), 'utf8')
  const api = readFileSync(join(here, '../../api/videohub/trainingOrchestration.ts'), 'utf8')

  assert.match(scene, /fetchCurrentTrainingProjection/)
  assert.match(scene, /generateTrainingProjection/)
  assert.match(scene, /fetchTrainingJob/)
  assert.match(scene, /while \(job\.status === 'queued' \|\| job\.status === 'running'\)/)
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

  assert.match(canvas, /label:\s*\{\s*show:\s*true[^}]*overflow:\s*'truncate'/)
  assert.match(canvas, /labelLayout:\s*\{\s*hideOverlap:\s*false,\s*moveOverlap:\s*'shiftY'/)
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
