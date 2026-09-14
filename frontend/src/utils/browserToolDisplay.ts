type Translate = (key: string, params?: Record<string, unknown>) => string
export type BrowserToolEvent = { arguments?: unknown; output?: unknown; error?: unknown; pending?: boolean; success?: boolean; tool_data?: unknown }
const methodKeys: Record<string, string> = {
  navigate: 'openPage', navigate_back: 'switchPage', navigate_forward: 'switchPage', reload: 'openPage',
  screenshot: 'captureScreenshot', stop: 'stop',
  observe: 'readPage', snapshot: 'readPage', get_html: 'readPage', tab_list: 'listTabs',
  click: 'clickPage', fill: 'fillPage', press: 'pressKey', wait_ms: 'waitPage', wait_for_navigation: 'waitPage',
  hover: 'hoverPage', wheel: 'scrollPage', scroll_to: 'scrollPage', focus: 'focusElement', blur: 'blurElement',
  select: 'selectOption', tab_close: 'closeTab', evaluate: 'runScript', console: 'readConsole', network: 'readNetwork',
  window_resize: 'resizeWindow', emulate: 'emulateDevice',
  tab_create: 'openTab', tab_select: 'switchTab', tab_borrow: 'authorizeTab', tab_return: 'returnTab',
  request_help: 'needHelp',
}
function record(value: unknown): Record<string, any> {
  if (typeof value === 'string') {
    try { value = JSON.parse(value) } catch { return {} }
  }
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, any> : {}
}
function text(value: unknown): string { return typeof value === 'string' ? value : '' }
// Show the destination without credentials or query/fragment tokens. Never make
// page-provided URLs clickable or fetch them while rendering a tool result.
export function browserPageAddress(value: unknown): string {
  try {
    const url = new URL(text(value))
    return ['https:', 'http:'].includes(url.protocol) ? `${url.origin}${url.pathname}` : ''
  } catch { return '' }
}
function sourceLocation(entry: Record<string, any>): string {
  const address = browserPageAddress(entry.url)
  return address ? address + (Number.isInteger(entry.line) ? ':' + entry.line : '') + (Number.isInteger(entry.column) ? ':' + entry.column : '') : ''
}
const navigationMethods = new Set(['navigate', 'navigate_back', 'navigate_forward', 'reload', 'wait_for_navigation'])
export function browserToolIncomplete(event: BrowserToolEvent): boolean {
  return event.success === false || navigationMethods.has(record(event.arguments).method) && record(event.output).reached === 'timeout'
}
export function browserActionLabel(t: Translate, method: string): string {
  return t(`localBrowser.${methodKeys[method] || 'browserAction'}`)
}
export function browserToolTitle(t: Translate, event: BrowserToolEvent): string {
  const args = record(event.arguments)
  const label = browserActionLabel(t, args.method)
  let target = ''
  if (args.method === 'navigate' || args.method === 'tab_create') {
    try { target = new URL(browserPageAddress(args.url)).host } catch { /* No valid destination yet. */ }
  } else if (args.method === 'press') {
    target = text(args.key).slice(0, 40)
  }
  return `${t('localBrowser.local')} · ${label}${target ? ` · ${target}` : ''}${event.pending ? '…' : browserToolIncomplete(event) ? ` · ${t('localBrowser.actionFailed')}` : ''}`
}
export function browserToolSummary(t: Translate, event: BrowserToolEvent): string {
  const failure = event.error || event.output
  const raw = typeof failure === 'string' ? failure : text(record(failure).message)
  if (event.pending) return t('localBrowser.actionPending')
  if (navigationMethods.has(record(event.arguments).method) && record(event.output).reached === 'timeout') return t('localBrowser.navigationIncomplete')
  if (event.success !== false) return t(event.success === true ? 'localBrowser.actionCompleted' : 'localBrowser.actionRecorded')
  if (/unfinished command|preview.*busy/i.test(raw)) return t('localBrowser.commandBusy')
  if (/Parameter validation failed|Invalid browser arguments|invalid_params|duration_ms/i.test(raw)) return t('localBrowser.invalidArguments')
  if (/paused|interrupted|timed out|timeout/i.test(raw)) return t('localBrowser.commandInterrupted')
  if (/disconnected|offline|connect BrowserSkill|not paired/i.test(raw)) return t('localBrowser.reconnectHint')
  return t('localBrowser.actionFailedHint')
}
export function browserToolContent(event: BrowserToolEvent) {
  const output = { ...record(event.tool_data), ...record(event.output) }
  const args = record(event.arguments)
  let content = text(output.text) || text(output.html)
  const entries = Array.isArray(output.entries) ? output.entries : []
  if (args.method === 'console') {
    content = entries.slice(0, 100).map(record).map(entry => {
      const location = sourceLocation(entry)
      const stack = Array.isArray(entry.stack_trace) ? entry.stack_trace.slice(0, 20).map(record).map(frame =>
        `  ${text(frame.function_name)} ${sourceLocation(frame)}`).join('\n') : ''
      return `[${text(entry.level) || text(entry.kind)}] ${text(entry.text)}${location ? '\n' + location : ''}${stack ? '\n' + stack : ''}`
    }).join('\n\n')
  } else if (args.method === 'network') {
    content = entries.slice(0, 100).map(record).map(entry =>
      [text(entry.method), typeof entry.status === 'number' ? String(entry.status) : '', browserPageAddress(entry.url), text(entry.status_text), text(entry.error_text)].filter(Boolean).join(' ')
    ).join('\n')
  } else if (args.method === 'evaluate' && Object.hasOwn(output, 'value')) {
    content = typeof output.value === 'string' && output.value !== '' ? output.value : JSON.stringify(output.value, null, 2) ?? ''
  }
  const resultError = record(output.error)
  const error = text(output.error_text) || text(resultError.message) || text(resultError.text) || text(event.error) || text(record(event.error).message)
  const diagnostics = args.method === 'console' || args.method === 'network'
  const image = text(output.image_base64)
  const format = text(output.format)
  return {
    error: error.slice(0, 4000),
    recoveryHint: text(output.recovery_hint).slice(0, 4000),
    empty: diagnostics && entries.length === 0 && !error && !event.pending && !browserToolIncomplete(event),
    title: text(output.title).slice(0, 300),
    address: browserPageAddress(output.final_url || output.url || args.url),
    text: content.slice(0, 12000),
    truncated: output.truncated === true || content.length > 12000 || diagnostics && (entries.length > 100 || entries.some(entry => record(entry).truncated === true || Array.isArray(record(entry).stack_trace) && record(entry).stack_trace.length > 20)),
    image: ['png', 'jpeg'].includes(format) && image.length <= 8 * 1024 * 1024 && /^[A-Za-z0-9+/\r\n]+={0,2}$/.test(image)
      ? `data:image/${format};base64,${image}` : '',
    tabs: Array.isArray(output.tabs) ? output.tabs.map(record).map(tab => ({
      title: text(tab.title).slice(0, 300), address: browserPageAddress(tab.url),
    })) : [],
    prompt: args.method === 'request_help' ? text(args.prompt) : '',
  }
}
