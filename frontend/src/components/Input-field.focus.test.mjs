import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

const source = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const focusCode = source.slice(source.indexOf('const focusInput ='), source.indexOf('const onInput ='))
const sendCode = source.slice(source.indexOf('const createSession ='), source.indexOf('const updateAgentModeDropdownPosition ='))

for (const remote of [false, true]) {
  test(`not-ready ${remote ? 'shared' : 'local'} agent preserves draft, then clears error on success`, async () => {
    const effects = []
    const submissionIssue = { value: '' }
    let ready = false
    const helperCode = source.slice(source.indexOf('const buildAgentNotReadyText ='), source.indexOf('watch(currentAgentConfig'))
    const context = {
      submissionIssue, props: {}, uploadedAttachments: { value: [] }, uploadedImages: { value: [] },
      allSelectedItems: { value: [] }, selectedModelId: { value: 'model' },
      selectedAgent: { value: { name: 'Agent', config: {} } }, settingsStore: { selectedAgentSourceTenantId: remote ? 'remote' : undefined },
      chatResources: { isLoaded: () => true }, collectAgentNotReadyReasons: () => ({ keys: ['summary_model'], labels: ready ? [] : ['Model'] }),
      locale: { value: 'en-US' }, formatLocalizedList: values => values.join(', '),
      canLocallyConfigureAgent: tenant => !tenant, t: key => key,
      showAgentNotReadyMessage: (agent, reasons, keys, tenant) => { submissionIssue.value = context.buildText(agent, reasons, tenant) },
      attachmentUploadRef: { value: null }, emit: name => effects.push(name), clearvalue: () => effects.push('clear'), focusInput: () => {},
    }
    context.buildText = vm.runInNewContext(ts.transpile(`${helperCode}\nbuildAgentNotReadyText`), context)
    const send = vm.runInNewContext(ts.transpile(`${sendCode}\ncreateSession`), context)
    await send('keep my draft')
    assert.equal(submissionIssue.value, remote ? 'input.sharedAgentNotReadyDetail' : 'input.agentNotReadyDetail')
    assert.deepEqual(effects, [])
    ready = true
    await send('keep my draft')
    assert.equal(submissionIssue.value, '')
    assert.deepEqual(effects, ['send-msg', 'clear'])
  })
}

test('changing agent or source tenant clears the old inline error', async () => {
  const start = source.indexOf('watch([selectedAgentId, () => settingsStore.selectedAgentSourceTenantId]')
  const code = source.slice(start, source.indexOf('// 智能体是否启用了网络搜索', start))
  let callback
  const submissionIssue = { value: 'old agent' }
  vm.runInNewContext(ts.transpile(code), {
    watch: (deps, fn) => { callback = fn }, selectedAgentId: {}, settingsStore: {}, submissionIssue,
    sharedAgentKbList: { value: [] },
  })
  await callback(['ready-agent', undefined])
  assert.equal(submissionIssue.value, '')
})

for (const mode of ['normal', 'embedded', 'after', 'inject']) {
  test(`${mode} send clears the draft and restores focus after the DOM update`, async () => {
    const effects = [], ticks = []
    const textarea = {
      isConnected: true,
      focus: options => effects.push(['focus', options.preventScroll]),
      blur: () => { throw new Error('sending must not blur the textarea') },
    }
    const context = {
      submissionIssue: { value: "" },
      props: { isReplying: ['after', 'inject'].includes(mode), canSteer: true, embeddedMode: mode === 'embedded' },
      uploadedAttachments: { value: [] }, uploadedImages: { value: [] },
      allSelectedItems: { value: [] }, selectedModelId: { value: 'model' },
      selectedAgent: { value: { config: {} } },
      settingsStore: {}, chatResources: { isLoaded: () => true },
      collectAgentNotReadyReasons: () => ({ keys: [], labels: [] }),
      attachmentUploadRef: { value: null },
      emit: name => effects.push([name]),
      clearvalue: () => effects.push(['clear']),
      getTextareaEl: () => textarea,
      nextTick: () => new Promise(resolve => ticks.push(resolve)),
    }
    const send = vm.runInNewContext(ts.transpile(`${focusCode}\n${sendCode}\ncreateSession`), context)
    await send('hello', mode === 'inject' ? 'inject' : 'after')
    assert.deepEqual(effects, [[context.props.isReplying ? 'steer-msg' : 'send-msg'], ['clear']])
    for (const resolve of ticks) resolve()
    await new Promise(setImmediate)
    assert.deepEqual(effects.at(-1), ['focus', true])
  })
}

test('a queued focus does not focus a textarea removed during navigation', async () => {
  let resolveTick, connected = true, focused = false
  const focus = vm.runInNewContext(`${focusCode}\nfocusInput`, {
    nextTick: () => new Promise(resolve => { resolveTick = resolve }),
    getTextareaEl: () => ({ isConnected: connected, focus: () => { focused = true } }),
  })
  const pending = focus()
  connected = false
  resolveTick()
  await pending
  assert.equal(focused, false)
})

test('teardown blurs the active textarea before it is detached', () => {
  const code = source.slice(source.indexOf('onBeforeUnmount(() =>'), source.indexOf('onUnmounted(() =>'))
  let teardown, blurred = false
  const textarea = { isConnected: true, blur: () => { blurred = true } }
  vm.runInNewContext(code, {
    onBeforeUnmount: fn => { teardown = fn }, getTextareaEl: () => textarea,
    document: { activeElement: textarea },
  })
  teardown()
  assert.equal(blurred, true)
})

test('new-session focus survives consumption of the first query and runs on child mount', () => {
  const page = readFileSync(new URL('../views/chat/index.vue', import.meta.url), 'utf8')
  const snapshot = page.match(/const focusComposerOnMount = [^;]+;/)[0]
  const firstQuery = { value: 'initial question' }
  const autoFocus = vm.runInNewContext(`${snapshot}\nfirstQuery.value = ''; focusComposerOnMount`, { firstQuery })
  assert.equal(autoFocus, true)
  assert.match(page, /:auto-focus="focusComposerOnMount"/)
  const mountStart = source.slice(source.indexOf('onMounted(() => {'), source.indexOf('// Embed 渠道'))
  let mounted, focused = false
  vm.runInNewContext(`${mountStart}\n});`, {
    props: { autoFocus }, onMounted: fn => { mounted = fn }, focusInput: () => { focused = true },
  })
  assert.equal(focused, false)
  mounted()
  assert.equal(focused, true)
})
