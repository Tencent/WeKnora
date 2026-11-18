import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'
import type * as SourceLocator from './sourceLocator'
import type * as TableSource from './tableSource'

const compile = (file: string) => ts.transpileModule(
  readFileSync(new URL(file, import.meta.url), 'utf8'),
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
).outputText
const locatorCode = compile('./sourceLocator.ts')
const tableCode = compile('./tableSource.ts')

function loadModules(segmenter: typeof Intl.Segmenter | undefined) {
  const locator = {} as typeof SourceLocator
  // Evaluate the module itself with the API absent, without changing global Intl.
  runInNewContext(locatorCode, { exports: locator, Intl: { Segmenter: segmenter } })
  const table = {} as typeof TableSource
  runInNewContext(tableCode, {
    exports: table,
    require: (name: string) => {
      assert.equal(name, './sourceLocator')
      return locator
    },
  })
  return { ...locator, ...table }
}

for (const segmenter of [Intl.Segmenter, undefined]) {
  const mode = segmenter ? 'with Intl.Segmenter' : 'without Intl.Segmenter'

  test(`citation modules load and preserve ordinary matching ${mode}`, () => {
    const { findInText, normalizeForMatch } = loadModules(segmenter)
    const hit = findInText('hello world', 'world')
    assert.ok(hit)
    assert.equal(hit.start, 6)
    assert.equal(hit.end, 11)
    assert.equal(normalizeForMatch('８.１４ Ｈello!'), '8.14hello')
    assert.equal(findInText('hello world world', 'world'), null)
    assert.equal(findInText('value 15', '1.5'), null)
  })

  test(`canonical matching preserves original UTF-16 spans ${mode}`, () => {
    const { findInText, normalizeForMatch } = loadModules(segmenter)
    assert.notEqual(normalizeForMatch('Café'), normalizeForMatch('Cafe'))
    for (const [raw, quote] of [
      ['Cafe\u0301', 'Café'],
      ['Café', 'Cafe\u0301'],
      ['A\u030Angstro\u0308m', 'Ångström'],
      ['Ångström', 'Ångström'], // A single code point can also change under NFC.
      ['\u1100\u1161\u11A8도', '각도'],
      ['가\u11A8도', '각도'],
      ['각도', '\u1100\u1161\u11A8도'],
      ['ab\u0301', 'ab'], // Uncomposed marks must still be included in the span.
      ['a\u0315\u0300b', 'à\u0315b'], // Canonical mark reordering.
    ] as const) {
      const text = `𝒜前言 ${raw} 后记`
      assert.equal(normalizeForMatch(raw), normalizeForMatch(quote))
      const hit = findInText(text, quote)
      assert.ok(hit, text)
      assert.equal(hit.start, text.indexOf(raw))
      assert.equal(hit.end, text.indexOf(raw) + raw.length)
      assert.equal(text.slice(hit.start, hit.end), raw)
    }
  })

  test(`table highlights retain combining marks ${mode}`, () => {
    const { findTableSource } = loadModules(segmenter)
    const page = 'Maths Cafe\u0301\nPhysics Andre\u0301\nHistory Chloe\u0301\nBiology Renee\u0301'
    const scope = '|Maths Physics History Biology|Café André Chloé Reneé|'
    const hits = findTableSource([page], scope)
    assert.equal(hits.length, 8)
    assert.ok(hits.some(hit => hit.quote === 'Cafe\u0301'))
    for (const hit of hits) assert.equal(page.slice(hit.start, hit.end), hit.quote)
  })
}
