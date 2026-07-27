import { get, post } from '@/utils/request'
import type { StructuredAnswer } from '@/utils/structuredQuestion'
import type { PendingUserInputSnapshot } from '@/utils/structuredQuestionEvents'

export async function resolveUserInput(pendingId: string, answer: StructuredAnswer): Promise<void> {
  await post(`/api/v1/agent/user-inputs/${encodeURIComponent(pendingId)}`, answer)
}

export function getPendingUserInput(sessionId: string): Promise<PendingUserInputSnapshot> {
  return get(`/api/v1/agent/user-inputs/pending?session_id=${encodeURIComponent(sessionId)}`)
}
