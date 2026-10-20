import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test, { type TestContext } from 'node:test'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import { change, fixture, flush } from '../../test-utils/vueFixture.ts'
import * as schedule from '../../utils/automationSchedule.ts'

const require = createRequire(import.meta.url)
const useInputNumber = require('tdesign-vue-next/cjs/input-number/hooks/useInputNumber.js').default
const numberProps = require('tdesign-vue-next/cjs/input-number/props.js').default

const automation = {
  id: 'automation-1', name: 'Reminder', instruction: 'Take a break',
  trigger: { type: 'cron', expr: '0 9 * * *', timezone: 'Asia/Shanghai' },
  approval_timeout_minutes: 1440, limits: { max_duration_minutes: 30 },
  enabled: true, session_mode: 'per_run', access_level: 'confirm',
  carry_last_result: false, misfire_policy: 'skip', overlap_policy: 'skip', notify_on_success: false,
}

function setup(t: TestContext, file: string, api: Record<string, unknown>, props: Record<string, unknown>) {
  const f = fixture(t)
  const url = new URL(file, import.meta.url)
  const page = f.component(url, {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, locale: vue.ref('en-US') }) },
    'vue-router': { useRoute: () => ({ params: {}, query: {} }), useRouter: () => ({ replace() {}, push() {} }) },
    'tdesign-vue-next': { MessagePlugin: { success() {}, error() {}, warning() {} } },
    '@/api/automation': api,
    '@/api/assistant': { ASSISTANT_ACCESS_LEVELS: ['read_only', 'confirm', 'auto_write'] },
    '@/stores/ui': { useUIStore: () => ({ openSettings() {} }) },
    '@/utils/automationSchedule': schedule,
    '@/composables/useModalShell': { useModalShell: () => ({ markClean() {}, requestClose() {} }) },
  }, props)
  const { descriptor } = parse(readFileSync(url, 'utf8'))
  const script = compileScript(descriptor, { id: 'automation-interactions' })
  const compiled = compileTemplate({
    source: descriptor.template!.content, filename: url.pathname, id: 'automation-interactions',
    compilerOptions: { bindingMetadata: script.bindings },
  })
  assert.deepEqual(compiled.errors, [])
  const { render } = f.evaluate(compiled.code, {
    vue: { ...vue, resolveComponent: (name: string) => name === 't-popup' ? { name } : name, resolveDynamicComponent: (name: string) => name,
      withCtx: (fn: unknown) => fn, withDirectives: (node: unknown) => node },
  })
  return { ...page, mount: f.mount, emit: f.emit, render: () => render(page.vm, [], props, page.vm, {}, {}) }
}

function nodes(node: any): any[] {
  if (!node || typeof node !== 'object') return []
  if (Array.isArray(node)) return node.flatMap(nodes)
  const children = node.children
  return [node, ...(Array.isArray(children) ? nodes(children)
    : children && typeof children === 'object'
      ? Object.values(children).flatMap(slot => typeof slot === 'function' ? nodes(slot()) : []) : [])]
}

