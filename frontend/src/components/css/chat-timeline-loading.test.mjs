import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const css = readFileSync(join(here, 'chat-timeline-loading.less'), 'utf8')

test('shared timeline styles do not add a detached loading row', () => {
  assert.doesNotMatch(css, /\.streaming-loading-node/)
  assert.doesNotMatch(css, /chatTimelineTypingBounce/)
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
