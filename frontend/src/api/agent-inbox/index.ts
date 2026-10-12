import { get, post } from "../../utils/request";

// Agent inbox UI signals (agent-sidebar-inbox P1).
// The backend whitelist (handler.PostUIEvent) only accepts these types;
// anything else is rejected — keep this list in sync with it.

export type AgentInboxUIEventType =
  | 'user.viewed_page'
  | 'user.viewed_document'
  | 'user.viewed_image'
  | 'user.switched_tab'
  | 'user.switched_kb'
  | 'user.error_toast';

export interface AgentInboxUIDetail {
  slug?: string;
  title?: string;
  tab?: string;
  message?: string;
  [key: string]: unknown;
}

/**
 * Report one UI-side signal for the agent inbox. Best-effort by design:
 * failures are swallowed — inbox availability must never break browsing.
 */
export async function postUIEvent(
  kbId: string,
  type: AgentInboxUIEventType,
  detail: AgentInboxUIDetail = {}
): Promise<void> {
  if (!kbId) return;
  try {
    await post(`/api/v1/knowledge-bases/${kbId}/ui-events`, { type, detail });
  } catch (_) {
    /* ignore — best-effort */
  }
}

// ── User-facing inbox panel (agent-sidebar-inbox Phase 3) ───────────────

/** One digest row as returned by GET /agent-inbox/events. */
export interface AgentInboxDigest {
  id: string;
  type: string;
  priority: string;
  /** RFC3339 timestamp. */
  time: string;
  payload?: Record<string, unknown>;
  /**
   * Shared read state (row-level read_at): the agent's check_inbox with
   * mark_read=true also consumes unread state, so treat it as a hint.
   */
  read?: boolean;
}

export interface AgentInboxListParams {
  /** Lookback window in minutes (default 1440, max 10080). */
  sinceMinutes?: number;
  /** Optional event type filter. */
  types?: string[];
  /** Optional KB scope filter. */
  kbIds?: string[];
  unreadOnly?: boolean;
  limit?: number;
}

/**
 * Read the inbox as the signed-in user: own user-scoped events plus
 * tenant-wide ones (parse lifecycle, external ingest). Read-only —
 * marking read goes through markInboxRead.
 */
export async function fetchInboxEvents(
  params: AgentInboxListParams = {}
): Promise<{ events: AgentInboxDigest[]; unread: number }> {
  const qs = new URLSearchParams();
  if (params.sinceMinutes) qs.set('since_minutes', String(params.sinceMinutes));
  if (params.types?.length) qs.set('types', params.types.join(','));
  if (params.kbIds?.length) qs.set('kb_ids', params.kbIds.join(','));
  if (params.unreadOnly) qs.set('unread_only', 'true');
  if (params.limit) qs.set('limit', String(params.limit));
  const suffix = qs.toString() ? `?${qs.toString()}` : '';
  const res: any = await get(`/api/v1/agent-inbox/events${suffix}`);
  return {
    events: res?.data?.events || [],
    unread: Number(res?.data?.unread) || 0,
  };
}

/** Mark the given inbox events as read. */
export async function markInboxRead(ids: string[]): Promise<void> {
  if (!ids.length) return;
  await post('/api/v1/agent-inbox/events/read', { ids });
}
