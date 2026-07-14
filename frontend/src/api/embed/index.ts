import { get, post, put, del } from '@/utils/request'
import { resolveEmbedBaseUrl } from '@/utils/embedBaseUrl'

export interface EmbedChannel {
  id: string
  tenant_id: number
  agent_id: string
  name: string
  enabled: boolean
  allowed_origins: string[]
  welcome_message: string
  rate_limit_per_minute: number
  rate_limit_per_day?: number
  primary_color?: string
  page_title?: string
  header_title_mode?: HeaderTitleMode
  show_suggested_questions?: boolean
  widget_position?: WidgetPosition
  allow_web_search?: boolean
  allow_memory?: boolean
  allow_file_upload?: boolean
  default_locale?: string
  webhook_url?: string
  has_webhook_secret?: boolean
  publish_token?: string
  created_at: string
  updated_at: string
}

export interface EmbedChannelPublicConfig {
  channel_id: string
  name: string
  display_title?: string
  knowledge_base_ids?: string[]
  agent_id: string
  agent_name?: string
  agent_avatar?: string
  welcome_message: string
  primary_color?: string
  page_title?: string
  header_title_mode?: HeaderTitleMode
  show_suggested_questions?: boolean
  widget_position?: WidgetPosition
  allow_web_search?: boolean
  allow_memory?: boolean
  allow_file_upload?: boolean
  agent_web_search_enabled?: boolean
  agent_image_upload_enabled?: boolean
  default_locale?: string
}

export type EmbedLocaleTag = 'zh-CN' | 'en-US' | 'ko-KR' | 'ru-RU' | ''

export interface EmbedChannelStats {
  session_count: number
}

export type HeaderTitleMode = 'channel' | 'session'
export type WidgetPosition = 'bottom-right' | 'bottom-left' | 'top-right' | 'top-left'

/** Prefix of short-lived embed session tokens minted by the backend. */
export const EMBED_SESSION_TOKEN_PREFIX = 'ems_'

/** localStorage key prefix for persisted embed chat sessions (per channel). */
export const EMBED_CHAT_SESSION_STORAGE_PREFIX = 'weknora-embed-session:'

/** localStorage key prefix for anonymous embed visitor ids (per channel). */
export const EMBED_VISITOR_STORAGE_PREFIX = 'weknora-embed-visitor:'

export function embedVisitorStorageKey(channelId: string): string {
  return `${EMBED_VISITOR_STORAGE_PREFIX}${channelId}`
}

/** Stable anonymous id for this browser on the given channel. */
export function getOrCreateEmbedVisitorId(channelId: string): string {
  if (typeof localStorage === 'undefined' || !channelId) {
    return crypto.randomUUID()
  }
  const key = embedVisitorStorageKey(channelId)
  try {
    const existing = localStorage.getItem(key)?.trim()
    if (existing) return existing
    const id = crypto.randomUUID()
    localStorage.setItem(key, id)
    return id
  } catch {
    return crypto.randomUUID()
  }
}

export function embedChatSessionStorageKey(channelId: string): string {
  return `${EMBED_CHAT_SESSION_STORAGE_PREFIX}${channelId}`
}

/** Drop a persisted embed chat session so the next load starts fresh. */
export function clearEmbedStoredChatSession(channelId: string): void {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.removeItem(embedChatSessionStorageKey(channelId))
  } catch {
    // localStorage may be unavailable in private mode.
  }
}

/** Clear a stored chat session when it was created under a different agent binding. */
export function clearEmbedStoredChatSessionIfAgentMismatch(channelId: string, agentId: string): void {
  if (typeof localStorage === 'undefined' || !channelId || !agentId) return
  try {
    const raw = localStorage.getItem(embedChatSessionStorageKey(channelId))
    if (!raw) return
    const parsed = JSON.parse(raw) as { agentId?: string }
    if (parsed?.agentId && parsed.agentId !== agentId) {
      localStorage.removeItem(embedChatSessionStorageKey(channelId))
    }
  } catch {
    // Malformed entry — remove so bootstrap can recover.
    localStorage.removeItem(embedChatSessionStorageKey(channelId))
  }
}

