// @weknora/plugin-ui: the bridge between a WeKnora plugin page and the app.
//
// A plugin page runs in a sandboxed iframe with an opaque origin and a CSP
// that forbids network access. Everything it needs goes through this bridge
// as postMessage calls the app answers: requests to the plugin's backend,
// toasts, confirmations, navigation, sizing. No dependencies; import it as
// an ES module from the page (ship a copy under ui/).
//
//   import { connect } from './weknora-plugin-ui.js'
//   const wk = await connect()
//   const { body } = await wk.get('/links')

/** Protocol version; also marks bridge messages. */
export const PROTOCOL = 1

const DEFAULT_TIMEOUT = 60_000

function isBridgeMessage(data) {
  return data !== null && typeof data === 'object' && data.weknora === PROTOCOL && typeof data.kind === 'string'
}

/** A failed bridge call: a refused request, or a non-2xx answer with `throwOnError`. */
export class BridgeError extends Error {
  constructor(message, status) {
    super(message)
    this.name = 'BridgeError'
    this.status = status
  }
}

/**
 * Applies the app's theme to this document: `data-theme` on <html> and each
 * token as a CSS variable `--wk-<name>` (e.g. --wk-brand-color).
 */
export function applyTheme(theme, doc = globalThis.document) {
  if (!doc || !theme) return
  const root = doc.documentElement
  root.setAttribute('data-theme', theme.mode === 'dark' ? 'dark' : 'light')
  for (const [name, value] of Object.entries(theme.tokens || {})) {
    if (/^[a-z0-9-]+$/.test(name) && typeof value === 'string') root.style.setProperty(`--wk-${name}`, value)
  }
}

/**
 * Connects to the app. Resolves once the app sent the page its context
 * (`init`). Options:
 *   autoResize  keep the frame as tall as the page (default true)
 *   applyTheme  apply the app theme as CSS variables (default true)
 *   timeout     ms to wait for each answer (default 60000)
 *   window      the page's window (tests)
 */
export function connect(options = {}) {
  const win = options.window || globalThis.window
  const parent = win.parent
  const timeout = options.timeout || DEFAULT_TIMEOUT
  const pending = new Map()
  const listeners = new Map()
  let nextId = 1
  let bridge

  const emit = (name, data) => {
    for (const fn of listeners.get(name) || []) {
      try {
        fn(data)
      } catch (e) {
        console.error(e)
      }
    }
  }

  const call = (method, params) =>
    new Promise((resolve, reject) => {
      const id = nextId++
      const timer = setTimeout(() => {
        pending.delete(id)
        reject(new BridgeError(`${method} timed out`))
      }, timeout)
      pending.set(id, { resolve, reject, timer })
      parent.postMessage({ weknora: PROTOCOL, kind: 'request', id, method, params }, '*')
    })

  return new Promise((resolveConnect) => {
    win.addEventListener('message', (event) => {
      if (event.source !== parent || !isBridgeMessage(event.data)) return
      const msg = event.data
      if (msg.kind === 'response') {
        const p = pending.get(msg.id)
        if (!p) return
        pending.delete(msg.id)
        clearTimeout(p.timer)
        if (msg.ok) p.resolve(msg.result)
        else p.reject(new BridgeError(msg.error?.message || 'request failed', msg.error?.status))
        return
      }
      if (msg.kind !== 'event') return
      if (msg.name === 'init') {
        const first = !bridge
        bridge = bridge || makeBridge(msg.data)
        bridge.context = msg.data
        if (options.applyTheme !== false) applyTheme(msg.data.theme, win.document)
        if (first) {
          if (options.autoResize !== false) watchHeight(win, bridge)
          resolveConnect(bridge)
        } else {
          emit('init', msg.data)
        }
        return
      }
      if (msg.name === 'theme' && bridge) {
        bridge.context = { ...bridge.context, theme: msg.data }
        if (options.applyTheme !== false) applyTheme(msg.data, win.document)
      }
      if (msg.name === 'locale' && bridge) bridge.context = { ...bridge.context, locale: msg.data }
      emit(msg.name, msg.data)
    })
    parent.postMessage({ weknora: PROTOCOL, kind: 'hello' }, '*')
  })

  function makeBridge(context) {
    const request = async (method, path, body, opts = {}) => {
      const res = await call('api.request', { method, path, body })
      if (opts.throwOnError !== false && (res.status < 200 || res.status >= 300)) {
        const err = res.body && typeof res.body === 'object' ? res.body.error : undefined
        const message = typeof err === 'string' ? err : err?.message || `HTTP ${res.status}`
        throw new BridgeError(message, res.status)
      }
      return res
    }
    return {
      /** pluginId, version, mount, locale, theme and the mount's context (e.g. knowledgeBaseId). */
      context,
      /** Calls the plugin's backend (its UI handler). Resolves {status, body}. */
      request,
      get: (path, opts) => request('GET', path, undefined, opts),
      post: (path, body, opts) => request('POST', path, body, opts),
      put: (path, body, opts) => request('PUT', path, body, opts),
      delete: (path, opts) => request('DELETE', path, undefined, opts),
      /** Shows a toast in the app: info, success, warning or error. */
      toast: (message, theme = 'info') => call('ui.toast', { message, theme }),
      /** Asks the user to confirm; resolves true or false. */
      confirm: (message, title) => call('ui.confirm', { message, title }),
      /** Opens a page of the app, by path ("/platform/knowledge-bases"). */
      navigate: (path) => call('ui.navigate', { path }),
      /** Sets the frame height in pixels (autoResize does this for you). */
      resize: (height) => call('ui.resize', { height }),
      /** Closes the page, where the mount allows it. */
      close: () => call('ui.close', {}),
      /**
       * Listens to app events: theme, locale, and init (the mount's context
       * changed, e.g. another knowledge base). Returns an unsubscribe function.
       */
      on(name, fn) {
        if (!listeners.has(name)) listeners.set(name, new Set())
        listeners.get(name).add(fn)
        return () => listeners.get(name).delete(fn)
      },
    }
  }
}

function watchHeight(win, bridge) {
  const doc = win.document
  if (!doc || typeof win.ResizeObserver !== 'function') return
  let last = 0
  const report = () => {
    const height = Math.ceil(doc.documentElement.scrollHeight)
    if (height !== last) {
      last = height
      bridge.resize(height).catch(() => {})
    }
  }
  new win.ResizeObserver(report).observe(doc.documentElement)
  report()
}
