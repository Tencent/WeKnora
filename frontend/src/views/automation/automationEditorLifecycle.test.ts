import assert from 'node:assert/strict'
import test, { type TestContext } from 'node:test'
import * as vue from 'vue'
import { fixture, flush } from '../../test-utils/vueFixture.ts'
import * as schedule from '../../utils/automationSchedule.ts'

const automation = {
  id: 'a', name: 'Reminder', instruction: 'Take a break',
  trigger: { type: 'cron', expr: '0 9 * * *', timezone: 'UTC' },
  approval_timeout_minutes: 1440, limits: { max_duration_minutes: 30 },
  enabled: true, session_mode: 'per_run', access_level: 'confirm',
  carry_last_result: false, misfire_policy: 'skip', overlap_policy: 'skip', notify_on_success: false,
}
function deferred<T = any>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function setup(t: TestContext, api: Record<string, unknown> = {}) {
  const f = fixture(t)
  const events: unknown[][] = []
  const messages: string[] = []
  let clean = 0
  const page = f.component(new URL('./AutomationEditor.vue', import.meta.url), {
    'vue-i18n': { useI18n: () => ({ t: (key: string) => key, locale: vue.ref('en-US') }) },
    'tdesign-vue-next': { MessagePlugin: {
      success: (s: string) => messages.push(s), error: (s: string) => messages.push(s), warning: (s: string) => messages.push(s),
    } },
    '@/api/automation': { validateAutomationTrigger: async () => ({ data: { next_runs: [] } }), ...api },
    '@/api/assistant': { ASSISTANT_ACCESS_LEVELS: ['read_only', 'confirm', 'auto_write'] },
    '@/stores/ui': { useUIStore: () => ({ openSettings() {} }) },
    '@/utils/automationSchedule': schedule,
    '@/composables/useModalShell': { useModalShell: () => ({ markClean() { clean++ }, requestClose() {} }) },
  }, { visible: false, automation: null,
    onSaved: (...args: unknown[]) => events.push(['saved', ...args]),
    'onUpdate:visible': (...args: unknown[]) => events.push(['visible', ...args]),
  })
  async function open(value: any = automation) {
    page.vm.$.props.automation = value
    page.vm.$.props.visible = true
    await vue.nextTick()
  }
  async function hide() { page.vm.$.props.visible = false; await vue.nextTick() }
  return { ...page, open, hide, events, messages, clean: () => clean }
}

test('repeated save dispatches only one create while the first is pending', async t => {
  const request = deferred()
  let calls = 0
  const p = setup(t, { createAutomation: () => { calls++; return request.promise } })
  await p.open(null)
  p.vm.form.name = 'New reminder'
  p.vm.form.instruction = 'Take a break'
  const first = p.vm.save()
  const second = p.vm.save()
  assert.equal(calls, 1)
  request.resolve({ data: automation })
  await Promise.all([first, second])
  assert.equal(p.events.filter(e => e[0] === 'saved').length, 1)
})

for (const outcome of ['success', 'failure'] as const) {
  test(`old save ${outcome} cannot close, clean or notify a reopened draft`, async t => {
    const old = deferred()
    const current = deferred()
    let calls = 0
    const p = setup(t, { updateAutomation: () => (++calls === 1 ? old.promise : current.promise) })
    await p.open()
    const first = p.vm.save()
    await p.hide()
    await p.open({ ...automation, id: 'b', name: 'Second reminder' })
    p.vm.form.name = 'Unsaved second draft'
    assert.equal(p.vm.saving, false)
    const second = p.vm.save()
    const clean = p.clean()
    if (outcome === 'success') old.resolve({ data: automation })
    else old.reject(new Error('Old failure'))
    await first
    assert.deepEqual(p.events, [])
    assert.deepEqual(p.messages, [])
    assert.equal(p.clean(), clean)
    assert.equal(p.vm.saving, true, 'old finalizer must not unlock the new save')
    assert.equal(p.vm.form.name, 'Unsaved second draft')
    current.resolve({ data: { ...automation, id: 'b' } })
    await second
    assert.equal(p.events.filter(e => e[0] === 'saved').length, 1)
  })
}

