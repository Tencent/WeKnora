export const WORKBENCH_IMAGE_LIMITS = {
  dimension: 4096,
  pixels: 12_000_000,
} as const

function u16le(bytes: Uint8Array, offset: number): number {
  return bytes[offset] | (bytes[offset + 1] << 8)
}

function u24le(bytes: Uint8Array, offset: number): number {
  return bytes[offset] | (bytes[offset + 1] << 8) | (bytes[offset + 2] << 16)
}

function u32be(bytes: Uint8Array, offset: number): number {
  return ((bytes[offset] << 24) | (bytes[offset + 1] << 16) |
    (bytes[offset + 2] << 8) | bytes[offset + 3]) >>> 0
}

export function workbenchImageDimensions(bytes: Uint8Array): { width: number; height: number } | null {
  if (bytes.length >= 24 && bytes[0] === 0x89 && bytes[1] === 0x50 && bytes[2] === 0x4e && bytes[3] === 0x47) {
    return { width: u32be(bytes, 16), height: u32be(bytes, 20) }
  }
  if (bytes.length >= 10 && String.fromCharCode(...bytes.subarray(0, 3)) === 'GIF') {
    return { width: u16le(bytes, 6), height: u16le(bytes, 8) }
  }
  if (bytes.length >= 30 && String.fromCharCode(...bytes.subarray(0, 4)) === 'RIFF' &&
    String.fromCharCode(...bytes.subarray(8, 12)) === 'WEBP') {
    const type = String.fromCharCode(...bytes.subarray(12, 16))
    if (type === 'VP8X') return { width: u24le(bytes, 24) + 1, height: u24le(bytes, 27) + 1 }
    if (type === 'VP8L' && bytes.length >= 25 && bytes[20] === 0x2f) {
      const packed = (bytes[21] | (bytes[22] << 8) | (bytes[23] << 16) | (bytes[24] << 24)) >>> 0
      return { width: (packed & 0x3fff) + 1, height: ((packed >>> 14) & 0x3fff) + 1 }
    }
    if (type === 'VP8 ' && bytes.length >= 30 && bytes[23] === 0x9d && bytes[24] === 0x01 && bytes[25] === 0x2a) {
      return { width: u16le(bytes, 26) & 0x3fff, height: u16le(bytes, 28) & 0x3fff }
    }
  }
  if (bytes.length >= 4 && bytes[0] === 0xff && bytes[1] === 0xd8) {
    let offset = 2
    while (offset + 4 <= bytes.length) {
      if (bytes[offset++] !== 0xff) continue
      while (offset < bytes.length && bytes[offset] === 0xff) offset++
      const marker = bytes[offset++]
      if (marker === 0xd8 || marker === 0xd9 || (marker >= 0xd0 && marker <= 0xd7)) continue
      if (offset + 2 > bytes.length) return null
      const length = (bytes[offset] << 8) | bytes[offset + 1]
      if (length < 2 || offset + length > bytes.length) return null
      if ((marker >= 0xc0 && marker <= 0xc3) || (marker >= 0xc5 && marker <= 0xc7) ||
        (marker >= 0xc9 && marker <= 0xcb) || (marker >= 0xcd && marker <= 0xcf)) {
        if (length < 7) return null
        return {
          height: (bytes[offset + 3] << 8) | bytes[offset + 4],
          width: (bytes[offset + 5] << 8) | bytes[offset + 6],
        }
      }
      offset += length
    }
  }
  return null
}

export async function validateWorkbenchImage(blob: Blob): Promise<{ width: number; height: number }> {
  const bytes = new Uint8Array(await blob.arrayBuffer())
  const dimensions = workbenchImageDimensions(bytes)
  if (!dimensions || dimensions.width <= 0 || dimensions.height <= 0 ||
    dimensions.width > WORKBENCH_IMAGE_LIMITS.dimension || dimensions.height > WORKBENCH_IMAGE_LIMITS.dimension ||
    dimensions.width * dimensions.height > WORKBENCH_IMAGE_LIMITS.pixels) {
    throw new Error('imageTooLarge')
  }
  return dimensions
}
