import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import { createRequire } from 'node:module'
import { JSDOM } from 'jsdom'
import JSZip from 'jszip'
import ts from 'typescript'
import { ref } from 'vue'
import * as XLSX from 'xlsx'
import * as filePreview from '../utils/filePreview.ts'
import { workbenchPreviewKind } from '../utils/workbenchPreview.ts'
import * as office from '../utils/workbenchOfficePreview.ts'
import { workbenchFileLimit } from '../utils/sandboxWorkbench.ts'

const require = createRequire(import.meta.url)
const dom = new JSDOM('')
globalThis.DOMParser = dom.window.DOMParser
test.after(() => dom.window.close())
const component = readFileSync(new URL('./WorkbenchDocumentPreview.vue', import.meta.url), 'utf8')
const body = component.slice(component.indexOf('function decodeTable('), component.indexOf('\nwatch('))
const compiled = ts.transpileModule(body, { compilerOptions: {
  module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022,
} }).outputText

async function preview(blob, type) {
  const props = { sourceBlob: blob, fileName: `report.${type}`, fileType: type, active: true }
  const state = Object.fromEntries(['loading', 'error', 'restrictedHtml', 'preparedBlob', 'preparedType', 'preparedKey'].map(key => [key, ref()]))
  let parsed = 0
  const context = {
    ...state, props, ...filePreview, ...office, workbenchPreviewKind, workbenchFileLimit,
    Blob, Uint8Array, TextDecoder, AbortController, document: dom.window.document,
    t: key => key, sanitizeWorkbenchPreview: html => html,
    require: name => {
      assert.equal(name, 'xlsx')
      return { ...require('xlsx'), read(...args) { parsed++; return XLSX.read(...args) } }
    },
  }
  const load = vm.runInNewContext(`let generation = 0; let request; ${compiled}; load`, context)
  await load()
  return { ...Object.fromEntries(Object.entries(state).map(([key, value]) => [key, value.value])), parsed }
}

async function workbookZip() {
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([['Count'], [42]]), 'Summary')
  return JSZip.loadAsync(XLSX.write(workbook, { type: 'array', bookType: 'xlsx' }))
}
const zipBlob = async zip => new Blob([await zip.generateAsync({ type: 'arraybuffer', compression: 'DEFLATE' })])

for (const type of ['xlsx', 'xls', 'csv', 'tsv', 'tab']) {
  test(`ZIP spreadsheet declared as ${type} is validated before auto-detection`, async () => {
    const zip = await workbookZip()
    zip.file('oversized.bin', new Uint8Array(office.WORKBENCH_OFFICE_LIMITS.entryBytes + 1))
    const source = await zipBlob(zip)
    assert.ok(source.size < 20000)
    const result = await preview(source, type)
    assert.equal(result.error, 'workbench.officeTooLarge')
    assert.equal(result.parsed, 0, 'the spreadsheet parser must not see unvalidated ZIP bytes')
    assert.equal(result.restrictedHtml, '')
  })
}

test('renamed XLSX retains aggregate expansion limits and external relationship rejection', async () => {
  const large = await workbookZip()
  for (let i = 0; i < 5; i++) large.file(`extra-${i}.bin`, new Uint8Array(office.WORKBENCH_OFFICE_LIMITS.entryBytes))
  assert.equal((await preview(await zipBlob(large), 'xls')).error, 'workbench.officeTooLarge')
  const external = await workbookZip()
  external.file('xl/externalLinks/_rels/link.xml.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="external" Target="https://example.invalid/private" TargetMode="External"/></Relationships>')
  assert.equal((await preview(await zipBlob(external), 'xls')).error, 'workbench.officeExternal')
})

test('valid renamed XLSX, legacy XLS, and delimited text retain table previews', async () => {
  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([['Count'], [42]]), 'Summary')
  for (const [source, type] of [
    [await zipBlob(await workbookZip()), 'xls'],
    [await zipBlob(await workbookZip()), 'csv'],
    [new Blob([XLSX.write(workbook, { type: 'array', bookType: 'biff8' })]), 'xls'],
    [new Blob(['PK,Count\nkey,42']), 'csv'],
    [new Blob(['Key\tCount\nkey\t42']), 'tsv'],
  ]) {
    const result = await preview(source, type)
    assert.equal(result.error, '')
    assert.equal(result.parsed, 1)
    assert.match(result.restrictedHtml, /<table/)
    assert.match(result.restrictedHtml, /42/)
  }
})

for (const type of ['mmd', 'mermaid', 'txt']) {
  test(`${type} text cannot re-enable the downstream diagram renderer`, async () => {
    const source = new Blob(['flowchart LR\nA@{ img: "https://example.invalid/private.png", label: "probe" }'])
    const result = await preview(source, type)
    assert.equal(result.error, '')
    assert.equal(result.preparedBlob, source)
    assert.equal(result.preparedType, 'txt')
    assert.equal(filePreview.resolvePreviewKind(filePreview.resolveFilePreviewExt(`report.${type}`, result.preparedType)), 'text')
    assert.equal(result.parsed, 0)
  })
}
