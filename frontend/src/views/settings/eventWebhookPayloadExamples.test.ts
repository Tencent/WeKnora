import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  WEBHOOK_PAYLOAD_EXAMPLE_TYPES,
  webhookPayloadExample,
} from './eventWebhookPayloadExamples.ts'

test('payload examples are valid JSON with matching type', () => {
  for (const type of WEBHOOK_PAYLOAD_EXAMPLE_TYPES) {
    const parsed = JSON.parse(webhookPayloadExample(type)) as {
      spec_version: string
      type: string
      data: { resource: string }
    }
    assert.equal(parsed.spec_version, '1')
    assert.equal(parsed.type, type)
    assert.ok(parsed.data.resource)
  }
})

test('file created example includes a download ticket', () => {
  const parsed = JSON.parse(webhookPayloadExample('knowledge.created')) as {
    data: { download: { available: boolean; ticket: string } }
  }
  assert.equal(parsed.data.download.available, true)
  assert.match(parsed.data.download.ticket, /^wdt1\./)
})

test('kb.deleted example has no download field', () => {
  const parsed = JSON.parse(webhookPayloadExample('kb.deleted')) as {
    data: { download?: unknown; cascade_knowledge: boolean }
  }
  assert.equal(parsed.data.download, undefined)
  assert.equal(parsed.data.cascade_knowledge, true)
})
