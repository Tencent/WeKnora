import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import { DOMParser } from '@xmldom/xmldom'
import ts from 'typescript'
import { createRenderer, h, ref } from 'vue'
import * as filePreview from '../utils/filePreview'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./document-preview.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'preview-encoding-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function fixture(bytes: Uint8Array, fileType: string) {
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue' || name === 'xlsx') return require(name)
      if (name === '@/utils/filePreview') return filePreview
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      // Encoding and SheetJS rendering stay real. DOM sanitization and the
      // unused preview formats/network adapters are outside this test's scope.
      if (name === '@/utils/security') return { sanitizeHTML: (html: string) => html }
      return {}
    },
    Blob, TextDecoder, URL, console,
    document: { addEventListener() {}, removeEventListener() {}, activeElement: {} },
    window: { addEventListener() {}, removeEventListener() {} },
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref<any>()
  const app = renderer.createApp({
    render: () => h(component, {
      ref: instance,
      sourceBlob: new Blob([Uint8Array.from(bytes)]),
      fileName: `encoding.${fileType}`, fileType,
      // Invoke loadPreview explicitly so the test can await the whole pipeline.
      active: false,
    }),
  })
  app.mount({})
  return { vm: instance.value, close: () => app.unmount() }
}

for (const fileType of ['csv', 'tsv', 'tab']) {
  for (const encoding of ['gbk', 'utf8', 'utf8-bom']) {
    test(`${fileType} preview preserves ${encoding} Chinese text and table columns`, async () => {
      const separator = fileType === 'csv' ? ',' : '\t'
      const text = `item${separator}count\r\n蜡${separator}1\r\n`
      // C0 AF encodes 蜡 in GBK, but is an invalid overlong UTF-8 sequence.
      const bytes = encoding === 'gbk'
        ? Uint8Array.from([
          ...new TextEncoder().encode(`item${separator}count\r\n`),
          0xC0, 0xAF,
          ...new TextEncoder().encode(`${separator}1\r\n`),
        ])
        : new TextEncoder().encode((encoding === 'utf8-bom' ? '\uFEFF' : '') + text)
      const f = fixture(bytes, fileType)
      try {
        await f.vm.loadPreview()
        assert.equal(f.vm.error, '')
        assert.equal(f.vm.loading, false)
        assert.equal(f.vm.previewType, 'excel')
        const doc = new DOMParser().parseFromString(f.vm.excelHtml, 'text/html')
        const cells = Array.from(doc.getElementsByTagName('td'), cell => cell.textContent)
        assert.deepEqual(cells, ['item', 'count', '蜡', '1'])
      } finally {
        f.close()
      }
    })
  }
}
