// Pure logic for sidebar session grouping (replaces the old source filter).

export type SessionGroupMode = 'none' | 'date'

export const SESSION_GROUP_MODE_STORAGE_KEY = 'weknora:session-group-mode'
export const DEFAULT_SESSION_GROUP_MODE: SessionGroupMode = 'none'

/** Mirrors backend types.EmbedSessionMarkerPrefix */
export const EMBED_SESSION_MARKER_PREFIX = 'embed_channel:'

/** Mirrors backend types.SessionOwnerAPITenantKeyPrefix (sessions.user_id owner). */
export const API_SESSION_OWNER_PREFIX = 'api_tenant_key:'

/** Mirrors backend types.SessionOwnerAPIExternalUserPrefix. */
export const API_EXTERNAL_USER_SESSION_OWNER_PREFIX = 'api_external_user:'

export interface SessionForGrouping {
  id: string
  kind?: '' | 'assistant'
  title?: string
  is_pinned?: boolean
  created_at?: string
  updated_at?: string
  im_platform?: string
  description?: string
  user_id?: string
  parent_session_id?: string
  // 创建时落定的产线 id；后端 omitempty，缺键按空串处理（归历史组）
  product_line?: string
  originalIndex?: number
}

export interface SessionGroup<T extends SessionForGrouping = SessionForGrouping> {
  key: string
  label: string
  items: T[]
}

export type SessionOrigin =
  | { kind: 'web' }
  | { kind: 'im'; platform: string }
  | { kind: 'embed'; channelId: string }
  | { kind: 'api' }

export interface GroupModeOption {
  value: SessionGroupMode
  label: string
}

export interface SourceGroupLabels {
  web: string
  api: string
  embedFallback: string
  embedChannel: (channelId: string) => string
  imPlatform: (platform: string) => string
}

// Distinct platform keys from the tenant's IM channel overview, in first-seen order.
export function configuredPlatforms(channels: Array<{ platform: string }>): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const c of channels) {
    if (!c.platform || seen.has(c.platform)) continue
    seen.add(c.platform)
    out.push(c.platform)
  }
  return out
}

export function buildGroupModeOptions(labels: {
  none: string
  date: string
}): GroupModeOption[] {
  return [
    { value: 'none', label: labels.none },
    { value: 'date', label: labels.date },
  ]
}

export function readStoredGroupMode(): SessionGroupMode {
  if (typeof localStorage === 'undefined') return DEFAULT_SESSION_GROUP_MODE
  const raw = localStorage.getItem(SESSION_GROUP_MODE_STORAGE_KEY)
  if (raw === 'none' || raw === 'date') return raw
  // Legacy "source" mode — channels are now separate folders like OpenAI projects.
  if (raw === 'source') return 'none'
  return DEFAULT_SESSION_GROUP_MODE
}

export function storeGroupMode(mode: SessionGroupMode): void {
  if (typeof localStorage === 'undefined') return
  localStorage.setItem(SESSION_GROUP_MODE_STORAGE_KEY, mode)
}

export function resolveSessionOrigin(session: SessionForGrouping): SessionOrigin {
  const platform = (session.im_platform || '').trim().toLowerCase()
  if (platform) return { kind: 'im', platform }
  const desc = (session.description || '').trim()
  if (desc.startsWith(EMBED_SESSION_MARKER_PREFIX)) {
    const channelId = desc.slice(EMBED_SESSION_MARKER_PREFIX.length).trim()
    if (channelId) return { kind: 'embed', channelId }
  }
  const ownerId = session.user_id || ''
  if (
    ownerId.startsWith(API_SESSION_OWNER_PREFIX) ||
    ownerId.startsWith(API_EXTERNAL_USER_SESSION_OWNER_PREFIX)
  ) {
    return { kind: 'api' }
  }
  return { kind: 'web' }
}

export function originGroupKey(origin: SessionOrigin): string {
  switch (origin.kind) {
    case 'web':
      return 'web'
    case 'im':
      return `im:${origin.platform}`
    case 'embed':
      return `embed:${origin.channelId}`
    case 'api':
      return 'api'
  }
}

function labelForOrigin(
  origin: SessionOrigin,
  labels: SourceGroupLabels,
  embedChannelNames: Record<string, string>,
): string {
  switch (origin.kind) {
    case 'web':
      return labels.web
    case 'api':
      return labels.api
    case 'im':
      return labels.imPlatform(origin.platform)
    case 'embed': {
      const name = embedChannelNames[origin.channelId]?.trim()
      return name || labels.embedChannel(origin.channelId)
    }
  }
}

