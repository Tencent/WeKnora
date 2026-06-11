import assert from 'node:assert/strict'
import test from 'node:test'
import { buildQueryWithHostContext } from './embedContext.ts'

test('buildQueryWithHostContext leaves query unchanged when context is empty', () => {
  assert.equal(buildQueryWithHostContext('hello', {}), 'hello')
  assert.equal(buildQueryWithHostContext('hello'), 'hello')
})

test('buildQueryWithHostContext prefixes host fields', () => {
  const out = buildQueryWithHostContext('pricing?', {
    user_id: 'u-1',
    page: '/checkout',
  })
  assert.match(out, /\[Host context\]/)
  assert.match(out, /user_id: u-1/)
  assert.match(out, /page: \/checkout/)
  assert.match(out, /pricing\?$/)
})