test('assistant mutations refresh the automation list and open detail without replacing an editor draft', async t => {
  let items = [{ ...automation }]
  let calls = 0
  const { vm, emit, close } = setup(t, './AutomationList.vue', {
    listAutomations: async () => { calls++; return { data: items } },
  }, {})
  await flush()
  vm.openDetail(vm.automations[0])
  vm.query = 'Reminder'
  vm.editing = { ...automation, name: 'Unsaved draft' }
  vm.editorVisible = true
  items = [{ ...automation, name: 'Updated reminder', enabled: false }, { ...automation, id: 'new' }]
  emit(change('agent.update'))
  t.mock.timers.tick(300)
  assert.equal(calls, 1)
  emit(change('automation.create'))
  emit(change('automation.update'))
  emit(change('automation.pause'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(calls, 2, 'one request per burst')
  assert.equal(vm.automations.length, 2)
  assert.equal(vm.detail.name, 'Updated reminder')
  assert.equal(vm.detail.enabled, false)
  assert.equal(vm.detailVisible, true)
  assert.equal(vm.query, 'Reminder')
  assert.equal(vm.editing.name, 'Unsaved draft')
  assert.equal(vm.editorVisible, true)
  items = []
  emit(change('automation.delete'))
  t.mock.timers.tick(300)
  await flush()
  assert.equal(vm.detailVisible, false)
  assert.equal(vm.detail, null)
  close()
  emit(change('automation.create'))
  t.mock.timers.tick(300)
  assert.equal(calls, 3, 'unmounted pages unsubscribe')
})

test('a stale automation list response cannot overwrite a newer assistant refresh', async t => {
  const requests: Array<(result: unknown) => void> = []
  const { vm, emit } = setup(t, './AutomationList.vue', {
    listAutomations: () => new Promise(resolve => requests.push(resolve)),
  }, {})
  emit(change('automation.update'))
  t.mock.timers.tick(300)
  assert.equal(requests.length, 2)
  requests[1]({ data: [{ ...automation, name: 'Fresh' }] })
  await flush()
  requests[0]({ data: [{ ...automation, name: 'Stale' }] })
  await flush()
  assert.equal(vm.automations[0].name, 'Fresh')
})

test('assistant run-now refreshes idle history after any in-flight load and preserves earlier pages', async t => {
  let first!: (value: unknown) => void
  let calls = 0
  const oldRun = { id: 'old', status: 'succeeded', scheduled_at: '2026-10-20T10:00:00Z' }
  const newRun = { id: 'new', status: 'queued', scheduled_at: '2026-10-20T11:00:00Z' }
  const { vm, emit } = setup(t, './AutomationDetail.vue', {
    listAutomationRuns: () => ++calls === 1 ? new Promise(resolve => { first = resolve })
      : Promise.resolve({ data: [newRun, oldRun], total: 3 }),
    listAutomationGrants: async () => ({ data: [] }),
    listAutomationGrantCandidates: async () => ({ data: [] }),
  }, { visible: true, automation })
  emit(change('automation.run_now'))
  t.mock.timers.tick(300)
  assert.equal(calls, 1, 'wait for the initial page before merging newer runs')
  first({ data: [oldRun, { ...oldRun, id: 'earlier' }], total: 2 })
  await flush()
  assert.equal(calls, 2)
  assert.deepEqual(Array.from(vm.runs, (run: any) => run.id), ['new', 'old', 'earlier'])
  assert.equal(vm.runsTotal, 3)
  vm.instructionExpanded = true
  vm.$.props.automation = { ...automation, name: 'Updated name' }
  await vue.nextTick()
  assert.equal(calls, 2, 'refreshing the same automation does not reset loaded history')
  assert.equal(vm.instructionExpanded, true)
  vm.$.props.visible = false
  await vue.nextTick()
  emit(change('automation.run_now'))
  t.mock.timers.tick(300)
  assert.equal(calls, 2, 'closed detail does not fetch history')
})

test('task content fades only while more text remains below the scroll position', async t => {
  const { vm, render } = setup(t, './AutomationDetail.vue', {},
    { visible: false, automation })
  const scroller = vue.markRaw({ scrollHeight: 320, clientHeight: 160, scrollTop: 0 })
  vm.instructionScroller = scroller
  await vue.nextTick()
  const scrollNode = () => nodes(render()).find(node =>
    String(node.props?.class).includes('automation-detail__instruction-scroll'))
  const toggle = () => nodes(render()).find(node =>
    node.props?.class === 'automation-detail__instruction-toggle')
  assert.equal(vm.instructionHasMore, true)
  assert.match(scrollNode().props.class, /instruction-scroll--more/)
  assert.doesNotMatch(scrollNode().props.class, /instruction-scroll--expanded/)
  assert.equal(toggle().props['aria-expanded'], false)
  toggle().props.onClick()
  await vue.nextTick()
  assert.equal(scrollNode().props.style.maxHeight, 'none')
  assert.match(scrollNode().props.class, /instruction-scroll--expanded/)
  assert.equal(vm.instructionHasMore, false, 'expanded text is not faded')
  assert.equal(toggle().props['aria-expanded'], true)
  toggle().props.onClick()
  await vue.nextTick()
  assert.equal(scrollNode().props.style.maxHeight, '160px')
  assert.doesNotMatch(scrollNode().props.class, /instruction-scroll--expanded/)
  assert.equal(vm.instructionHasMore, true)

  scroller.scrollTop = 160
  scrollNode().props.onScroll()
  assert.equal(vm.instructionHasMore, false, 'the last line is fully visible at the bottom')
  assert.doesNotMatch(scrollNode().props.class, /instruction-scroll--more/)

  scroller.scrollTop = 50
  scrollNode().props.onScroll()
  assert.equal(vm.instructionHasMore, true)
  scroller.scrollHeight = 100
  scroller.scrollTop = 0
  vm.updateInstructionOverflow()
  assert.equal(vm.instructionHasMore, false, 'short content has no fade')
  assert.equal(toggle(), undefined, 'short content has no expand control')
})

test('cards refresh live run states and return to the last result after completion', async t => {
  let state = { ...automation, active_run_status: '', last_run_status: 'succeeded' }
  let calls = 0
  const { vm, render, close } = setup(t, './AutomationList.vue', {
    listAutomations: async () => { calls++; return { data: [{ ...state }] } },
  }, {})
  await flush()
  const status = () => nodes(render()).find(node => node.props && 'status' in node.props)?.props.status
  assert.equal(status(), 'succeeded')
  vm.openDetail(vm.automations[0])
  vm.editing = { ...automation, name: 'Unsaved draft' }
  vm.editorVisible = true
  for (const active of ['queued', 'running', 'awaiting_approval', '']) {
    state = { ...state, active_run_status: active }
    t.mock.timers.tick(5000)
    await flush()
    assert.equal(status(), active || 'succeeded')
    assert.equal(vm.detail.active_run_status, active)
    assert.equal(vm.editing.name, 'Unsaved draft')
    assert.equal(vm.loading, false, 'background refresh keeps cards visible')
  }
  assert.equal(calls, 5)
  close()
  t.mock.timers.tick(15000)
  await flush()
  assert.equal(calls, 5, 'leaving the list stops polling')
})

test('list polling waits for a slow response and does not restart after unmount', async t => {
  let resolve!: (value: unknown) => void
  let calls = 0
  const { close } = setup(t, './AutomationList.vue', {
    listAutomations: () => {
      calls++
      return new Promise(done => { resolve = done })
    },
  }, {})
  t.mock.timers.tick(15000)
  assert.equal(calls, 1, 'no overlapping polling requests')
  resolve({ data: [automation] })
  await flush()
  t.mock.timers.tick(5000)
  assert.equal(calls, 2)
  close()
  resolve({ data: [automation] })
  await flush()
  t.mock.timers.tick(15000)
  assert.equal(calls, 2)
})

test('a stale poll cannot restore a deleted card and polling recovers from errors', async t => {
  let resolve!: (value: unknown) => void
  let calls = 0
  const { vm } = setup(t, './AutomationList.vue', {
    listAutomations: async () => {
      calls++
      if (calls === 1) return { data: [automation] }
      if (calls === 2) return new Promise(done => { resolve = done })
      if (calls === 3) throw new Error('Temporarily unavailable')
      return { data: [] }
    },
    deleteAutomation: async () => {},
  }, {})
  await flush()
  t.mock.timers.tick(5000)
  await vm.remove(vm.automations[0])
  resolve({ data: [automation] })
  await flush()
  assert.equal(vm.automations.length, 0)
  t.mock.timers.tick(5000)
  await flush()
  assert.equal(calls, 3)
  t.mock.timers.tick(5000)
  await flush()
  assert.equal(calls, 4)
  assert.equal(vm.automations.length, 0)
})

test('approval hours update in both directions and save as minutes, without resetting on an empty edit', async t => {
  let saved: any
  const props = { visible: false, automation }
  const { vm, render, mount } = setup(t, './AutomationEditor.vue', {
    updateAutomation: async (_id: string, input: unknown) => { saved = input; return { data: automation } },
    validateAutomationTrigger: async () => ({ data: { next_runs: [] } }),
  }, props)
  vm.$.props.visible = true
  await vue.nextTick()
  assert.equal(vm.approvalHours, 24)
  const input = nodes(render()).find(node => node.type === 't-input-number' && node.props.max === 168)
  assert.ok(input)
  assert.equal(input.props.theme, 'row', 'both controls stay visible with full-sized click targets')
  assert.equal(input.props.suffix, undefined, 'the unit cannot crowd the numeric input or controls')
  // Use TDesign's real step handlers against the editor's compiled v-model binding.
  let control: any
  const NumberControl = vue.defineComponent({
    props: numberProps,
    setup(props) { control = useInputNumber(props); return () => null },
  })
  mount({ setup: () => () => vue.h(NumberControl,
    nodes(render()).find(node => node.type === 't-input-number' && node.props.max === 168).props) })
  control.handleAdd({})
  await vue.nextTick()
  assert.equal(vm.form.approval_timeout_minutes, 1500)
  assert.equal(control.userInput.value, '25')
  control.handleReduce({})
  await vue.nextTick()
  assert.equal(vm.form.approval_timeout_minutes, 1440)
  assert.equal(control.userInput.value, '24')
  const update = input.props['onUpdate:modelValue']
  update(undefined)
  assert.equal(vm.approvalHours, 24)
  update(0)
  assert.equal(vm.approvalHours, 1)
  update(169)
  assert.equal(vm.approvalHours, 168)
  update(4)
  await vm.save()
  assert.equal(saved.approval_timeout_minutes, 240)
})

test('the editor preserves, changes, and clears an automation model override', async t => {
  const saved: any[] = []
  const { vm } = setup(t, './AutomationEditor.vue', {
    updateAutomation: async (_id: string, input: unknown) => { saved.push(input); return { data: automation } },
    validateAutomationTrigger: async () => ({ data: { next_runs: [] } }),
  }, { visible: false, automation: { ...automation, model_id: 'model-one' } })
  vm.$.props.visible = true
  await vue.nextTick()
  assert.equal(vm.form.model_id, 'model-one')
  vm.form.model_id = 'model-two'
  await vm.save()
  assert.equal(saved[0].model_id, 'model-two')
  vm.form.model_id = ''
  await vm.save()
  assert.equal(saved[1].model_id, '', 'clearing restores the assistant default instead of omitting the patch')
})

test('list run and delete actions call APIs only after their inline confirmation', async t => {
  let runCalls = 0
  let deleteCalls = 0
  const { vm, render } = setup(t, './AutomationList.vue', {
    listAutomations: async () => ({ data: [{ ...automation, active_run_status: runCalls ? 'queued' : '' }] }),
    runAutomationNow: async () => { runCalls++; return { data: { id: 'run-1' } } },
    deleteAutomation: async () => { deleteCalls++ },
  }, {})
  await flush()
  const popups = nodes(render()).filter(node => node.type === 't-popconfirm')
  assert.equal(popups.length, 2)
  for (const popup of popups) {
    const trigger = nodes(popup).find(node => node.type === 'button')
    assert.ok(trigger)
    trigger.props.onClick?.()
    popup.props.onCancel?.()
  }
  assert.equal(runCalls, 0)
  assert.equal(deleteCalls, 0)
  await popups[0].props.onConfirm()
  await flush()
  assert.equal(runCalls, 1)
  assert.equal(vm.detailVisible, true)
  assert.equal(vm.automations[0].active_run_status, 'queued', 'manual runs refresh without waiting for the next poll')
  await popups[1].props.onConfirm()
  assert.equal(deleteCalls, 1)
  assert.equal(vm.automations.length, 0)
})

test('run history groups by the automation timezone and drawer run requires confirmation', async t => {
  let runCalls = 0
  const props = { visible: false, automation }
  const { vm, render } = setup(t, './AutomationDetail.vue', {
    runAutomationNow: async () => { runCalls++; return { data: { id: 'new-run' } } },
  }, props)
  vm.runs = [
    { id: 'a', scheduled_at: '2026-10-20T16:30:00Z' },
    { id: 'b', scheduled_at: '2026-10-20T16:00:00Z' },
    { id: 'c', scheduled_at: '2026-10-20T15:59:00Z' },
  ].map(run => ({ ...run, status: 'succeeded', session_id: 'chat', trigger_kind: 'schedule' }))
  assert.equal(vm.runGroups.length, 2)
  assert.equal(vm.runGroups[0].date, 'October 21, 2026')
  assert.equal(vm.runGroups[0].runs.length, 2)
  assert.equal(vm.runTime(vm.runs[0].scheduled_at), '00:30')
  const popup = nodes(render()).find(node => node.type === 't-popconfirm')
  assert.ok(popup)
  const trigger = nodes(popup).find(node => node.type === 'button')
  assert.equal(trigger.props['aria-label'], 'automation.actions.runNow')
  trigger.props.onClick?.()
  assert.equal(runCalls, 0)
  await popup.props.onConfirm()
  assert.equal(runCalls, 1)
})

test('grant popup preserves history and dismisses when starting a run or reopening the drawer', async t => {
  const { vm, render } = setup(t, './AutomationDetail.vue', {
    listAutomationGrants: async () => ({ data: [] }),
    listAutomationGrantCandidates: async () => ({ data: [{ prompt_id: 'approval-1', label: 'Update document', risk: 'write' }] }),
    listAutomationRuns: async () => ({ data: [], total: 0 }),
    runAutomationNow: async () => ({ data: { id: 'new-run' } }),
  }, { visible: false, automation })
  assert.equal(vm.grantsVisible, false)
  const popup = nodes(render()).find(node => node.type?.name === 't-popup')
  popup.props['onUpdate:visible'](true)
  assert.equal(vm.grantsVisible, true)
  await vm.loadGrants()
  assert.equal(vm.grantsVisible, true, 'loading grants must not dismiss their popup')
  const trigger = nodes(render()).find(node => node.type === 'button' && node.props?.class?.includes('automation-detail__grants-trigger'))
  assert.ok(trigger.props.class.includes('is-attention'), 'candidates remain visible from the trigger')
  assert.equal(trigger.props['aria-expanded'], true)
  assert.ok(nodes(render()).some(node => node.props?.class === 'automation-detail__history'))
  await vm.runNow()
  assert.equal(vm.grantsVisible, false, 'a manual run dismisses the popup to reveal progress')
  assert.equal(vm.runs[0].id, 'new-run')
  popup.props['onUpdate:visible'](true)
  vm.$.props.visible = true
  await flush()
  assert.equal(vm.grantsVisible, false, 'reopening the drawer does not restore a stale popup')
})

for (const file of ['./AutomationList.vue', './AutomationDetail.vue']) {
  test(`${file}: elapsed one-shot schedules have no resume entry, but manual run remains available`, async t => {
    let resumeCalls = 0
    const item = { ...automation, enabled: false, trigger: { type: 'once', at: new Date(Date.now() - 60_000).toISOString() } }
    const { vm, render } = setup(t, file, {
      listAutomations: async () => ({ data: [item] }),
      resumeAutomation: async () => { resumeCalls++; return { data: { ...item, enabled: true } } },
    }, { visible: false, automation: item })
    await flush()
    const resumeButton = () => nodes(render()).find(node => node.type === 'button'
      && (node.props?.role === 'switch' || node.props?.['aria-label'] === 'automation.actions.resume'))
    assert.equal(resumeButton(), undefined)
    assert.ok(nodes(render()).some(node => node.type === 't-popconfirm'
      && node.props?.['confirm-btn']?.content === 'automation.actions.runNow'))
    await vm.toggle(item)
    assert.equal(resumeCalls, 0, 'stale handlers cannot resume an elapsed schedule')

    item.trigger.at = new Date(Date.now() + 60_000).toISOString()
    assert.ok(resumeButton(), 'a paused future one-shot can still be resumed')
    if (file.includes('Detail')) {
      assert.ok(nodes(render()).some(node => node.props?.name === 'resume'), 'resume uses its own icon')
    }
    await vm.toggle(item)
    assert.equal(resumeCalls, 1)
  })
}