export function classifyDateBucket(dateStr: string | undefined): DateBucketKey {
  if (!dateStr) return 'earlier'

  const date = new Date(dateStr)
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const yesterday = new Date(today.getTime() - 24 * 60 * 60 * 1000)
  const sevenDaysAgo = new Date(today.getTime() - 7 * 24 * 60 * 60 * 1000)
  const thirtyDaysAgo = new Date(today.getTime() - 30 * 24 * 60 * 60 * 1000)
  const oneYearAgo = new Date(today.getTime() - 365 * 24 * 60 * 60 * 1000)
  const sessionDate = new Date(date.getFullYear(), date.getMonth(), date.getDate())

  if (sessionDate.getTime() >= today.getTime()) return 'today'
  if (sessionDate.getTime() >= yesterday.getTime()) return 'yesterday'
  if (date.getTime() >= sevenDaysAgo.getTime()) return 'last7Days'
  if (date.getTime() >= thirtyDaysAgo.getTime()) return 'last30Days'
  if (date.getTime() >= oneYearAgo.getTime()) return 'lastYear'
  return 'earlier'
}

const DATE_BUCKET_ORDER = [
  'pinned',
  'today',
  'yesterday',
  'last7Days',
  'last30Days',
  'lastYear',
  'earlier',
] as const

export type DateBucketKey = (typeof DATE_BUCKET_ORDER)[number]

export function groupSessionsByDate<T extends SessionForGrouping>(
  sessions: T[],
  bucketLabels: Record<DateBucketKey, string>,
  categorize: (session: T) => DateBucketKey,
): SessionGroup<T>[] {
  const buckets = new Map<DateBucketKey, T[]>()
  for (const key of DATE_BUCKET_ORDER) buckets.set(key, [])

  for (const session of sessions) {
    const bucket: DateBucketKey = session.is_pinned ? 'pinned' : categorize(session)
    buckets.get(bucket)!.push(session)
  }

  return DATE_BUCKET_ORDER
    .filter((key) => (buckets.get(key)?.length ?? 0) > 0)
    .map((key) => ({
      key,
      label: bucketLabels[key],
      items: buckets.get(key)!,
    }))
}

export function groupSessionsBySource<T extends SessionForGrouping>(
  sessions: T[],
  labels: SourceGroupLabels,
  embedChannelNames: Record<string, string>,
  configuredImPlatforms: string[],
  pinnedLabel: string,
): SessionGroup<T>[] {
  const pinned: T[] = []
  const byKey = new Map<string, T[]>()

  for (const session of sessions) {
    if (session.is_pinned) {
      pinned.push(session)
      continue
    }
    const origin = resolveSessionOrigin(session)
    const key = originGroupKey(origin)
    if (!byKey.has(key)) byKey.set(key, [])
    byKey.get(key)!.push(session)
  }

  const orderedKeys: string[] = []
  if (byKey.has('web')) orderedKeys.push('web')

  const seenIm = new Set<string>()
  for (const platform of configuredImPlatforms) {
    const key = `im:${platform}`
    if (byKey.has(key)) {
      orderedKeys.push(key)
      seenIm.add(platform)
    }
  }
  const extraIm = Array.from(byKey.keys())
    .filter((k) => k.startsWith('im:') && !seenIm.has(k.slice(3)))
    .sort((a, b) => a.localeCompare(b))
  orderedKeys.push(...extraIm)

  const embedKeys = Array.from(byKey.keys())
    .filter((k) => k.startsWith('embed:'))
    .sort((a, b) => {
      const idA = a.slice(7)
      const idB = b.slice(7)
      const nameA = embedChannelNames[idA] || idA
      const nameB = embedChannelNames[idB] || idB
      return nameA.localeCompare(nameB)
    })
  orderedKeys.push(...embedKeys)

  const groups: SessionGroup<T>[] = []
  if (pinned.length > 0) {
    groups.push({ key: 'pinned', label: pinnedLabel, items: pinned })
  }
  for (const key of orderedKeys) {
    const items = byKey.get(key)
    if (!items?.length) continue
    const sample = items[0]
    const origin = resolveSessionOrigin(sample)
    groups.push({
      key,
      label: labelForOrigin(origin, labels, embedChannelNames),
      items,
    })
  }
  return groups
}

export function groupSessionsFlat<T extends SessionForGrouping>(
  sessions: T[],
  pinnedLabel: string,
): SessionGroup<T>[] {
  const pinned = sessions.filter((s) => s.is_pinned)
  const rest = sessions.filter((s) => !s.is_pinned)
  const groups: SessionGroup<T>[] = []
  if (pinned.length > 0) groups.push({ key: 'pinned', label: pinnedLabel, items: pinned })
  if (rest.length > 0) groups.push({ key: 'all', label: '', items: rest })
  return groups
}

