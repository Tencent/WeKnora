import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(join(here, 'MentionSelector.vue'), 'utf8')

test('MCP mention hover shows catalog summary instead of a bare name', () => {
  assert.match(source, /mentionDetail.mcpToolCount/)
  assert.match(source, /mentionDetail.mcpNotSynced/)
  assert.match(source, /mentionDetail.mcpStale/)
  assert.match(source, /item.type === 'mcp' && item.catalogSynced/)
  assert.doesNotMatch(source, /detail-type-badge doc">MCP/)
  assert.doesNotMatch(source, /t-dialog/)
})