/** Whether a token is already a short-lived session token (secure mode). */
export function isEmbedSessionToken(token: string): boolean {
  return typeof token === 'string' && token.trim().startsWith(EMBED_SESSION_TOKEN_PREFIX)
}

export async function listEmbedChannels(agentId: string) {
  return get<{ success: boolean; data: EmbedChannel[] }>(`/api/v1/agents/${agentId}/embed-channels`)
}

export async function listAllEmbedChannels() {
  return get<{ success: boolean; data: EmbedChannel[] }>('/api/v1/embed-channels')
}

export async function createEmbedChannel(agentId: string, data: Partial<EmbedChannel>) {
  return post<{ success: boolean; data: EmbedChannel }>(`/api/v1/agents/${agentId}/embed-channels`, data)
}

export async function getEmbedChannel(channelId: string) {
  return get<{ success: boolean; data: EmbedChannel }>(`/api/v1/embed-channels/${channelId}`)
}

export async function updateEmbedChannel(channelId: string, data: Partial<EmbedChannel>) {
  return put<{ success: boolean; data: EmbedChannel }>(`/api/v1/embed-channels/${channelId}`, data)
}

export async function deleteEmbedChannel(channelId: string) {
  return del(`/api/v1/embed-channels/${channelId}`)
}

export async function rotateEmbedToken(channelId: string) {
  return post<{ success: boolean; data: EmbedChannel }>(`/api/v1/embed-channels/${channelId}/rotate-token`, {})
}

/** Short-lived session token for management UI preview (JWT auth, no publish token needed). */
export async function getEmbedChannelStats(channelId: string) {
  return get<{ success: boolean; data: EmbedChannelStats }>(
    `/api/v1/embed-channels/${channelId}/stats`,
  )
}

export async function issueEmbedPreviewSession(channelId: string) {
  return post<{ success: boolean; data: { session_token: string; expires_in: number } }>(
    `/api/v1/embed-channels/${channelId}/preview-session`,
    {},
  )
}

export interface SuggestedQuestion {
  question: string
  source?: string
}

export interface EmbedMessageSuggestionItem {
  id: string
  text: string
  category?: string
  source: string
}

export interface EmbedMessageSuggestionSet {
  id: string
  status: 'generating' | 'ready' | 'suppressed' | 'failed'
  allow_regenerate: boolean
  questions: EmbedMessageSuggestionItem[]
}

export async function getEmbedChunkById(channelId: string, token: string, chunkId: string) {
  return get<{ success: boolean; data: { content?: string } }>(
    `/api/v1/embed/${channelId}/chunks/${chunkId}`,
    { headers: { Authorization: `Embed ${token}` } },
  )
}

export async function getEmbedSuggestedQuestions(channelId: string, token: string, limit = 6) {
  return get<{ success: boolean; data: { questions: SuggestedQuestion[] } }>(
    `/api/v1/embed/${channelId}/suggested-questions?limit=${limit}`,
    { headers: { Authorization: `Embed ${token}` } },
  )
}

export async function ensureEmbedMessageSuggestions(
  channelId: string,
  token: string,
  sessionId: string,
  messageId: string,
  sessionSig: string,
  visitorId: string,
  regenerate = false,
) {
  return post<{ success: boolean; data: EmbedMessageSuggestionSet }>(
    `/api/v1/embed/${channelId}/sessions/${sessionId}/messages/${messageId}/suggestions`,
    { regenerate },
    { headers: embedSessionHeaders(token, sessionSig, visitorId) },
  )
}

export async function getEmbedMessageSuggestions(
  channelId: string,
  token: string,
  sessionId: string,
  messageId: string,
  sessionSig: string,
  visitorId: string,
) {
  return get<{ success: boolean; data: EmbedMessageSuggestionSet }>(
    `/api/v1/embed/${channelId}/sessions/${sessionId}/messages/${messageId}/suggestions`,
    { headers: embedSessionHeaders(token, sessionSig, visitorId) },
  )
}

