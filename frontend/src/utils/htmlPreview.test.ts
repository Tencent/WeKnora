import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import { buildHtmlPreview } from './htmlPreview.ts'

const bootstrap = buildHtmlPreview('').slice('<script>'.length, -'</script>'.length)

function createFrame(overrides: Record<string, unknown> = {}) {
  const window = Object.defineProperties({}, {
    localStorage: { configurable: true, get() { throw new Error('SecurityError') } },
    sessionStorage: { configurable: true, get() { throw new Error('SecurityError') } },
  }) as Window
  const context = vm.createContext({ window, ...overrides })
  vm.runInContext(bootstrap, context)
  return { window, context }
}

test('blocked storage no longer prevents generated page initialization and event handlers', () => {
  const button = { onclick: null as null | (() => void) }
  const { window, context } = createFrame({ button })
  vm.runInContext(`
    let best = Number(window.localStorage.getItem('best')) || 0;
    button.onclick = () => window.localStorage.setItem('best', ++best);
  `, context)
  assert.equal(typeof button.onclick, 'function')
  button.onclick!()
  button.onclick!()
  assert.equal(window.localStorage.getItem('best'), '2')
})

test('memory storage supports string conversion, keys, removal and clearing', () => {
  const { localStorage: storage } = createFrame().window
  assert.equal(storage.getItem('missing'), null)
  assert.equal(storage.length, 0)
  storage.setItem('best', 42 as unknown as string)
  storage.setItem('empty', '')
  storage.setItem('null', null as unknown as string)
  assert.equal(storage.getItem('best'), '42')
  assert.equal(storage.getItem('empty'), '')
  assert.equal(storage.getItem('null'), 'null')
  assert.equal(storage.length, 3)
  assert.equal(storage.key(0), 'best')
  assert.equal(storage.key(3), null)
  assert.equal(storage.key(-1), null)
  storage.removeItem('best')
  assert.equal(storage.key(0), 'empty')
  storage.clear()
  assert.equal(storage.length, 0)
})

test('property access, deletion and enumeration share the same stored values', () => {
  const { localStorage: storage } = createFrame().window
  storage.score = 128
  assert.equal(storage.getItem('score'), '128')
  storage.setItem('board', '[2,4]')
  assert.equal(storage.board, '[2,4]')
  assert.deepEqual(Object.keys(storage), ['score', 'board'])
  assert.equal(JSON.stringify(storage), '{"score":"128","board":"[2,4]"}')
  assert.equal('score' in storage, true)
  delete storage.score
  assert.equal(storage.score, undefined)
  assert.equal('score' in storage, false)
  storage.setItem('__proto__', 'safe')
  assert.equal(storage.getItem('__proto__'), 'safe')
  assert.equal(Object.getPrototypeOf(storage), null)
  storage.setItem('getItem', 'stored')
  assert.equal(storage.getItem('getItem'), 'stored')
})

test('each frame and each storage area has independent, temporary state', () => {
  const first = createFrame().window
  const second = createFrame().window
  first.localStorage.setItem('best', '100')
  first.sessionStorage.setItem('best', '20')
  assert.equal(first.localStorage.getItem('best'), '100')
  assert.equal(first.sessionStorage.getItem('best'), '20')
  assert.equal(second.localStorage.getItem('best'), null)
  assert.equal(second.sessionStorage.getItem('best'), null)
})

test('available native storage is preserved independently for each area', () => {
  const native = { length: 1, getItem: () => 'existing' }
  const window = { localStorage: native }
  vm.runInNewContext(bootstrap, { window })
  assert.equal(window.localStorage, native)
  assert.equal(window.localStorage.getItem(), 'existing')
  assert.equal((window as unknown as Window).sessionStorage.length, 0)
})

test('bootstrap precedes application scripts while preserving doctype, head and all source bytes', () => {
  const cases = [
    ['', '<button>Play</button>'],
    ['<!DOCTYPE html>', '<script>start()</script>'],
    ['\uFEFF\n<!-- <head> decoy -->\n<!doctype html>\n<html lang="en">\n<head data-test=">">', '<script>start()</script></head><body></body></html>'],
    ['<HTML><HEAD>', '<script>start()</script></HEAD></HTML>'],
    ['', '<script>const text = "<head>";</script>'],
  ]
  for (const [prefix, rest] of cases) {
    const source = prefix + rest
    const rendered = buildHtmlPreview(source)
    assert.equal(rendered, prefix + buildHtmlPreview('') + rest)
    assert.equal(rendered.replace(buildHtmlPreview(''), ''), source)
  }
})

test('only executable artifact previews use the compatibility copy; source and sandbox stay intact', () => {
  const source = readFileSync(new URL('../components/document-preview.vue', import.meta.url), 'utf8')
  const htmlCase = source.slice(source.indexOf("case 'html':"), source.indexOf("case 'docx':"))
  assert.match(htmlCase, /if \(allowsHtmlScriptPreview\(\)\) \{\s*const previewHtml = buildHtmlPreview\(await blob\.text\(\)\)/)
  assert.match(htmlCase, /new Blob\(\[previewHtml\], \{ type: 'text\/html;charset=utf-8' \}\)/)
  assert.match(htmlCase, /await renderText\(blob, ft \|\| 'html'\)/)
  assert.match(source, /sandbox="allow-scripts"/)
  assert.doesNotMatch(source, /allow-same-origin/)
})
