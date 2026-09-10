import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, nextTick, ref } from 'vue'
import { useBrowserTextInput } from '../../../composables/useBrowserTextInput.ts'

const source = readFileSync(new URL('./SandboxBrowser.vue', import.meta.url), 'utf8')
const script = source.split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*$/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
const tick = () => new Promise(resolve => setImmediate(resolve))
function setup() {
  const requests = []
  let callbacks
  const ui = vm.runInNewContext(compiled + '\n;({ frame, actualSize, controlling, browserState, busy, controlBusy, navigationBusy, canInteract, address, addressEditing, addressDirty, textInput, remoteCursor, pointFor, onWheel, navigate, send, pumpHover, queueHover, cancelHover, get wheelPump() { return wheelPump } })', {
    ref, computed, nextTick, useBrowserTextInput, crypto: { randomUUID: () => 'test-control-token' },
    defineProps: () => ({ sessionId: 'test' }), useI18n: () => ({ t: key => key }),
    useBrowserStream(options) {
      callbacks = options
      return { connected: ref(true), connecting: ref(false), connect() {}, disconnect() {},
        command(cmd) { return new Promise(resolve => requests.push({ cmd, resolve })) } }
    },
    onMounted() {}, onBeforeUnmount() {}, document: { hidden: false }, setTimeout, clearTimeout,
  })
  ui.frame.value = { ok: true, capabilities: ['stream', 'cursor'], url: 'https://example.com', revision: 1 }
  ui.browserState.value = 'running'; ui.controlling.value = true
  return { ui, requests, callbacks }
}

test('trackpad bursts coalesce without disabling inputs or leaving a scroll backlog', async () => {
  const { ui, requests } = setup()
  let prevented = 0
  const wheel = { deltaY: 20, deltaMode: 0, preventDefault() { prevented++ } }
  ui.onWheel(wheel)
  for (let i = 0; i < 50; i++) ui.onWheel(wheel)
  await tick()
  assert.equal(requests.length, 1)
  assert.equal(ui.busy.value, false)
  assert.equal(ui.canInteract.value, true)
  requests[0].resolve({ ok: true, controlled: true })
  await tick()
  assert.equal(requests.length, 2)
  assert.equal(requests[1].cmd.delta, 1000)
  requests[1].resolve({ ok: true, controlled: true })
  await ui.wheelPump
  assert.equal(requests.length, 2)
  assert.equal(prevented, 51)
  assert.equal(ui.canInteract.value, true)
})

test('view-only scrolling stays local; loss of control discards accumulated scroll', async () => {
  const { ui, requests } = setup()
  const wheel = { deltaY: 100, deltaMode: 1, preventDefault() {} }
  ui.onWheel(wheel); ui.onWheel(wheel)
  await tick()
  assert.equal(requests[0].cmd.delta, 1200)
  ui.controlling.value = false
  requests[0].resolve({ ok: true, controlled: false })
  await ui.wheelPump
  ui.onWheel({ ...wheel, preventDefault() { assert.fail('view-only scroll should not be trapped') } })
  assert.equal(requests.length, 1)
})

test('remote URL updates preserve an address draft', async () => {
  const { ui, callbacks, requests } = setup()
  ui.address.value = 'https://draft.example'; ui.addressEditing.value = true; ui.addressDirty.value = true
  callbacks.onURL('https://remote.example')
  assert.equal(ui.address.value, 'https://draft.example')
  await tick(); requests[0].resolve({ ok: true, url: 'https://remote.example', controlled: true })
  await tick()
  assert.equal(ui.address.value, 'https://draft.example')
  ui.addressEditing.value = false; ui.addressDirty.value = false
  callbacks.onURL('https://new.example')
  assert.equal(ui.address.value, 'https://new.example')
  await tick(); requests[1].resolve({ ok: true })
})

test('hover feedback is optional, bounded to CSS cursor keywords and discarded after leaving', async () => {
  const { ui, requests } = setup()
  ui.queueHover({ x: 50, y: 50 })
  await new Promise(resolve => setTimeout(resolve, 100))
  assert.equal(requests.length, 1)
  assert.equal(requests[0].cmd.action, 'hover')
  requests[0].resolve({ ok: true, cursor: 'url(https://untrusted.example/cursor), pointer' })
  await tick(); assert.equal(ui.remoteCursor.value, 'crosshair')
  ui.queueHover({ x: 60, y: 50 })
  await new Promise(resolve => setTimeout(resolve, 100))
  ui.cancelHover(); requests[1].resolve({ ok: true, cursor: 'pointer' })
  await tick(); assert.equal(ui.remoteCursor.value, 'crosshair')
})


test('pointer mapping stays in remote coordinates in fit and actual-size modes', () => {
  const { ui } = setup()
  ui.frame.value.width = 1280; ui.frame.value.height = 800
  for (const scale of [0.5, 1]) {
    const point = ui.pointFor({ clientX: 40 + 400 * scale, clientY: 20 + 300 * scale,
      currentTarget: { getBoundingClientRect: () => ({ left: 40, top: 20, width: 1280 * scale, height: 800 * scale }) } })
    assert.equal(point.x, 400)
    assert.equal(point.y, 300)
  }
})


test('actual-size mode allows horizontal local panning while vertical scrolling controls the page', () => {
  const { ui, requests } = setup()
  ui.actualSize.value = true
  ui.onWheel({ deltaX: 100, deltaY: 0, preventDefault() { assert.fail('horizontal panning should remain local') } })
  ui.onWheel({ deltaX: 0, deltaY: 100, shiftKey: true, preventDefault() { assert.fail('Shift+wheel should remain local') } })
  assert.equal(requests.length, 0)
})