export async function recordEmbedMessageSuggestionEvent(
  channelId: string,
  token: string,
  sessionId: string,
  sessionSig: string,
  visitorId: string,
  suggestionSetId: string,
  eventType: 'impression' | 'click' | 'dismiss',
  questionId = '',
) {
  return post(
    `/api/v1/embed/${channelId}/sessions/${sessionId}/suggestion-events`,
    { suggestion_set_id: suggestionSetId, question_id: questionId, event_type: eventType },
    { headers: embedSessionHeaders(token, sessionSig, visitorId) },
  )
}

export async function getEmbedConfig(channelId: string, token: string) {
  return get<{ success: boolean; data: EmbedChannelPublicConfig }>(
    `/api/v1/embed/${channelId}/config`,
    { headers: { Authorization: `Embed ${token}` } },
  )
}

export async function createEmbedSession(channelId: string, token: string) {
  return post<{ success: boolean; data: { id: string; sig: string } }>(
    `/api/v1/embed/${channelId}/sessions`,
    {},
    { headers: { Authorization: `Embed ${token}` } },
  )
}

export async function exchangeEmbedSession(channelId: string, publishToken: string) {
  return post<{ success: boolean; data: { session_token: string; expires_in: number } }>(
    `/api/v1/embed/${channelId}/exchange`,
    {},
    { headers: { Authorization: `Embed ${publishToken}` } },
  )
}

export async function stopEmbedSession(
  channelId: string,
  token: string,
  sessionId: string,
  messageId: string,
  sessionSig: string,
) {
  const headers: Record<string, string> = {
    Authorization: `Embed ${token}`,
    'X-Embed-Session': sessionSig,
  }
  return post(`/api/v1/embed/${channelId}/sessions/${sessionId}/stop`, { message_id: messageId }, { headers })
}

function embedSessionHeaders(token: string, sessionSig: string, visitorId?: string): Record<string, string> {
  const headers: Record<string, string> = {
    Authorization: `Embed ${token}`,
    'X-Embed-Session': sessionSig,
  }
  const visitor = visitorId?.trim()
  if (visitor) headers['X-Embed-Visitor'] = visitor
  return headers
}

export async function resolveEmbedMCPOAuth(
  channelId: string,
  token: string,
  sessionId: string,
  sessionSig: string,
  visitorId: string,
  pendingId: string,
  body: { service_id: string; decision?: 'authorize' | 'cancel' },
): Promise<void> {
  await post(
    `/api/v1/embed/${channelId}/sessions/${encodeURIComponent(sessionId)}/mcp-oauth-resolutions/${encodeURIComponent(pendingId)}`,
    body,
    { headers: embedSessionHeaders(token, sessionSig, visitorId) },
  )
}

export async function cancelEmbedMCPOAuth(
  channelId: string,
  token: string,
  sessionId: string,
  sessionSig: string,
  visitorId: string,
  pendingId: string,
): Promise<void> {
  await post(
    `/api/v1/embed/${channelId}/sessions/${encodeURIComponent(sessionId)}/mcp-oauth-resolutions/${encodeURIComponent(pendingId)}/cancel`,
    {},
    { headers: embedSessionHeaders(token, sessionSig, visitorId) },
  )
}

export async function getEmbedMCPOAuthAuthorizeURL(
  channelId: string,
  token: string,
  sessionId: string,
  sessionSig: string,
  visitorId: string,
  serviceId: string,
  body: { redirect_uri: string; frontend_redirect?: string },
): Promise<string> {
  const response: any = await post(
    `/api/v1/embed/${channelId}/sessions/${encodeURIComponent(sessionId)}/mcp-services/${encodeURIComponent(serviceId)}/oauth/authorize-url`,
    body,
    { headers: embedSessionHeaders(token, sessionSig, visitorId) },
  )
  return (response.data ?? response)?.authorization_url ?? ''
}

