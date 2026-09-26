// The app's side of the plugin page bridge (packages/plugin-ui is the
// page's side). A plugin page runs in a sandboxed iframe with an opaque
// origin; it can only post messages to the app, and the app answers only
// messages that come from that iframe, at a bounded rate.

/** Protocol version; also marks bridge messages. */
export const BRIDGE_PROTOCOL = 1

export interface BridgeTheme {
  mode: 'light' | 'dark'
  tokens: Record<string, string>
}

/** What the page receives in `init`. */
export interface BridgeInit {
  pluginId: string
  version: string
  mount: string
  locale: string
  /** The user's workspace role, for what the page offers; the server decides. */
  role: string
  theme: BridgeTheme
  context: Record<string, unknown>
}

export interface ApiResult {
  status: number
  body: unknown
}

/** What the app does for each call a page makes. */
export interface BridgeHandlers {
  apiRequest(params: { method: string; path: string; body?: unknown }): Promise<ApiResult>
  toast(message: string, theme: ToastTheme): void
  confirm(message: string, title?: string): Promise<boolean>
  navigate(path: string): void
  resize(height: number): void
  close(): void
}

export type ToastTheme = 'info' | 'success' | 'warning' | 'error'

export interface BridgeHostOptions {
  /** The iframe's window; messages from any other source are ignored. */
  frame: () => Window | null | undefined
  init: () => BridgeInit
  handlers: BridgeHandlers
  /** Calls allowed in a burst and per second after it. */
  burst?: number
  perSecond?: number
  now?: () => number
}

interface RequestMessage {
  weknora: number
  kind: 'request'
  id: number
  method: string
  params?: unknown
}

export class BridgeCallError extends Error {
  constructor(
    message: string,
    readonly status?: number,
  ) {
    super(message)
  }
}

const MAX_TOAST = 300
const MAX_HEIGHT = 20_000
const TOAST_THEMES = new Set<ToastTheme>(['info', 'success', 'warning', 'error'])

function isObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}

/** A path inside the app: "/platform/...", never another origin. */
export function isAppPath(path: unknown): path is string {
  return typeof path === 'string' && path.startsWith('/') && !path.startsWith('//') && !path.includes('\\')
}

/** A token bucket: `burst` calls at once, refilled at `perSecond`. */
export function createRateLimiter(burst: number, perSecond: number, now: () => number = Date.now) {
  let tokens = burst
  let last = now()
  return () => {
    const t = now()
    tokens = Math.min(burst, tokens + ((t - last) / 1000) * perSecond)
    last = t
    if (tokens < 1) return false
    tokens -= 1
    return true
  }
}

export interface BridgeHost {
  /** Feed every window `message` event here. */
  handle(event: MessageEvent): void
  /** Sends an event to the page (init, theme, locale). */
  send(name: string, data: unknown): void
}

export function createBridgeHost(opts: BridgeHostOptions): BridgeHost {
  const allow = createRateLimiter(opts.burst ?? 30, opts.perSecond ?? 10, opts.now)
  const post = (msg: Record<string, unknown>) => {
    // The page's origin is opaque ("null"), so it cannot be named as the
    // target; only that window receives the message.
    opts.frame()?.postMessage({ weknora: BRIDGE_PROTOCOL, ...msg }, '*')
  }
  const send = (name: string, data: unknown) => post({ kind: 'event', name, data })
  const reply = (id: number, result: unknown) => post({ kind: 'response', id, ok: true, result })
  const fail = (id: number, err: unknown) => {
    const message = err instanceof Error ? err.message : String(err)
    const status = err instanceof BridgeCallError ? err.status : undefined
    post({ kind: 'response', id, ok: false, error: { message, status } })
  }

  const dispatch = async (method: string, params: Record<string, unknown>): Promise<unknown> => {
    const h = opts.handlers
    switch (method) {
      case 'api.request': {
        const verb = typeof params.method === 'string' ? params.method.toUpperCase() : 'GET'
        if (!/^[A-Z]{3,7}$/.test(verb)) throw new BridgeCallError('bad method', 400)
        if (typeof params.path !== 'string' || !params.path.startsWith('/')) {
          throw new BridgeCallError('path must start with /', 400)
        }
        return h.apiRequest({ method: verb, path: params.path, body: params.body })
      }
      case 'ui.toast': {
        if (typeof params.message !== 'string' || !params.message) throw new BridgeCallError('message is required', 400)
        const theme = TOAST_THEMES.has(params.theme as ToastTheme) ? (params.theme as ToastTheme) : 'info'
        h.toast(params.message.slice(0, MAX_TOAST), theme)
        return null
      }
      case 'ui.confirm': {
        if (typeof params.message !== 'string' || !params.message) throw new BridgeCallError('message is required', 400)
        const title = typeof params.title === 'string' ? params.title.slice(0, 100) : undefined
        return h.confirm(params.message.slice(0, MAX_TOAST), title)
      }
      case 'ui.navigate':
        if (!isAppPath(params.path)) throw new BridgeCallError('path must be a path of the app', 400)
        h.navigate(params.path)
        return null
      case 'ui.resize': {
        const height = Number(params.height)
        if (!Number.isFinite(height) || height < 0) throw new BridgeCallError('height must be a number', 400)
        h.resize(Math.min(Math.ceil(height), MAX_HEIGHT))
        return null
      }
      case 'ui.close':
        h.close()
        return null
      default:
        throw new BridgeCallError(`unknown method ${method}`, 404)
    }
  }

  return {
    send,
    handle(event: MessageEvent) {
      const frame = opts.frame()
      if (!frame || event.source !== frame) return
      const data = event.data as unknown
      if (!isObject(data) || data.weknora !== BRIDGE_PROTOCOL) return
      if (data.kind === 'hello') {
        send('init', opts.init())
        return
      }
      if (data.kind !== 'request') return
      const msg = data as unknown as RequestMessage
      if (typeof msg.id !== 'number' || typeof msg.method !== 'string') return
      if (!allow()) {
        fail(msg.id, new BridgeCallError('too many requests', 429))
        return
      }
      dispatch(msg.method, isObject(msg.params) ? msg.params : {}).then(
        (result) => reply(msg.id, result ?? null),
        (err) => fail(msg.id, err),
      )
    },
  }
}

/** The app's design tokens a page may use, read from the root element. */
export const THEME_TOKENS = [
  'brand-color',
  'brand-color-light',
  'text-color-primary',
  'text-color-secondary',
  'text-color-placeholder',
  'bg-color-page',
  'bg-color-container',
  'bg-color-secondarycontainer',
  'component-border',
  'border-level-1-color',
  'error-color',
  'warning-color',
  'success-color',
  'radius-default',
  'font-family',
] as const

export function readTheme(doc: Document = document): BridgeTheme {
  const root = doc.documentElement
  const style = getComputedStyle(root)
  const tokens: Record<string, string> = {}
  for (const name of THEME_TOKENS) {
    const value = style.getPropertyValue(`--td-${name}`).trim()
    if (value) tokens[name] = value
  }
  return { mode: root.getAttribute('theme-mode') === 'dark' ? 'dark' : 'light', tokens }
}
