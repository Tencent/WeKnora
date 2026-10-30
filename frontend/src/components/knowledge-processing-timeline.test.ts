import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'knowledge-processing-timeline.vue'), 'utf8')

test('counts skipped stages when determining the current stage', () => {
  const currentStageIndex = source.slice(
    source.indexOf('const currentStageIndex'),
    source.indexOf('function formatDuration'),
  )

  assert.match(currentStageIndex, /status === 'done' \|\| s\.status === 'skipped'/)
  assert.match(currentStageIndex, /Math\.min\(traversed \+ 1, stages\.value\.length\)/)
})

test('counts skipped stages in the completed stage total', () => {
  const stagesStatDisplay = source.slice(
    source.indexOf('const stagesStatDisplay'),
    source.indexOf('const postprocessTaskStats'),
  )

  assert.match(stagesStatDisplay, /status === 'done' \|\| s\.status === 'skipped'/)
  assert.match(stagesStatDisplay, /value: `\$\{completedCount\}\/\$\{total\}`/)
})

test('switching document fetches spans even while the previous request is in flight', () => {
  const knowledgeIdWatcher = source.slice(
    source.indexOf('() => props.knowledgeId,'),
    source.indexOf('function onKeydown'),
  )

  assert.match(knowledgeIdWatcher, /fetchSpans\(\{ force: true \}\)/)
})

test('switching attempt and retrying fetch spans even while a request is in flight', () => {
  const onAttemptChange = source.slice(
    source.indexOf('function onAttemptChange'),
    source.indexOf('() => props.knowledgeId,'),
  )
  const onRetry = source.slice(
    source.indexOf('async function onRetry'),
    source.indexOf('async function onManualRefresh'),
  )

  assert.match(onAttemptChange, /fetchSpans\(\{ force: true \}\)/)
  assert.match(onRetry, /fetchSpans\(\{ force: true \}\)/)
})

test('fetchSpans drops responses for a superseded request before touching state', () => {
  const fetchSpans = source.slice(
    source.indexOf('async function fetchSpans'),
    source.indexOf('function ensureAttemptStatuses'),
  )

  const staleCheck = fetchSpans.indexOf('if (!isCurrent()) return')
  assert.ok(staleCheck > 0, 'fetchSpans checks isCurrent after the request resolves')
  assert.ok(
    staleCheck < fetchSpans.indexOf('data.value = res.data'),
    'the stale check runs before the response is applied',
  )
  assert.ok(
    staleCheck < fetchSpans.indexOf("emit('update:hasSpans'"),
    'the stale check runs before hasSpans is reported to the parent',
  )
  assert.match(fetchSpans, /if \(issued\.seq === spansRequestSeq\) \{/)
})
