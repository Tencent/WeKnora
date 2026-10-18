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

test('mention menu exposes a search box that is editable only when opened from the button', () => {
  assert.match(source, /class="mention-search__input"/)
  assert.match(source, /:readonly="!searchable"/)
  assert.match(source, /emit\('update:query'/)
  assert.match(source, /emit\('close'\)/)
})

test('global search caps each group and lets the user drill into the full group', () => {
  assert.match(source, /const FLAT_GROUP_LIMIT = 5/)
  assert.match(source, /mentionDetail\.viewAll/)
  assert.match(source, /enterActiveGroup/)
  // 分组内输入关键词时保持在分组内过滤，而不是跳回全局结果
  assert.match(source, /isFlatMode = computed\(\(\) => !currentGroupType\.value && trimmedQuery\.value\.length > 0\)/)
})

test('mention menu follows the shared popover look of the other input selectors', () => {
  assert.match(source, /border-radius: var\(--app-radius-lg\)/)
  assert.match(source, /box-shadow: var\(--td-shadow-2\)/)
  assert.doesNotMatch(source, /label: '标签'/)
})
