import assert from 'node:assert/strict'
import { after, test } from 'node:test'
import JSZip from 'jszip'
import { DOMParser as XmlParser, XMLSerializer } from '@xmldom/xmldom'
import { renderEpubPreview, resolveEpubPath } from './epubPreview'

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

async function fixture(body: string, imagePaths: string[] = []) {
  const zip = new JSZip()
  zip.file('META-INF/container.xml', '<container><rootfiles><rootfile full-path="EPUB/book.opf"/></rootfiles></container>')
  zip.file('EPUB/book.opf', '<package><manifest><item id="chapter" href="text/chapter.xhtml"/></manifest><spine><itemref idref="chapter"/></spine></package>')
  zip.file('EPUB/text/chapter.xhtml', `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:xlink="http://www.w3.org/1999/xlink"><head><title>Images</title></head><body>${body}</body></html>`)
  zip.file('EPUB/images/picture.gif', Buffer.from(inlineImage.split(',')[1], 'base64'))
  for (const path of imagePaths) {
    zip.file(path, Buffer.from(inlineImage.split(',')[1], 'base64'))
  }
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

test('EPUB resolves encoded paths and tolerates malformed percent escapes', () => {
  for (const [href, expected] of [
    ['../images/my%20picture.gif#cover', 'EPUB/images/my picture.gif'],
    ['../images/100%.gif', 'EPUB/images/100%.gif'],
    ['../images/bad%GG.gif', 'EPUB/images/bad%GG.gif'],
    ['../images/%E0%A4.gif', 'EPUB/images/%E0%A4.gif'],
  ]) {
    assert.equal(resolveEpubPath('EPUB/text/chapter.xhtml', href!), expected)
  }
})

test('malformed missing image paths do not hide the chapter or its working images', async () => {
  const result = await renderEpubPreview(await fixture(
    '<p>Readable chapter</p><img src="../images/picture.gif"/><img src="missing%.gif"/>' +
    '<svg xmlns="http://www.w3.org/2000/svg"><image href="missing%GG.gif"/><image xlink:href="missing%E0%A4.gif"/></svg>',
  ))
  try {
    assert.match(result.html, /Readable chapter/)
    assert.match(result.html, /data-epub-section="1"/)
    assert.equal(result.objectUrls.length, 1)
    assert.ok(result.html.includes(result.objectUrls[0]!))
    assert.doesNotMatch(result.html, /missing%/)
    assert.match(result.html, /src=""/)
    assert.equal((result.html.match(/href=""/g) || []).length, 2)
  } finally {
    result.objectUrls.forEach(url => URL.revokeObjectURL(url))
  }
})

test('EPUB reads literal percent filenames and normally encoded filenames from the ZIP', async () => {
  const result = await renderEpubPreview(await fixture(
    '<img src="../images/100%.gif"/><img src="../images/my%20picture.gif"/>',
    ['EPUB/images/100%.gif', 'EPUB/images/my picture.gif'],
  ))
  try {
    assert.equal(result.objectUrls.length, 2)
    for (const url of result.objectUrls) {
      assert.ok(result.html.includes(url))
      assert.deepEqual(Buffer.from(await (await fetch(url)).arrayBuffer()), Buffer.from(inlineImage.split(',')[1], 'base64'))
    }
  } finally {
    result.objectUrls.forEach(url => URL.revokeObjectURL(url))
  }
})

test('EPUB revokes previously created image URLs if rendering later fails', async (t) => {
  const createObjectURL = URL.createObjectURL.bind(URL)
  const revokeObjectURL = URL.revokeObjectURL.bind(URL)
  const created: string[] = []
  const failure = new Error('image URL allocation failed')
  t.mock.method(URL, 'createObjectURL', (blob: Blob) => {
    if (created.length === 2) throw failure
    const url = createObjectURL(blob)
    created.push(url)
    return url
  })
  const revoke = t.mock.method(URL, 'revokeObjectURL', revokeObjectURL)
  try {
    await assert.rejects(renderEpubPreview(await fixture(
      '<img src="../images/picture.gif"/><img src="../images/second.gif"/><img src="../images/third.gif"/>',
      ['EPUB/images/second.gif', 'EPUB/images/third.gif'],
    )), error => error === failure)
    assert.equal(created.length, 2)
    assert.deepEqual(revoke.mock.calls.map(call => call.arguments[0]), created)
    for (const url of created) await assert.rejects(fetch(url))
  } finally {
    created.forEach(revokeObjectURL)
  }
})
