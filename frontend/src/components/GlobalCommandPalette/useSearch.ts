import { ref, watch, onMounted, computed } from 'vue'
import { listKnowledgeBases, knowledgeSemanticSearch } from '@/api/knowledge-base'
import { searchMessages, type MessageSearchGroupItem } from '@/api/chat-history'
import { listAgents } from '@/api/agent'
import { useOrganizationStore } from '@/stores/organization'
import { useMenuStore } from '@/stores/menu'

export interface CmdkKb {
  id: string
  name: string
  type?: string
}

export interface CmdkAgent {
  id: string
  name: string
  description?: string
  avatar?: string
  isBuiltin: boolean
  source: 'own' | 'shared'
  orgName?: string
}

export interface CmdkSessionItem {
  id: string
  title: string
}

export interface CmdkChunk {
  id: string
  chunk_index: number
  knowledge_id: string
  knowledge_base_id: string
  knowledge_title: string
  kb_name: string
  content: string
  matched_content?: string
  match_type: 'vector' | 'keyword' | string
  score: number
}

export interface CmdkFileGroup {
  knowledgeId: string
  kbId: string
  title: string
  kbName: string
  chunks: CmdkChunk[]
}

export interface CmdkMsgGroup {
  sessionId: string
  sessionTitle: string
  items: MessageSearchGroupItem[]
}

/**
 * useCmdkSearch — debounced, cancellable search that fans out to
 *   • /api/v1/knowledge-search (chunks / files)
 *   • /api/v1/messages/search  (chat history)
 * and produces three client-side groups: kbs / files / messages.
 *
 * Cancellation is implemented with a monotonically-increasing request id so
 * that stale responses are dropped (AbortController is not used to keep the
 * backend API untouched).
 *
 * `lockedKbIds` is a getter returning the current scope. When it returns a
 * non-empty array, chunk search is limited to those KBs and message search
 * is skipped. Changes to the getter's return value re-trigger the search.
 */
