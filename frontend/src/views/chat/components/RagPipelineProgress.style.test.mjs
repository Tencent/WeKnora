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
  assert.match(source, /tree-root-summary/)
  assert.match(source, /collapsedSummaryHtml/)
  assert.match(source, /visible = computed\(\(\) => steps\.value\.length > 0 \|\| showPrePipelineWait\.value\)/)
})

test('rag pipeline toggles expand and collapse from the root header', () => {
  assert.match(source, /class="action-card tree-root" @click="toggleExpanded"/)
  assert.match(source, /showExpandedTimeline \? 'chevron-down' : 'chevron-right'/)
  assert.doesNotMatch(source, /refsExpanded \? 'chevron/)
  assert.doesNotMatch(source, /tree-collapse-bar/)
})

test('only the collapsed root summary shows an expand chevron', () => {
  assert.match(source, /tree-root-summary[\s\S]*class="action-show-icon"/)
  assert.equal((source.match(/class="action-show-icon"/g) || []).length, 1)
})

test('rag pipeline embeds references in the timeline instead of a separate card', () => {
  assert.match(source, /timeline-mode/)
  assert.match(source, /content-only/)
  assert.match(source, /rag-ref-step[\s\S]*name="file-search"/)
})

test('rag pipeline keeps loading inside the timeline without duplicate dots', () => {
  assert.match(source, /showPrePipelineWait/)
  assert.match(source, /showActivityIndicator = computed\(\s*\(\) => steps\.value\.length > 0 && !hasAnswer\.value/)
  assert.match(source, /showPrePipelineWait = computed/)
})

test('rag pipeline places references before the done row', () => {
  const refsIndex = source.indexOf('class="tree-child rag-ref-step"')
  const doneIndex = source.indexOf('agent-step-done')
  assert.ok(refsIndex > -1 && doneIndex > -1)
  assert.ok(refsIndex < doneIndex)
})

test('clickable timeline headers use pointer cursor', () => {
  assert.match(source, /\.tool-event \{[\s\S]*\.action-header \{[\s\S]*cursor: pointer/)
  assert.match(source, /\.action-header \{[\s\S]*&\.no-results \{[\s\S]*cursor: default/)
  assert.match(source, /\.tree-root \{[\s\S]*cursor: pointer/)
})

test('collapsed summary uses the same title-to-answer spacing as agent', () => {
  assert.match(source, /\.tree-container \{\s*margin: 0 0 16px;/)
  assert.match(source, /\.rag-pipeline-progress \{[\s\S]*margin: 0;/)
})
