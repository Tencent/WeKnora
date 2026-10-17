import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

const source = readFileSync(new URL('./Input-field.vue', import.meta.url), 'utf8')
const focusCode = source.slice(source.indexOf('const focusInput ='), source.indexOf('const onInput ='))
const sendCode = source.slice(source.indexOf('const createSession ='), source.indexOf('const updateAgentModeDropdownPosition ='))

for (const mode of ['normal', 'embedded', 'after', 'inject']) {
  test(`${mode} send clears the draft and restores focus after the DOM update`, async () => {
    const effects = [], ticks = []
    let sentModel
    const textarea = {
      isConnected: true,
      focus: options => effects.push(['focus', options.preventScroll]),
      blur: () => { throw new Error('sending must not blur the textarea') },
    }
    const context = {
      props: { isReplying: ['after', 'inject'].includes(mode), canSteer: true, embeddedMode: mode === 'embedded' },
      uploadedAttachments: { value: [] }, uploadedImages: { value: [] },
      allSelectedItems: { value: [] }, selectedModelId: { value: 'model' },
      composerModelId: { value: 'host-model' },
      selectedAgent: { value: { config: {} } },
      settingsStore: {}, chatResources: { isLoaded: () => true },
      collectAgentNotReadyReasons: () => ({ keys: [], labels: [] }),
      attachmentUploadRef: { value: null },
      emit: (name, _query, model) => {
        effects.push([name])
        if (name === 'send-msg') sentModel = model
      },
      clearvalue: () => effects.push(['clear']),
      getTextareaEl: () => textarea,
      nextTick: () => new Promise(resolve => ticks.push(resolve)),
    }
    const send = vm.runInNewContext(ts.transpile(`${focusCode}\n${sendCode}\ncreateSession`), context)
    await send('hello', mode === 'inject' ? 'inject' : 'after')
    assert.deepEqual(effects, [[context.props.isReplying ? 'steer-msg' : 'send-msg'], ['clear']])
    // Embedded, the host's model goes out, never the main chat's pick.
    if (mode === 'embedded') assert.equal(sentModel, 'host-model')
    if (mode === 'normal') assert.equal(sentModel, 'model')
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

for (const embeddedMode of [false, true]) {
  for (const delivery of ['after', 'inject']) {
    test(`${embeddedMode ? 'embedded' : 'main'} ${delivery} steer respects the composer's resource scope`, async () => {
      const selected = [
        { id: 'main-kb', name: 'Main KB', type: 'kb' },
        { id: 'main-file', name: 'Main file', type: 'file', kbId: 'main-kb' },
        { id: 'main-tag', name: 'Main tag', type: 'tag', kbId: 'main-kb' },
        { id: 'main-mcp', name: 'Main MCP', type: 'mcp' },
        { id: 'main-skill', name: 'Main skill', type: 'skill', skillName: 'main-skill' },
      ]
      let emitted
      const send = vm.runInNewContext(ts.transpile(`${sendCode}\ncreateSession`), {
        props: { embeddedMode, isReplying: true, canSteer: true },
        uploadedAttachments: { value: [] }, uploadedImages: { value: [] },
        allSelectedItems: { value: selected },
        emit: (...args) => { emitted = args },
        clearvalue() {}, focusInput: async () => {},
      })
      await send(' continue ', delivery)
      const [event, query, mentions, mode] = emitted
      assert.equal(event, 'steer-msg')
      assert.equal(query, 'continue')
      assert.equal(mode, delivery)
      assert.deepEqual(Array.from(mentions, item => item.id), embeddedMode ? [] : selected.map(item => item.id))
      assert.equal(selected.length, 5, 'sending must not clear the main composer selection')
    })
  }
}

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
  const autoFocus = vm.runInNewContext(`${snapshot}\nfirstQuery.value = ''; focusComposerOnMount`, {
    firstQuery, props: { embeddedMode: false, initialQuery: '' },
  })
  assert.equal(autoFocus, true)
  // An embedded host passes its own first message; the main chat's draft is not its.
  const embedded = { embeddedMode: true, initialQuery: 'host question' }
  assert.equal(vm.runInNewContext(`${snapshot}\nfocusComposerOnMount`, { firstQuery, props: embedded }), true)
  assert.equal(vm.runInNewContext(`${snapshot}\nfocusComposerOnMount`, {
    firstQuery: { value: 'main draft' }, props: { embeddedMode: true, initialQuery: '' },
  }), false)
  assert.match(page, /:auto-focus="focusComposerOnMount"/)
  const mountStart = source.slice(source.indexOf('onMounted(() => {'), source.indexOf('// 嵌入时 agent/KB 由宿主注入，只需'))
  let mounted, focused = false
  vm.runInNewContext(`${mountStart}\n});`, {
    props: { autoFocus }, onMounted: fn => { mounted = fn }, focusInput: () => { focused = true },
  })
  assert.equal(focused, false)
  mounted()
  assert.equal(focused, true)
})
