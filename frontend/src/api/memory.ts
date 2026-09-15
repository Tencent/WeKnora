import { get, put, post, del } from '@/utils/request'

// MemorySettings is already merged server-side, so the UI never has to combine
// a workspace switch with a personal one itself.
export interface MemorySettings {
  workspace_enabled: boolean
  user_enabled: boolean
  effective: boolean
  write_mode: string
  episode_count: number
  max_episodes: number
}

export interface MemoryConfig {
  enabled: boolean
  write_mode: 'explicit_only' | 'auto'
  extract_model_id: string
  max_episodes: number
  /** Debounce before distillation runs, in seconds. */
  extract_delay_seconds: number
  /** Floor between two distillation runs for one person, in seconds. */
  extract_min_interval_seconds: number
  /** Workspace-specific rules appended to the distillation prompt. */
  extract_instructions: string
  /** How many separate conversations a subject must appear in to count as a recurring one. */
  interest_threshold: number
  /** Whether memory may shape retrieval, not only the answer prompt. */
  retrieval_conditioning: boolean
  /** Model used to score memory against a question. Blank = lexical matching only. */
  embedding_model_id: string
  /** Whether recall also matches on meaning, not only on wording. */
  vector_recall: boolean
}

/** The single consolidated document about a person, injected on every turn. */
export interface MemoryDigest {
  body: string
  /** Bumped on every rewrite and on every save from this page. */
  revision: number
  /** How many conversation accounts the current body was written from. */
  episode_count: number
  /** Set once a human has edited the body; a rewrite would discard that wording. */
  user_edited_at: string | null
  generated_at: string | null
  created_at: string
  updated_at: string
}

/** Whether the conversation reached an answer, as judged when it was written up. */
/**
 * What the conversation achieved. A later reader needs it to know whether an
 * approach recorded in an account is one to repeat, since "we did X" and "we
 * tried X and it did not work" are the same text with opposite meanings. An
 * account that does not say how it ended is `uncertain`, never `success`.
 */
export type MemoryEpisodeOutcome = 'success' | 'partial' | 'fail' | 'uncertain'

/** One conversation written up as a narrative account, once it went quiet. */
export interface MemoryEpisode {
  id: string
  session_id: string
  slug: string
  title: string
  outcome: MemoryEpisodeOutcome
  summary: string
  keywords: string[]
  from_at: string
  to_at: string
  use_count: number
  last_used_at: string | null
  created_at: string
  updated_at: string
}

/** A sentence the user asked to have kept, stored word for word. */
export interface MemoryNote {
  id: string
  content: string
  source_session_id: string
  source_message_id: string
  created_at: string
  updated_at: string
}

/** The profile body is capped server-side; the editor surfaces the same number. */
export const MEMORY_PROFILE_MAX_LENGTH = 2400
/** Per-note length cap, enforced server-side. */
export const MEMORY_NOTE_MAX_LENGTH = 300
/** How many notes one person may hold at once. */
export const MEMORY_NOTE_MAX_COUNT = 20

// ---------------------------------------------------------------------------
// Personal memory. Every endpoint operates on the caller's own memory space,
// which the server derives from the request principal, so none of these take
// an owner parameter.
// ---------------------------------------------------------------------------

export function getMemorySettings() {
  return get<{ success: boolean; data: MemorySettings }>('/api/v1/memory/settings')
}

export function updateMemoryEnabled(enabled: boolean) {
  return put<{ success: boolean; data: MemorySettings }>('/api/v1/memory/settings', { enabled })
}

/** Resolves with `data: null` while too few conversations exist to write one. */
export function getMemoryProfile() {
  return get<{ success: boolean; data: MemoryDigest | null }>('/api/v1/memory/profile')
}

export function updateMemoryProfile(body: string) {
  return put<{ success: boolean; data: { revision: number } }>('/api/v1/memory/profile', { body })
}

export function deleteMemoryProfile() {
  return del<{ success: boolean }>('/api/v1/memory/profile')
}

export function listMemoryEpisodes(params: { limit?: number; offset?: number } = {}) {
  const query = new URLSearchParams()
  if (params.limit != null) query.set('limit', String(params.limit))
  if (params.offset != null) query.set('offset', String(params.offset))
  const suffix = query.toString() ? `?${query.toString()}` : ''
  return get<{ success: boolean; data: MemoryEpisode[]; total: number }>(
    `/api/v1/memory/episodes${suffix}`,
  )
}

/** The list carries titles only; the narrative itself has to be asked for. */
export function getMemoryEpisode(id: string) {
  return get<{ success: boolean; data: MemoryEpisode }>(
    `/api/v1/memory/episodes/${encodeURIComponent(id)}`,
  )
}

export function deleteMemoryEpisode(id: string) {
  return del<{ success: boolean }>(`/api/v1/memory/episodes/${encodeURIComponent(id)}`)
}

export function listMemoryNotes(params: { limit?: number } = {}) {
  const suffix = params.limit != null ? `?limit=${params.limit}` : ''
  return get<{ success: boolean; data: MemoryNote[] }>(`/api/v1/memory/notes${suffix}`)
}

export function createMemoryNote(content: string) {
  return post<{ success: boolean; data: MemoryNote }>('/api/v1/memory/notes', { content })
}

export function deleteMemoryNote(id: string) {
  return del<{ success: boolean }>(`/api/v1/memory/notes/${encodeURIComponent(id)}`)
}

/** Drops the profile, every account and every note in one call. */
export function clearAllMemories() {
  return del<{ success: boolean; removed: number }>('/api/v1/memory/all')
}

export interface MemoryExport {
  profile: MemoryDigest | null
  episodes: MemoryEpisode[]
  notes: MemoryNote[]
}

export function exportMemories() {
  return get<{ success: boolean; total: number; truncated: boolean; data: MemoryExport }>(
    '/api/v1/memory/export',
  )
}

/** Why a rewrite left the profile as it was. Empty when it did rewrite it. */
export type MemoryConsolidationSkip = 'too_soon' | 'too_few_items' | 'model_unavailable'

export interface MemoryConsolidationResult {
  merged: number
  demoted: number
  expired: number
  /** How many conversation accounts the rewrite read. */
  reviewed: number
  candidates: number
  skipped?: MemoryConsolidationSkip
}

/** Rewrite the consolidated profile now, without waiting for the scheduled pass. */
export function consolidateMemory() {
  return post<{ success: boolean; data: MemoryConsolidationResult }>('/api/v1/memory/consolidate', {})
}

// ---------------------------------------------------------------------------
// Workspace configuration, stored on the tenant like the other KV configs.
// ---------------------------------------------------------------------------

export function getTenantMemoryConfig() {
  return get<{ success: boolean; data: MemoryConfig }>('/api/v1/tenants/kv/memory-config')
}

export function updateTenantMemoryConfig(config: MemoryConfig) {
  return put<{ success: boolean; data: MemoryConfig }>('/api/v1/tenants/kv/memory-config', config)
}
