import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

const source = readFileSync(new URL('./useAppViewport.ts', import.meta.url), 'utf8').replace(/^import.*\n/gm, '').replace('export function', 'function')
test('keyboard resize updates viewport height, pinch zoom preserves it, and last owner releases it', () => {
  const values = new Map(), mounted = [], unmounted = []
  const visualViewport = new EventTarget()
  visualViewport.height = 844
  visualViewport.scale = 1
  const window = new EventTarget()
  window.visualViewport = visualViewport
  window.innerHeight = 844
  const use = vm.runInNewContext(ts.transpile(`${source}\nuseAppViewport`), {
    window, document: { documentElement: { style: {
      setProperty: (key, value) => values.set(key, value), removeProperty: key => values.delete(key),
    } } }, useFont: () => ({ currentSize: {} }), getRootZoom: () => 1,
    onMounted: fn => mounted.push(fn), onUnmounted: fn => unmounted.push(fn), watch() {}, nextTick: fn => fn(),
  })
  use(); use()
  mounted.forEach(fn => fn())
  assert.equal(values.get('--app-viewport-height'), '844px')
  visualViewport.height = 420
  visualViewport.dispatchEvent(new Event('resize'))
  assert.equal(values.get('--app-viewport-height'), '420px')
  visualViewport.scale = 2
  visualViewport.height = 200
  visualViewport.dispatchEvent(new Event('resize'))
  assert.equal(values.get('--app-viewport-height'), '420px')
  unmounted[0]()
  assert.equal(values.get('--app-viewport-height'), '420px')
  unmounted[1]()
  assert.equal(values.has('--app-viewport-height'), false)
})
