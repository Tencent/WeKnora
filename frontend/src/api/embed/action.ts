import { post } from '@/utils/request'

// A2UI confirm/form card click over the embed (iframe) channel. Separate tiny
// module on purpose: importing it from api/embed/index.ts would drag the
// whole postMessage bridge into the main-chat bundle via AgentStreamDisplay.
// The URL/headers mirror stopEmbedSession: the signed session handle rides in
// a header (never a query param, so it stays out of access logs), and the
// explicit `Embed` Authorization outrides the default axios auth interceptor
// — a locally logged-in user's weknora_token must never ride into channel
// calls.
export async function executeEmbedAgentAction(
  channelId: string,
  token: string,
  sessionId: string,
  sig: string,
  visitor: string,
  payload: Record<string, unknown>,
) {
  const headers: Record<string, string> = {
    Authorization: `Embed ${token}`,
    'X-Embed-Session': sig,
  }
  if (visitor) headers['X-Embed-Visitor'] = visitor
  return post(
    `/api/v1/embed/${channelId}/sessions/${sessionId}/agent-actions/execute`,
    payload,
    { headers },
  )
}