export async function getEmbedMCPOAuthStatus(
  channelId: string,
  token: string,
  sessionId: string,
  sessionSig: string,
  visitorId: string,
  serviceId: string,
): Promise<boolean> {
  const response: any = await get(
    `/api/v1/embed/${channelId}/sessions/${encodeURIComponent(sessionId)}/mcp-services/${encodeURIComponent(serviceId)}/oauth/status`,
    { headers: embedSessionHeaders(token, sessionSig, visitorId) },
  )
  return Boolean((response.data ?? response)?.authorized)
}

export async function resolveEmbedToolApproval(
  channelId: string,
  token: string,
  sessionId: string,
  sessionSig: string,
  visitorId: string,
  pendingId: string,
  body: { decision: 'approve' | 'reject'; modified_args?: Record<string, unknown>; reason?: string },
): Promise<void> {
  await post(
    `/api/v1/embed/${channelId}/sessions/${encodeURIComponent(sessionId)}/tool-approvals/${encodeURIComponent(pendingId)}`,
    body,
    { headers: embedSessionHeaders(token, sessionSig, visitorId) },
  )
}

export async function getEmbedMessageList(
  channelId: string,
  token: string,
  sessionId: string,
  limit: number,
  beforeTime?: string,
  sig?: string,
) {
  const params = new URLSearchParams({ limit: String(limit) })
  if (beforeTime) {
    params.set('before_time', beforeTime)
  }
  const headers: Record<string, string> = { Authorization: `Embed ${token}` }
  // Signed session handle — sent as a header so it never lands in URL/access logs.
  if (sig) headers['X-Embed-Session'] = sig
  return get<{ success: boolean; data: unknown[] }>(
    `/api/v1/embed/${channelId}/messages/${sessionId}/load?${params.toString()}`,
    { headers },
  )
}

const EMBED_MSG_SOURCE = 'weknora-embed'
const EMBED_HOST_SOURCE = 'weknora-host'

// The exact parent origin, learned from the first trusted host message
// (trust-on-first-use). Once known, every inbound/outbound message is pinned to
// it so conversation content is never broadcast to an unexpected window.
let verifiedParentOrigin = ''

function referrerParentOrigin(): string {
  if (window.parent === window) return ''
  try {
    if (document.referrer) {
      return new URL(document.referrer).origin
    }
  } catch {
    // ignore malformed referrer
  }
  return ''
}

/** Best-known parent origin: verified handshake first, then referrer. */
function knownParentOrigin(): string {
  return verifiedParentOrigin || referrerParentOrigin()
}

function isTrustedParentMessage(event: MessageEvent): boolean {
  if (window.parent === window) return false
  if (event.source !== window.parent) return false
  if (!event.data || event.data.source !== EMBED_HOST_SOURCE) return false
  if (typeof event.origin !== 'string' || event.origin === 'null') return false
  const expected = knownParentOrigin()
  if (expected) {
    if (event.origin !== expected) return false
  } else {
    // First trusted handshake with no referrer hint: pin to this origin.
    verifiedParentOrigin = event.origin
  }
  return true
}

/**
 * Post a message to the host page.
 * `sensitive` payloads (conversation content) are dropped when the parent
 * origin is unknown rather than broadcast to '*'. Non-sensitive handshake
 * messages (bootstrap_request/ready) may fall back to '*' so token handoff can
 * still bootstrap when the referrer is stripped.
 */
function postToParent(payload: Record<string, unknown>, opts?: { sensitive?: boolean }) {
  if (window.parent === window) return
  const target = knownParentOrigin()
  if (!target) {
    if (opts?.sensitive) return
    window.parent.postMessage({ source: EMBED_MSG_SOURCE, ...payload }, '*')
    return
  }
  window.parent.postMessage({ source: EMBED_MSG_SOURCE, ...payload }, target)
}

