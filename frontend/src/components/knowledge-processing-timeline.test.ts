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
