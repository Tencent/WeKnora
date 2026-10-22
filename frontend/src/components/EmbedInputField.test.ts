import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, defineComponent, h, nextTick } from 'vue'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./EmbedInputField.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const compiled = ts.transpileModule(compileScript(descriptor, {
  id: 'embed-input', inlineTemplate: true,
}).content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

type Node = { tag: string; props: Record<string, any>; children: Node[]; parent?: Node; querySelector: () => null }
const node = (tag: string): Node => ({ tag, props: {}, children: [], querySelector: () => null })

async function fixture(showFileUploadToggle = true) {
  const sent: any[][] = [], alerts: string[] = [], revoked: string[] = []
  const exports: any = {}
  runInNewContext(compiled, {
    exports, HTMLTextAreaElement: class {},
    URL: { createObjectURL: URL.createObjectURL, revokeObjectURL(url: string) { revoked.push(url); URL.revokeObjectURL(url) } },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/utils/embedToast') return { embedToast: (message: string) => alerts.push(message) }
      if (name === '@/utils/embedFile') return require('../utils/embedFile.ts')
      if (name.endsWith('.svg')) return 'send.svg'
      throw new Error(`Unexpected import: ${name}`)
    },
  })
  const renderer = createRenderer<Node, Node>({
    createElement: node, createText: () => node('#text'), createComment: () => node('#comment'),
    insert(child, parent) { child.parent = parent; parent.children.push(child) },
    remove(child) { const siblings = child.parent?.children; if (siblings) siblings.splice(siblings.indexOf(child), 1) },
    setElementText() {}, setText() {}, patchProp(el, key, _old, value) { el.props[key] = value },
    parentNode: el => el.parent || null, nextSibling: () => null,
  })
  const app = renderer.createApp(exports.default, {
    isReplying: false, showFileUploadToggle,
    onSendMsg: (...args: any[]) => sent.push(args),
  })
  app.component('t-textarea', defineComponent({ props: ['modelValue'], setup: (props, { attrs }) => () => h('textarea', { ...attrs, value: props.modelValue }) }))
  app.component('t-icon', { render: () => null })
  app.component('t-tooltip', defineComponent({ props: ['placement', 'content'], setup: (_props, { slots }) => () => slots.default?.() }))
  const root = node('root')
  app.mount(root)
  await nextTick(); await nextTick()
  const all = (el: Node = root): Node[] => [el, ...el.children.flatMap(child => all(child))]
  const images = () => all().filter(el => el.tag === 'img' && el.props.src?.startsWith('blob:'))
  const click = async (cssClass: string) => {
    const button = all().find(el => el.tag === 'button' && String(el.props.class).split(' ').includes(cssClass))
    assert.ok(button, `Missing button: ${cssClass}`)
    button.props.onClick(); await nextTick()
  }
  const paste = async (files: File[] = [], text = '') => {
    let prevented = false
    const event = { clipboardData: { items: [
      ...files.map(file => ({ type: file.type, getAsFile: () => file })),
      ...(text ? [{ type: 'text/plain', getAsFile: () => null }] : []),
    ] }, preventDefault() { prevented = true } }
    all().find(el => el.tag === 'textarea')?.props.onPaste?.(event)
    await nextTick()
    return prevented
  }
  return { sent, alerts, revoked, images, click, paste, dispose: () => app.unmount() }
}
const file = (type = 'image/png', size = 10) => new File([new Uint8Array(size)], 'screenshot', { type })

test('pasted screenshot is previewed, sent without text, and its preview is released', async () => {
  const f = await fixture()
  try {
    const screenshot = file()
    await f.paste([screenshot])
    assert.equal(f.images().length, 1)
    await f.click('embed-send-btn')
    assert.equal(f.sent.length, 1)
    assert.equal(f.sent[0][0], '')
    assert.equal(f.sent[0][1][0], screenshot)
    assert.equal(f.sent[0][2].length, 0)
    assert.equal(f.images().length, 0)
    assert.equal(f.revoked.length, 1)
  } finally { f.dispose() }
})

test('disabled uploads and text-only paste retain native paste behavior', async () => {
  for (const enabled of [false, true]) {
    const f = await fixture(enabled)
    try {
      assert.equal(await f.paste([], 'hello'), false)
      if (!enabled) assert.equal(await f.paste([file()], 'hello'), false)
      assert.equal(f.images().length, 0)
      assert.equal(f.alerts.length, 0)
    } finally { f.dispose() }
  }
})

test('pasted images use the existing type, size and count limits', async () => {
  const f = await fixture()
  try {
    await f.paste([file('image/svg+xml'), file('image/png', 10 * 1024 * 1024 + 1)])
    assert.equal(f.images().length, 0)
    assert.deepEqual(f.alerts, ['chat.imageTypeSizeError', 'chat.imageTypeSizeError'])
    await f.paste([file('image/png', 10 * 1024 * 1024), file('image/jpeg'), file('image/gif'), file('image/webp'), file(), file()])
    assert.equal(f.images().length, 5)
    assert.equal(f.alerts.at(-1), 'chat.imageTooMany')
    await f.click('embed-image-thumb__remove')
    assert.equal(f.images().length, 4)
    await f.paste([file()])
    assert.equal(f.images().length, 5)
  } finally { f.dispose() }
  assert.equal(f.revoked.length, 6)
})
