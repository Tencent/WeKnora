import { post } from "../../utils/request";

// Agent inbox UI signals (agent-sidebar-inbox P1).
// The backend whitelist (handler.PostUIEvent) only accepts these types;
// anything else is rejected — keep this list in sync with it.

export type AgentInboxUIEventType =
  | 'user.viewed_page'
  | 'user.switched_tab'
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
