import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const css = readFileSync(join(here, 'chat-timeline-loading.less'), 'utf8')

test('timeline loading uses shared gray bounce dots on the axis', () => {
  assert.match(css, /\.streaming-loading-node\s*\{[\s\S]*left:\s*-42px/)
  assert.match(css, /width:\s*4px/)
  assert.match(css, /background:\s*var\(--td-text-color-placeholder\)/)
  assert.match(css, /animation:\s*chatTimelineTypingBounce/)
  assert.match(css, /animation-delay:\s*0\.2s/)
})

test('in-progress step titles get the streaming shimmer sweep', () => {
  // Both timelines opt into the shimmer: agent (.action-pending) and RAG (.is-running).
  assert.match(css, /\.action-card\.action-pending \.action-name,\s*\.action-name\.is-running/)
  assert.match(css, /-webkit-background-clip:\s*text/)
  assert.match(css, /animation:\s*chatStreamShimmer/)
  assert.match(css, /@keyframes chatStreamShimmer/)
})

test('shimmer is disabled under reduced motion', () => {
  assert.match(css, /@media \(prefers-reduced-motion: reduce\)/)
  const reducedBlock = css.slice(css.indexOf('@media (prefers-reduced-motion: reduce)'))
  assert.match(reducedBlock, /animation:\s*none/)
})
