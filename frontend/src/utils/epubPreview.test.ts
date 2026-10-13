import assert from 'node:assert/strict'
import { after, test } from 'node:test'
import JSZip from 'jszip'
import { DOMParser as XmlParser, XMLSerializer } from '@xmldom/xmldom'
import { renderEpubPreview } from './epubPreview'

// Adapt the XML test DOM's missing browser conveniences. EPUB parsing and ZIP
// resolution still run through the production renderer, with no mocked output.
const originalParser = globalThis.DOMParser
class TestParser {
  parseFromString(text: string) {
    const doc = new XmlParser().parseFromString(text, 'application/xhtml+xml')
    for (const element of Array.from(doc.getElementsByTagName('*'))) {
      Object.defineProperty(element, 'innerHTML', {
        get: () => Array.from(element.childNodes).map(node => new XMLSerializer().serializeToString(node)).join(''),
      })
      Object.assign(element, {
        querySelectorAll(selector: string) {
          assert.ok(selector === 'img' || selector === 'a[href]')
          const tag = selector === 'img' ? 'img' : 'a'
          return Array.from(element.getElementsByTagName(tag))
            .filter(node => tag !== 'a' || node.hasAttribute('href'))
        },
      })
    }
    Object.assign(doc, { body: doc.getElementsByTagName('body')[0] })
    return doc
  }
}
globalThis.DOMParser = TestParser as unknown as typeof DOMParser
after(() => {
  if (originalParser) globalThis.DOMParser = originalParser
  else Reflect.deleteProperty(globalThis, 'DOMParser')
})

const inlineImage = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=='

async function fixture(body: string) {
  const zip = new JSZip()
  zip.file('META-INF/container.xml', '<container><rootfiles><rootfile full-path="EPUB/book.opf"/></rootfiles></container>')
  zip.file('EPUB/book.opf', '<package><manifest><item id="chapter" href="text/chapter.xhtml"/></manifest><spine><itemref idref="chapter"/></spine></package>')
  zip.file('EPUB/text/chapter.xhtml', `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:xlink="http://www.w3.org/1999/xlink"><head><title>Images</title></head><body>${body}</body></html>`)
  zip.file('EPUB/images/picture.gif', Buffer.from(inlineImage.split(',')[1], 'base64'))
  return zip.generateAsync({ type: 'arraybuffer' })
}

test('EPUB keeps inline HTML and SVG image sources without creating blob URLs', async () => {
  const result = await renderEpubPreview(await fixture(
    `<img src="${inlineImage}"/><svg xmlns="http://www.w3.org/2000/svg"><image href="${inlineImage}"/><image xlink:href="${inlineImage}"/></svg>`,
  ))
  assert.equal(result.html.split(inlineImage).length - 1, 3)
  assert.deepEqual(result.objectUrls, [])
  assert.match(result.html, /data-epub-section="1"/)
})

test('EPUB keeps relative archive images working alongside inline images and reuses blobs', async () => {
  const result = await renderEpubPreview(await fixture(
    `<img src="${inlineImage}"/><img src="../images/picture.gif"/><img src="../images/picture.gif"/>`,
  ))
  try {
    assert.ok(result.html.includes(inlineImage))
    assert.equal(result.objectUrls.length, 1)
    assert.equal(result.html.split(result.objectUrls[0]!).length - 1, 2)
    const blob = await (await fetch(result.objectUrls[0]!)).blob()
    assert.equal(blob.type, 'image/gif')
    assert.deepEqual(Buffer.from(await blob.arrayBuffer()), Buffer.from(inlineImage.split(',')[1], 'base64'))
  } finally {
    result.objectUrls.forEach(url => URL.revokeObjectURL(url))
  }
})

test('non-image data and executable image sources are not preserved', async () => {
  const result = await renderEpubPreview(await fixture(
    '<img src="data:text/html;base64,PHNjcmlwdD4="/><img src="javascript:alert(1)"/><img src="missing.gif"/>',
  ))
  assert.doesNotMatch(result.html, /data:text\/html|javascript:/)
  assert.deepEqual(result.objectUrls, [])
})
