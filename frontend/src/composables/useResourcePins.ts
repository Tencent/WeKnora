// useResourcePins – per-(user, tenant) favorites + per-user recents for
// KBs and custom agents.
//
// Favorites are DB-backed (see migration 000047) so they sync across
// devices and survive a logout. They are scoped per (user, tenant) on
// the server, which means switching tenants automatically re-points the
// reads — see `refresh()` and the tenant-change watcher inside the
// composable.
//
// Recents stay in localStorage. They're a personal navigation aid, not a
// shareable resource; round-tripping every card click through the
// network would add latency for marginal value. If the product later
// needs cross-device recents we can promote them to a server-side
// audit-log derivation, which is a much smaller migration than starting
// from scratch.

import { ref, computed, watch, type ComputedRef, type Ref } from 'vue'
import {
  listFavorites,
  addFavorite,
  removeFavorite,
  type FavoriteResourceType,
} from '@/api/user-favorites'
import { safeGetItem, safeSetItem, readUserId } from './preferenceStorage'
import { useAuthStore } from '@/stores/auth'

export type ResourceType = FavoriteResourceType

export interface PinEntry {
  type: ResourceType
  id: string
  /** Unix ms when the pin was created (favorite) or last accessed (recent). */
  ts: number
}

const RECENTS_SUFFIX = 'resource_recents'
const RECENTS_CAP = 30

// Recents are scoped by (user, tenant) — same rationale as favorites. The
// hydration step in the list views joins recent entries against the
// current tenant's KB/Agent index, so a recent from tenant A would
// silently drop out of the rendered list but still count in the sidebar
// badge ("最近 (2)" but empty body). Keying the storage by tenant
// prevents that mismatch entirely.
//
// `tenantSegmentForKey` returns "" when no tenant is resolvable (e.g.
// during the very first render before auth state hydrates); the resulting
// key is still per-user, so we never accidentally read another user's
// recents. Once the tenant id resolves, the tenant watcher below bumps
// the revision so views pick up the right namespace.
function tenantSegmentForKey(): string {
  try {
    const authStore = useAuthStore()
    const tid = authStore.effectiveTenantId
    return tid ? `t${tid}_` : ''
  } catch {
    return ''
  }
}

function recentsKey(): string {
  return `WeKnora_${readUserId()}_${tenantSegmentForKey()}${RECENTS_SUFFIX}`
}

function readRecents(): PinEntry[] {
  const raw = safeGetItem(recentsKey())
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter(
      (e: unknown): e is PinEntry =>
        !!e &&
        typeof (e as PinEntry).type === 'string' &&
        ((e as PinEntry).type === 'kb' || (e as PinEntry).type === 'agent') &&
        typeof (e as PinEntry).id === 'string' &&
        typeof (e as PinEntry).ts === 'number'
    )
  } catch {
    return []
  }
}

function writeRecents(list: PinEntry[]): void {
  safeSetItem(recentsKey(), JSON.stringify(list))
}

// Module-level shared state: a single source of truth for the whole tab
// so all list views stay in sync after a star toggle, and so two list
// views don't double-fetch the same data.
const favoritesByType: Record<ResourceType, Ref<PinEntry[]>> = {
  kb: ref<PinEntry[]>([]),
  agent: ref<PinEntry[]>([]),
}
const loaded: Record<ResourceType, boolean> = { kb: false, agent: false }
const inFlight: Record<ResourceType, Promise<void> | null> = { kb: null, agent: null }

// recents revision counter — same bump-to-invalidate pattern as before.
const recentsRevision = ref(0)
function bumpRecents() {
  recentsRevision.value++
}

if (typeof window !== 'undefined') {
  // Cross-tab sync for recents (localStorage event); favorites changes from
  // another tab are not propagated because the server is the source of
  // truth and a manual refresh / next mutation will pull fresh state.
  window.addEventListener('storage', (e) => {
    if (e.key?.endsWith(`_${RECENTS_SUFFIX}`)) bumpRecents()
  })
}

async function fetchFavorites(type: ResourceType): Promise<void> {
  // Single-flight guard: list views mount in parallel and would otherwise
  // each fire a request. Reuse the in-flight promise instead.
  if (inFlight[type]) return inFlight[type]!
  inFlight[type] = (async () => {
    try {
      const res = (await listFavorites(type)) as unknown as { success: boolean; data?: any[] }
      const data = res?.data || []
      favoritesByType[type].value = data.map((e: any) => ({
        type: e.resource_type as ResourceType,
        id: e.resource_id as string,
        ts: e.created_at ? new Date(e.created_at).getTime() : Date.now(),
      }))
      loaded[type] = true
    } catch (err) {
      console.warn(`[useResourcePins] failed to load ${type} favorites:`, err)
      favoritesByType[type].value = []
    } finally {
      inFlight[type] = null
    }
  })()
  return inFlight[type]!
}

