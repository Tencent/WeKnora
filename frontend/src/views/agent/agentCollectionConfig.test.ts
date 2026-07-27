import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import type { CustomAgentConfig } from '@/api/agent'
import {
  createCollectionField,
  ensureCollectionDefaults,
  nextCollectionFieldKey,
  validateCollectionConfig,
} from './agentCollectionConfig.ts'

test('ensures backward-compatible collection defaults', () => {
  const config: CustomAgentConfig = {}
  ensureCollectionDefaults(config)
  assert.equal(config.collection_extraction_threshold, 0.85)
  assert.deepEqual(config.collection_fields, [])
})

test('validates choices and conditional ordering', () => {
  const config: CustomAgentConfig = {
    collection_enabled: true,
    collection_goal: '了解用户情况',
    collection_fields: [{
      key: 'reason', label: '原因', type: 'single_choice', required: true, enabled: true, order: 0,
      options: [{ id: 'yes', label: '是' }],
      visible_when: { field: 'future', operator: 'equals', value: 'yes' },
    }],
  }
  const errors = validateCollectionConfig(config)
  assert.ok(errors.some((error) => error.includes('2 到 50')))
  assert.ok(errors.some((error) => error.includes('前面的字段')))
})

test('generates a unique field key', () => {
  assert.equal(nextCollectionFieldKey([
    { key: 'field_2', label: 'A', type: 'short_text', required: false, enabled: true, order: 0 },
    { key: 'field_3', label: 'B', type: 'short_text', required: false, enabled: true, order: 1 },
  ]), 'field_4')
})

test('creates new collection fields as optional by default', () => {
  const field = createCollectionField([], 0)
  assert.equal(field.required, false)
  assert.equal(field.enabled, true)
})

test('published collection fields can be deleted from the editor', () => {
  const source = readFileSync(new URL('./components/AgentCollectionConfig.vue', import.meta.url), 'utf8')
  assert.match(source, /content="删除（历史数据保留为已停用字段）"/)
  assert.doesNotMatch(source, /:disabled="readOnly \|\| isPublished\(field\)"[^>]*@click="removeField/)
})
