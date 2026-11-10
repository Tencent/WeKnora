import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./desktopProjectDir.ts', import.meta.url), 'utf8')

test('dirFromPickerResponse reads the HTTP envelope', () => {
  assert.match(source, /body\?\.data\?\.dir/)
  assert.match(source, /typeof dir === 'string'/)
})

test('pickHostProjectDir calls the same-process HTTP picker', () => {
  assert.match(source, /\/api\/v1\/system\/host-project-dir/)
  assert.match(source, /timeout: 0/)
  assert.doesNotMatch(source, /window\['go'\]/)
  assert.doesNotMatch(source, /window\.go\.main/)
  assert.doesNotMatch(source, /EventsEmit/)
})
