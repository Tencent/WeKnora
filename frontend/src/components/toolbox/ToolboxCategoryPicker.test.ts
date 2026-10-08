import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'

const require = createRequire(import.meta.url)
const { descriptor } = parse(readFileSync(new URL('./ToolboxCategoryPicker.vue', import.meta.url), 'utf8'))
const script = compileScript(descriptor, { id: 'category-picker-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

function fixture(create: (name: string) => Promise<{ id: string; name: string }>) {
  const props = reactive({ modelValue: ['existing'], categories: [] as Array<{ id: string; name: string }>, disabled: false })
  const errors: string[] = []
  const busy: boolean[] = []
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { error: (text: string) => errors.push(text) } }
      if (name === '@/api/toolbox-category') return { createToolboxCategory: create }
      assert.fail(`Unexpected import: ${name}`)
    },
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance,
    'onUpdate:modelValue': (ids: string[]) => { props.modelValue = ids },
    onCreated: (category: { id: string; name: string }) => props.categories.push(category),
    onBusy: (value: boolean) => busy.push(value),
  }) })
  app.mount({})
  return { vm: instance.value, props, errors, busy, close: () => app.unmount() }
}

test('inline tag creation selects the new tag without losing existing selections', async () => {
  const f = fixture(async name => ({ id: 'contracts', name }))
  try {
    f.vm.newName = '  Contracts  '
    await f.vm.createCategory()
    assert.deepEqual(Array.from(f.props.modelValue), ['existing', 'contracts'])
    assert.deepEqual(f.props.categories, [{ id: 'contracts', name: 'Contracts' }])
    assert.equal(f.vm.newName, '')
    assert.deepEqual(f.busy, [true, false])
  } finally { f.close() }
})

test('failed tag creation preserves the draft and selection for retry', async () => {
  let fail = true
  const f = fixture(async name => { if (fail) throw new Error('Name already exists'); return { id: 'new', name } })
  try {
    f.vm.newName = 'Contracts'
    await f.vm.createCategory()
    assert.deepEqual(f.errors, ['Name already exists'])
    assert.deepEqual(f.props.modelValue, ['existing'])
    assert.equal(f.vm.newName, 'Contracts')
    assert.equal(f.vm.creating, false)
    fail = false
    f.vm.newName = 'Legal'
    await f.vm.createCategory()
    assert.deepEqual(Array.from(f.props.modelValue), ['existing', 'new'])
  } finally { f.close() }
})

test('disabled, empty and repeated submissions do not create duplicate tags', async () => {
  let calls = 0
  let resolve!: (category: { id: string; name: string }) => void
  const f = fixture(() => { calls++; return new Promise(done => { resolve = done }) })
  try {
    await f.vm.createCategory()
    f.vm.newName = 'Contracts'
    f.props.disabled = true
    await nextTick()
    await f.vm.createCategory()
    assert.equal(calls, 0)
    f.props.disabled = false
    await nextTick()
    const pending = f.vm.createCategory()
    await f.vm.createCategory()
    assert.equal(calls, 1)
    resolve({ id: 'new', name: 'Contracts' })
    await pending
  } finally { f.close() }
})
