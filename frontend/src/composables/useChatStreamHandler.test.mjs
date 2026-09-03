import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./useChatStreamHandler.ts', import.meta.url), 'utf8')

test('failed tool results keep stdout/output instead of replacing it with the short error', () => {
  assert.match(source, /toolCallEvent\.output = dataPayload\.output \|\| data\.content/)
  assert.doesNotMatch(
    source,
    /toolCallEvent\.output = success\s*\?[\s\S]*dataPayload\.error/,
  )
})
