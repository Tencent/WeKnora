type Translate = (key: string, params?: Record<string, unknown>) => string
export type BrowserToolEvent = { arguments?: unknown; output?: unknown; error?: unknown; pending?: boolean; success?: boolean }
const methodKeys: Record<string, string> = {
  navigate: 'openPage', navigate_back: 'switchPage', navigate_forward: 'switchPage', reload: 'openPage',
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
function pageAddress(value: unknown): string {
  try {
    const url = new URL(text(value))
    return ['https:', 'http:'].includes(url.protocol) ? `${url.origin}${url.pathname}` : ''
  } catch { return '' }
}
export function browserToolTitle(t: Translate, event: BrowserToolEvent): string {
  const args = record(event.arguments)
  const label = t(`localBrowser.${methodKeys[args.method] || 'browserAction'}`)
  let target = ''
  if (args.method === 'navigate' || args.method === 'tab_create') {
    try { target = new URL(pageAddress(args.url)).host } catch { /* No valid destination yet. */ }
  } else if (args.method === 'press') {
    target = text(args.key).slice(0, 40)
  }
  return `${t('localBrowser.local')} · ${label}${target ? ` · ${target}` : ''}${event.pending ? '…' : event.success === false ? ` · ${t('localBrowser.actionFailed')}` : ''}`
}
export function browserToolSummary(t: Translate, event: BrowserToolEvent): string {
  const failure = event.error || event.output
  const raw = typeof failure === 'string' ? failure : text(record(failure).message)
  if (event.pending) return t('localBrowser.actionPending')
  if (event.success !== false) return t(event.success === true ? 'localBrowser.actionCompleted' : 'localBrowser.actionRecorded')
  if (/unfinished command|preview.*busy/i.test(raw)) return t('localBrowser.commandBusy')
  if (/Parameter validation failed|Invalid browser arguments|invalid_params|duration_ms/i.test(raw)) return t('localBrowser.invalidArguments')
  if (/paused|interrupted|timed out|timeout/i.test(raw)) return t('localBrowser.commandInterrupted')
  if (/disconnected|offline|connect BrowserSkill|not paired/i.test(raw)) return t('localBrowser.reconnectHint')
  return t('localBrowser.actionFailedHint')
}
export function browserToolContent(event: BrowserToolEvent) {
  const output = record(event.output)
  const args = record(event.arguments)
  const content = text(output.text) || text(output.html)
  const image = text(output.image_base64)
  const format = text(output.format)
  return {
    title: text(output.title).slice(0, 300),
    address: pageAddress(output.final_url || output.url || args.url),
    text: content.slice(0, 12000),
    truncated: output.truncated === true || content.length > 12000,
    image: ['png', 'jpeg'].includes(format) && image.length <= 8 * 1024 * 1024 && /^[A-Za-z0-9+/\r\n]+={0,2}$/.test(image)
      ? `data:image/${format};base64,${image}` : '',
    tabs: Array.isArray(output.tabs) ? output.tabs.map(record).map(tab => ({
      title: text(tab.title).slice(0, 300), address: pageAddress(tab.url),
    })) : [],
    prompt: args.method === 'request_help' ? text(args.prompt) : '',
  }
}
