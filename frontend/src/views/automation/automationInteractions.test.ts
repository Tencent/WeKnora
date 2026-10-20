import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test, { type TestContext } from 'node:test'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'
import * as schedule from '../../utils/automationSchedule.ts'

const require = createRequire(import.meta.url)
const useInputNumber = require('tdesign-vue-next/cjs/input-number/hooks/useInputNumber.js').default
const numberProps = require('tdesign-vue-next/cjs/input-number/props.js').default

const automation = {
  id: 'automation-1', name: 'Reminder', instruction: 'Take a break', agent_id: 'assistant',
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
    '@/api/agent': { BUILTIN_WEKNORA_ASSISTANT_ID: 'assistant' },
    '@/api/assistant': { ASSISTANT_ACCESS_LEVELS: ['read_only', 'confirm', 'auto_write'] },
    '@/stores/auth': { useAuthStore: () => ({ currentTenantId: 'tenant' }) },
    './agents': { loadAutomationAgents: async () => [] },
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
  return { ...page, mount: f.mount, render: () => render(page.vm, [], props, page.vm, {}, {}) }
}

function nodes(node: any): any[] {
  if (!node || typeof node !== 'object') return []
  if (Array.isArray(node)) return node.flatMap(nodes)
  const children = node.children
  return [node, ...(Array.isArray(children) ? nodes(children)
    : children && typeof children === 'object'
      ? Object.values(children).flatMap(slot => typeof slot === 'function' ? nodes(slot()) : []) : [])]
}

test('approval hours update in both directions and save as minutes, without resetting on an empty edit', async t => {
  let saved: any
  const props = { visible: false, automation, agents: [], defaultAgentId: 'assistant' }
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

test('list run and delete actions call APIs only after their inline confirmation', async t => {
  let runCalls = 0
  let deleteCalls = 0
  const { vm, render } = setup(t, './AutomationList.vue', {
    listAutomations: async () => ({ data: [automation] }),
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
  assert.equal(runCalls, 1)
  assert.equal(vm.detailVisible, true)
  await popups[1].props.onConfirm()
  assert.equal(deleteCalls, 1)
  assert.equal(vm.automations.length, 0)
})

test('run history groups by the automation timezone and drawer run requires confirmation', async t => {
  let runCalls = 0
  const props = { visible: false, automation, agentName: 'Assistant' }
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
  }, { visible: false, automation, agentName: 'Assistant' })
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
    }, { visible: false, automation: item, agentName: 'Assistant' })
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
