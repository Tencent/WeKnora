import { get, post, put, del } from '@/utils/request'

export interface EmbedChannel {
  id: string
  tenant_id: number
  knowledge_base_id: string
  agent_id: string
  name: string
  enabled: boolean
  allowed_origins: string[]
  welcome_message: string
  rate_limit_per_minute: number
  publish_token?: string
  created_at: string
  updated_at: string
}

export interface EmbedChannelPublicConfig {
  channel_id: string
  name: string
  knowledge_base_id: string
  agent_id: string
  welcome_message: string
}

export async function listEmbedChannels(kbId: string) {
  return get<{ success: boolean; data: EmbedChannel[] }>(`/api/v1/knowledge-bases/${kbId}/embed-channels`)
}

export async function createEmbedChannel(kbId: string, data: Partial<EmbedChannel>) {
  return post<{ success: boolean; data: EmbedChannel }>(`/api/v1/knowledge-bases/${kbId}/embed-channels`, data)
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

export function buildEmbedURL(channelId: string, token: string) {
  const base = window.location.origin
  const params = new URLSearchParams({ token })
  return `${base}/embed/${channelId}?${params.toString()}`
}

export function buildEmbedSnippet(channelId: string, token: string) {
  const url = buildEmbedURL(channelId, token)
  return `<iframe src="${url}" style="width:400px;height:600px;border:none;border-radius:12px" allow="clipboard-write"></iframe>`
}
