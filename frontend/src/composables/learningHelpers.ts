import type { LearningMasteryView, LearningQuizView } from '../api/learning'

export const learningStates = ['unseen', 'learning', 'mastered', 'review_due'] as const
export const learningColors: Record<LearningMasteryView['state'], string> = {
  unseen: '#8c8c8c', learning: '#0052d9', mastered: '#2ba471', review_due: '#e37318',
}
export type LearningScope = { tenantId: string; userId: string; kbId: string; available: boolean }
export const learningScopeKey = (scope: LearningScope) => JSON.stringify([scope.tenantId, scope.userId, scope.kbId, scope.available])
export const learningPrincipalKey = (scope: LearningScope) => JSON.stringify([scope.tenantId, scope.userId])
export const eligibleLearningPage = (page?: { status: string; page_type: string } | null) =>
  page?.status === 'published' && ['entity', 'concept', 'synthesis', 'comparison'].includes(page.page_type)

export const POLL_LIMIT = 12
export function learningAttemptId(source: Pick<Crypto, 'getRandomValues'> & { randomUUID?: () => string } = crypto): string {
  if (source.randomUUID) return source.randomUUID()
  // getRandomValues is also available on HTTP intranet deployments.
  const bytes = source.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6] & 15) | 64
  bytes[8] = (bytes[8] & 63) | 128
  const hex = Array.from(bytes, byte => byte.toString(16).padStart(2, '0'))
  return [hex.slice(0, 4), hex.slice(4, 6), hex.slice(6, 8), hex.slice(8, 10), hex.slice(10)].map(part => part.join('')).join('-')
}
export const pollDelay = (attempt: number) => Math.min(1000 * 2 ** attempt, 10000)
export function waitForPoll(delay: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const abort = () => { clearTimeout(timer); reject(new Error('cancelled')) }
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve() }, delay)
    if (signal.aborted) abort()
    else signal.addEventListener('abort', abort, { once: true })
  })
}

// Identity fences reject even transports that finish after cancellation.
export function createLearningRequests() {
  const lanes = new Map<string, AbortController>()
  function cancel(lane: string) { lanes.get(lane)?.abort(); lanes.delete(lane) }
  return {
    start(lane: string) {
      cancel(lane)
      const controller = new AbortController()
      lanes.set(lane, controller)
      return { signal: controller.signal, current: () => lanes.get(lane) === controller && !controller.signal.aborted }
    },
    cancel,
    reset() { for (const lane of lanes.keys()) cancel(lane) },
  }
}

export function learningError(error: unknown): string {
  const value = error as { status?: number; error?: { code?: string }; code?: string } | null
  const code = String(value?.error?.code || value?.code || '').toLowerCase().replace(/^err/, '')
  if (code.includes('disabled')) return 'disabled'
  if (value?.status === 403 || code.includes('forbidden')) return 'unavailable'
  if (value?.status === 404 || code.includes('notfound') || code.includes('not_found')) return 'notFound'
  if (code.includes('stale')) return 'stale'
  if (code.includes('notready') || code.includes('not_ready')) return 'notReady'
  if (code.includes('conflict') || value?.status === 409) return 'conflict'
  if (code.includes('busy') || value?.status === 429) return 'busy'
  if (code.includes('evidence')) return 'evidence'
  return 'request'
}

// Tool transcripts never enter this function; they only supply resource IDs.
export function publicLearningQuiz(quiz: LearningQuizView): LearningQuizView {
  return {
    id: quiz.id, page_id: quiz.page_id, knowledge_base_id: quiz.knowledge_base_id,
    slug: quiz.slug, title: quiz.title, status: quiz.status,
    error_code: quiz.error_code, algorithm_version: quiz.algorithm_version,
    questions: quiz.status !== 'ready' ? [] : (quiz.questions || []).map(question => {
      const base = { id: question.id, prompt: question.prompt,
        options: (question.options || []).map(option => ({ id: option.id, text: option.text })) }
      return question.answered === true
        ? { ...base, answered: true, result: question.result }
        : { ...base, answered: false }
    }),
  }
}

const pointerPrefix = 'weknora.learning.quiz:'
export function learningQuizPointers(storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem' | 'key' | 'length'>) {
  const key = (scope: LearningScope, page: string) => pointerPrefix + JSON.stringify([scope.tenantId, scope.userId, scope.kbId, page])
  return {
    get(scope: LearningScope, page: string) {
      try { return storage?.getItem(key(scope, page)) || '' } catch { return '' }
    },
    set(scope: LearningScope, page: string, quiz: string) {
      try { storage?.setItem(key(scope, page), quiz) } catch { /* HTTP remains authoritative without storage. */ }
    },
    clear(scope: LearningScope, allKBs = false) {
      if (!storage) return
      try {
        const keys = Array.from({ length: storage.length }, (_, index) => storage.key(index))
        for (const entry of keys) {
          if (!entry?.startsWith(pointerPrefix)) continue
          const [tenant, user, kb] = JSON.parse(entry.slice(pointerPrefix.length))
          if (tenant === scope.tenantId && user === scope.userId && (allKBs || kb === scope.kbId)) storage.removeItem(entry)
        }
      } catch { /* Storage may be disabled. */ }
    },
  }
}

type LearningChange = { principal: string; kbId?: string; origin: symbol; phase: 'invalidate' | 'reload' | 'assessment' }
const listeners = new Set<(change: LearningChange) => void>()
export const learningChanges = {
  emit(change: LearningChange) { for (const listener of listeners) listener(change) },
  subscribe(listener: (change: LearningChange) => void) { listeners.add(listener); return () => listeners.delete(listener) },
}

export function learningToolDisplayType(toolName?: string) {
  const types = { get_learning_profile: 'learning_profile', recommend_learning_topics: 'learning_recommendations', prepare_learning_quiz: 'learning_quiz' } as const
  return types[toolName as keyof typeof types]
}

export function learningToolReference(output?: string, data?: unknown) {
  const object = (value: unknown): Record<string, unknown> => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
  const parse = (value: unknown) => { try { return object(JSON.parse(String(value || ''))) } catch { return {} } }
  const live = object(data)
  const payload = { ...parse(live.output), ...parse(output) }
  for (const key of ['display_type', 'quiz_id', 'knowledge_base_id']) {
    if (typeof live[key] === 'string') payload[key] = live[key]
  }
  const text = (key: string) => typeof payload[key] === 'string' ? payload[key] as string : ''
  const display_type = text('display_type')
  if (!['learning_quiz', 'learning_profile', 'learning_recommendations'].includes(display_type)) return null
  // Do not retain questions, feedback or profile data from model context.
  return { display_type, quiz_id: text('quiz_id'), knowledge_base_id: text('knowledge_base_id') }
}
