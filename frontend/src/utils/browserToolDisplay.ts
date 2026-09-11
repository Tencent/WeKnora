type Translate = (key: string, params?: Record<string, unknown>) => string
export type BrowserToolEvent = { arguments?: any; output?: unknown; error?: unknown; pending?: boolean; success?: boolean }
const methodKeys: Record<string, string> = {
  navigate:'openPage', navigate_back:'switchPage', navigate_forward:'switchPage', reload:'openPage',
  observe:'readPage', snapshot:'readPage', get_html:'readPage', tab_list:'listTabs',
  click:'clickPage', fill:'fillPage', press:'clickPage', wait_ms:'waitPage', wait_for_navigation:'waitPage',
  tab_create:'openTab', tab_select:'switchTab', tab_borrow:'authorizeTab', tab_return:'returnTab',
  request_help:'needHelp',
}
export function browserToolTitle(t: Translate, event: BrowserToolEvent): string {
  const args = event.arguments || {}
  const label = t(`localBrowser.${methodKeys[args.method] || 'browserAction'}`)
  let host = ''
  if (args.method === 'navigate' || args.method === 'tab_create') {
    try { host = new URL(args.params?.url).hostname } catch { /* No untrusted URL in titles. */ }
  }
  return `${label}${host ? ` · ${host}` : ''}${event.pending ? '…' : event.success === false ? ` · ${t('localBrowser.actionFailed')}` : ''}`
}
export function browserToolSummary(t: Translate, event: BrowserToolEvent): string {
  const raw = String(event.error || event.output || '')
  if (event.success !== false) return t('localBrowser.actionCompleted')
  if (/unfinished command|preview.*busy/i.test(raw)) return t('localBrowser.commandBusy')
  if (/invalid_params|duration_ms/i.test(raw)) return t('localBrowser.invalidArguments')
  if (/paused|interrupted|timed out|timeout/i.test(raw)) return t('localBrowser.commandInterrupted')
  if (/disconnected|offline|connect BrowserSkill|not paired/i.test(raw)) return t('localBrowser.reconnectHint')
  return t('localBrowser.actionFailedHint')
}
