import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import { effectScope, nextTick, reactive, shallowRef as ref, watch } from 'vue'
import { parse, compileStyleAsync } from '@vue/compiler-sfc'

const source = readFileSync(new URL('./document-preview.vue', import.meta.url), 'utf8')
const start = source.indexOf('function toggleFullscreen()')
const end = source.indexOf('function ensureBlobType(', start)
assert.ok(start >= 0 && end > start)
const fullscreenSource = ts.transpile(source.slice(start, end))

function setup(t, native = true) {
  const scope = effectScope()
  const mounted = [], unmounted = [], listeners = new Map()
  const events = {
    addEventListener(name, fn, capture) { listeners.set(name, { fn, capture }) },
    removeEventListener(name, fn) {
      if (listeners.get(name)?.fn === fn) listeners.delete(name)
    },
  }
  const document = { ...events, body: { style: { overflow: 'clip' } }, fullscreenElement: null }
  const isFullscreen = ref(false)
  const props = reactive({ active: true })
  const root = { isConnected: true }
  if (native) {
    root.requestFullscreen = async () => {
      document.fullscreenElement = root
      listeners.get('fullscreenchange').fn()
    }
  }
  document.exitFullscreen = async () => {
    document.fullscreenElement = null
    listeners.get('fullscreenchange')?.fn()
  }
  const state = scope.run(() => vm.runInNewContext(`${fullscreenSource}; ({
    enterPreviewFullscreen, exitPreviewFullscreen, toggleFullscreen, onFullscreenEscape,
  })`, {
    props, isFullscreen, previewRoot: ref(root), document, window: events, watch,
    onMounted: fn => mounted.push(fn), onUnmounted: fn => unmounted.push(fn),
  }))
  mounted.forEach(fn => fn())
  const dispose = () => { unmounted.splice(0).forEach(fn => fn()); scope.stop() }
  t.after(dispose)
  return { ...state, props, root, document, isFullscreen, listeners, dispose }
}

test('native fullscreen synchronizes browser Escape and button exit without changing body scroll', async t => {
  const ctx = setup(t)
  await ctx.enterPreviewFullscreen()
  assert.equal(ctx.document.fullscreenElement, ctx.root)
  assert.equal(ctx.isFullscreen.value, true)
  assert.equal(ctx.document.body.style.overflow, 'clip')
  // Browser Escape works even when the iframe consumes all keyboard events.
  ctx.document.fullscreenElement = null
  ctx.listeners.get('fullscreenchange').fn()
  assert.equal(ctx.isFullscreen.value, false)
  await ctx.enterPreviewFullscreen()
  await ctx.exitPreviewFullscreen()
  assert.equal(ctx.isFullscreen.value, false)
  assert.equal(ctx.document.fullscreenElement, null)
})

test('fallback Escape exits only fullscreen and restores the existing scroll lock', async t => {
  const ctx = setup(t, false)
  await ctx.enterPreviewFullscreen()
  assert.equal(ctx.isFullscreen.value, true)
  assert.equal(ctx.document.body.style.overflow, 'hidden')
  assert.equal(ctx.listeners.get('keydown').capture, true)
  const calls = []
  ctx.onFullscreenEscape({ key: 'Escape', preventDefault: () => calls.push('prevent'), stopImmediatePropagation: () => calls.push('stop') })
  assert.deepEqual(calls, ['prevent', 'stop'])
  assert.equal(ctx.isFullscreen.value, false)
  assert.equal(ctx.document.body.style.overflow, 'clip')
  ctx.onFullscreenEscape({ key: 'Escape', preventDefault: () => assert.fail('regular Escape belongs to the drawer') })
})

test('ordinary keys and IME Escape are left alone', async t => {
  const ctx = setup(t, false)
  await ctx.enterPreviewFullscreen()
  for (const event of [{ key: 'PageDown' }, { key: 'Escape', isComposing: true }]) {
    ctx.onFullscreenEscape({ ...event, preventDefault: () => assert.fail('key must remain native') })
  }
  assert.equal(ctx.isFullscreen.value, true)
})

