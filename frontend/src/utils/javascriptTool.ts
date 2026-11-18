export interface JavaScriptCallRecord {
  tool?: string
  ok?: boolean
  ms?: number
  chars?: number
  error?: string
}

export interface JavaScriptLiveProgress {
  nested_call_id?: string
  tool_name?: string
  phase?: 'start' | 'result' | string
  index?: number
  success?: boolean
  duration_ms?: number
  chars?: number
  error?: string
}

export interface JavaScriptPayload {
  result?: unknown
  logs?: string[]
  calls?: JavaScriptCallRecord[]
  error?: string
}

export interface JavaScriptEventView {
  code: string
  payload: JavaScriptPayload | null
  calls: JavaScriptCallRecord[]
  logs: string[]
  resultText: string
  error: string
  lineCount: number
  failedCalls: number
  durationMs: number
  rawOutput: string
}

function parseJSON(raw: string): any {
  try {
    return JSON.parse(raw)
  } catch {
    return null
  }
}

/**
 * Normalizes a run_javascript tool event for the timeline UI. The tool emits a
 * JSON envelope ({result, logs, calls, error}) as its output; historical
 * events carry the same output on `event.output` and live events may also
 * expose it through `event.tool_data.output`.
 */
export function parseJavaScriptEvent(event: any): JavaScriptEventView {
  const args = typeof event?.arguments === 'string'
    ? parseJSON(event.arguments)
    : event?.arguments
  const code = typeof args?.code === 'string' ? args.code : ''

  const rawOutput = typeof event?.output === 'string' && event.output
    ? event.output
    : typeof event?.tool_data?.output === 'string'
      ? event.tool_data.output
      : ''

  const parsed = rawOutput ? parseJSON(rawOutput) : null
  const payload: JavaScriptPayload | null = parsed && typeof parsed === 'object' ? parsed : null
  const calls = Array.isArray(payload?.calls) ? payload.calls : []
  const logs = Array.isArray(payload?.logs) ? payload.logs.map(String) : []

  let resultText = ''
  if (payload && payload.result !== undefined) {
    resultText = typeof payload.result === 'string'
      ? payload.result
      : JSON.stringify(payload.result, null, 2)
  }

  const durationMs = Number(event?.duration_ms ?? event?.duration ?? 0)
  return {
    code,
    payload,
    calls,
    logs,
    resultText,
    error: String(payload?.error || event?.error || ''),
    lineCount: code ? code.split('\n').length : 0,
    failedCalls: calls.filter(call => call?.ok === false).length,
    durationMs: Number.isFinite(durationMs) ? durationMs : 0,
    rawOutput,
  }
}
