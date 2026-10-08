import assert from 'node:assert/strict'
import test from 'node:test'
import JSZip from 'jszip'
import { DOMParser, XMLSerializer } from '@xmldom/xmldom'
import { preparePptxPreview, isCompletePptxRender } from './pptxPreview'

Object.assign(globalThis, { DOMParser, XMLSerializer })
const typesNs = 'http://schemas.openxmlformats.org/package/2006/content-types'
const relsNs = 'http://schemas.openxmlformats.org/package/2006/relationships'
const masterType = 'application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml'
const missingPart = '/ppt/slideMasters/slideMaster2.xml'

async function fixture({ dangling = true, reference = '', external = false, prefix = '' } = {}) {
  const zip = new JSZip()
  zip.file('ppt/presentation.xml', '<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldIdLst><p:sldId id="256"/><p:sldId id="257"/></p:sldIdLst></p:presentation>')
  zip.file('ppt/slides/slide1.xml', '<slide>First slide</slide>')
  zip.file('ppt/slides/slide2.xml', '<slide>Second slide</slide>')
  zip.file('ppt/slideMasters/slideMaster1.xml', '<master/>')
  const tag = prefix ? `${prefix}:` : ''
  zip.file('[Content_Types].xml', `<${tag}Types xmlns${prefix ? ':' + prefix : ''}="${typesNs}"><${tag}Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="${masterType}"/>${dangling ? `<${tag}Override PartName="${missingPart}" ContentType="${masterType}"/>` : ''}</${tag}Types>`)
  zip.file('ppt/slideLayouts/_rels/slideLayout1.xml.rels', `<Relationships xmlns="${relsNs}"><Relationship Id="rId1" Target="${reference || '../slideMasters/slideMaster1.xml'}"${external ? ' TargetMode="External"' : ''}/></Relationships>`)
  return zip.generateAsync({ type: 'arraybuffer' })
}

test('removes unused missing masters from a preview copy and preserves every other part', async () => {
  const input = await fixture()
  const originalBytes = new Uint8Array(input).slice()
  const result = await preparePptxPreview(input)
  assert.equal(result.slideCount, 2)
  assert.deepEqual(new Uint8Array(input), originalBytes)
  const before = await JSZip.loadAsync(input), after = await JSZip.loadAsync(result.data)
  const xml = await after.file('[Content_Types].xml')!.async('string')
  assert.ok(!xml.includes(missingPart))
  assert.ok(xml.includes('/ppt/slideMasters/slideMaster1.xml'))
  assert.deepEqual(Object.keys(before.files), Object.keys(after.files))
  for (const part of Object.values(before.files)) {
    if (!part.dir && part.name !== '[Content_Types].xml') {
      assert.deepEqual(await part.async('uint8array'), await after.file(part.name)!.async('uint8array'))
    }
  }
})

test('does not repack a valid presentation', async () => {
  const input = await fixture({ dangling: false })
  assert.equal((await preparePptxPreview(input)).data, input)
})

test('never removes missing masters referenced by relative, absolute or encoded OPC paths', async () => {
  for (const reference of ['../slideMasters/slideMaster2.xml', '/ppt/slideMasters/slideMaster2.xml', '../slideMasters/slideMaster%32.xml']) {
    await assert.rejects(preparePptxPreview(await fixture({ reference })), /references a missing slide master/)
  }
})

test('supports namespace prefixes and ignores external relationships', async () => {
  const input = await fixture({ prefix: 'ct', reference: '../slideMasters/slideMaster2.xml', external: true })
  const result = await preparePptxPreview(input)
  const zip = await JSZip.loadAsync(result.data)
  assert.ok(!(await zip.file('[Content_Types].xml')!.async('string')).includes(missingPart))
})

test('zero and partial render success events must be treated as failures', () => {
  for (const result of [null, undefined, {}, { slides: [] }, { slides: [{}] }]) {
    assert.equal(isCompletePptxRender(result, 2), false)
  }
  assert.equal(isCompletePptxRender({ slides: [{}, {}] }, 2), true)
  assert.equal(isCompletePptxRender({ slides: [] }, 0), false)
})
