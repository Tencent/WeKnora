import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { type TestContext } from 'node:test'
import { reactive } from 'vue'
import { fixture, flush } from '../test-utils/vueFixture.ts'

function setup(t: TestContext, options: {
  newUser?: boolean
  seenAssistant?: boolean
  visible?: boolean
  entry?: 'sidebar' | 'launcher' | 'palette'
  settings?: boolean
} = {}) {
  const saved = new Map<string, string>()
  const f = fixture(t, {
    getItem: key => saved.get(key) ?? null,
    setItem: (key, value) => { saved.set(key, value) },
    removeItem: key => { saved.delete(key) },
  })
  const guides = f.evaluate(readFileSync(new URL('../config/contextualGuides.ts', import.meta.url), 'utf8'), {})
  if (!options.newUser) saved.set(guides.GLOBAL_USER_GUIDE_KEY, '1')
  if (options.seenAssistant) saved.set(guides.ASSISTANT_ENTRY_GUIDE_KEY, '1')
  const assistant = reactive({ visible: options.visible ?? false, entry: options.entry ?? 'sidebar' })
  const ui = reactive({
    showSettingsModal: options.settings ?? false,
    expandSidebar() {},
    openSettings() { this.showSettingsModal = true },
    closeSettings() { this.showSettingsModal = false },
  })
  const dependencies = {
    '@/config/contextualGuides': guides,
    '@/stores/assistant': { useAssistantStore: () => assistant },
    '@/stores/ui': { useUIStore: () => ui },
  }
  const mount = () => f.component(new URL('./NewUserGuide.vue', import.meta.url), dependencies)
  return { ...f, mount, assistant, ui, guides, saved, dependencies }
}

test('upgrading shows only the assistant entry and waits before showing page tours', async t => {
  const f = setup(t)
  const { vm } = f.mount()
  const page = f.component(new URL('./ContextualGuide.vue', import.meta.url), f.dependencies, {
    tour: 'kbList', when: true,
  })
  assert.equal(f.guides.isGlobalUserGuideDone(), false)
  t.mock.timers.tick(700)
  await flush()
  assert.equal(vm.active, true)
  assert.equal(vm.assistantOnly, true)
  assert.deepEqual(Array.from(vm.steps, (step: any) => step.key), ['assistant'])
  assert.equal(vm.steps[0].target, '[data-guide="assistant-entry"]')
  assert.equal(vm.steps[0].interact, true)
  assert.equal(page.vm.active, false)

  f.assistant.visible = true
  await flush()
  assert.equal(vm.active, false, 'opening the highlighted entry dismisses the overlay')
  assert.equal(f.guides.isGlobalUserGuideDone(), true)
  t.mock.timers.tick(400)
  await flush()
  t.mock.timers.tick(500)
  await flush()
  assert.equal(page.vm.active, true, 'the existing page tour runs after the entry guide')
})

test('acknowledging the assistant entry is remembered across remounts', async t => {
  const f = setup(t)
  const first = f.mount()
  t.mock.timers.tick(700)
  first.vm.onFinish()
  first.close()
  assert.equal(f.assistant.visible, false, 'dismissal does not open the assistant')
  const second = f.mount()
  t.mock.timers.tick(2000)
  await flush()
  assert.equal(second.vm.active, false)
  assert.equal(f.guides.isGlobalUserGuideDone(), true)
})

test('new users see the assistant in the existing full tour without a second introduction', async t => {
  const f = setup(t, { newUser: true })
  const first = f.mount()
  t.mock.timers.tick(700)
  assert.equal(first.vm.active, true)
  assert.equal(first.vm.assistantOnly, false)
  const keys = Array.from(first.vm.steps, (step: any) => step.key)
  assert.deepEqual(keys, ['welcome', 'assistant', 'knowledge', 'agents', 'chat', 'settings', 'models', 'done'])
  f.assistant.visible = true
  await flush()
  assert.deepEqual(Array.from(first.vm.steps, (step: any) => step.key), keys,
    'opening the assistant must not shift the current tour step')
  first.vm.onFinish()
  first.close()
  f.assistant.visible = false
  const next = f.mount()
  t.mock.timers.tick(2000)
  await flush()
  assert.equal(next.vm.active, false)
})

test('entry introduction waits for settings to close and supports the floating launcher', async t => {
  const f = setup(t, { settings: true, entry: 'launcher' })
  const { vm } = f.mount()
  t.mock.timers.tick(2000)
  assert.equal(vm.active, false)
  f.ui.showSettingsModal = false
  await flush()
  t.mock.timers.tick(700)
  assert.equal(vm.active, true)
  assert.equal(vm.steps[0].placement, 'left')
})

test('an already open assistant or deliberately hidden entry does not block other tours', async t => {
  const f = setup(t, { visible: true })
  const first = f.mount()
  t.mock.timers.tick(700)
  assert.equal(first.vm.active, false)
  assert.equal(f.guides.isGlobalUserGuideDone(), true)
  first.close()
  f.saved.delete(f.guides.ASSISTANT_ENTRY_GUIDE_KEY)
  f.assistant.visible = false
  f.assistant.entry = 'palette'
  await flush()
  const second = f.mount()
  t.mock.timers.tick(700)
  assert.equal(second.vm.active, false)
  assert.equal(f.guides.isGlobalUserGuideDone(), true)
})

test('manual replay cancels the scheduled upgrade prompt and unmount cancels pending work', async t => {
  const f = setup(t)
  const first = f.mount()
  f.guides.openNewUserGuide()
  assert.equal(first.vm.active, true)
  assert.equal(first.vm.assistantOnly, false)
  assert.ok(first.vm.steps.some((step: any) => step.key === 'assistant'))
  t.mock.timers.tick(1000)
  await flush()
  assert.equal(first.vm.assistantOnly, false)
  first.close()

  const pending = f.mount()
  pending.close()
  t.mock.timers.tick(1000)
  assert.equal(pending.vm.active, false)
  assert.equal(f.saved.has(f.guides.ASSISTANT_ENTRY_GUIDE_KEY), false)
})
