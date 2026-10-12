import {
  clearSessionMessages,
  delSession,
  pinSession,
  unpinSession,
  updateSession,
} from '@/api/chat'

export const SESSION_MUTATION_EVENT = 'weknora:session-mutation'

export interface SessionMutationPatch {
  title?: string
  is_pinned?: boolean
  pinned_at?: string | null
}

export interface SessionMutationDetail {
  sessionId: string
  patch?: SessionMutationPatch
  messagesCleared?: boolean
  removed?: boolean
  // Batch "delete all" has no per-id list the UI can enumerate — one event
  // tells listeners (e.g. the agent chat dock) that every session of the
  // user is gone. Carries no sessionId on purpose.
  allRemoved?: boolean
  // A session was created outside the main sidebar (agent chat dock, wiki
  // fix drawer, ...). Carries a minimal row so the sidebar can prepend it
  // to the web bucket without refetching.
  created?: boolean
  session?: {
    id: string
    title?: string
    updated_at?: string
  }
}

export function notifySessionMutation(detail: SessionMutationDetail): void {
  if (typeof window === 'undefined') return
  window.dispatchEvent(new CustomEvent<SessionMutationDetail>(SESSION_MUTATION_EVENT, { detail }))
}

export function notifySessionsAllRemoved(): void {
  notifySessionMutation({ sessionId: '', allRemoved: true })
}

export function notifySessionCreated(session: {
  id: string
  title?: string
  updated_at?: string
}): void {
  notifySessionMutation({ sessionId: session.id, created: true, session })
}

function ensureSuccess(response: any): any {
  if (!response?.success) {
    throw new Error(response?.message || 'session mutation failed')
  }
  return response
}

export async function renameSession(
  sessionId: string,
  title: string,
  description = '',
): Promise<any> {
  const response = ensureSuccess(await updateSession(sessionId, { title, description }))
  const nextTitle = response.data?.title || title
  notifySessionMutation({ sessionId, patch: { title: nextTitle } })
  return response.data
}

export async function setSessionPinned(sessionId: string, pinned: boolean): Promise<void> {
  const response = ensureSuccess(pinned ? await pinSession(sessionId) : await unpinSession(sessionId))
  notifySessionMutation({
    sessionId,
    patch: {
      is_pinned: pinned,
      pinned_at: pinned ? new Date().toISOString() : null,
    },
  })
}

export async function clearSession(sessionId: string): Promise<void> {
  ensureSuccess(await clearSessionMessages(sessionId))
  notifySessionMutation({ sessionId, messagesCleared: true })
}

export async function removeSession(sessionId: string): Promise<void> {
  ensureSuccess(await delSession(sessionId))
  notifySessionMutation({ sessionId, removed: true })
}
