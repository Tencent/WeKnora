import assert from 'node:assert/strict'
import test from 'node:test'
import type { CatalogEntry, VueComponentNode } from 'a2ui-vue'

import { a2uiButtonClass, a2uiTextFieldInputType, weknoraCatalog } from './a2uiCatalog.ts'

// A literal stand-in for the official catalog (shape copied from DEFAULT_CATALOG in dist/index-BwIwZ7ky.js:
// every component is { type: loader, props: node => ({...}) }). Again no import of the library itself — a static
// import 'a2ui-vue' would drag the lazily-loaded 400 KB into the test runtime, and "this module must not statically
// import the library" is one of the constraints guarded here (a lazy-load acceptance check asserts the entry bundle never
// contains the data-a2ui-frames marker). type-only imports are erased; the runtime stays dependency-free.
const BASE_CATALOG = {
  Text: {
    type: () => Promise.resolve({ name: 'A2UIText' }),
    props: () => ({ text: '', variant: null }),
  },
  Card: {
    type: () => Promise.resolve({ name: 'A2UICard' }),
    props: () => ({}),
  },
  Column: {
    type: () => Promise.resolve({ name: 'A2UIColumn' }),
    props: () => ({ align: 'stretch', justify: 'start' }),
  },
  Button: {
    type: () => Promise.resolve({ name: 'A2UIButton' }),
    props: () => ({ action: undefined, variant: undefined }),
  },
  TextField: {
    type: () => Promise.resolve({ name: 'A2UITextField' }),
    props: () => ({ label: undefined, variant: undefined, value: undefined }),
  },
}

// CatalogEntry is a union of "bare loader | { type, props }". Our override must be the latter shape,
// so the shape check itself is written as an assertion: without props/type the override is wrong, and it goes red.
function entryOf(entry: CatalogEntry): { type: unknown; props: (node: VueComponentNode) => Record<string, any> } {
  if (typeof entry === 'function' || !('props' in entry) || !('type' in entry)) {
    throw new assert.AssertionError({ message: '覆盖后的 Button 不是 { type, props } 形态' })
  }
  return entry
}

const node = (properties?: Record<string, unknown>): VueComponentNode =>
  ({ id: 'b1', type: 'Button', properties, dataContextPath: '/' }) as VueComponentNode

test('variant → 类名：三档各自精确', () => {
  assert.equal(a2uiButtonClass('primary'), 'a2ui-btn a2ui-btn--primary')
  assert.equal(a2uiButtonClass('default'), 'a2ui-btn a2ui-btn--default')
  assert.equal(a2uiButtonClass('borderless'), 'a2ui-btn a2ui-btn--borderless')
})

test('白名单外的 variant 落 default，不能把任意串拼进 class', () => {
  // variant comes from the model's fenced block — untrusted input.
  // Pair the negative assertion with a positive anchor on the same fixture (which string it equals), otherwise an implementation returning an empty string would stay green.
  assert.equal(a2uiButtonClass('primary a2ui-btn--injected'), 'a2ui-btn a2ui-btn--default')
  assert.equal(a2uiButtonClass('primary'), 'a2ui-btn a2ui-btn--primary') // anchor: the whitelist really passes
  assert.equal(a2uiButtonClass('PRIMARY'), 'a2ui-btn a2ui-btn--default')
  assert.equal(a2uiButtonClass('primary; background: red'), 'a2ui-btn a2ui-btn--default')
  assert.equal(a2uiButtonClass(undefined), 'a2ui-btn a2ui-btn--default')
  assert.equal(a2uiButtonClass(null), 'a2ui-btn a2ui-btn--default')
  assert.equal(a2uiButtonClass(''), 'a2ui-btn a2ui-btn--default')
  assert.equal(a2uiButtonClass(3), 'a2ui-btn a2ui-btn--default')
  assert.equal(a2uiButtonClass({}), 'a2ui-btn a2ui-btn--default')
  assert.equal(a2uiButtonClass('__proto__'), 'a2ui-btn a2ui-btn--default')
  assert.equal(a2uiButtonClass('constructor'), 'a2ui-btn a2ui-btn--default')
})

