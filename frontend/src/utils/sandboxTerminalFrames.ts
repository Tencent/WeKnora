export type TerminalStdinFrame = {
  type: 'stdin'
  encoding: 'base64'
  data: string
}

const encoder = new TextEncoder()

function base64(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}

export function terminalBinaryBytes(data: string): Uint8Array {
  const bytes = new Uint8Array(data.length)
  for (let index = 0; index < data.length; index++) {
    bytes[index] = data.charCodeAt(index) & 0xff
  }
  return bytes
}

export function terminalTextBytes(data: string): Uint8Array {
  return encoder.encode(data)
}

export function terminalStdinFrames(bytes: Uint8Array, maxFrameBytes: number): TerminalStdinFrame[] {
  const limit = Number.isFinite(maxFrameBytes) && maxFrameBytes >= 256
    ? Math.floor(maxFrameBytes)
    : 16 * 1024
  // Base64 expands by 4/3. Leave room for the JSON envelope and escaping.
  let chunkSize = Math.max(1, Math.floor((limit - 128) * 3 / 4))
  const frames: TerminalStdinFrame[] = []
  for (let offset = 0; offset < bytes.length;) {
    let end = Math.min(bytes.length, offset + chunkSize)
    let frame: TerminalStdinFrame = {
      type: 'stdin', encoding: 'base64', data: base64(bytes.subarray(offset, end)),
    }
    while (encoder.encode(JSON.stringify(frame)).byteLength > limit && end > offset + 1) {
      chunkSize = Math.max(1, Math.floor((end - offset) * 0.9))
      end = offset + chunkSize
      frame = { type: 'stdin', encoding: 'base64', data: base64(bytes.subarray(offset, end)) }
    }
    if (encoder.encode(JSON.stringify(frame)).byteLength > limit) return []
    frames.push(frame)
    offset = end
  }
  return frames
}
