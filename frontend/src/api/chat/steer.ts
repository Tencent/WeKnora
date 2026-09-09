import { del, get, post } from "../../utils/request";

export type SteerDelivery = 'inject' | 'after'

export type SteerQueueItem = {
  steer_id: string
  content: string
  delivery: SteerDelivery
  mentioned_items?: unknown[]
  promoting?: boolean
  awaitingIdleSend?: boolean
  // True while POST /steer is in flight. The overlay uses a client-side
  // id until the server replies with the durable one; promote/remove are
  // disabled in that window so they cannot hit /steer/{client_id}.
  pending?: boolean
  client_id?: string
}

/**
 * Append a message to a running agent turn.
 * Returns { success, status: 'queued' | 'new_run', steer_id?, delivery?, assistant_message_id? }.
 * - delivery 'after' (default): waits until the current run exits, then starts a follow-up turn.
 * - delivery 'inject': the running engine injects it at the next round boundary.
 * - 'new_run': no run is live; the caller should fall back to a normal send.
 * Lookup failures (503) reject rather than returning new_run — treating them
 * as idle would start a second turn on top of the one still generating.
 */
export async function steerSession(
  session_id: string,
  query: string,
  mentionedItems: any[] = [],
  delivery: SteerDelivery = 'after',
) {
  return post(`/api/v1/sessions/${session_id}/steer`, {
    query,
    mentioned_items: mentionedItems,
    channel: 'web',
    delivery,
  });
}

/** Flip a queued after-message to inject so the running turn reads it next. */
export async function promoteSteerSession(session_id: string, steer_id: string) {
  return post(`/api/v1/sessions/${session_id}/steer/${steer_id}/inject`, {});
}

/** Pending overlay items for the live run. Empty when nothing is generating. */
export async function listSteerSession(session_id: string) {
  return get(`/api/v1/sessions/${session_id}/steer`);
}

/** Drop a queued overlay item so it is neither injected nor sent as a follow-up. */
export async function removeSteerSession(session_id: string, steer_id: string) {
  return del(`/api/v1/sessions/${session_id}/steer/${steer_id}`);
}
