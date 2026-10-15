// All polling paths are chosen locally. Tool output cannot supply a URL or verb.
export type TaskKind = 'parse' | 'move' | 'sync' | 'evaluation' | 'faq_import' | 'unknown'
export type TaskState = 'running' | 'completed' | 'partial' | 'failed' | 'cancelled' | 'unknown'
export interface AssistantTask {
  kind: TaskKind; id: string; callId: string; sessionId: string; operation: string
  state: TaskState; change: Record<string, unknown>; startedAt: number
}
export const ASSISTANT_TASK_EVENT = 'weknora:assistant-task'
const kinds = new Set<TaskKind>(['parse', 'move', 'sync', 'evaluation', 'faq_import', 'unknown'])
const record = (x: unknown): x is Record<string, unknown> => typeof x === 'object' && x !== null && !Array.isArray(x)
const validID = (x: unknown): x is string => typeof x === 'string' && x.length > 0 && x.length <= 256 && !/[\x00-\x1f]/.test(x)

export function tasksFromEvent(raw: unknown, owner: string, now = Date.now()): AssistantTask[] {
  if (!record(raw) || `${raw.user_id}:${raw.tenant_id}` !== owner || !validID(raw.session_id)) return []
  if (!record(raw.change) || typeof raw.change.operation !== 'string' || !Array.isArray(raw.tasks)) return []
  const change = raw.change
  const operation = String(change.operation)
  return raw.tasks.slice(0, 20).filter((t): t is {kind: TaskKind; id: string} =>
    record(t) && kinds.has(t.kind as TaskKind) && validID(t.id)).map(t => ({
      ...t, callId: typeof raw.call_id === 'string' ? raw.call_id : String(raw.started_at || ''),
      sessionId: raw.session_id as string, operation, change,
      startedAt: typeof raw.started_at === 'number' && raw.started_at > 0 ? Math.min(raw.started_at, now) : now, state: t.kind === 'unknown' ? 'unknown' : 'running',
    }))
}

export function taskStatusPath(task: Pick<AssistantTask, 'kind' | 'id'>): string | undefined {
  const id = encodeURIComponent(task.id)
  switch (task.kind) {
    case 'parse': return `/api/v1/knowledge/${id}/spans`
    case 'move': return `/api/v1/knowledge/move/progress/${id}`
    case 'sync': return `/api/v1/datasource/logs/${id}`
    case 'evaluation': return `/api/v1/evaluation?task_id=${id}`
    case 'faq_import': return `/api/v1/faq/import/progress/${id}`
    default: return undefined
  }
}

export function taskState(kind: TaskKind, response: unknown): TaskState {
  if (!record(response) || response.success === false || Number(response.$httpStatus || 200) >= 400) return 'unknown'
  const d = record(response.data) ? response.data : response
  const task = kind === 'evaluation' && record(d.task) ? d.task : d
  const status = kind === 'parse' ? task.parse_status : task.status
  if (kind === 'evaluation') return ({0: 'running', 1: 'running', 2: 'completed', 3: 'failed'} as Record<string, TaskState>)[String(status)] || 'unknown'
  if (['pending', 'processing', 'running', 'finalizing'].includes(String(status))) return 'running'
  if (['canceled', 'cancelled'].includes(String(status))) return 'cancelled'
  if (status === 'failed') return 'failed'
  if (status === 'partial') return 'partial'
  if (['success', 'completed'].includes(String(status))) {
    return Number(d.failed || 0) + Number(d.failed_count || 0) + Number(d.partial_failed_count || 0) > 0 ? 'partial' : 'completed'
  }
  return 'unknown'
}

export function emitAssistantTasks(raw: unknown) {
  if (typeof window !== 'undefined') window.dispatchEvent(new CustomEvent(ASSISTANT_TASK_EVENT, {detail: raw}))
}