test('a denied native request falls back and deactivation exits fullscreen', async t => {
  const ctx = setup(t)
  ctx.root.requestFullscreen = async () => { throw new Error('Permissions policy') }
  await ctx.enterPreviewFullscreen()
  assert.equal(ctx.isFullscreen.value, true)
  ctx.props.active = false
  await nextTick()
  assert.equal(ctx.isFullscreen.value, false)
  assert.equal(ctx.document.body.style.overflow, 'clip')
})

test('native fullscreen is exited when the preview is hidden', async t => {
  const ctx = setup(t)
  await ctx.enterPreviewFullscreen()
  ctx.props.active = false
  await nextTick()
  assert.equal(ctx.document.fullscreenElement, null)
  assert.equal(ctx.isFullscreen.value, false)
})

test('teardown restores fallback styles and unregisters both listeners', async t => {
  const ctx = setup(t, false)
  await ctx.enterPreviewFullscreen()
  ctx.dispose()
  assert.equal(ctx.document.body.style.overflow, 'clip')
  assert.equal(ctx.listeners.size, 0)
  await ctx.enterPreviewFullscreen()
  assert.equal(ctx.isFullscreen.value, false)
})

test('a late native request cannot leave a disposed preview fullscreen', async t => {
  const ctx = setup(t)
  let complete
  ctx.root.requestFullscreen = () => new Promise(resolve => {
    complete = () => { ctx.document.fullscreenElement = ctx.root; resolve() }
  })
  const pending = ctx.enterPreviewFullscreen()
  ctx.dispose()
  complete()
  await pending
  assert.equal(ctx.document.fullscreenElement, null)
  assert.equal(ctx.isFullscreen.value, false)
})

test('diagram zoom exits native fullscreen before opening its body-mounted viewer', async () => {
  const code = source.slice(source.indexOf('async function openMermaid()'), source.indexOf('async function loadPreview()'))
  const calls = []
  let finishExit
  const open = vm.runInNewContext(`${code}; openMermaid`, {
    mermaidSvg: { value: '<svg />' }, props: { active: true }, previewRoot: { value: { isConnected: true } },
    exitPreviewFullscreen: () => new Promise(resolve => { finishExit = resolve }),
    openMermaidFullscreen: svg => calls.push(svg),
  })
  const pending = open()
  assert.deepEqual(calls, [])
  finishExit()
  await pending
  assert.deepEqual(calls, ['<svg />'])
})

test('toolbar reserves layout space in normal and fullscreen modes and keeps exit visible on errors', async () => {
  const { descriptor } = parse(source)
  assert.match(descriptor.template.content, /v-if="isFullscreen \|\|/)
  assert.match(descriptor.template.content, /:aria-label="isFullscreen \? \$t\('preview.exitFullscreen'\)/)
  assert.match(descriptor.template.content, /<Teleport :to="toolbarTarget \|\| 'body'" :disabled="isFullscreen \|\| !toolbarTarget">/)
  assert.doesNotMatch(descriptor.template.content, /\{\{ isFullscreen \? \$t\('preview.exitFullscreen'\)/)
  const result = await compileStyleAsync({
    source: descriptor.styles[0].content, filename: 'document-preview.vue', id: 'preview', preprocessLang: 'less',
  })
  assert.deepEqual(result.errors, [])
  const toolbars = [...result.code.matchAll(/[^{}]*\.preview-toolbar\s*\{([^}]+)\}/g)]
  assert.ok(toolbars.length >= 2)
  for (const [, css] of toolbars) assert.doesNotMatch(css, /position:\s*(absolute|fixed)|opacity:/)
  assert.ok(toolbars.some(([, css]) => /flex-shrink:\s*0/.test(css)))
})
