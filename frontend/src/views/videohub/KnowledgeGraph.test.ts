import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

test('opens graph knowledge links through the native Wiki reading drawer', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const source = readFileSync(join(here, 'KnowledgeGraph.vue'), 'utf8')

  assert.match(source, /tab:\s*'graph',\s*slug/)
  assert.match(source, /name:\s*'knowledgeBaseDetail'/)
  assert.match(source, /params:\s*\{\s*kbId\s*\}/)
})
