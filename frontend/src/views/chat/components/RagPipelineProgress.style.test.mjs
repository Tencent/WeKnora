import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'RagPipelineProgress.vue'), 'utf8')

test('rag pipeline uses agent-style timeline structure', () => {
  assert.match(source, /class="tree-children"/)
  assert.match(source, /class="tree-child/)
  assert.match(source, /getAgentToolIconName/)
  assert.match(source, /name="check-circle"/)
  assert.match(source, /streaming-loading-node/)
  assert.match(source, /@import ['"]@\/components\/css\/chat-timeline-loading\.less['"]/)
  assert.match(source, /search-results-summary-fixed/)
})

test('rag pipeline persists and collapses after the answer arrives', () => {
  assert.match(source, /showCollapsedRoot/)
  assert.match(source, /tree-root-status/)
  assert.match(source, /collapsedStatusText/)
  assert.match(source, /referenceSummaryText/)
  assert.match(source, /hasThinking\.value/)
  assert.match(source, /const visible = computed\([\s\S]*showPrePipelineWait\.value/)
})

test('rag pipeline toggles expand and collapse from the root header', () => {
  assert.match(source, /class="tree-root-expand"/)
  assert.match(source, /@click="toggleExpanded"/)
  assert.match(source, /showExpandedTimeline \? 'chevron-down' : 'chevron-right'/)
  assert.doesNotMatch(source, /refsExpanded \? 'chevron/)
  assert.doesNotMatch(source, /tree-collapse-bar/)
})

test('only the collapsed root summary shows an expand chevron', () => {
  const template = source.split('<script')[0]
  assert.match(template, /tree-root-expand__icon/)
  assert.equal((template.match(/tree-root-expand__icon/g) || []).length, 1)
})

test('rag pipeline opens references from search steps and the drawer composable', () => {
  assert.match(source, /useChatReferencesDrawer/)
  assert.match(source, /toggleReferencesDrawer/)
  assert.match(source, /has-reference-trigger/)
  assert.match(source, /handleStepClick/)
})

test('rag pipeline uses a native pending step and lets the thinking title shimmer while pending', () => {
  assert.match(source, /showPrePipelineWait/)
  assert.match(source, /class="action-card action-pending"/)
  assert.match(source, /t\('chat\.thinkingAlt'\)/)
  assert.match(source, /showThinkingStep/)
  assert.match(source, /'action-pending': thinkingPending/)
  assert.match(source, /hasThinkingEvent/)
  assert.doesNotMatch(source, /thinking-loading/)
  assert.doesNotMatch(source, /showActivityIndicator/)
})

test('done row appears only after the full turn completes', () => {
  assert.match(source, /const showDoneRow = computed\(\(\) => \{[\s\S]*hasAnswer\.value/)
})

test('rag pipeline renders model thinking inside the timeline before the done row', () => {
  assert.match(source, /rag-thinking-step/)
  assert.match(source, /showThinkingStep/)
  assert.match(source, /name="lightbulb"/)
  const doneIndex = source.indexOf('agent-step-done')
  const thinkingIndex = source.indexOf('rag-thinking-step')
  assert.ok(doneIndex > -1 && thinkingIndex > -1)
  assert.ok(thinkingIndex < doneIndex)
})

test('clickable timeline headers use pointer cursor', () => {
  assert.match(source, /\.tool-event \{[\s\S]*\.action-header \{[\s\S]*cursor: pointer/)
  assert.match(source, /\.action-header \{[\s\S]*&\.no-results \{[\s\S]*cursor: default/)
  assert.match(source, /\.has-reference-trigger \{[\s\S]*cursor: pointer/)
})

test('collapsed summary uses compact spacing before the answer', () => {
  assert.match(source, /\.tree-container \{\s*margin: 0 0 8px;/)
  assert.match(source, /\.rag-pipeline-progress \{[\s\S]*margin: 0;/)
})

test('rag pipeline auto-scrolls capped thinking detail while streaming', () => {
  assert.match(source, /isThinkingStreaming/)
  assert.match(source, /scrollThinkingDetailToBottom/)
  assert.match(source, /watch\(thinkingContent[\s\S]*scrollThinkingDetailToBottom/)
})

test('rag pipeline includes attachment prep steps on the timeline', () => {
  assert.match(source, /RAG_TIMELINE_TOOL_NAMES/)
  assert.match(source, /getAttachmentParsingSummaryHtml/)
  assert.match(source, /isAttachmentTool/)
})
