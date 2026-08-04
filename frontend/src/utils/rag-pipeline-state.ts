/** How long the wait row stays hidden so a fast model answer never flashes it. */
export const RAG_WAIT_REVEAL_DELAY_MS = 250

/**
 * How long the wait row keeps claiming progress. A dropped SSE connection never
 * sets `is_completed` (the stream layer only raises a toast), so without this cap
 * the row would promise an answer forever.
 */
export const RAG_WAIT_STALL_DELAY_MS = 60_000

export type RagWaitKind = 'none' | 'preparing' | 'model'

export interface RagPipelineWaitInput {
  isCompleted: boolean
  hasAnswer: boolean
  hasThinkingEvent: boolean
  stepCount: number
  allStepsDone: boolean
  hasCompletedRetrievalStep: boolean
}

/**
 * Describe the quiet gap after every visible RAG pipeline step has finished and
 * before the model emits thinking or answer text.
 *
 * `model` is only claimed once retrieval actually finished; turns that never run
 * a retrieval step (attachment-only Q&A) still get the neutral `preparing` row
 * rather than no feedback at all.
 */
export function getRagPipelineWaitKind(state: RagPipelineWaitInput): RagWaitKind {
  if (state.isCompleted || state.hasAnswer || state.hasThinkingEvent) return 'none'
  if (state.stepCount === 0 || !state.allStepsDone) return 'none'
  return state.hasCompletedRetrievalStep ? 'model' : 'preparing'
}

export interface RagWaitView {
  kind: RagWaitKind
  stalled: boolean
}

export interface RagWaitScheduler {
  setTimeout: (callback: () => void, ms: number) => unknown
  clearTimeout: (handle: unknown) => void
}

export interface RagWaitController {
  update: (kind: RagWaitKind) => void
  dispose: () => void
}

const defaultScheduler: RagWaitScheduler = {
  setTimeout: (callback, ms) => setTimeout(callback, ms),
  clearTimeout: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
}

/**
 * Turn the raw wait kind into what the timeline renders: delayed reveal, in-place
 * label swaps once visible, and a stalled state when the answer never arrives.
 */
export function createRagWaitController(
  onChange: (view: RagWaitView) => void,
  scheduler: RagWaitScheduler = defaultScheduler,
): RagWaitController {
  let target: RagWaitKind = 'none'
  let view: RagWaitView = { kind: 'none', stalled: false }
  let revealHandle: unknown
  let stallHandle: unknown

  const cancelTimers = () => {
    if (revealHandle !== undefined) {
      scheduler.clearTimeout(revealHandle)
      revealHandle = undefined
    }
    if (stallHandle !== undefined) {
      scheduler.clearTimeout(stallHandle)
      stallHandle = undefined
    }
  }

  const emit = (next: RagWaitView) => {
    if (next.kind === view.kind && next.stalled === view.stalled) return
    view = next
    onChange(view)
  }

  const armStall = () => {
    stallHandle = scheduler.setTimeout(() => {
      stallHandle = undefined
      emit({ kind: target, stalled: true })
    }, RAG_WAIT_STALL_DELAY_MS)
  }

  return {
    update(kind) {
      if (kind === target) return
      target = kind
      cancelTimers()

      if (kind === 'none') {
        emit({ kind: 'none', stalled: false })
        return
      }

      if (view.kind !== 'none') {
        emit({ kind, stalled: false })
        armStall()
        return
      }

      revealHandle = scheduler.setTimeout(() => {
        revealHandle = undefined
        emit({ kind: target, stalled: false })
        armStall()
      }, RAG_WAIT_REVEAL_DELAY_MS)
    },
    dispose() {
      cancelTimers()
      target = 'none'
    },
  }
}
