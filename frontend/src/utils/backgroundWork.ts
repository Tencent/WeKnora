import type { BackgroundWorkItem } from '../api/chat/background-work'

function itemTime(item: BackgroundWorkItem): number {
  const parsed = Date.parse(item.started_at ?? item.finished_at ?? '')
  return Number.isFinite(parsed) ? parsed : 0
}

function sameWorkItem(a: BackgroundWorkItem, b: BackgroundWorkItem): boolean {
  return a.work_id === b.work_id
    && a.kind === b.kind
    && a.title === b.title
    && a.state === b.state
    && a.started_at === b.started_at
    && a.finished_at === b.finished_at
}

function compareWork(a: BackgroundWorkItem, b: BackgroundWorkItem): number {
  const rank = (item: BackgroundWorkItem) => item.state === 'running' ? 0 : 1
  const byState = rank(a) - rank(b)
  if (byState !== 0) return byState
  return itemTime(a) - itemTime(b)
}

/**
 * Merge by work_id. Incoming wins. Unchanged items keep their object identity,
 * and an unchanged list keeps the same array, so a poll tick does not rerender.
 * Running rows stay above terminal ones; within a group, earlier times come first.
 */
export function mergeBackgroundWork(
  existing: BackgroundWorkItem[],
  incoming: BackgroundWorkItem[],
): BackgroundWorkItem[] {
  const byID = new Map<string, BackgroundWorkItem>()
  for (const item of existing) byID.set(item.work_id, item)
  for (const item of incoming) {
    const prev = byID.get(item.work_id)
    if (prev && sameWorkItem(prev, item)) continue
    byID.set(item.work_id, item)
  }
  const sorted = [...byID.values()].sort(compareWork)
  if (sorted.length === existing.length && sorted.every((item, index) => item === existing[index])) {
    return existing
  }
  return sorted
}

export interface PollerOptions {
  /** Current session, or a getter so a switch can drop the in-flight response. */
  sessionId: string | (() => string)
  intervalMs?: number
  list: (
    sessionId: string,
    signal: AbortSignal,
  ) => Promise<BackgroundWorkItem[] | { data?: BackgroundWorkItem[] | null }>
  onUpdate: (items: BackgroundWorkItem[]) => void
  onError?: (message: string) => void
}

function readSessionId(sessionId: PollerOptions['sessionId']): string {
  return typeof sessionId === 'function' ? sessionId() : sessionId
}

function asWorkItems(
  value: BackgroundWorkItem[] | { data?: BackgroundWorkItem[] | null } | null | undefined,
): BackgroundWorkItem[] {
  if (Array.isArray(value)) return value
  if (value && Array.isArray(value.data)) return value.data
  return []
}

export function createBackgroundWorkPoller(options: PollerOptions): {
  start(): void; stop(): void; refresh(): Promise<void>; dispose(): void
} {
  const intervalMs = options.intervalMs ?? 5000
  let timer: ReturnType<typeof setTimeout> | null = null
  let abort: AbortController | null = null
  let running = false
  let disposed = false

  const stop = () => {
    running = false
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
    abort?.abort()
    abort = null
  }

  const tick = async () => {
    if (!running || disposed) return
    const sessionId = readSessionId(options.sessionId)
    const controller = new AbortController()
    abort?.abort()
    abort = controller
    try {
      const raw = await options.list(sessionId, controller.signal)
      if (!running || controller.signal.aborted) return
      if (readSessionId(options.sessionId) !== sessionId) {
        void tick()
        return
      }
      const items = asWorkItems(raw)
      options.onUpdate(items)
      if (!running) return
      if (items.length === 0) {
        running = false
        timer = null
        return
      }
      timer = setTimeout(() => { void tick() }, intervalMs)
    } catch (err) {
      if (!running || controller.signal.aborted) return
      options.onError?.(err instanceof Error ? err.message : String(err))
      if (!running) return
      timer = setTimeout(() => { void tick() }, intervalMs)
    }
  }

  return {
    start() {
      if (running || disposed) return
      running = true
      void tick()
    },
    stop,
    async refresh() {
      if (disposed) return
      // Manual refreshes use the same abort/generation boundary as polling.
      // A newer request, session switch or unmount invalidates their results.
      stop()
      running = true
      await tick()
    },
    dispose() {
      disposed = true
      stop()
    },
  }
}
