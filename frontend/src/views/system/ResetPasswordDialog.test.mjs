import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import * as vue from 'vue'
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc'
import ts from 'typescript'

const read = (file) => readFileSync(new URL(file, import.meta.url), 'utf8')
const flush = () => new Promise(setImmediate)

function compileComponent(source, imports) {
  const { descriptor } = parse(source)
  const script = compileScript(descriptor, { id: 'reset-test' })
  const template = compileTemplate({
    source: descriptor.template.content,
    filename: 'ResetPasswordDialog.vue',
    id: 'reset-test',
    compilerOptions: { bindingMetadata: script.bindings },
  })
  assert.deepEqual(template.errors, [])
  const evaluate = (source) => {
    const exports = {}
    const { outputText } = ts.transpileModule(source, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    })
    runInNewContext(outputText, {
      exports,
      require(name) {
        if (name === 'vue') return vue
        assert.ok(name in imports, `Unexpected import: ${name}`)
        return imports[name]
      },
    })
    return exports
  }
  return Object.assign(evaluate(script.content).default, { render: evaluate(template.code).render })
}

function mountResetRow() {
  const requests = []
  const notices = []
  const Dialog = compileComponent(read('./ResetPasswordDialog.vue'), {
    'tdesign-vue-next': { MessagePlugin: { success: (msg) => notices.push(msg), error: (msg) => notices.push(msg) } },
    'vue-i18n': { useI18n: () => ({ t: (key) => key }) },
    '@/api/auth': { getAuthConfig: async () => ({ complex_password_enabled: true }) },
    '@/utils/passwordPolicy': { newPasswordRules: () => [] },
    '@/api/system': {
      resetUserPassword: (body) => new Promise((resolve, reject) => requests.push({ body, resolve, reject })),
    },
  })
  // Compile the real parent row so reverting its lifetime/visibility binding
  // also fails this test, instead of testing a hand-written approximation.
  const { descriptor } = parse(read('./SystemSettings.vue'))
  function findRow(node) {
    if (node.props?.some((prop) => prop.name === 'class' && prop.value?.content.includes('setting-row--password-reset'))) {
      return node.loc.source
    }
    return node.children?.map(findRow).find(Boolean)
  }
  const row = findRow(descriptor.template.ast)
  assert.ok(row, 'password reset row must exist')
  const Parent = compileComponent(`<template>${row.replace('<ResetPasswordDialog ', '<ResetPasswordDialog ref="dialogRef" ')}</template>
    <script setup>
    import { ref } from 'vue'
    import ResetPasswordDialog from './ResetPasswordDialog.vue'
    const activeSettingsSection = ref('access')
    const passwordResetVisible = ref(false)
    const saveAnnouncement = ref('')
    const dialogRef = ref()
    const t = (key) => key
    </script>`, { './ResetPasswordDialog.vue': { default: Dialog, __esModule: true } })

  const renderer = vue.createRenderer({
    createElement: (tag) => ({ tag, children: [], style: {} }),
    createText: (text) => ({ text }),
    createComment: (text) => ({ text }),
    insert(child, parent, anchor) {
      if (child.parent) child.parent.children = child.parent.children.filter((item) => item !== child)
      const index = anchor ? parent.children.indexOf(anchor) : -1
      parent.children.splice(index < 0 ? parent.children.length : index, 0, child)
      child.parent = parent
    },
    remove(child) {
      if (child.parent) child.parent.children = child.parent.children.filter((item) => item !== child)
      child.parent = null
    },
    parentNode: (node) => node?.parent,
    nextSibling: (node) => node.parent?.children[node.parent.children.indexOf(node) + 1] ?? null,
    setText(node, text) { node.text = text },
    setElementText(node, text) { node.children = []; node.text = text },
    patchProp(node, key, _, value) { if (key !== 'style') node[key] = value },
  })
  const passthrough = { setup: (_, { slots }) => () => vue.h('div', slots.default?.()) }
  const app = renderer.createApp(Parent)
  for (const name of ['t-button', 't-tag', 't-icon', 't-input', 't-form-item']) app.component(name, passthrough)
  app.component('t-form', {
    setup(_, { slots, expose }) {
      expose({ validate: async () => true, clearValidate() {} })
      return () => vue.h('form', slots.default?.())
    },
  })
  app.component('t-popup', {
    props: ['visible'],
    setup: (props, { slots }) => () => vue.h('div', [slots.default?.(), props.visible ? slots.content?.() : null]),
  })
  app.mount({ children: [] })
  const parent = app._instance.setupState
  return { app, parent, requests, notices, dialog: () => parent.dialogRef.$.setupState }
}

for (const outcome of ['success', 'failure']) {
  test(`pending reset survives tab changes and reports ${outcome}`, async (t) => {
    const { app, parent, requests, dialog } = mountResetRow()
    t.after(() => app.unmount())
    parent.passwordResetVisible = true
    await flush()
    const initial = dialog()
    Object.assign(initial.form, { email: ' user@example.com ', newPassword: 'FirstSecret1!', confirmPassword: 'FirstSecret1!' })
    const pending = initial.submit()
    await flush()
    assert.equal(requests.length, 1)
    assert.equal(requests[0].body.email, 'user@example.com')
    initial.onVisibleChange(false)
    assert.equal(parent.passwordResetVisible, true)

    parent.activeSettingsSection = 'runtime'
    await flush()
    assert.equal(dialog(), initial, 'tab changes must preserve the pending request owner')
    assert.equal(parent.dialogRef.$.subTree.component.props.visible, false, 'teleported popup must be hidden')
    parent.activeSettingsSection = 'access'
    await flush()
    assert.equal(dialog(), initial)
    assert.equal(parent.dialogRef.$.subTree.component.props.visible, true)
    assert.equal(initial.complexPasswordEnabled, true)
    assert.equal(initial.submitting, true)
    assert.equal(initial.form.newPassword, 'FirstSecret1!')
    await initial.submit()
    assert.equal(requests.length, 1, 'returning to the tab must not allow a concurrent reset')

    parent.activeSettingsSection = 'runtime'
    await flush()
    if (outcome === 'success') requests[0].resolve({})
    else requests[0].reject(new Error('Reset failed'))
    await pending
    await flush()
    assert.equal(initial.submitting, false)
    assert.equal(parent.saveAnnouncement, outcome === 'success' ? 'system.globalSettings.passwordReset.success' : 'Reset failed')
    assert.equal(parent.passwordResetVisible, outcome !== 'success')
    parent.activeSettingsSection = 'access'
    await flush()
    assert.equal(parent.dialogRef.$.subTree.component.props.visible, outcome !== 'success')
    assert.equal(initial.form.newPassword, outcome === 'success' ? '' : 'FirstSecret1!')
  })
}
