import assert from 'node:assert/strict'
import test from 'node:test'

import { localizedText } from './localizedText'

test('localizedText resolves exact locale, then language, then default', () => {
  const text = { default: 'Feishu', 'zh-CN': '飞书' }
  assert.equal(localizedText(text, 'zh-CN'), '飞书')
  assert.equal(localizedText(text, 'zh-TW'), '飞书')
  assert.equal(localizedText(text, 'en-US'), 'Feishu')
  assert.equal(localizedText(undefined, 'en-US'), '')
})
