import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import { reactive, nextTick } from 'vue'
import { fixture } from '../../test-utils/vueFixture.ts'

function setup(t: TestContext, file: string, props: Record<string, unknown>) {
  const calls: Array<[string, unknown?]> = []
  const assistant = {
    pageAccess: true,
    setPageSelection: (value: unknown) => calls.push(['selection', value]),
    restorePageContext: () => calls.push(['context']),
    ask: (value: string) => calls.push(['ask', value]),
    focusComposer: () => calls.push(['focus']),
    showActionSuggestions: (value: unknown) => calls.push(['suggestions', value]),
  }
  const state = reactive(props)
  const { vm } = fixture(t).component(new URL(file, import.meta.url), {
    '@/stores/assistant': { useAssistantStore: () => assistant },
    'vue-i18n': { useI18n: () => ({ t: (key: string, params?: unknown) => JSON.stringify({ key, params }) }) },
  }, state)
  return { vm, calls, assistant, state }
}

test('opening the assistant offers actions without sending and retains the clicked scope', async t => {
  let message = 'old'
  const f = setup(t, './AssistantActionsButton.vue', {
    label: 'Actions', selection: { text: 'Selected answer' },
    actions: [{ key: 'doc', label: 'Document', query: 'Use message-1' }],
    onBeforeOpen: () => { nextTick(() => { message = 'message-1' }) },
  })
  f.assistant.showActionSuggestions = suggestions => f.calls.push(['suggestions', { suggestions, message }])
  const pending = f.vm.openAssistant()
  ;(f.state.selection as { text: string }).text = 'Another answer'
  ;(f.state.actions as { query: string }[])[0].query = 'Use message-2'
  await pending
  assert.deepEqual(f.calls.map(([name]) => name), ['selection', 'context', 'suggestions'])
  assert.deepEqual({ ...f.calls[0][1] as object }, { text: 'Selected answer' })
  assert.equal(JSON.stringify(f.calls[2][1]), JSON.stringify({
    suggestions: { label: 'Actions', actions: [{ key: 'doc', label: 'Document', query: 'Use message-1' }] },
    message: 'message-1',
  }))
})

test('assistant entries honor access and disabled states and clear stale selections on open', async t => {
  const actions = [{ key: 'compare', label: 'Compare', query: 'Compare', disabled: true }]
  const f = setup(t, './AssistantActionsButton.vue', {
    label: 'Actions', actions,
  })
  f.assistant.pageAccess = false
  await f.vm.openAssistant()
  f.assistant.pageAccess = true
  f.vm.$.props.disabled = true
  await nextTick()
  await f.vm.openAssistant()
  assert.equal(f.calls.length, 0)
  f.vm.$.props.disabled = false
  await nextTick()
  await f.vm.openAssistant()
  assert.equal(JSON.stringify(f.calls), JSON.stringify([
    ['selection', null], ['context'], ['suggestions', { label: 'Actions', actions }],
  ]))
})

for (const kind of ['agent', 'automation']) {
  test(`${kind} creation sends the entered request once after closing the dialog`, async t => {
    const updates: boolean[] = []
    const f = setup(t, './AssistantCreateDialog.vue', {
      visible: true, kind, 'onUpdate:visible': (value: boolean) => updates.push(value),
    })
    f.vm.description = '  Summarize product updates  '
    const pending = f.vm.create()
    await f.vm.create()
    await pending
    assert.deepEqual(updates, [false])
    assert.deepEqual(f.calls.map(([name]) => name), ['selection', 'context', 'ask'])
    const query = JSON.parse(f.calls[2][1] as string)
    assert.equal(query.key, `assistant.prompts.create${kind === 'agent' ? 'Agent' : 'Automation'}`)
    assert.equal(query.params.request, 'Summarize product updates')
    assert.ok(query.params.timezone)
    assert.equal(f.calls[0][1], null)
  })
}

test('empty creation requests and manual configuration never send an assistant message', async t => {
  let manual = 0
  const f = setup(t, './AssistantCreateDialog.vue', { visible: true, kind: 'agent', onManual: () => { manual++ } })
  f.vm.description = '  '
  await f.vm.create()
  f.vm.manual()
  assert.equal(manual, 1)
  assert.equal(f.calls.length, 0)
})
