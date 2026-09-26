import { inject, provide, type InjectionKey } from 'vue'

import type { OptionsSource } from './schema'

/** One choice a plugin offers for an x-options field. */
export interface FieldOption {
  value: string
  label?: string
  description?: string
}

/** What starting an OAuth connection returned. */
export interface OAuthStart {
  authorizeUrl: string
  state: string
  /** Where the provider sends the browser back; its origin posts the result. */
  redirectUri: string
}

/**
 * Where plugin-backed widgets (x-options, x-oauth) get their data. The page
 * hosting a plugin form provides one; without it those fields fall back to
 * plain inputs.
 */
export interface SchemaFormSource {
  /** Current value of a field another depends on (x-options dependsOn). */
  dependency(path: string): unknown
  options(field: string, source: OptionsSource, query: string): Promise<FieldOption[]>
  startOAuth(field: string): Promise<OAuthStart>
  /**
   * Polls how an authorization ended, for when the callback page cannot
   * post it back (the desktop app authorizes in the system browser).
   */
  oauthResult?(state: string): Promise<OAuthPoll>
}

/** An authorization's outcome as polled; pending until it comes back. */
export interface OAuthPoll {
  status: 'pending' | 'done'
  ok?: boolean
  connection?: string
  error?: string
}

const key: InjectionKey<SchemaFormSource> = Symbol('schema-form-source')

export function provideSchemaFormSource(source: SchemaFormSource) {
  provide(key, source)
}

export function useSchemaFormSource(): SchemaFormSource | undefined {
  return inject(key, undefined)
}

/**
 * Opens a URL in the system browser when the app runs as the desktop app
 * (whose webview cannot host the provider's consent popup); undefined in a
 * browser.
 */
export function systemBrowser(
  w: { runtime?: { BrowserOpenURL?: (url: string) => void } } = globalThis as never,
): ((url: string) => void) | undefined {
  const open = w.runtime?.BrowserOpenURL
  return typeof open === 'function' ? (url: string) => open(url) : undefined
}

/** The OAuth result the callback page posts to the form that opened it. */
export interface OAuthMessage {
  type: 'weknora-plugin-oauth'
  state: string
  ok: boolean
  connection?: string
  error?: string
}

/**
 * Whether a message event is the result of this authorization: from the
 * popup, from the callback page's origin, for this state.
 */
export function isOAuthResult(
  ev: { data: unknown; origin: string; source: unknown },
  popup: unknown,
  start: OAuthStart,
): ev is { data: OAuthMessage; origin: string; source: unknown } {
  let origin = ''
  try {
    origin = new URL(start.redirectUri).origin
  } catch {
    return false
  }
  const d = ev.data as Partial<OAuthMessage> | null
  return (
    !!popup &&
    ev.source === popup &&
    ev.origin === origin &&
    !!d &&
    d.type === 'weknora-plugin-oauth' &&
    d.state === start.state
  )
}

/**
 * Sorts a failed options request: a plugin answering invalid_config says the
 * form lacks what the list needs (no token yet), which the form shows as a
 * hint; anything else is an error worth retrying.
 */
export function optionsFailure(e: unknown): { hint?: string; error?: string } {
  const err = e as { message?: string; error?: { code?: string; message?: string } } | null
  const message = err?.error?.message || err?.message || ''
  if (err?.error?.code === 'invalid_config') return { hint: message }
  return { error: message }
}

/** The values dependsOn names, keyed by path, for reload decisions. */
export function dependencyKey(source: SchemaFormSource | undefined, spec: OptionsSource | undefined): string {
  if (!source || !spec?.dependsOn?.length) return ''
  return JSON.stringify(spec.dependsOn.map(p => source.dependency(p) ?? null))
}

/**
 * Looks a dotted path up in an object ("auth.site"); for forms that keep
 * their values in one object.
 */
export function valueAt(values: Record<string, unknown> | undefined, path: string): unknown {
  let v: unknown = values
  for (const k of path.split('.')) {
    if (typeof v !== 'object' || v === null || Array.isArray(v)) return undefined
    v = (v as Record<string, unknown>)[k]
  }
  return v
}