export function useCmdkSearch(options: {
  /** When set, scopes knowledge search to these KBs. Re-evaluated on each search. */
  lockedKbIds?: () => string[]
  /** How many knowledge chunks to keep after grouping; default no-cap. */
  chunkLimit?: number
  /** Debounce delay in ms. */
  debounceMs?: number
}) {
  const debounceMs = options.debounceMs ?? 350
  const query = ref('')
  const loading = ref(false)
  const hasSearched = ref(false)

  // All KBs the current user can see (own + shared). Loaded lazily & cached.
  const knowledgeBases = ref<CmdkKb[]>([])
  const kbsLoaded = ref(false)
  let kbsLoadingPromise: Promise<void> | null = null

  const fileGroups = ref<CmdkFileGroup[]>([])
  const messageGroups = ref<CmdkMsgGroup[]>([])
  const totalChunks = ref(0)
  const totalMessages = ref(0)

  // Agents the current user can access (own + builtin + shared via orgs).
  const agents = ref<CmdkAgent[]>([])
  const agentsLoaded = ref(false)
  let agentsLoadingPromise: Promise<void> | null = null

  const orgStore = useOrganizationStore()
  const menuStore = useMenuStore()

  const ensureKbs = async (): Promise<void> => {
    if (kbsLoaded.value) return
    if (kbsLoadingPromise) return kbsLoadingPromise
    kbsLoadingPromise = (async () => {
      try {
        const [kbRes, shared] = await Promise.all([
          listKnowledgeBases(),
          orgStore.fetchSharedKnowledgeBases(),
        ])
        const own: CmdkKb[] = ((kbRes as any)?.data || []).map((kb: any) => ({
          id: String(kb.id),
          name: kb.name || '',
          type: kb.type,
        }))
        const ownIds = new Set(own.map(k => k.id))
        const sharedList: CmdkKb[] = (shared || [])
          .filter((s: any) => s?.knowledge_base != null)
          .map((s: any) => ({
            id: String(s.knowledge_base.id),
            name: s.knowledge_base.name || '',
            type: s.knowledge_base.type,
          }))
          .filter((k: CmdkKb) => !ownIds.has(k.id))
        knowledgeBases.value = [...own, ...sharedList]
        kbsLoaded.value = true
      } catch (e) {
        // Best-effort; silent failure keeps the palette usable.
        console.error('[cmdk] failed to load knowledge bases', e)
      } finally {
        kbsLoadingPromise = null
      }
    })()
    return kbsLoadingPromise
  }

  // Derived: KB name lookup
  const getKbName = (kbId: string): string => {
    if (!kbId) return ''
    const kb = knowledgeBases.value.find(k => k.id === kbId)
    return kb?.name || ''
  }

  // KB name hits (client-side, works even before a remote search returns).
  const kbMatches = computed<CmdkKb[]>(() => {
    const q = query.value.trim().toLowerCase()
    if (!q) return []
    return knowledgeBases.value
      .filter(k => k.name.toLowerCase().includes(q))
      .slice(0, 4)
  })

  // Agents (own + shared). Lazily loaded & cached; no backend search endpoint
  // exists so we always filter client-side.
  const ensureAgents = async (): Promise<void> => {
    if (agentsLoaded.value) return
    if (agentsLoadingPromise) return agentsLoadingPromise
    agentsLoadingPromise = (async () => {
      try {
        const [ownRes, shared] = await Promise.all([
          listAgents(),
          orgStore.fetchSharedAgents(),
        ])
        const own: CmdkAgent[] = ((ownRes as any)?.data || []).map((a: any) => ({
          id: String(a.id),
          name: a.name || '',
          description: a.description,
          avatar: a.avatar,
          isBuiltin: !!a.is_builtin,
          source: 'own',
        }))
        const ownIds = new Set(own.map(a => a.id))
        const sharedList: CmdkAgent[] = (shared || [])
          .filter((s: any) => s?.agent != null)
          .map((s: any) => ({
            id: String(s.agent.id),
            name: s.agent.name || '',
            description: s.agent.description,
            avatar: s.agent.avatar,
            isBuiltin: !!s.agent.is_builtin,
            source: 'shared' as const,
            orgName: s.org_name,
          }))
          .filter((a: CmdkAgent) => !ownIds.has(a.id))
        agents.value = [...own, ...sharedList]
        agentsLoaded.value = true
      } catch (e) {
        console.error('[cmdk] failed to load agents', e)
      } finally {
        agentsLoadingPromise = null
      }
    })()
    return agentsLoadingPromise
  }

  const agentMatches = computed<CmdkAgent[]>(() => {
    const q = query.value.trim().toLowerCase()
    if (!q) return []
    return agents.value
      .filter(a => {
        const name = (a.name || '').toLowerCase()
        const desc = (a.description || '').toLowerCase()
        return name.includes(q) || desc.includes(q)
      })
      .slice(0, 5)
  })

  // Sessions: no dedicated list API; we re-use whatever menuStore already has
  // cached (the sidebar already paginates them). Good enough for "jump to a
  // session I've seen recently" — not a substitute for message-content search.
  const sessionList = computed<CmdkSessionItem[]>(() => {
    const chatMenu = (menuStore.menuArr as any[]).find((m: any) => m.path === 'creatChat')
    const children = (chatMenu?.children as any[]) || []
    return children.map((c: any) => ({
      id: String(c.id || ''),
      title: c.title || '',
    }))
  })

  const sessionMatches = computed<CmdkSessionItem[]>(() => {
    const q = query.value.trim().toLowerCase()
    if (!q) return []
    // If a session already appears in the message-content group, suppress it
    // here — showing the same session title twice (once with a message hit,
    // once by title match) is noise. The message-group entry is strictly
    // more informative (it shows which message matched), so it wins.
    const coveredBySession = new Set(messageGroups.value.map(g => g.sessionId))
    return sessionList.value
      .filter(s => s.title && s.title.toLowerCase().includes(q))
      .filter(s => !coveredBySession.has(s.id))
      .slice(0, 5)
  })

  // Monotonic id to drop stale responses.
  let requestSeq = 0
  let debounceTimer: ReturnType<typeof setTimeout> | null = null

  const clearResults = () => {
    fileGroups.value = []
    messageGroups.value = []
    totalChunks.value = 0
    totalMessages.value = 0
    hasSearched.value = false
  }

  const runSearch = async (q: string, seq: number) => {
    loading.value = true
    hasSearched.value = true

    // Determine knowledge_base_ids for chunk search. A non-empty lockedKbIds
    // narrows the scope and also disables message search (out of scope).
    const locked = options.lockedKbIds?.() || []
    const scoped = locked.length > 0
    let kbIds: string[]
    if (scoped) {
      kbIds = locked
    } else {
      await ensureKbs()
      kbIds = knowledgeBases.value.map(k => k.id)
    }

    const knowledgePromise = kbIds.length > 0
      ? knowledgeSemanticSearch({ query: q, knowledge_base_ids: kbIds })
          .then((res: any) => (res?.success && res.data ? res.data : []))
          .catch((e) => {
            console.error('[cmdk] knowledge search failed', e)
            return []
          })
      : Promise.resolve([])

    const messagesPromise = scoped
      ? Promise.resolve({ items: [], total: 0 })
      : searchMessages({ query: q, mode: 'hybrid', limit: 30 })
          .then((res: any) => (res?.success && res.data ? res.data : { items: [], total: 0 }))
          .catch((e) => {
            console.error('[cmdk] message search failed', e)
            return { items: [], total: 0 }
          })

    const [chunks, msgRes] = await Promise.all([knowledgePromise, messagesPromise])

    // Stale response guard.
    if (seq !== requestSeq) return

    // Group chunks by knowledge_id.
    const fmap = new Map<string, CmdkFileGroup>()
    for (const item of chunks as any[]) {
      const kid = item.knowledge_id || 'unknown'
      if (!fmap.has(kid)) {
        fmap.set(kid, {
          knowledgeId: kid,
          kbId: item.knowledge_base_id || '',
          title: item.knowledge_title || item.knowledge_filename || kid,
          kbName: getKbName(item.knowledge_base_id),
          chunks: [],
        })
      }
      fmap.get(kid)!.chunks.push({
        id: item.id,
        chunk_index: item.chunk_index,
        knowledge_id: kid,
        knowledge_base_id: item.knowledge_base_id || '',
        knowledge_title: item.knowledge_title || '',
        kb_name: getKbName(item.knowledge_base_id),
        content: item.content || '',
        matched_content: item.matched_content,
        match_type: item.match_type || 'vector',
        score: item.score || 0,
      })
    }
    fileGroups.value = Array.from(fmap.values())
    totalChunks.value = (chunks as any[]).length

    // Group messages by session_id.
    const mmap = new Map<string, CmdkMsgGroup>()
    const items: MessageSearchGroupItem[] = (msgRes as any).items || []
    for (const m of items) {
      const sid = m.session_id || 'unknown'
      if (!mmap.has(sid)) {
        mmap.set(sid, {
          sessionId: sid,
          sessionTitle: m.session_title || '',
          items: [],
        })
      }
      mmap.get(sid)!.items.push(m)
    }
    messageGroups.value = Array.from(mmap.values())
    totalMessages.value = (msgRes as any).total || items.length

    loading.value = false
  }

  // Watch query changes (debounced). lockedKbIds is tracked as a derived
  // string so the search re-runs when the KB scope toggles.
  const lockedSignature = () =>
    (options.lockedKbIds?.() || []).slice().sort().join(',')

  watch([query, lockedSignature], ([val]) => {
    if (debounceTimer) clearTimeout(debounceTimer)
    const trimmed = (val as string).trim()
    if (!trimmed) {
      requestSeq++ // cancel in-flight
      loading.value = false
      clearResults()
      return
    }
    debounceTimer = setTimeout(() => {
      const seq = ++requestSeq
      runSearch(trimmed, seq)
    }, debounceMs)
  })

  onMounted(() => {
    // Preload KB and agent lists so name matches show up instantly on first
    // keystroke. Sessions come from the menuStore (already populated by the
    // sidebar) so no extra fetch needed here.
    ensureKbs()
    ensureAgents()
  })

  return {
    query,
    loading,
    hasSearched,
    knowledgeBases,
    fileGroups,
    messageGroups,
    kbMatches,
    agents,
    agentMatches,
    sessionMatches,
    totalChunks,
    totalMessages,
    clearResults,
    ensureKbs,
    ensureAgents,
  }
}
