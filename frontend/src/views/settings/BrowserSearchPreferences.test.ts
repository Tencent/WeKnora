import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick } from 'vue'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./BrowserSearchPreferences.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'browser-search-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

async function fixture(stored?: string, saveResult?: () => Promise<any>) {
  const calls: any[] = []
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/api/auth') return {
        getCurrentUser: async () => ({ success: true, data: {
          user: { preferences: { browser_search_instructions: stored } },
          preference_defaults: { browser_search_instructions: 'Default from server' },
        } }),
        updateMyPreferences: async (patch: any) => {
          calls.push(patch)
          return saveResult ? saveResult() : { success: true, data: patch }
        },
      }
      throw new Error(name)
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
  await new Promise(resolve => setImmediate(resolve))
  await nextTick()
  return { vm, calls, close: () => app.unmount() }
}

test('loads server default, saves only browser instructions, and restores inherited default', async () => {
  const f = await fixture()
  try {
    assert.equal(f.vm.draft, 'Default from server')
    assert.equal(f.vm.dirty, false)
    f.vm.draft = 'Use my preferred engine'
    await f.vm.save()
    assert.equal(f.calls[0].browser_search_instructions, 'Use my preferred engine')
    assert.deepEqual(Object.keys(f.calls[0]), ['browser_search_instructions'])
    assert.equal(f.vm.saved, true)
    f.vm.restoreDefault()
    assert.equal(f.vm.dirty, true)
    await f.vm.save()
    assert.equal(f.calls[1].browser_search_instructions, '')
    assert.equal(f.vm.draft, 'Default from server')
    assert.equal(f.vm.dirty, false)
  } finally { f.close() }
})

test('saved custom text is displayed and save failures preserve the draft', async () => {
  const f = await fixture('Saved custom instructions', async () => ({ success: false, message: 'save failed' }))
  try {
    assert.equal(f.vm.draft, 'Saved custom instructions')
    f.vm.draft = 'Unsaved edit'
    await f.vm.save()
    assert.equal(f.vm.draft, 'Unsaved edit')
    assert.equal(f.vm.error, 'save failed')
    assert.equal(f.vm.dirty, true)
    assert.equal(f.vm.saving, false)
  } finally { f.close() }
})

test('duplicate saves are blocked and late responses do not update an unmounted editor', async () => {
  let resolve!: (value: any) => void
  const pending = new Promise(resolvePromise => { resolve = resolvePromise })
  const f = await fixture('Saved', () => pending)
  f.vm.draft = 'New'
  const saving = f.vm.save()
  await f.vm.save()
  assert.equal(f.calls.length, 1)
  f.close()
  resolve({ success: true, data: { browser_search_instructions: 'New' } })
  await saving
  assert.equal(f.vm.persisted, 'Saved')
})
