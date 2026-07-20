// OpenAI-style sidebar: channel folders (IM / embed) + flat/date-grouped chats,
// each bucket paginates independently via the API `source` param.

import type { SessionForGrouping } from './sessionGrouping'

export const SIDEBAR_BUCKET_PAGE_SIZE = 30

export type SidebarBucketKind = 'web' | 'im' | 'embed' | 'api'

export interface SidebarSessionBucket {
  key: string
  apiSource: string
  label: string
  kind: SidebarBucketKind
  platform?: string
  page: number
  total: number
  items: SessionForGrouping[]
  loaded: boolean
  loading: boolean
  /** 已通过轻量 count 探测（page_size=1），用于隐藏无会话的渠道文件夹 */
  countKnown: boolean
}

export interface BucketDefinition {
  key: string
  apiSource: string
  label: string
  kind: SidebarBucketKind
  platform?: string
}

export function createEmptyBucket(def: BucketDefinition): SidebarSessionBucket {
  return {
    ...def,
    page: 0,
    total: 0,
    items: [],
    loaded: false,
    loading: false,
    countKnown: false,
  }
}

export function bucketHasMore(bucket: SidebarSessionBucket): boolean {
  return bucket.items.length < bucket.total
}

export function bucketVisible(bucket: SidebarSessionBucket): boolean {
  if (!isChannelBucket(bucket)) return true
  if (!bucket.countKnown) return false
  return bucket.total > 0
}

export function applyBucketCountProbe(
  bucket: SidebarSessionBucket,
  total: number,
): SidebarSessionBucket {
  return { ...bucket, total, countKnown: true }
}

export function isChannelBucket(bucket: SidebarSessionBucket): boolean {
  return bucket.kind === 'im' || bucket.kind === 'embed' || bucket.kind === 'api'
}

export function isChannelBucketKey(key: string): boolean {
  return key.startsWith('im:') || key.startsWith('embed:') || key === API_SESSION_BUCKET_KEY
}

// API_SESSION_BUCKET_KEY is the admin-only bucket that lists every API-key
// session in the tenant. It is treated like a channel folder: probed for a
// count first and only surfaced in the source filter when it has sessions.
export const API_SESSION_BUCKET_KEY = 'api'

export function buildBucketDefinitions(
  imPlatforms: string[],
  embedChannels: Record<string, string>,
  labels: {
    web: string
    imPlatform: (platform: string) => string
    embedChannel: (name: string) => string
    api: string
  },
  options: { includeApiBucket?: boolean } = {},
): BucketDefinition[] {
  const imDefs = imPlatforms.map((platform) => ({
    key: `im:${platform}`,
    apiSource: platform,
    label: labels.imPlatform(platform),
    kind: 'im' as const,
    platform,
  }))
  const embedDefs = Object.entries(embedChannels).map(([id, name]) => ({
    key: `embed:${id}`,
    apiSource: `embed:${id}`,
    label: labels.embedChannel(name || id.slice(0, 8)),
    kind: 'embed' as const,
  }))
  const apiDefs: BucketDefinition[] = options.includeApiBucket
    ? [
        {
          key: API_SESSION_BUCKET_KEY,
          apiSource: API_SESSION_BUCKET_KEY,
          label: labels.api,
          kind: 'api' as const,
        },
      ]
    : []
  return [
    ...imDefs,
    ...embedDefs,
    ...apiDefs,
    {
      key: 'web',
      apiSource: 'web',
      label: labels.web,
      kind: 'web',
    },
  ]
}

export function mergeBucketPage(
  bucket: SidebarSessionBucket,
  rows: SessionForGrouping[],
  total: number,
  page: number,
): SidebarSessionBucket {
  const seen = new Set(bucket.items.map((s) => s.id))
  const merged = [...bucket.items]
  for (const row of rows) {
    if (seen.has(row.id)) continue
    seen.add(row.id)
    merged.push(row)
  }
  return {
    ...bucket,
    page,
    total,
    items: merged,
    loaded: true,
    loading: false,
  }
}

export function flattenBucketItems(
  buckets: Record<string, SidebarSessionBucket>,
  order: string[],
): SessionForGrouping[] {
  const out: SessionForGrouping[] = []
  const seen = new Set<string>()
  for (const key of order) {
    const bucket = buckets[key]
    if (!bucket) continue
    for (const item of bucket.items) {
      if (seen.has(item.id)) continue
      seen.add(item.id)
      out.push(item)
    }
  }
  return out
}

export function prependSessionToWebBucket(
  bucket: SidebarSessionBucket,
  session: SessionForGrouping,
): SidebarSessionBucket {
  if (bucket.items.some((row) => row.id === session.id)) return bucket
  return {
    ...bucket,
    items: [session, ...bucket.items],
    total: bucket.total + 1,
    loaded: true,
  }
}

export function removeSessionFromBuckets(
  buckets: Record<string, SidebarSessionBucket>,
  sessionId: string,
): Record<string, SidebarSessionBucket> {
  const next: Record<string, SidebarSessionBucket> = {}
  for (const [key, bucket] of Object.entries(buckets)) {
    const items = bucket.items.filter((s) => s.id !== sessionId)
    next[key] = {
      ...bucket,
      items,
      total: Math.max(0, bucket.total - (items.length < bucket.items.length ? 1 : 0)),
    }
  }
  return next
}
