import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import {
  clampInstallPercent,
  formatBusyInstallStatus,
  liveInstallPercent,
  progressKey,
} from './skillInstallProgress.ts'

const source = readFileSync(new URL('./useConfigSkillInstallProgress.ts', import.meta.url), 'utf8')

test('catalog install progress follows the same SSE as the sandbox panel', () => {
  assert.match(source, /fetchEventSource/)
  assert.match(source, /configSkillInstallEventsUrl/)
  assert.match(source, /openWhenHidden: true/)
  assert.match(source, /if \(!configId \|\| !skillId \|\| abortByKey\.has\(key\)\) return/)
  assert.doesNotMatch(
    source,
    /existing\?\.done && existing\.stage !== 'detached'/,
    'a done event must not block reconnect: retry reuses the same skill id',
  )
})

test('progress keys isolate one install per sandbox', () => {
  assert.equal(progressKey('cfg-a', 'skill-1'), 'cfg-a:skill-1')
})

test('percent values are clamped to 0-100', () => {
  assert.equal(clampInstallPercent(37.4), 37)
  assert.equal(clampInstallPercent(-8), 0)
  assert.equal(clampInstallPercent(140), 100)
  assert.equal(clampInstallPercent('37'), null)
  assert.equal(clampInstallPercent(Number.NaN), null)
})

test('live percent hides a closed stream that never reported progress', () => {
  assert.equal(liveInstallPercent(undefined), null)
  assert.equal(liveInstallPercent({ percent: 0, stage: 'installing', done: true }), null)
  assert.equal(liveInstallPercent({ percent: 0, stage: 'accepted', done: false }), 0)
  assert.equal(liveInstallPercent({ percent: 37, stage: 'installing', done: false }), 37)
  assert.equal(liveInstallPercent({ percent: 100, stage: 'done', done: true }), 100)
})

test('busy status appends a live percent when one exists', () => {
  assert.equal(formatBusyInstallStatus('安装中', null), '安装中')
  assert.equal(formatBusyInstallStatus('安装中', 37), '安装中 · 37%')
})