// Invalidate caches when the active tenant changes — favorites are
// (user, tenant)-scoped on the server, so a tenant switch means the
// currently loaded list belongs to the *previous* tenant. We trigger one
// watcher at module load so any later switch refetches automatically.
let tenantWatcherInstalled = false
function installTenantWatcher(): void {
  if (tenantWatcherInstalled) return
  tenantWatcherInstalled = true
  const authStore = useAuthStore()
  watch(
    () => authStore.effectiveTenantId,
    () => {
      loaded.kb = false
      loaded.agent = false
      favoritesByType.kb.value = []
      favoritesByType.agent.value = []
      // Eagerly refetch both types so any already-mounted list view sees
      // fresh data without a manual refresh.
      void fetchFavorites('kb')
      void fetchFavorites('agent')
      // Recents are keyed on the tenant id (see `recentsKey`); bump the
      // revision so any active computed re-reads against the new key.
      bumpRecents()
    }
  )
}

export interface UseResourcePinsResult {
  favorites: ComputedRef<PinEntry[]>
  recents: ComputedRef<PinEntry[]>
  isFavorite: (type: ResourceType, id: string) => boolean
  toggleFavorite: (type: ResourceType, id: string) => Promise<boolean>
  touchRecent: (type: ResourceType, id: string) => void
  removeRecent: (type: ResourceType, id: string) => void
  /** Force a refetch (e.g. after a debug-y state mismatch). */
  refresh: () => Promise<void>
}

export function useResourcePins(): UseResourcePinsResult {
  installTenantWatcher()

  // Lazy first load. Calling the composable always kicks off (or reuses)
  // the in-flight fetch for both types; the templates render with empty
  // arrays until data lands, which is fine because the counts default to
  // 0 and no view depends on a synchronous read.
  if (!loaded.kb) void fetchFavorites('kb')
  if (!loaded.agent) void fetchFavorites('agent')

  const favorites = computed<PinEntry[]>(() => {
    // Merge both type lists and sort by ts desc.
    return [...favoritesByType.kb.value, ...favoritesByType.agent.value].sort(
      (a, b) => b.ts - a.ts
    )
  })

  const recents = computed<PinEntry[]>(() => {
    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    const _ = recentsRevision.value
    return readRecents().sort((a, b) => b.ts - a.ts)
  })

  const isFavorite = (type: ResourceType, id: string): boolean => {
    return favoritesByType[type].value.some((e) => e.id === id)
  }

  const toggleFavorite = async (type: ResourceType, id: string): Promise<boolean> => {
    const list = favoritesByType[type].value
    const idx = list.findIndex((e) => e.id === id)
    // Optimistic update first so the star animation feels instant; on
    // network failure we roll back to the previous state. Errors are
    // swallowed here (logged + rollback) because callers wire this to a
    // click handler that can't surface a useful message — the rolled-back
    // UI itself is the error indication.
    if (idx >= 0) {
      const removed = list[idx]
      favoritesByType[type].value = list.filter((_, i) => i !== idx)
      try {
        await removeFavorite(type, id)
        return false
      } catch (err) {
        favoritesByType[type].value = [removed, ...favoritesByType[type].value]
        console.warn('[useResourcePins] removeFavorite failed; reverted:', err)
        return true
      }
    }
    const optimistic: PinEntry = { type, id, ts: Date.now() }
    favoritesByType[type].value = [optimistic, ...list]
    try {
      await addFavorite(type, id)
      return true
    } catch (err) {
      favoritesByType[type].value = favoritesByType[type].value.filter((e) => e.id !== id)
      console.warn('[useResourcePins] addFavorite failed; reverted:', err)
      return false
    }
  }

  const touchRecent = (type: ResourceType, id: string): void => {
    const list = readRecents()
    const idx = list.findIndex((e) => e.type === type && e.id === id)
    if (idx >= 0) list.splice(idx, 1)
    list.unshift({ type, id, ts: Date.now() })
    if (list.length > RECENTS_CAP) list.length = RECENTS_CAP
    writeRecents(list)
    bumpRecents()
  }

  const removeRecent = (type: ResourceType, id: string): void => {
    const list = readRecents()
    const next = list.filter((e) => !(e.type === type && e.id === id))
    if (next.length !== list.length) {
      writeRecents(next)
      bumpRecents()
    }
  }

  const refresh = async (): Promise<void> => {
    loaded.kb = false
    loaded.agent = false
    await Promise.all([fetchFavorites('kb'), fetchFavorites('agent')])
  }

  return { favorites, recents, isFavorite, toggleFavorite, touchRecent, removeRecent, refresh }
}
