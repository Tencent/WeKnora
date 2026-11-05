import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { ref } from 'vue'
import { DOMParser, XMLSerializer } from '@xmldom/xmldom'
import { mermaidImage } from '../utils/mermaidImage'

const source = readFileSync(new URL('./document-preview.vue', import.meta.url), 'utf8')
const start = source.indexOf('async function renderMermaid(')
const end = source.indexOf('async function openMermaid()', start)
assert(start >= 0 && end > start)
const code = ts.transpileModule(source.slice(start, end), {
  compilerOptions: { target: ts.ScriptTarget.ES2022 },
}).outputText
function fixture(svg: string | null, serializedSvg?: string) {
  const mermaidSvg = ref(''),
    previewType = ref('mermaid'),
    sanitized: string[] = [],
    text: any[] = []
  const context = {
    mermaidSvg,
    previewType,
    mermaidImage,
    Blob,
    Date,
    XMLSerializer: serializedSvg
      ? class {
          serializeToString() {
            return serializedSvg
          }
        }
      : XMLSerializer,
    // The browser regression uses native template parsing for HTML labels.
    // Node fixtures use well-formed SVG and real XML serialization here.
    document: {
      createElement: () => {
        let parsed: any = null
        return {
          set innerHTML(html: string) {
            parsed = html ? new DOMParser().parseFromString(html, 'image/svg+xml') : null
          },
          content: { querySelector: () => parsed?.documentElement },
        }
      },
    },
    renderMermaidToSvg: async () => svg,
    sanitizeMarkdownHTML: (html: string) => {
      sanitized.push(html)
      return html
    },
    renderText: async (blob: Blob, language: string) => {
      text.push([await blob.text(), language])
    },
  }
  const render = new Function(...Object.keys(context), code + ';return renderMermaid')(
    ...Object.values(context),
  )
  return { render, mermaidSvg, previewType, sanitized, text }
}

test('standalone Mermaid passes only an image wrapper to sanitization and fullscreen state', async () => {
  const original =
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 100"><style>.node{fill:#f00}</style><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">设备归还</div></foreignObject></svg>'
  const f = fixture(original)
  await f.render(new Blob(['flowchart TD\n A[设备归还] --> B[完成]']))
  const doc = new DOMParser().parseFromString(f.mermaidSvg.value, 'image/svg+xml')
  const images = doc.getElementsByTagName('image')
  assert.equal(images.length, 1)
  assert.equal(doc.getElementsByTagName('foreignObject').length, 0)
  assert.equal(doc.getElementsByTagName('style').length, 0)
  const uri = images.item(0)!.getAttribute('href')!
  assert.equal(decodeURIComponent(uri.slice(uri.indexOf(',') + 1)), original)
  assert.deepEqual(f.sanitized, [f.mermaidSvg.value])
  assert.deepEqual(f.text, [])
})

for (const rendered of [null, '<svg viewBox="0 0 0 10"/>']) {
  test(`a failed render or unusable image geometry falls back to Mermaid source (${rendered})`, async () => {
    const f = fixture(rendered)
    const text = 'flowchart TD\n  A --> B'
    await f.render(new Blob([text]))
    assert.equal(f.previewType.value, 'text')
    assert.equal(f.mermaidSvg.value, '')
    assert.deepEqual(f.sanitized, [])
    assert.deepEqual(f.text, [[text, 'mmd']])
  })
}

test('the encoded image uses XML serialization of the inert fragment, not raw renderer HTML', async () => {
  const raw = '<svg viewBox="0 0 100 50"><text>renderer</text></svg>'
  const serialized =
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50"><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">XML<br/>label</div></foreignObject></svg>'
  const f = fixture(raw, serialized)
  await f.render(new Blob(['flowchart TD\n A --> B']))
  const doc = new DOMParser().parseFromString(f.mermaidSvg.value, 'image/svg+xml')
  const uri = doc.getElementsByTagName('image').item(0)!.getAttribute('href')!
  assert.equal(decodeURIComponent(uri.slice(uri.indexOf(',') + 1)), serialized)
})
