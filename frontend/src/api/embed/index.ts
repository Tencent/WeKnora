import { get, post, put, del } from '@/utils/request'

export interface EmbedChannel {
  id: string
  tenant_id: number
  agent_id: string
  knowledge_base_id?: string
  name: string
  enabled: boolean
  allowed_origins: string[]
  welcome_message: string
  rate_limit_per_minute: number
  primary_color?: string
  page_title?: string
  widget_position?: WidgetPosition
  publish_token?: string
  created_at: string
  updated_at: string
}

export interface EmbedChannelPublicConfig {
  channel_id: string
  name: string
  knowledge_base_id?: string
  knowledge_base_ids?: string[]
  agent_id: string
  welcome_message: string
  primary_color?: string
  page_title?: string
}

export type WidgetPosition = 'bottom-right' | 'bottom-left' | 'top-right' | 'top-left'

export async function listEmbedChannels(agentId: string) {
  return get<{ success: boolean; data: EmbedChannel[] }>(`/api/v1/agents/${agentId}/embed-channels`)
}

export async function createEmbedChannel(agentId: string, data: Partial<EmbedChannel>) {
  return post<{ success: boolean; data: EmbedChannel }>(`/api/v1/agents/${agentId}/embed-channels`, data)
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

export async function getEmbedConfig(channelId: string, token: string) {
  return get<{ success: boolean; data: EmbedChannelPublicConfig }>(
    `/api/v1/embed/${channelId}/config`,
    { headers: { Authorization: `Embed ${token}` } },
  )
}

export async function createEmbedSession(channelId: string, token: string) {
  return post<{ success: boolean; data: { id: string } }>(
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

export async function getEmbedMessageList(
  channelId: string,
  token: string,
  sessionId: string,
  limit: number,
  beforeTime?: string,
) {
  const params = new URLSearchParams({ limit: String(limit) })
  if (beforeTime) {
    params.set('before_time', beforeTime)
  }
  return get<{ success: boolean; data: unknown[] }>(
    `/api/v1/embed/${channelId}/messages/${sessionId}/load?${params.toString()}`,
    { headers: { Authorization: `Embed ${token}` } },
  )
}

const EMBED_MSG_SOURCE = 'weknora-embed'
const EMBED_HOST_SOURCE = 'weknora-host'

function resolveParentOrigin(): string {
  if (window.parent === window) return ''
  try {
    if (document.referrer) {
      return new URL(document.referrer).origin
    }
  } catch {
    // ignore malformed referrer
  }
  return '*'
}

function isTrustedParentMessage(event: MessageEvent): boolean {
  if (window.parent === window) return false
  if (event.source !== window.parent) return false
  if (!event.data || event.data.source !== EMBED_HOST_SOURCE) return false
  const parentOrigin = resolveParentOrigin()
  if (parentOrigin !== '*' && event.origin !== parentOrigin) return false
  return true
}

function postToParent(payload: Record<string, unknown>) {
  if (window.parent === window) return
  window.parent.postMessage({ source: EMBED_MSG_SOURCE, ...payload }, resolveParentOrigin())
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
  postToParent({
    type: 'message_sent',
    channel_id: channelId,
    session_id: sessionId,
    query,
  })
}

/** Notify the parent page when an assistant reply completes. */
export function postEmbedMessageReceived(channelId: string, sessionId: string, content: string) {
  postToParent({
    type: 'message_received',
    channel_id: channelId,
    session_id: sessionId,
    content,
  })
}

export function parseEmbedTokenFromLocation(): string {
  const queryToken = new URLSearchParams(window.location.search).get('token')
  if (queryToken) return queryToken

  const hash = window.location.hash.startsWith('#') ? window.location.hash.slice(1) : ''
  if (!hash) return ''
  return new URLSearchParams(hash).get('token') || ''
}

export function buildEmbedURL(channelId: string, token?: string) {
  const base = window.location.origin
  const path = `${base}/embed/${channelId}`
  if (!token) return path
  return `${path}#token=${encodeURIComponent(token)}`
}

export function buildEmbedSnippet(channelId: string) {
  const url = buildEmbedURL(channelId)
  return `<iframe src="${url}" style="width:400px;height:600px;border:none;border-radius:12px" allow="clipboard-write"></iframe>`
}

export function buildWidgetSnippet(
  channelId: string,
  token: string,
  opts?: { primaryColor?: string; title?: string; position?: WidgetPosition; baseUrl?: string },
) {
  const base = opts?.baseUrl || window.location.origin
  const position = opts?.position || 'bottom-right'
  const attrs = [
    `src="${base}/weknora-widget.js"`,
    `data-channel="${channelId}"`,
    `data-token="${token}"`,
    `data-position="${position}"`,
  ]
  if (opts?.primaryColor) attrs.push(`data-primary-color="${opts.primaryColor}"`)
  if (opts?.title) attrs.push(`data-title="${opts.title}"`)
  return `<script ${attrs.join('\n        ')}></script>`
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
