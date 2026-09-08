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
  assert.match(source, /<SceneView v-else-if="activeView === 'scene'" ref="sceneViewRef" \/>/)
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
  assert.match(scene, /function selectCluster\(key: string\) \{ selectedCluster\.value = key; selectedRelation\.value = null;/)
  assert.doesNotMatch(scene, /关系依据|起点主题簇|终点主题簇/)
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

test('detail panel groups formal and reading relations with graph targets', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, '../../components/videohub/NodeDetailPanel.vue'), 'utf8')

  assert.match(source, /<h3>知识关系/)
  assert.match(source, /<h4>正式关系/)
  assert.match(source, /<h4>阅读关联/)
  assert.match(source, /getRelationDescription\(title, edge\.type, outgoing\)/)
  assert.match(source, /在图谱中查看\$\{link\.title\}/)
  assert.match(source, /@click="selectGraphNode\(link\)"/)
  assert.match(source, /targetPageId/)
  assert.doesNotMatch(source, />延伸阅读</)
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
