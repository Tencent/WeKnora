import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./index.vue', import.meta.url), 'utf8')

test('knowledge graph uses the same frosted sidebar shell as video home', () => {
  assert.match(source, /route\.name === 'videoList' \|\| route\.name === 'videoDetail' \|\| route\.name === 'knowledgeGraph'/)
  assert.match(source, /\.main--video-home > \.aside_box/)
})
