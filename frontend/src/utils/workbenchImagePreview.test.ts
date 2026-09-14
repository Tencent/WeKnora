import assert from 'node:assert/strict'
import test from 'node:test'
import { validateWorkbenchImage, workbenchImageDimensions } from './workbenchImagePreview'

function png(width: number, height: number): Uint8Array {
  const bytes = new Uint8Array(24)
  bytes.set([0x89, 0x50, 0x4e, 0x47], 0)
  new DataView(bytes.buffer).setUint32(16, width)
  new DataView(bytes.buffer).setUint32(20, height)
  return bytes
}

test('restricted image dimensions are parsed before browser decoding', async () => {
  assert.deepEqual(workbenchImageDimensions(png(800, 600)), { width: 800, height: 600 })
  await assert.rejects(validateWorkbenchImage(new Blob([png(12000, 12000).buffer as ArrayBuffer])), /imageTooLarge/)
  await assert.rejects(validateWorkbenchImage(new Blob([new Uint8Array([1, 2, 3])])), /imageTooLarge/)
})
