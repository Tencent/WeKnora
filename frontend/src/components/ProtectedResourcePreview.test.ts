import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick } from 'vue'
import { RESOURCE_PREVIEW_EVENT } from '../utils/protectedResource.ts'

const require = createRequire(import.meta.url)
const { descriptor } = parse(readFileSync(new URL('./ProtectedResourcePreview.vue', import.meta.url), 'utf8'))
const script = compileScript(descriptor, { id: 'protected-resource-preview-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

test('preview owns its download URL across source release, replacement, close and unmount', async t => {
  const events = new EventTarget()
  const exports: any = {}
  runInNewContext(compiled, {
    exports, Blob, URL, window: events,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === './document-preview.vue') return { default: {} }
      if (name === '@/utils/protectedResource') return require('../utils/protectedResource.ts')
      throw new Error(`Unexpected component dependency: ${name}`)
    },
  })
  exports.default.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp(exports.default)
  const vm: any = app.mount({})
  const blob = new Blob(['preview bytes'], { type: 'text/plain' })
  const sourceURL = URL.createObjectURL(blob)
  t.after(() => { app.unmount(); URL.revokeObjectURL(sourceURL) })
  const open = () => {
    const event = new CustomEvent(RESOURCE_PREVIEW_EVENT, {
      cancelable: true, detail: { blob, blobURL: sourceURL, fileName: 'report.txt' },
    })
    events.dispatchEvent(event)
    assert.equal(event.defaultPrevented, true)
    return vm.file.blobURL as string
  }
  const first = open()
  assert.notEqual(first, sourceURL)
  URL.revokeObjectURL(sourceURL)
  assert.equal(await (await fetch(first)).text(), 'preview bytes')
  const second = open()
  await assert.rejects(fetch(first))
  assert.equal(await (await fetch(second)).text(), 'preview bytes')
  vm.visible = false
  await nextTick()
  assert.equal(vm.file, null, 'closed drawer releases the Blob reference as well')
  await assert.rejects(fetch(second))
  const third = open()
  app.unmount()
  await assert.rejects(fetch(third))
  const event = new CustomEvent(RESOURCE_PREVIEW_EVENT, { cancelable: true, detail: { blob, blobURL: sourceURL } })
  events.dispatchEvent(event)
  assert.equal(event.defaultPrevented, false, 'unmounted preview removes its event listener')
})
