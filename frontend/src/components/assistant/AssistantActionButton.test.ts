import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import ts from 'typescript'

const { descriptor } = parse(readFileSync(new URL('./AssistantActionButton.vue', import.meta.url), 'utf8'))
const script = compileScript(descriptor, { id: 'assistant-action-test', inlineTemplate: true }).content

function fixture(selection?: { text: string; field?: string }) {
  const calls: Array<[string, unknown?]> = []
  const assistant = {
    pageAccess: true,
    displayName: 'Assistant',
    setPageSelection: (value: unknown) => calls.push(['selection', value]),
    restorePageContext: () => calls.push(['context']),
    ask: (query: string) => calls.push(['ask', query]),
  }
  const dependencies: Record<string, unknown> = {
    vue,
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key }) },
    '@/stores/assistant': { useAssistantStore: () => assistant },
    './AssistantButton.vue': { render: () => null },
  }
  const exports: any = {}
  runInNewContext(ts.transpileModule(script, {
    compilerOptions: { module: ts.ModuleKind.CommonJS },
  }).outputText, { exports, require: (name: string) => dependencies[name] })
  const props = { label: 'Improve prompt', query: 'Improve the attached prompt', selection, disabled: false }
  const render = exports.default.setup(props, { expose() {} })
  return { assistant, calls, props, render, click: () => render({}, []).props.onClick({ stopPropagation() {} }) }
}

test('contextual actions attach a snapshot before asking in the current conversation', () => {
  const selection = { text: 'Current prompt', field: 'System prompt' }
  const f = fixture(selection)
  f.click()
  assert.deepEqual(f.calls.map(([name]) => name), ['selection', 'context', 'ask'])
  assert.deepEqual({ ...f.calls[0][1] as object }, selection)
  assert.equal(f.calls[2][1], f.props.query)
  selection.text = 'Later edit'
  assert.equal((f.calls[0][1] as { text: string }).text, 'Current prompt')
})

test('an empty prompt clears any previous selection', () => {
  for (const selection of [undefined, { text: '  \n ' }]) {
    const f = fixture(selection)
    f.click()
    assert.equal(f.calls[0][1], null)
  }
})

test('actions respect page access and expose their disabled state', () => {
  const f = fixture()
  f.props.disabled = true
  assert.equal(f.render({}, []).props.disabled, true)
  f.assistant.pageAccess = false
  assert.equal(f.render({}, []).type, vue.Comment)
  assert.equal(f.calls.length, 0)
})
