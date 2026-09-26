import assert from 'node:assert/strict'
import test from 'node:test'
import { computed } from 'vue'

import { createRegistry } from './registry'

test('registry registers, rejects duplicates and unregisters', () => {
  const r = createRegistry<{ key: string; n: number }>('thing')
  const off = r.register({ key: 'a', n: 1 })
  r.register({ key: 'b', n: 2 })
  assert.deepEqual(r.items.map((i) => i.key), ['a', 'b'])
  assert.equal(r.get('b')?.n, 2)
  assert.throws(() => r.register({ key: 'a', n: 3 }), /thing "a" is already registered/)
  off()
  assert.deepEqual(r.items.map((i) => i.key), ['b'])
  r.unregister('missing')
  assert.equal(r.items.length, 1)
})

test('registry reads are reactive', () => {
  const r = createRegistry<{ key: string }>('thing')
  const count = computed(() => r.items.length)
  assert.equal(count.value, 0)
  r.register({ key: 'x' })
  assert.equal(count.value, 1)
})