/** Notify the parent page that the embed widget is ready. */
export function postEmbedReady(channelId: string) {
  postToParent({ type: 'ready', channel_id: channelId })
}

/** Request a publish token from the parent host page. */
export function postEmbedBootstrapRequest(channelId: string) {
  postToParent({ type: 'bootstrap_request', channel_id: channelId })
}

/** Notify the parent page when a user message is sent. */
export function postEmbedMessageSent(channelId: string, sessionId: string, query: string) {
  postToParent(
    {
      type: 'message_sent',
      channel_id: channelId,
      session_id: sessionId,
      query,
    },
    { sensitive: true },
  )
}

/** Relay a chat event to the channel webhook (server-side, best-effort). */
export function relayEmbedWebhookEvent(
  channelId: string,
  token: string,
  sessionId: string,
  sessionSig: string,
  body: { type: 'message_sent' | 'message_received'; query?: string; content?: string },
) {
  const headers: Record<string, string> = {
    Authorization: `Embed ${token}`,
    'X-Embed-Session': sessionSig,
  }
  void post(
    `/api/v1/embed/${channelId}/sessions/${sessionId}/events`,
    { type: body.type, session_id: sessionId, query: body.query, content: body.content },
    { headers },
  ).catch(() => {
    // Webhook relay must not block chat.
  })
}

/** Notify the parent page when an assistant reply completes. */
export function postEmbedMessageReceived(channelId: string, sessionId: string, content: string) {
  postToParent(
    {
      type: 'message_received',
      channel_id: channelId,
      session_id: sessionId,
      content,
    },
    { sensitive: true },
  )
}

export function parseEmbedTokenFromLocation(): string {
  const queryToken = new URLSearchParams(window.location.search).get('token')
  if (queryToken) return queryToken

  const hash = window.location.hash.startsWith('#') ? window.location.hash.slice(1) : ''
  if (!hash) return ''
  return new URLSearchParams(hash).get('token') || ''
}

export function buildEmbedURL(
  channelId: string,
  token?: string,
  opts?: { locale?: string; refreshKey?: number; baseUrl?: string },
) {
  const base = safeBaseUrl(opts?.baseUrl) || resolveEmbedBaseUrl()
  let path = `${base}/embed/${encodeURIComponent(channelId)}`
  const params = new URLSearchParams()
  if (opts?.locale?.trim()) params.set('locale', opts.locale.trim())
  if (opts?.refreshKey) params.set('r', String(opts.refreshKey))
  const qs = params.toString()
  if (qs) path += `?${qs}`
  if (token) path += `#token=${encodeURIComponent(token)}`
  return path
}

