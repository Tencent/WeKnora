import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'

const requireFromFrontend = createRequire(
  new URL('../../../../frontend/package.json', import.meta.url),
)
const { DOMParser, XMLSerializer } = requireFromFrontend('@xmldom/xmldom')
Object.assign(globalThis, { DOMParser, XMLSerializer })

async function main() {
  const input = process.argv[2]
  const expectedSlides = Number(process.argv[3] || '5')
  assert.ok(input, 'usage: verify_weknora_preview.ts <presentation.pptx> [slide-count]')
  assert.ok(Number.isInteger(expectedSlides) && expectedSlides > 0, 'slide-count must be positive')

  const { preparePptxPreview } = await import(
    '../../../../frontend/src/utils/pptxPreview.ts'
  )
  const bytes = await readFile(input)
  const source = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength)
  const prepared = await preparePptxPreview(source)

  assert.equal(prepared.slideCount, expectedSlides)
  assert.ok(prepared.data.byteLength > 10_000, 'preview input is unexpectedly small')
  console.log(JSON.stringify({ preview: 'accepted', slides: prepared.slideCount, bytes: prepared.data.byteLength }))
}

void main()
