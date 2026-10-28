import { computed, inject, provide, reactive, ref, type ComputedRef, type InjectionKey, type Ref } from 'vue'
import { isAwaitingInput } from '@/composables/useChatStreamHandler'
import { persistedAssistantId } from '@/utils/steerStreamFork'
import { isHostApprovalEvent } from '@/views/chat/components/hostApproval'

type PromptEvent = Record<string, any>
type ChatRow = Record<string, unknown>

const DOCKED_TYPES = new Set(['tool_approval_required', 'agent_prompt_required', 'mcp_oauth_required'])

// Answers given on this page, by pending id. The stream reports a prompt
// resolved only once its run consumes the answer, which for a suspended run
// is after it resumes; until then this keeps the prompt out of the dock and
// lets its timeline row show what was sent.
const submissions = reactive(new Map<string, Record<string, unknown>>())

export function markPromptSubmitted(pendingId: string, answer: Record<string, unknown> = {}) {
  submissions.set(pendingId, answer)
}

export function promptSubmission(pendingId: string): Record<string, unknown> | undefined {
  return submissions.get(pendingId)
}

function isDockable(e: PromptEvent): boolean {
  if (!DOCKED_TYPES.has(String(e?.type || '')) || e.resolved || !e.pending_id) return false
  // Lite host commands use the composer card, which shows the command and grant.
  if (e.type === 'tool_approval_required' && isHostApprovalEvent(e)) return false
  if (e.type === 'agent_prompt_required' && e.kind !== 'ask_user') return false
  if (submissions.has(e.pending_id)) return false
  if (e.suspended && e.expires_at) {
    const expires = Date.parse(String(e.expires_at))
    if (!Number.isNaN(expires) && expires <= Date.now()) return false
  }
  return true
}

/**
 * Prompts still waiting on the user, oldest first. Only a run that is
 * streaming or suspended on input can be waiting; cards in settled messages
 * are history.
 */
export function pendingPrompts(messages: ChatRow[]): PromptEvent[] {
  const out: PromptEvent[] = []
  const liveTurns = new Set(messages
    .filter(m => m.role === 'assistant' && (!m.is_completed || isAwaitingInput(m)))
    .map(persistedAssistantId).filter(Boolean))
  const seen = new Set<string>()
  for (const message of messages) {
    if (message.role !== 'assistant') continue
    // Steering seals a display segment, not the parent run or its children.
    // Keep that segment's cards actionable while its continuation is live.
    if (message.is_completed && !isAwaitingInput(message) &&
      !(message.steerForked && liveTurns.has(persistedAssistantId(message)))) continue
    for (const e of (message.agentEventStream as PromptEvent[] | undefined) || []) {
      if (isDockable(e) && !seen.has(e.pending_id)) {
        seen.add(e.pending_id)
        out.push(e)
      }
    }
  }
  return out
}

export interface PromptDock {
  prompts: ComputedRef<PromptEvent[]>
  activeId: Ref<string>
  collapsed: Ref<boolean>
  /** Bumped when something asks the dock to take focus. */
  focusRequest: Ref<number>
  isDocked: (pendingId: string) => boolean
  focus: (pendingId: string) => void
}

const promptDockKey: InjectionKey<PromptDock> = Symbol('promptDock')

/** Called by a chat view; its timeline cards and its dock share the result. */
export function providePromptDock(messages: ChatRow[]): PromptDock {
  const prompts = computed(() => pendingPrompts(messages))
  const activeId = ref('')
  const collapsed = ref(false)
  const focusRequest = ref(0)
  const dock: PromptDock = {
    prompts,
    activeId,
    collapsed,
    focusRequest,
    isDocked: (pendingId) => prompts.value.some((e) => e.pending_id === pendingId),
    focus: (pendingId) => {
      activeId.value = pendingId
      collapsed.value = false
      focusRequest.value++
    },
  }
  provide(promptDockKey, dock)
  return dock
}

export function usePromptDock(): PromptDock | null {
  return inject(promptDockKey, null)
}
