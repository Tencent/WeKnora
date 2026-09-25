export interface ApiPlaygroundSSEProgress {
  raw: string
  answer: string
}

export type ApiPlaygroundSSEResult = ApiPlaygroundSSEProgress & (
  | { status: 'success' }
  | { status: 'failed'; reason: 'terminal-error' | 'unexpected-eof'; error?: string }
)

interface TerminalState {
  status: 'success' | 'failed'
  error?: string
}

interface StreamEventPayload {
  response_type?: unknown
  type?: unknown
  content?: unknown
  message?: unknown
  error?: unknown
  done?: unknown
}

function readErrorMessage(payload: StreamEventPayload): string | undefined {
  if (typeof payload.content === 'string' && payload.content.trim()) return payload.content
  if (typeof payload.message === 'string' && payload.message.trim()) return payload.message
  if (typeof payload.error === 'string' && payload.error.trim()) return payload.error
  if (
    payload.error
    && typeof payload.error === 'object'
    && 'message' in payload.error
    && typeof payload.error.message === 'string'
    && payload.error.message.trim()
  ) {
    return payload.error.message
  }
  return undefined
}

function readEventData(frame: string): string | undefined {
  const dataLines: string[] = []
  for (const line of frame.split(/\r?\n/)) {
    if (line === 'data') {
      dataLines.push('')
    } else if (line.startsWith('data:')) {
      const value = line.slice(5)
      dataLines.push(value.startsWith(' ') ? value.slice(1) : value)
    }
  }
  return dataLines.length ? dataLines.join('\n') : undefined
}

function applyFrame(frame: string, answerChunks: string[]): TerminalState | undefined {
  const data = readEventData(frame)
  if (data === undefined) return undefined
  if (data.trim() === '[DONE]') return { status: 'success' }

  let payload: StreamEventPayload
  try {
    const parsed: unknown = JSON.parse(data)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return undefined
    payload = parsed as StreamEventPayload
  } catch {
    return undefined
  }

  const responseType = payload.response_type ?? payload.type
  if (responseType === 'answer' && typeof payload.content === 'string') {
    answerChunks.push(payload.content)
  }
  if (responseType === 'error') {
    return { status: 'failed', error: readErrorMessage(payload) }
  }
  if (responseType === 'complete' || (responseType === 'answer' && payload.done === true)) {
    return { status: 'success' }
  }
  return undefined
}

/**
 * Consume an API Playground SSE stream until its business-level terminal event.
 * The parser owns chunk buffering and terminal-state reduction so callers never
 * need to infer success from the transport reaching EOF.
 */
export async function consumeApiPlaygroundSSE(
  stream: ReadableStream<Uint8Array>,
  onProgress?: (progress: ApiPlaygroundSSEProgress) => void,
): Promise<ApiPlaygroundSSEResult> {
  const reader = stream.getReader()
  const decoder = new TextDecoder()
  const answerChunks: string[] = []
  let raw = ''
  let buffer = ''

  const progress = () => {
    const value = { raw, answer: answerChunks.join('') }
    onProgress?.(value)
    return value
  }

  const consumeFrames = (flush = false): TerminalState | undefined => {
    while (true) {
      const boundary = buffer.match(/\r?\n\r?\n/)
      if (!boundary || boundary.index === undefined) break
      const frame = buffer.slice(0, boundary.index)
      buffer = buffer.slice(boundary.index + boundary[0].length)
      const terminal = applyFrame(frame, answerChunks)
      if (terminal) return terminal
    }
    if (flush && buffer) {
      const terminal = applyFrame(buffer, answerChunks)
      buffer = ''
      return terminal
    }
    return undefined
  }

  const finish = (terminal: TerminalState): ApiPlaygroundSSEResult => {
    const value = progress()
    if (terminal.status === 'failed') {
      return { ...value, status: 'failed', reason: 'terminal-error', error: terminal.error }
    }
    return { ...value, status: 'success' }
  }

  while (true) {
    const { done, value } = await reader.read()
    if (done) {
      const tail = decoder.decode()
      raw += tail
      buffer += tail
      const terminal = consumeFrames(true)
      if (terminal) return finish(terminal)
      const value = progress()
      return { ...value, status: 'failed', reason: 'unexpected-eof' }
    }

    const chunk = decoder.decode(value, { stream: true })
    raw += chunk
    buffer += chunk
    const terminal = consumeFrames()
    if (terminal) {
      void reader.cancel().catch(() => undefined)
      return finish(terminal)
    }
    progress()
  }
}
