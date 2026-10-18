// Turns a raw data-source error into something a user can act on.
//
// Connectors report failures as English API envelopes, e.g. a DingTalk 403:
//
//   list DingTalk workspaces: invalid credentials: DingTalk API status=403
//   code=Forbidden.AccessDenied.IpNotInWhiteList message=访问ip不在白名单之中，
//   request ip=198.51.100.7 appKey(dingxxxx)
//
// Shown verbatim that is both unreadable and unsafe: it names the operator's
// egress IP and the app key. The helpers below keep the diagnostic (minus the
// secrets) for a collapsed detail section, and hand the UI an i18n key for the
// sentence the user actually needs. They are pure so both the picker and the
// connection test can share them, and so the mappings can be unit-tested.

export type DatasourceErrorKind =
  | 'ip-whitelist'
  | 'invalid-credentials'
  | 'forbidden'
  | 'unauthorized'
  | 'network'
  | 'unknown'

export interface ClassifiedDatasourceError {
  /** What the raw error was recognised as. */
  kind: DatasourceErrorKind
  /** i18n key for the localised, user-facing sentence. */
  messageKey: string
  /** Technical text with IPs, app keys and credentials removed. */
  detail: string
  /** Vendor documentation the message refers to, when there is one. */
  faqUrl?: string
}

/** DingTalk's own guidance for the whitelist error. */
export const DINGTALK_IP_WHITELIST_FAQ =
  'https://open.dingtalk.com/document/org-faq/app-faq'

const MESSAGE_KEYS: Record<DatasourceErrorKind, string> = {
  'ip-whitelist': 'datasource.loadError.ipNotInWhitelist',
  'invalid-credentials': 'datasource.loadError.invalidCredentials',
  forbidden: 'datasource.loadError.forbidden',
  unauthorized: 'datasource.loadError.unauthorized',
  network: 'datasource.loadError.network',
  unknown: 'datasource.loadError.unknown',
}

/**
 * Reads a message out of anything the HTTP layer rejects with. It rejects with
 * `{ status, message, ...body }` for a response and `{ message }` for a
 * transport failure, but tests (and older call sites) also throw `Error` or a
 * bare string.
 */
export function datasourceErrorText(error: unknown): string {
  if (error == null) return ''
  if (typeof error === 'string') return error.trim()
  if (error instanceof Error) return (error.message || String(error)).trim()

  const value = error as Record<string, any>
  const candidates = [
    value.message,
    value.error,
    value.detail,
    value.data?.error,
    value.data?.message,
    value.response?.data?.error,
    value.response?.data?.message,
  ]
  for (const candidate of candidates) {
    if (typeof candidate === 'string' && candidate.trim()) return candidate.trim()
    if (candidate && typeof candidate === 'object') {
      const nested = candidate.message || candidate.error
      if (typeof nested === 'string' && nested.trim()) return nested.trim()
    }
  }
  try {
    const serialised = JSON.stringify(error)
    return serialised === '{}' ? '' : serialised
  } catch {
    return String(error)
  }
}

// Credentials and identifiers that must never be rendered, even inside the
// collapsed detail. The value runs to whitespace, a comma/semicolon or the
// closing parenthesis of the `appKey(...)` form.
const SECRET_PATTERNS: Array<[RegExp, string]> = [
  [/(request\s+ip\s*[=:]\s*)[^\s,;)]+/gi, '$1[redacted]'],
  [/(\bip\s*[=:]\s*)(?:\d{1,3}\.){3}\d{1,3}\b/gi, '$1[redacted]'],
  [/(\bappKey\s*\()([^)]*)(\))/gi, '$1[redacted]$3'],
  [/(\bappKey\s*[=:]\s*)[^\s,;)]+/gi, '$1[redacted]'],
  [/(\bclient[_-]?secret\s*[=:]\s*)[^\s,;)]+/gi, '$1[redacted]'],
  [/(\baccess[_-]?token\s*[=:]\s*)[^\s,;)]+/gi, '$1[redacted]'],
  [/(\bauthorization\s*[=:]\s*)[^\s,;]+/gi, '$1[redacted]'],
]

/**
 * Removes the values that identify the operator or the application from an
 * error body while leaving the diagnostic (status, code, message) intact.
 */
export function redactDatasourceErrorText(raw: string): string {
  let text = (raw || '').trim()
  if (!text) return ''
  for (const [pattern, replacement] of SECRET_PATTERNS) {
    text = text.replace(pattern, replacement)
  }
  return text
}

function includesAny(haystack: string, needles: readonly string[]): boolean {
  return needles.some(needle => haystack.includes(needle))
}

/**
 * Maps a raw connector error to an actionable i18n key. The whitelist check
 * comes first on purpose: DingTalk's body also says `invalid credentials` and
 * `status=403`, and telling the user to rotate credentials would be wrong when
 * the real fix is adding the egress IP to the app.
 */
export function classifyDatasourceError(error: unknown): ClassifiedDatasourceError {
  const raw = datasourceErrorText(error)
  const lower = raw.toLowerCase()
  const detail = redactDatasourceErrorText(raw)

  if (
    includesAny(lower, [
      'ipnotinwhitelist',
      'ip not in white',
      'not in whitelist',
      'not in white list',
      '白名单',
    ])
  ) {
    return {
      kind: 'ip-whitelist',
      messageKey: MESSAGE_KEYS['ip-whitelist'],
      detail,
      faqUrl: DINGTALK_IP_WHITELIST_FAQ,
    }
  }

  if (
    includesAny(lower, [
      'invalid credentials',
      'invalidcredential',
      'invalidauthentication',
      'invalid appkey',
      'invalid app key',
      'appkey is invalid',
    ])
  ) {
    return { kind: 'invalid-credentials', messageKey: MESSAGE_KEYS['invalid-credentials'], detail }
  }

  if (includesAny(lower, ['status=401', 'unauthorized', 'authentication failed', 'token expired'])) {
    return { kind: 'unauthorized', messageKey: MESSAGE_KEYS.unauthorized, detail }
  }

  if (
    includesAny(lower, [
      'status=403',
      'forbidden',
      'accessdenied',
      'access denied',
      'permission denied',
      'no permission',
    ])
  ) {
    return { kind: 'forbidden', messageKey: MESSAGE_KEYS.forbidden, detail }
  }

  if (
    includesAny(lower, [
      'timeout',
      'timed out',
      'econnrefused',
      'connection refused',
      'dial tcp',
      'network',
      'fetch failed',
      'no such host',
    ])
  ) {
    return { kind: 'network', messageKey: MESSAGE_KEYS.network, detail }
  }

  return { kind: 'unknown', messageKey: MESSAGE_KEYS.unknown, detail }
}
