import { onMounted, onUnmounted } from 'vue'

export const ASSISTANT_DATA_CHANGE_EVENT = 'weknora:assistant-data-change'

export interface AssistantDataChange {
  operation: string
  area: string
  ids: { knowledgeBaseIds: string[]; knowledgeIds: string[]; agentIds: string[] }
}

type ChangeHandler = (changes: AssistantDataChange[]) => void
interface ChangeOptions { areas?: string[]; debounceMs?: number }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function stringIds(value: unknown): string[] {
  return Array.isArray(value)
    ? [...new Set(value.filter((id): id is string => typeof id === 'string').map(id => id.trim()).filter(Boolean))]
    : []
}

export function emitAssistantDataChange(raw: unknown): void {
  if (typeof window === 'undefined' || !isRecord(raw)) return
  if (typeof raw.operation !== 'string' || typeof raw.area !== 'string') return
  const operation = raw.operation.trim()
  const area = raw.area.trim()
  if (!area || !operation.startsWith(`${area}.`) || operation.length <= area.length + 1) return
  if (raw.ids !== undefined && !isRecord(raw.ids)) return
  const ids = isRecord(raw.ids) ? raw.ids : {}
  const change: AssistantDataChange = {
    operation, area,
    ids: {
      knowledgeBaseIds: stringIds(ids.knowledge_base_id),
      knowledgeIds: stringIds(ids.knowledge_id),
      agentIds: stringIds(ids.agent_id),
    },
  }
  window.dispatchEvent(new CustomEvent(ASSISTANT_DATA_CHANGE_EVENT, { detail: change }))
}

export function subscribeAssistantDataChange(handler: ChangeHandler, { areas, debounceMs = 300 }: ChangeOptions = {}) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let pending: AssistantDataChange[] = []
  const listener = (event: Event) => {
    const change = (event as CustomEvent<AssistantDataChange>).detail
    if (!change || !isRecord(change.ids) || typeof change.operation !== 'string' || typeof change.area !== 'string') return
    const { knowledgeBaseIds, knowledgeIds, agentIds } = change.ids
    if (![knowledgeBaseIds, knowledgeIds, agentIds].every(ids =>
      Array.isArray(ids) && ids.every(id => typeof id === 'string'))) return
    if (areas && !areas.includes(change.area)) return
    pending.push(change)
    clearTimeout(timer)
    timer = setTimeout(() => {
      const changes = pending
      pending = []
      timer = undefined
      handler(changes)
    }, debounceMs)
  }
  window.addEventListener(ASSISTANT_DATA_CHANGE_EVENT, listener)
  return () => {
    window.removeEventListener(ASSISTANT_DATA_CHANGE_EVENT, listener)
    clearTimeout(timer)
    pending = []
  }
}

export function useAssistantDataChange(handler: ChangeHandler, options: ChangeOptions = {}) {
  let unsubscribe: (() => void) | undefined
  onMounted(() => { unsubscribe = subscribeAssistantDataChange(handler, options) })
  onUnmounted(() => { unsubscribe?.() })
}