test('覆盖只动 Button 与 TextField，其余组件原样传过去', () => {
  const catalog = weknoraCatalog(BASE_CATALOG)
  assert.deepEqual(Object.keys(catalog).sort(), ['Button', 'Card', 'Column', 'Text', 'TextField'])
  assert.equal(catalog.Text, BASE_CATALOG.Text)
  assert.equal(catalog.Card, BASE_CATALOG.Card)
  assert.equal(catalog.Column, BASE_CATALOG.Column)
  assert.notEqual(catalog.Button, BASE_CATALOG.Button)
  assert.notEqual(catalog.TextField, BASE_CATALOG.TextField)
})

test('覆盖不就地改传入的 catalog', () => {
  weknoraCatalog(BASE_CATALOG)
  // The original entry's props still behave the original way (returns action/variant, no class).
  assert.deepEqual(BASE_CATALOG.Button.props(), { action: undefined, variant: undefined })
  assert.deepEqual(Object.keys(BASE_CATALOG).sort(), ['Button', 'Card', 'Column', 'Text', 'TextField'])
})

test('自家 Button 的 props 只从节点里取 action 与 variant', () => {
  const entry = entryOf(weknoraCatalog(BASE_CATALOG).Button)
  assert.equal(typeof entry.type, 'function')
  // Expected values written as literals throughout. In the protocol, action is {event:{name}}, passed through verbatim and not interpreted at this layer.
  assert.deepEqual(
    entry.props(
      node({
        child: 'b1_lbl',
        variant: 'primary',
        action: { event: { name: 'approve', context: {} } },
      }),
    ),
    { action: { event: { name: 'approve', context: {} } }, variant: 'primary' },
  )
  // The label does not enter props: child is read from properties by the component itself (same as the official one).
  const picked = entry.props(node({ child: 'b1_lbl', variant: 'borderless' }))
  assert.deepEqual(Object.keys(picked).sort(), ['action', 'variant'])
  assert.equal(picked.variant, 'borderless')
})

test('节点连 properties 都没有时不抛，两个读数都是 undefined', () => {
  const entry = entryOf(weknoraCatalog(BASE_CATALOG).Button)
  // Should never happen at the protocol layer, but the renderer is lenient toward components with unregistered schemas — this layer must not crash along with it.
  assert.deepEqual(entry.props(node(undefined)), { action: undefined, variant: undefined })
  assert.deepEqual(entry.props({} as VueComponentNode), { action: undefined, variant: undefined })
})

test('自家 TextField 的 props 只转发 variant，label/value 一概不抄', () => {
  const entry = entryOf(weknoraCatalog(BASE_CATALOG).TextField)
  assert.equal(typeof entry.type, 'function')
  assert.deepEqual(
    entry.props(node({ variant: 'number', label: { path: '/x' }, value: { path: '/name' } })),
    { variant: 'number' },
  )
  assert.deepEqual(Object.keys(entry.props(node(undefined))).sort(), ['variant'])
  assert.equal(entry.props(node(undefined)).variant, undefined)
})

test('输入类型白名单：未知/伪造 variant 一律退 text，prototype 键不算命中', () => {
  assert.equal(a2uiTextFieldInputType('number'), 'number')
  assert.equal(a2uiTextFieldInputType('obscured'), 'password')
  assert.equal(a2uiTextFieldInputType('longText'), 'text') // 多行走 textarea，不进 input type
  assert.equal(a2uiTextFieldInputType('shortText'), 'text')
  assert.equal(a2uiTextFieldInputType(undefined), 'text')
  assert.equal(a2uiTextFieldInputType('constructor'), 'text')
  assert.equal(a2uiTextFieldInputType('__proto__'), 'text')
  assert.equal(a2uiTextFieldInputType('number; x'), 'text')
  assert.equal(a2uiTextFieldInputType(3), 'text')
})
