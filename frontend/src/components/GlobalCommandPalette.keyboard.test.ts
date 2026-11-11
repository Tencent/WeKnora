import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import { parse } from '@vue/compiler-sfc'
import ts from 'typescript'

const { descriptor } = parse(readFileSync(new URL('./GlobalCommandPalette.vue', import.meta.url), 'utf8'))
const source = ts.createSourceFile('palette.ts', descriptor.scriptSetup!.content, ts.ScriptTarget.ES2022, true)
const declaration = source.statements.find(node => ts.isVariableStatement(node)
  && node.declarationList.declarations.some(item => item.name.getText(source) === 'onKeyDown'))
assert.ok(declaration, 'the palette must have its keyboard handler')
const code = ts.transpileModule(`${declaration.getText(source)}\nonKeyDown`, {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText

function fixture(settingsOpen = false) {
  const actions: unknown[] = []
  const settingsVisible = { value: settingsOpen }
  const handler = vm.runInNewContext(code, {
    settingsVisible,
    moveSelection: (direction: number) => actions.push(['move', direction]),
    flatItems: { value: [{ run: (event: { cmd: boolean }) => actions.push(['digit', event.cmd]) }] },
    primaryActionForSelected: (event: { cmd: boolean }) => actions.push(['primary', event.cmd]),
    handleClose: () => actions.push(['close']),
  }) as (event: KeyboardEvent) => void
  const key = (options: Partial<KeyboardEvent> = {}) => {
    let prevented = false
    handler({
      key: 'Enter', keyCode: 13, isComposing: false, metaKey: false, ctrlKey: false,
      target: { closest: () => null },
      ...options,
      preventDefault() { prevented = true },
    } as unknown as KeyboardEvent)
    return prevented
  }
  return { key, actions, settingsVisible }
}

test('composition confirmation does not activate a result or cancel the IME event', () => {
  for (const options of [{ isComposing: true }, { keyCode: 229 }]) {
    const f = fixture()
    assert.equal(f.key(options), false)
    assert.deepEqual(f.actions, [])
  }
})

test('Enter within a native button leaves activation to the button', () => {
  const f = fixture()
  const button = { tagName: 'BUTTON' }
  const target = { closest: (selector: string) => selector === 'button' ? button : null }
  assert.equal(f.key({ target: target as unknown as EventTarget }), false)
  assert.deepEqual(f.actions, [])
})

test('Enter and Cmd/Ctrl+Enter from the search input keep their existing actions', () => {
  for (const modifiers of [{}, { metaKey: true }, { ctrlKey: true }]) {
    const f = fixture()
    assert.equal(f.key(modifiers), true)
    assert.deepEqual(f.actions, [['primary', !!(modifiers.metaKey || modifiers.ctrlKey)]])
  }
})

test('native-button handling does not replace the existing modified Enter shortcut', () => {
  for (const modifiers of [{ metaKey: true }, { ctrlKey: true }]) {
    const f = fixture()
    const target = { closest: () => ({ tagName: 'BUTTON' }) }
    assert.equal(f.key({ ...modifiers, target: target as unknown as EventTarget }), true)
    assert.deepEqual(f.actions, [['primary', true]])
  }
})

test('arrow, digit, Escape and unrelated keys keep their existing behavior', () => {
  const f = fixture()
  assert.equal(f.key({ key: 'ArrowDown' }), true)
  assert.equal(f.key({ key: 'ArrowUp' }), true)
  assert.equal(f.key({ key: '1', metaKey: true }), true)
  assert.equal(f.key({ key: 'Escape' }), true)
  assert.equal(f.key({ key: 'a' }), false)
  assert.equal(f.key({ key: ' ' }), false)
  assert.deepEqual(f.actions, [['move', 1], ['move', -1], ['digit', false], ['close']])
  const settings = fixture(true)
  assert.equal(settings.key({ key: 'Escape' }), true)
  assert.equal(settings.settingsVisible.value, false)
  assert.deepEqual(settings.actions, [])
})
