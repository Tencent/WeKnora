import { ref } from 'vue'

export const HOST_APPROVAL_KIND = 'host_command'

// The open chat reads this directly. Several tools can wait at once when
// parallel calls are on, so this is a list keyed by pending_id, not one slot.
export const pendingComposerApprovals = ref<Record<string, any>[]>([])

export function showComposerApproval(event: Record<string, any>) {
  const id = event?.pending_id
  if (!id) return
  const list = pendingComposerApprovals.value
  const index = list.findIndex((item) => item.pending_id === id)
  if (index >= 0) {
    const next = list.slice()
    next[index] = event
    pendingComposerApprovals.value = next
    return
  }
  pendingComposerApprovals.value = [...list, event]
}

// A stored agent-step approval becomes the same timeline event the live
// stream used, already resolved so it stays as a record.
export function approvalEventFromRecord(record: Record<string, any>) {
  return {
    type: 'tool_approval_required',
    pending_id: record.pending_id,
    service_name: record.service_name,
    mcp_tool_name: record.mcp_tool_name,
    description: record.description,
    args_json: record.args_json,
    timeout_seconds: record.timeout_seconds,
    requested_at: record.requested_at,
    tool_call_id: record.tool_call_id,
    kind: record.kind,
    host: record.host,
    resolved: record.resolved === true,
    approved: record.approved,
    resolve_reason: record.reason,
    timed_out: record.timed_out,
    canceled: record.canceled,
    resolve_scope: record.scope,
  }
}

export function clearComposerApproval(pendingId?: string) {
  if (!pendingId) {
    pendingComposerApprovals.value = []
    return
  }
  pendingComposerApprovals.value = pendingComposerApprovals.value.filter(
    (item) => item.pending_id !== pendingId,
  )
}

export interface HostApprovalPayload {
  reason: string
  reviewer?: 'user' | 'model'
  risk_level?: string
  tool?: string
  command?: string
  cwd?: string
  grant_path?: string
  grant_access?: string
  denial_snippet?: string
  first_attempt_ran?: boolean
  allow_session?: boolean
  session_rules?: string[]
  file_path?: string
  write_mode?: 'overwrite' | 'append' | 'edit'
  write_bytes?: number
  write_preview?: string
  env?: Record<string, string>
  skill_name?: string
  stdin?: string
  scripts?: { path: string; content: string }[]
}

// What "allow for this session" remembers for a delete. The trailing … says
// the file names may differ next time.
export function sessionRuleLabels(host: HostApprovalPayload): string[] {
  if (host.reason !== 'delete' || !host.allow_session) return []
  return (host.session_rules || []).map((rule) => `${rule} …`)
}

// A delete card can carry the directory the delete needs, but removing a file
// takes write access, so only a sandbox block offers read-only.
export function canNarrowAccess(host: HostApprovalPayload): boolean {
  return host.reason === 'sandbox_denied' && !!host.grant_path
}

export type HostApprovalScope = 'once' | 'session'
export type HostApprovalAccess = 'read' | 'write'

export interface HostResolveBody {
  decision: 'approve' | 'reject'
  scope?: HostApprovalScope
  access?: HostApprovalAccess
  reason?: string
}

export function isHostApprovalEvent(event: { kind?: string; host?: unknown }): boolean {
  return event.kind === HOST_APPROVAL_KIND || event.host != null
}

export function hostApprovalTitleKey(reason: string): string {
  switch (reason) {
    case 'delete':
      return 'agentStream.hostApproval.titleDelete'
    case 'sandbox_denied':
      return 'agentStream.hostApproval.titleDenied'
    case 'file_write':
      return 'agentStream.hostApproval.titleFileWrite'
    default:
      return 'agentStream.hostApproval.titleGeneric'
  }
}

// The server re-checks both rules; this keeps the card from sending a
// request it would refuse.
export function buildHostResolveBody(
  decision: 'approve' | 'reject',
  opts: {
    scope?: HostApprovalScope
    access?: HostApprovalAccess
    proposedAccess?: string
    allowSession?: boolean
    reason?: string
  },
): HostResolveBody {
  if (decision === 'reject') return { decision, reason: opts.reason }
  const body: HostResolveBody = {
    decision,
    scope: opts.scope === 'session' && opts.allowSession ? 'session' : 'once',
  }
  if (opts.access === 'read' && opts.proposedAccess === 'write') body.access = 'read'
  return body
}

// Auto mode: the review model answered. No card, only a timeline row.
export function isModelReview(event: { host?: unknown } | null | undefined): boolean {
  const host = event?.host
  if (host == null || typeof host !== 'object') return false
  return (host as { reviewer?: unknown }).reviewer === 'model'
}

export function hostReviewLabelKey(event: { resolved?: boolean; approved?: boolean }): string {
  if (!event.resolved) return 'agentStream.hostReview.reviewing'
  return event.approved ? 'agentStream.hostReview.approved' : 'agentStream.hostReview.denied'
}

// A blocked command already ran once, so approving repeats its side effects.
export function showsRerunNotice(host: HostApprovalPayload, resolved?: boolean): boolean {
  return !!host.first_attempt_ran && !resolved
}

// What the command runs with besides its own text, as NAME=value lines.
export function hostEnvLines(host: HostApprovalPayload): string[] {
  return Object.entries(host.env || {})
    .map(([name, value]) => `${name}=${value}`)
    .sort()
}
