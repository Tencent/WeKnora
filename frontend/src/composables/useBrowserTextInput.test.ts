import assert from 'node:assert/strict'
import test from 'node:test'
import { effectScope, ref } from 'vue'
import { useBrowserTextInput } from './useBrowserTextInput.ts'

function setup() {
  const enabled = ref(true), commands: unknown[] = [], errors: string[] = []
  const scope = effectScope()
  const input = scope.run(() => useBrowserTextInput({ enabled: () => enabled.value, send: (action, fields) => { commands.push({ action, ...fields }) }, onTooLong: () => { errors.push('too long') } }))!
  const node = { value: '', focus() {}, blur() {} }
  input.element.value = node as HTMLTextAreaElement
  input.focusAt(75, 80)
  return { enabled, commands, errors, scope, input, node }
}
const event = (fields = {}) => ({ preventDefault() {}, ...fields }) as any

test('printable keys and paste are delivered once using input events', () => {
  const { input, node, commands, scope } = setup()
  input.onKey(event({ key: 'a' })); assert.equal(commands.length, 0)
  node.value = 'a'; input.onInput(event({ data: 'a', inputType: 'insertText' }))
  input.onPaste(event({ clipboardData: { getData: () => '中文\n🙂' } }))
  assert.deepEqual(commands, [{ action: 'type', text: 'a' }, { action: 'type', text: '中文\n🙂' }])
  assert.equal(node.value, '')
  scope.stop()
})

test('IME commits once with final input before or after compositionend', () => {
  for (const finalBeforeEnd of [true, false]) {
    const { input, node, commands, scope } = setup()
    input.onCompositionStart()
    node.value = 'zhong'; input.onCompositionUpdate(event({ data: 'zhong' }))
    input.onInput(event({ data: 'zhong', isComposing: true }))
    input.onKey(event({ key: 'Enter', keyCode: 229 }))
    assert.equal(commands.length, 0)
    if (finalBeforeEnd) { node.value = '中'; input.onInput(event({ data: '中', isComposing: true })) }
    input.onCompositionEnd(event({ data: '中' }))
    if (!finalBeforeEnd) { node.value = '中'; input.onInput(event({ data: '中', inputType: 'insertFromComposition' })) }
    assert.deepEqual(commands, [{ action: 'type', text: '中' }])
    input.onCompositionStart(); input.onCompositionEnd(event({ data: '中' }))
    assert.equal(commands.length, 2, 'repeated identical words are distinct compositions')
    scope.stop()
  }
})

test('navigation keys go remote without submitting an IME candidate', () => {
  const { input, commands, scope } = setup()
  input.onKey(event({ key: 'Tab', shiftKey: true }))
  input.onKey(event({ key: 'a', metaKey: true }))
  input.onKey(event({ key: 'Enter' }))
  assert.deepEqual(commands, [{ action: 'press', key: 'Shift+Tab' }, { action: 'press', key: 'ControlOrMeta+A' }, { action: 'press', key: 'Enter' }])
  input.onCompositionStart(); input.onKey(event({ key: 'Enter', isComposing: true })); input.onCompositionEnd(event({ data: '' }))
  assert.equal(commands.length, 3)
  scope.stop()
})

test('leaving the viewport or losing control cancels pending composition', () => {
  for (const blur of [true, false]) {
    const { input, enabled, commands, node, scope } = setup()
    input.onCompositionStart(); node.value = 'pending'
    if (blur) input.onBlur(); else enabled.value = false
    input.onCompositionEnd(event({ data: '不应发送' }))
    assert.deepEqual(commands, [])
    assert.equal(input.composing.value, false)
    assert.equal(node.value, '')
    scope.stop()
  }
})

test('IME anchor follows clicked coordinates and oversized paste is not truncated', () => {
  const { input, commands, errors, scope } = setup()
  assert.match(input.style.value.left, /75%/)
  assert.match(input.style.value.top, /80%/)
  input.onPaste(event({ clipboardData: { getData: () => 'a'.repeat(10001) } }))
  assert.deepEqual(commands, []); assert.deepEqual(errors, ['too long'])
  scope.stop()
})
