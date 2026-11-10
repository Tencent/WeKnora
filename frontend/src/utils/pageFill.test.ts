import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { canUndoFill, fillPage, undoFill } from './pageFill.ts'
import { editableTextField } from './pageSnapshot.ts'

// Minimal DOM fixture: exercise the real ref lookup, value reader, native
// setter, fill history and undo path without adding a browser test dependency.
class ElementFixture extends EventTarget {
  isConnected = true
  parentElement = null
  previousElementSibling = null
  style = {}
  classList = { contains: () => false, add() {}, remove() {} }
  offsetWidth = 100
  clientWidth = 100
  clientHeight = 20
  scrollHeight = 20
  scrollTop = 0
  getAttribute(name: string) { return name === 'aria-label' ? 'Description' : null }
  hasAttribute() { return false }
  closest() { return null }
  getClientRects() { return [{}] }
  getBoundingClientRect() { return { top: 0, bottom: 0, left: 0 } }
  scrollIntoView() {}
  appendChild() {}
  remove() { this.isConnected = false }
}

class InputFixture extends ElementFixture {
  type = 'text'
  disabled = false
  readOnly = false
  private text = ''
  get value() { return this.text }
  set value(value: string) { this.text = value }
}

class TextareaFixture extends ElementFixture {
  disabled = false
  readOnly = false
  private text = ''
  get value() { return this.text }
  set value(value: string) { this.text = value }
}

const globals = {
  Element: ElementFixture,
  HTMLInputElement: InputFixture,
  HTMLTextAreaElement: TextareaFixture,
  document: { body: new ElementFixture(), createElement: () => new TextareaFixture() },
  getComputedStyle: () => ({ display: 'block', visibility: 'visible' }),
}
const saved = new Map<string, PropertyDescriptor | undefined>()
before(() => {
  for (const [key, value] of Object.entries(globals)) {
    saved.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
    Object.defineProperty(globalThis, key, { configurable: true, value })
  }
})
after(() => {
  for (const [key, descriptor] of saved) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

for (const Control of [InputFixture, TextareaFixture]) {
  for (const [name, edited] of [
    ['different text', 'A new draft'],
    ['case', 'hello world'],
    ['leading/trailing whitespace', ' Hello World '],
    ['repeated whitespace', 'Hello  World'],
    ['line break', 'Hello\nWorld'],
  ] as const) {
    if (Control === InputFixture && name === 'line break') continue
    test(`undo preserves a later ${name} edit in ${Control.name}`, async () => {
      const control = new Control()
      control.value = 'Original'
      const { ref } = editableTextField(control as unknown as Element)!
      await fillPage([{ ref, value: 'Hello World' }], ref)
      assert.equal(control.value, 'Hello World')
      assert.equal(canUndoFill(ref), true)

      control.value = edited
      control.dispatchEvent(new Event('input', { bubbles: true }))
      const result = await undoFill(ref)

      assert.equal(control.value, edited, 'undo must not overwrite the user edit')
      assert.deepEqual(result, { restored: 0, skipped: 1 })
      assert.equal(canUndoFill(ref), false)
    })
  }

  test(`undo restores unchanged ${Control.name} and consumes its history`, async () => {
    const control = new Control()
    control.value = 'Original'
    const events: string[] = []
    for (const type of ['input', 'change']) control.addEventListener(type, () => events.push(type))
    const { ref } = editableTextField(control as unknown as Element)!
    await fillPage([{ ref, value: 'Hello World' }], ref)
    assert.equal(control.value, 'Hello World')
    assert.deepEqual(await undoFill(ref), { restored: 1, skipped: 0 })
    assert.equal(control.value, 'Original')
    assert.deepEqual(events, ['input', 'change', 'input', 'change'])
    assert.equal(canUndoFill(ref), false)
    assert.deepEqual(await undoFill(ref), { restored: 0, skipped: 0 })
  })

  test(`undo skips disconnected ${Control.name}`, async () => {
    const control = new Control()
    control.value = 'Original'
    const { ref } = editableTextField(control as unknown as Element)!
    await fillPage([{ ref, value: 'Hello World' }], ref)
    control.remove()
    assert.deepEqual(await undoFill(ref), { restored: 0, skipped: 1 })
    assert.equal(control.value, 'Hello World')
  })
}
