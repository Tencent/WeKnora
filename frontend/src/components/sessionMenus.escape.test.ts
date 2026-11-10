import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { cloneVNode, createRenderer, defineComponent, h, markRaw, nextTick } from 'vue'

const require = createRequire(import.meta.url)

type Host = {
  tag: string
  text: string
  props: Record<string, any>
  children: Host[]
  parent: Host | null
  focus(): void
  select(): void
  addEventListener(): void
}

function fixture(name: 'ChatHeader' | 'SessionSidebarRow') {
  const { descriptor } = parse(readFileSync(new URL(`./${name}.vue`, import.meta.url), 'utf8'))
  const compiled = ts.transpileModule(compileScript(descriptor, {
    id: 'session-menu-escape-test', inlineTemplate: true,
  }).content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console,
    require(module: string) {
      if (module === 'vue') return require('vue')
      if (module === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (module === 'tdesign-vue-next') return { MessagePlugin: {} }
      if (module.endsWith('useSessionTitleMotion')) return { useSessionTitleMotion() {} }
      if (module.endsWith('deploymentCapabilities')) {
        return { useDeploymentCapabilitiesStore: () => ({ isSupported: () => false }) }
      }
      if (module === './sessionTitleEdit') return require('./sessionTitleEdit.ts')
      if (module === '@/utils/hostWorkspace') return require('../utils/hostWorkspace.ts')
      if (module.startsWith('@/api/') || module.startsWith('@/utils/') || module === './sessionMutations') return {}
      throw new Error(`Unexpected import: ${module}`)
    },
  })

  let activeElement: Host | null = null
  const node = (tag = '', text = ''): Host => markRaw({
    tag, text, props: {}, children: [], parent: null,
    focus() { activeElement = this }, select() {}, addEventListener() {},
  })
  const renderer = createRenderer<Host, Host>({
    createElement: tag => node(tag), createText: text => node('#text', text), createComment: () => node('#comment'),
    insert(child, parent, anchor) {
      if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1)
      child.parent = parent
      const index = anchor ? parent.children.indexOf(anchor) : -1
      if (index < 0) parent.children.push(child)
      else parent.children.splice(index, 0, child)
    },
    remove(child) {
      if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1)
      child.parent = null
    },
    setElementText(el, text) { el.text = text; el.children = [] },
    setText(el, text) { el.text = text },
    patchProp(el, key, _old, value) { el.props[key] = value },
    parentNode: el => el.parent,
    nextSibling: el => el.parent?.children[el.parent.children.indexOf(el) + 1] || null,
  })
  const Popup = defineComponent({
    props: { visible: Boolean }, emits: ['update:visible'],
    setup(props, { emit, slots }) {
      return () => h('popup', [
        cloneVNode(slots.default!()[0], { onClick: () => emit('update:visible', !props.visible) }),
        props.visible ? slots.content?.() : null,
      ])
    },
  })
  const events: string[] = []
  const props = name === 'ChatHeader'
    ? { session: { id: 'session-1', title: 'Original title' }, onDelete: () => events.push('delete') }
    : {
      item: { id: 'session-1', path: '/chat/session-1', title: 'Original title' },
      batchMode: false, activePath: '', selectedIds: [],
      menuOptions: [
        { value: 'rename', content: 'Rename' },
        { value: 'clearMessages', content: 'Clear' },
        { value: 'delete', content: 'Delete' },
      ],
      onMenuClick: () => events.push('menu-click'), onRenameSubmit: () => events.push('rename-submit'),
    }
  const app = renderer.createApp(exports.default, props)
  app.component('t-popup', Popup)
  for (const name of ['t-icon', 't-loading', 't-checkbox', 'router-link']) app.component(name, { render: () => null })
  const root = node('root')
  app.mount(root)
  const descendants = (el: Host): Host[] => [el, ...el.children.flatMap(descendants)]
  const find = (predicate: (el: Host) => boolean) => {
    const el = descendants(root).find(predicate)
    assert.ok(el, 'expected control to be rendered')
    return el
  }
  const text = (el: Host): string => el.text + el.children.map(text).join('')
  const trigger = () => find(el => el.tag === 'button' && el.props['aria-label'] === 'chatHeader.moreActions')
  const click = async (el: Host) => {
    const event = { stopPropagation() {} }
    for (const handler of [el.props.onClick].flat()) handler?.(event)
    await nextTick()
  }
  const keydown = async (target: Host, key = 'Escape') => {
    let stopped = false
    const event = {
      key, defaultPrevented: false,
      preventDefault() { this.defaultPrevented = true },
      stopPropagation() { stopped = true },
    }
    for (let el: Host | null = target; el && !stopped; el = el.parent) {
      for (const handler of [el.props.onKeydown].flat()) handler?.(event)
    }
    await nextTick()
    return event
  }
  return { root, find, text, trigger, click, keydown, events, activeElement: () => activeElement, dispose: () => app.unmount() }
}

for (const name of ['ChatHeader', 'SessionSidebarRow'] as const) {
  test(`${name}: Escape on the trigger or a menu action closes the popup and restores focus`, async t => {
    const f = fixture(name)
    t.after(f.dispose)
    for (const location of ['trigger', 'action']) {
      await f.click(f.trigger())
      assert.equal(f.trigger().props['aria-expanded'], true)
      const target = location === 'trigger' ? f.trigger() : f.find(el => el.tag === 'button' && el.props.class?.includes('card-menu-item'))
      target.focus()
      const event = await f.keydown(target)
      assert.equal(event.defaultPrevented, true)
      assert.equal(f.trigger().props['aria-expanded'], false)
      assert.ok(f.activeElement() === f.trigger(), 'focus returns to the menu trigger')
    }
    assert.deepEqual(f.events, [])
  })

  test(`${name}: cancelling delete with Escape returns to normal actions on reopening`, async t => {
    const f = fixture(name)
    t.after(f.dispose)
    await f.click(f.trigger())
    await f.click(f.find(el => el.tag === 'button' && f.text(el) === (name === 'ChatHeader' ? 'chatHeader.deleteSession' : 'Delete')))
    const confirm = f.find(el => el.tag === 'button' && f.text(el) === 'common.delete')
    confirm.focus()
    await f.keydown(confirm)
    assert.ok(f.activeElement() === f.trigger(), 'focus returns to the menu trigger')
    await f.click(f.trigger())
    f.find(el => el.tag === 'button' && f.text(el) === (name === 'ChatHeader' ? 'menu.renameSession' : 'Rename'))
    assert.deepEqual(f.events, [])
  })

  test(`${name}: other keys leave the menu open and Escape keeps title-edit cancellation`, async t => {
    const f = fixture(name)
    t.after(f.dispose)
    assert.equal((await f.keydown(f.trigger())).defaultPrevented, false)
    await f.click(f.trigger())
    const action = f.find(el => el.tag === 'button' && f.text(el) === (name === 'ChatHeader' ? 'menu.renameSession' : 'Rename'))
    assert.equal((await f.keydown(action, 'ArrowDown')).defaultPrevented, false)
    assert.equal(f.trigger().props['aria-expanded'], true)
    await f.click(action)
    const input = f.find(el => el.tag === 'input')
    await f.keydown(input)
    f.find(el => el.tag === (name === 'ChatHeader' ? 'h1' : 'span') && f.text(el) === 'Original title')
    assert.deepEqual(f.events, [])
  })
}