export function groupSessions<T extends SessionForGrouping>(
  mode: SessionGroupMode,
  sessions: T[],
  opts: {
    pinnedLabel: string
    bucketLabels: Record<DateBucketKey, string>
    categorizeDate: (session: T) => DateBucketKey
    sourceLabels: SourceGroupLabels
    embedChannelNames: Record<string, string>
    configuredImPlatforms: string[]
  },
): SessionGroup<T>[] {
  if (!sessions.length) return []
  switch (mode) {
    case 'none':
      return groupSessionsFlat(sessions, opts.pinnedLabel)
    case 'date':
    default:
      return groupSessionsByDate(sessions, opts.bucketLabels, opts.categorizeDate)
  }
}

/** Chat list rows show source badges when channels live in separate folders. */
export function shouldShowSessionSourceBadge(_mode: SessionGroupMode): boolean {
  return false
}

// ---- 客户侧栏两级分组（customer-model-lines AC-16）----
// 第一级 = 产线组（按配置顺序）+ 末尾「历史」组；第二级 = 今天/昨天/更早；置顶进所在组 pinned 桶。

/** 客户侧栏第二级的三档时间桶（不含 pinned，pinned 由分组函数单独处理）。 */
export type LineDateBucketKey = 'today' | 'yesterday' | 'earlier'

/** 历史组固定 id：产线为空/未知的会话都归这里，排在所有产线组之后。 */
export const HISTORY_LINE_GROUP_ID = '__history__'

/** 分组入参只需要产线的 id 与显示名。 */
export interface ProductLineLike {
  id: string
  name: string
}

/** 一个产线组：四个桶按 pinned→today→yesterday→earlier 渲染，均为空则整组不输出。 */
export interface ProductLineGroup<T extends SessionForGrouping = SessionForGrouping> {
  lineId: string
  /** 产线显示名；历史组为空串，由调用方给 i18n 文案 */
  label: string
  isHistory: boolean
  pinned: T[]
  today: T[]
  yesterday: T[]
  earlier: T[]
}

/**
 * 三档日期归类。语义与 classifyDateBucket 的同名档一致：
 * 按本地时区的日界比较（今天 0 点 / 昨天 0 点），无效或更早的日期落 earlier。
 */
export function classifyLineDateBucket(
  dateStr: string | undefined,
  now: Date = new Date(),
): LineDateBucketKey {
  if (!dateStr) return 'earlier'
  const date = new Date(dateStr)
  // 无效日期的 getTime() 为 NaN，比较恒为 false，自然落 earlier（与现有函数行为一致）
  const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const dayStart = new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
  if (dayStart >= todayStart) return 'today'
  if (dayStart >= todayStart - 24 * 60 * 60 * 1000) return 'yesterday'
  return 'earlier'
}

function lineGroupHasItems(group: ProductLineGroup): boolean {
  return (
    group.pinned.length > 0 ||
    group.today.length > 0 ||
    group.yesterday.length > 0 ||
    group.earlier.length > 0
  )
}

/**
 * 客户侧栏两级分组的纯函数。
 * - 产线组按 lines 传入顺序输出，只输出非空组；历史组固定追加在末尾（同样只输出非空）。
 * - product_line 为空或不在 lines 清单里的会话都进历史组。
 * - is_pinned 会话进所在组的 pinned 桶；桶内保持传入顺序（与 groupSessionsByDate 一致，顺序由接口分页保证）。
 */
export function groupSessionsByProductLine<T extends SessionForGrouping>(
  sessions: T[],
  lines: ReadonlyArray<ProductLineLike>,
  now: Date = new Date(),
): ProductLineGroup<T>[] {
  const byLineId = new Map<string, ProductLineGroup<T>>()
  for (const line of lines) {
    if (byLineId.has(line.id)) continue
    byLineId.set(line.id, {
      lineId: line.id,
      label: line.name,
      isHistory: false,
      pinned: [],
      today: [],
      yesterday: [],
      earlier: [],
    })
  }

  const history: ProductLineGroup<T> = {
    lineId: HISTORY_LINE_GROUP_ID,
    label: '',
    isHistory: true,
    pinned: [],
    today: [],
    yesterday: [],
    earlier: [],
  }

  for (const session of sessions) {
    // 后端 omitempty：缺键=空串=历史组；未知产线 id 也归历史组
    const lineId = session.product_line ?? ''
    const group = lineId && byLineId.has(lineId) ? byLineId.get(lineId)! : history
    const bucket = session.is_pinned
      ? 'pinned'
      : classifyLineDateBucket(session.updated_at || session.created_at, now)
    group[bucket].push(session)
  }

  const groups: ProductLineGroup<T>[] = []
  for (const line of lines) {
    const group = byLineId.get(line.id)
    if (group && lineGroupHasItems(group)) groups.push(group)
  }
  if (lineGroupHasItems(history)) groups.push(history)
  return groups
}
