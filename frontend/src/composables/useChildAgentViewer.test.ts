import assert from 'node:assert/strict'
import { test } from 'node:test'
import { effectScope } from 'vue'
import { useChildAgentViewer } from './useChildAgentViewer'

test('opening a child in another message closes the previous viewer without a stale close clearing the new one', () => {
  const first = effectScope()
  const second = effectScope()
  const a = first.run(useChildAgentViewer)!
  const b = second.run(useChildAgentViewer)!
  a.value = 'a'
  assert.equal(a.value, 'a')
  assert.equal(b.value, '')
  b.value = 'b'
  assert.equal(a.value, '')
  assert.equal(b.value, 'b')
  first.stop()
  assert.equal(b.value, 'b')
  second.stop()
  assert.equal(b.value, '')
})
