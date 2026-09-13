import assert from 'node:assert/strict'
import test from 'node:test'
import { nextTick, ref } from 'vue'
import { useFakeReveal } from './useFakeReveal'

test('reveals items one at a time and restarts when the source changes', async () => {
  const callbacks: Array<() => void> = []
  const previousWindow = globalThis.window
  globalThis.window = {
    matchMedia: () => ({ matches: false }),
    setInterval: (callback: () => void) => {
      callbacks.push(callback)
      return callbacks.length as unknown as number
    },
    clearInterval: () => undefined,
  } as unknown as Window & typeof globalThis

  try {
    const source = ref([1, 2, 3])
    const { visibleCount } = useFakeReveal(source, { intervalMs: 1 })
    assert.equal(visibleCount.value, 0)
    callbacks[0]()
    assert.equal(visibleCount.value, 1)
    callbacks[0]()
    assert.equal(visibleCount.value, 2)

    source.value = [4, 5]
    await nextTick()
    assert.equal(visibleCount.value, 0)
    callbacks[1]()
    assert.equal(visibleCount.value, 1)
  } finally {
    globalThis.window = previousWindow
  }
})

test('reveals all items when reduced motion is preferred', () => {
  const previousWindow = globalThis.window
  globalThis.window = {
    matchMedia: () => ({ matches: true }),
    clearInterval: () => undefined,
  } as unknown as Window & typeof globalThis

  try {
    const { visibleCount } = useFakeReveal(ref(['a', 'b']))
    assert.equal(visibleCount.value, 2)
  } finally {
    globalThis.window = previousWindow
  }
})
