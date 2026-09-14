import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const here = dirname(fileURLToPath(import.meta.url))
const list = readFileSync(join(here, 'McpToolsList.vue'), 'utf8')
const panel = readFileSync(join(here, 'McpMetadataPanel.vue'), 'utf8')

test('tool details open in a popup with description, parameters and schema tabs', () => {
  assert.match(list, /t-popup/)
  assert.match(list, /mcp-tool-detail-popup/)
  assert.match(list, /mcpMetadata.details/)
  assert.match(list, /mcpMetadata.description/)
  assert.match(list, /schemaView === tab/)
  assert.match(list, /mcpSchemaParameters/)
  assert.doesNotMatch(list, /t-dialog/)
  assert.doesNotMatch(list, /hideParameters/)
})

test('server documentation sits on the snapshot row as a popup', () => {
  assert.match(panel, /mcp-server-docs-popup-overlay/)
  assert.match(panel, /snapshot-meta__docs/)
  assert.doesNotMatch(panel, /<details/)
  assert.match(panel, /metadata-body/)
})
