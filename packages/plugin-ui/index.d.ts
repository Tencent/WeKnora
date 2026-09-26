/** Protocol version of the bridge. */
export const PROTOCOL: 1

export interface Theme {
  mode: 'light' | 'dark'
  /** Design tokens of the app, applied as --wk-<name> CSS variables. */
  tokens: Record<string, string>
}

export interface PageContext {
  pluginId: string
  version: string
  /** "<point>/<id>", e.g. "pages/links". */
  mount: string
  locale: string
  /** The user's workspace role: viewer, contributor, admin or owner. For what the page offers; the plugin's backend gets the role WeKnora checked. */
  role: string
  theme: Theme
  /** What the mount passes: knowledgeBaseId for kbTabs. */
  context: Record<string, unknown>
}

export interface Response<T = unknown> {
  status: number
  body: T
}

export interface RequestOptions {
  /** Reject non-2xx answers (default true). */
  throwOnError?: boolean
}

export interface Bridge {
  context: PageContext
  request<T = unknown>(method: string, path: string, body?: unknown, opts?: RequestOptions): Promise<Response<T>>
  get<T = unknown>(path: string, opts?: RequestOptions): Promise<Response<T>>
  post<T = unknown>(path: string, body?: unknown, opts?: RequestOptions): Promise<Response<T>>
  put<T = unknown>(path: string, body?: unknown, opts?: RequestOptions): Promise<Response<T>>
  delete<T = unknown>(path: string, opts?: RequestOptions): Promise<Response<T>>
  toast(message: string, theme?: 'info' | 'success' | 'warning' | 'error'): Promise<void>
  confirm(message: string, title?: string): Promise<boolean>
  navigate(path: string): Promise<void>
  resize(height: number): Promise<void>
  close(): Promise<void>
  /** The form an instance editor page sits in (connectors, webSearch editors). */
  form: {
    /** Merges values into the form, in the shape the plugin's schema has. */
    set(values: Record<string, unknown>): Promise<void>
  }
  on(name: 'theme', fn: (theme: Theme) => void): () => void
  on(name: 'locale', fn: (locale: string) => void): () => void
  /** An editor's form changed; values are in the plugin's schema shape. */
  on(name: 'values', fn: (values: Record<string, unknown>) => void): () => void
  /** The mount's context changed (another knowledge base). */
  on(name: 'init', fn: (context: PageContext) => void): () => void
}

export interface ConnectOptions {
  /**
   * Keep the frame as tall as the page's content (default true): the bottom
   * of <body> plus its margin, so the frame shrinks as well as grows. A body
   * stretched to the viewport (height: 100vh) keeps the frame from shrinking.
   */
  autoResize?: boolean
  applyTheme?: boolean
  timeout?: number
  window?: Window
}

export class BridgeError extends Error {
  status?: number
}

export function connect(options?: ConnectOptions): Promise<Bridge>
export function applyTheme(theme: Theme, doc?: Document): void
/** The page content's height in pixels, as autoResize reports it. */
export function contentHeight(win?: Window): number