/** Escape a value for safe interpolation inside an HTML double-quoted attribute. */
function escapeHtmlAttr(value: string): string {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

/** Validate that a base URL is a well-formed http(s) origin; fall back otherwise. */
function safeBaseUrl(raw?: string): string {
  const fallback = window.location.origin
  if (!raw) return fallback
  try {
    const u = new URL(raw, window.location.href)
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return fallback
    return u.origin
  } catch {
    return fallback
  }
}

export function buildEmbedSnippet(channelId: string, token?: string) {
  // A bare iframe has no token-handoff host, so the snippet must carry the
  // publish token in the URL hash, otherwise the embed page cannot bootstrap.
  const url = escapeHtmlAttr(buildEmbedURL(channelId, token))
  return `<iframe src="${url}" style="width:400px;height:600px;border:none;border-radius:12px" allow="clipboard-write"></iframe>`
}

export function buildWidgetSnippet(
  channelId: string,
  token: string,
  opts?: { primaryColor?: string; title?: string; position?: WidgetPosition; baseUrl?: string },
) {
  const base = safeBaseUrl(opts?.baseUrl)
  const position = opts?.position || 'bottom-right'
  const attrs = [
    `src="${escapeHtmlAttr(`${base}/weknora-widget.js`)}"`,
    `data-channel="${escapeHtmlAttr(channelId)}"`,
    `data-token="${escapeHtmlAttr(token)}"`,
    `data-position="${escapeHtmlAttr(position)}"`,
  ]
  if (opts?.primaryColor) attrs.push(`data-primary-color="${escapeHtmlAttr(opts.primaryColor)}"`)
  if (opts?.title) attrs.push(`data-title="${escapeHtmlAttr(opts.title)}"`)
  // Cross-origin embed: add data-sandbox="true" when using a dedicated embed subdomain.
  return `<script ${attrs.join('\n        ')}></script>`
}

/** Default placeholder for the integrator's own token-minting endpoint. */
export const SECURE_TOKEN_ENDPOINT_PLACEHOLDER = 'https://your-backend.example.com/weknora/embed-token'

/**
 * Secure-mode widget snippet: the page references an endpoint on the
 * integrator's own backend (data-token-endpoint) instead of carrying the
 * publish token. The publish token never reaches the browser.
 */
export function buildSecureWidgetSnippet(
  channelId: string,
  opts?: { primaryColor?: string; title?: string; position?: WidgetPosition; baseUrl?: string; tokenEndpoint?: string },
) {
  const base = safeBaseUrl(opts?.baseUrl)
  const position = opts?.position || 'bottom-right'
  const endpoint = opts?.tokenEndpoint || SECURE_TOKEN_ENDPOINT_PLACEHOLDER
  const attrs = [
    `src="${escapeHtmlAttr(`${base}/weknora-widget.js`)}"`,
    `data-channel="${escapeHtmlAttr(channelId)}"`,
    `data-token-endpoint="${escapeHtmlAttr(endpoint)}"`,
    `data-position="${escapeHtmlAttr(position)}"`,
  ]
  if (opts?.primaryColor) attrs.push(`data-primary-color="${escapeHtmlAttr(opts.primaryColor)}"`)
  if (opts?.title) attrs.push(`data-title="${escapeHtmlAttr(opts.title)}"`)
  return `<script ${attrs.join('\n        ')}></script>`
}

/**
 * Server-side reference (Node/Express) for the token-minting endpoint that
 * secure mode points at. The publish token stays on the server; the endpoint
 * exchanges it for a short-lived session token and returns that to the page.
 */
export function buildSecureServerNodeExample(channelId: string, opts?: { baseUrl?: string }) {
  const base = safeBaseUrl(opts?.baseUrl)
  const exchangeUrl = `${base}/api/v1/embed/${channelId}/exchange`
  return [
    `// Node/Express — keep WEKNORA_PUBLISH_TOKEN only on the server (env var).`,
    `app.get('/weknora/embed-token', async (req, res) => {`,
    `  // Only mint for logged-in visitors — e.g. session cookie or Bearer token.`,
    `  const auth = req.headers.authorization || ''`,
    `  const hasSession = Boolean(req.cookies?.session_id)`,
    `  if (!hasSession && !auth.startsWith('Bearer ')) {`,
    `    return res.status(401).json({ error: 'unauthorized' })`,
    `  }`,
    `  const r = await fetch('${exchangeUrl}', {`,
    `    method: 'POST',`,
    `    headers: {`,
    `      Authorization: 'Embed ' + process.env.WEKNORA_PUBLISH_TOKEN,`,
    `      Origin: 'https://your-site.example.com', // must match channel allowed_origins`,
    `    },`,
    `  })`,
    `  const body = await r.json()`,
    `  if (!body?.data?.session_token) return res.status(502).json({ error: 'mint failed' })`,
    `  res.json({ token: body.data.session_token, expiresIn: body.data.expires_in })`,
    `})`,
  ].join('\n')
}

export function buildSecureServerGoExample(channelId: string, opts?: { baseUrl?: string }) {
  const base = safeBaseUrl(opts?.baseUrl)
  const exchangeUrl = `${base}/api/v1/embed/${channelId}/exchange`
  return [
    `// Go net/http — keep WEKNORA_PUBLISH_TOKEN only on the server (env var).`,
    `func embedTokenHandler(w http.ResponseWriter, r *http.Request) {`,
    `  if r.Header.Get("Authorization") == "" && r.Header.Get("Cookie") == "" {`,
    `    http.Error(w, \`{"error":"unauthorized"}\`, http.StatusUnauthorized)`,
    `    return`,
    `  }`,
    `  req, _ := http.NewRequest(http.MethodPost, "${exchangeUrl}", nil)`,
    `  req.Header.Set("Authorization", "Embed "+os.Getenv("WEKNORA_PUBLISH_TOKEN"))`,
    `  req.Header.Set("Origin", "https://your-site.example.com") // must match channel allowed_origins`,
    `  resp, err := http.DefaultClient.Do(req)`,
    `  if err != nil || resp.StatusCode >= 300 {`,
    `    http.Error(w, \`{"error":"mint failed"}\`, http.StatusBadGateway)`,
    `    return`,
    `  }`,
    `  defer resp.Body.Close()`,
    `  var body struct { Data struct { SessionToken string \`json:"session_token"\` ExpiresIn int \`json:"expires_in"\` } \`json:"data"\` }`,
    `  if json.NewDecoder(resp.Body).Decode(&body) != nil || body.Data.SessionToken == "" {`,
    `    http.Error(w, \`{"error":"mint failed"}\`, http.StatusBadGateway)`,
    `    return`,
    `  }`,
    `  w.Header().Set("Content-Type", "application/json")`,
    `  json.NewEncoder(w).Encode(map[string]any{"token": body.Data.SessionToken, "expiresIn": body.Data.ExpiresIn})`,
    `}`,
  ].join('\n')
}

/** @deprecated Use buildSecureServerNodeExample */
export function buildSecureServerExample(channelId: string, opts?: { baseUrl?: string }) {
  return buildSecureServerNodeExample(channelId, opts)
}

export function buildSecureServerExamples(channelId: string, opts?: { baseUrl?: string }) {
  return [
    buildSecureServerNodeExample(channelId, opts),
    '',
    buildSecureServerGoExample(channelId, opts),
  ].join('\n')
}

/** Listen for context injected by the parent page (embed host). */
export function onEmbedHostContext(handler: (payload: Record<string, unknown>) => void) {
  const listener = (e: MessageEvent) => {
    if (!isTrustedParentMessage(e) || e.data.type !== 'set_context') return
    handler(e.data.payload || {})
  }
  window.addEventListener('message', listener)
  return () => window.removeEventListener('message', listener)
}

/** Listen for a publish token provided by the parent host page. */
export function onEmbedHostToken(handler: (token: string, channelId?: string) => void) {
  const listener = (e: MessageEvent) => {
    if (!isTrustedParentMessage(e) || e.data.type !== 'provide_token') return
    const token = String(e.data.token || '').trim()
    if (!token) return
    handler(token, e.data.channel_id)
  }
  window.addEventListener('message', listener)
  return () => window.removeEventListener('message', listener)
}

/** Listen for locale changes from the parent host page. */
export function onEmbedHostLocale(handler: (locale: string) => void) {
  const listener = (e: MessageEvent) => {
    if (!isTrustedParentMessage(e) || e.data.type !== 'set_locale') return
    const locale = String(e.data.payload?.locale || e.data.locale || '').trim()
    if (!locale) return
    handler(locale)
  }
  window.addEventListener('message', listener)
  return () => window.removeEventListener('message', listener)
}

/** Listen for a pre-filled query from the parent host page. */
export function onEmbedHostOpenWithQuery(handler: (query: string) => void) {
  const listener = (e: MessageEvent) => {
    if (!isTrustedParentMessage(e) || e.data.type !== 'open_with_query') return
    const query = String(e.data.payload?.query || e.data.query || '').trim()
    if (!query) return
    handler(query)
  }
  window.addEventListener('message', listener)
  return () => window.removeEventListener('message', listener)
}