test('an invalid schedule invalidates an already running preview', async t => {
  const request = deferred()
  const p = setup(t, { validateAutomationTrigger: () => request.promise })
  await p.open()
  t.mock.timers.tick(400)
  p.vm.schedule.kind = 'weekly'
  p.vm.schedule.days = []
  await vue.nextTick()
  assert.equal(p.vm.trigger, null)
  assert.equal(p.vm.previewLoading, false)
  request.resolve({ data: { next_runs: ['2026-11-03T09:00:00Z'] } })
  await flush()
  assert.equal(p.vm.previewRuns.length, 0)
})

test('closing cancels a debounced preview before it sends a request', async t => {
  let calls = 0
  const p = setup(t, { validateAutomationTrigger: async () => { calls++; return { data: {} } } })
  await p.open()
  await p.hide()
  t.mock.timers.tick(400)
  await flush()
  assert.equal(calls, 0)
  assert.equal(p.vm.previewLoading, false)
})

test('unmount cancels a debounced preview before it sends a request', async t => {
  let calls = 0
  const p = setup(t, { validateAutomationTrigger: async () => { calls++; return { data: {} } } })
  await p.open()
  p.close()
  t.mock.timers.tick(400)
  await flush()
  assert.equal(calls, 0)
})

test('unmount ignores an in-flight save result', async t => {
  const request = deferred()
  const p = setup(t, { updateAutomation: () => request.promise })
  await p.open()
  const pending = p.vm.save()
  const clean = p.clean()
  p.close()
  request.resolve({ data: automation })
  await pending
  assert.deepEqual(p.messages, [])
  assert.equal(p.clean(), clean)
})

for (const outcome of ['success', 'failure'] as const) {
  test(`closing invalidates an in-flight preview ${outcome}`, async t => {
    const request = deferred()
    const p = setup(t, { validateAutomationTrigger: () => request.promise })
    await p.open()
    t.mock.timers.tick(400)
    await p.hide()
    if (outcome === 'success') request.resolve({ data: { next_runs: ['2026-11-03T09:00:00Z'] } })
    else request.reject(new Error('Outdated schedule error'))
    await flush()
    assert.equal(p.vm.previewRuns.length, 0)
    assert.equal(p.vm.previewError, '')
    assert.equal(p.vm.previewLoading, false)
  })
}

test('A to B to A rejects the first A save even though the task ID matches again', async t => {
  const request = deferred()
  const p = setup(t, { updateAutomation: () => request.promise })
  await p.open()
  const pending = p.vm.save()
  await p.open({ ...automation, id: 'b' })
  await p.open(automation)
  p.vm.form.name = 'Latest A draft'
  const clean = p.clean()
  request.resolve({ data: automation })
  await pending
  assert.deepEqual(p.events, [])
  assert.deepEqual(p.messages, [])
  assert.equal(p.clean(), clean)
  assert.equal(p.vm.form.name, 'Latest A draft')
})

test('a current save error remains visible and permits a subsequent retry', async t => {
  let calls = 0
  const p = setup(t, { updateAutomation: async () => {
    if (++calls === 1) throw new Error('Temporary failure')
    return { data: automation }
  } })
  await p.open()
  await p.vm.save()
  assert.deepEqual(p.messages, ['Temporary failure'])
  assert.equal(p.vm.saving, false)
  assert.deepEqual(p.events, [])
  await p.vm.save()
  assert.equal(calls, 2)
  assert.equal(p.events.filter(e => e[0] === 'saved').length, 1)
})

test('a hidden editor cannot submit a save', async t => {
  let calls = 0
  const p = setup(t, { updateAutomation: async () => { calls++; return { data: automation } } })
  await p.open()
  await p.hide()
  await p.vm.save()
  assert.equal(calls, 0)
})
