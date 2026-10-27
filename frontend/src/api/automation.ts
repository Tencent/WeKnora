import { get, post, put, del } from '@/utils/request'
import type { AssistantAccessLevel } from '@/api/assistant'

// Shapes mirror internal/types/automation.go. Every endpoint works on the
// caller's own automations; the server derives the owner from the request.

export type AutomationTriggerType = 'cron' | 'once'
export type AutomationSessionMode = 'per_run' | 'persistent'
export type AutomationMisfirePolicy = 'skip' | 'run_once'
export type AutomationOverlapPolicy = 'skip' | 'queue'
export type AutomationRunStatus =
  | 'queued'
  | 'running'
  | 'awaiting_approval'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'skipped'
  | 'needs_attention'
export type AutomationRunTrigger = 'schedule' | 'manual' | 'misfire'

export interface AutomationTrigger {
  type: AutomationTriggerType
  /** Five fields (minute first), six (seconds first) or a descriptor such as @daily. */
  expr?: string
  /** When a once trigger fires, RFC 3339. */
  at?: string
  /** IANA zone; empty means UTC. */
  timezone?: string
}

export interface AutomationLimits {
  /** Execution time of one run, not counting approval waits. 0 uses the default. */
  max_duration_minutes?: number
}

export interface Automation {
  id: string
  name: string
  description: string
  enabled: boolean
  model_id: string
  instruction: string
  trigger: AutomationTrigger
  session_mode: AutomationSessionMode
  persistent_session_id: string
  carry_last_result: boolean
  misfire_policy: AutomationMisfirePolicy
  overlap_policy: AutomationOverlapPolicy
  access_level: AssistantAccessLevel
  approval_timeout_minutes: number
  limits: AutomationLimits
  notify_on_success: boolean
  next_run_at: string | null
  last_run_at: string | null
  last_success_at: string | null
  last_run_status: AutomationRunStatus | ''
  active_run_status?: 'queued' | 'running' | 'awaiting_approval' | ''
  consecutive_failures: number
  paused_reason: string
  created_at: string
  updated_at: string
}

/** Fields of a create or update; on update a field left out is kept. */
export type AutomationInput = Partial<Pick<Automation,
  | 'name'
  | 'description'
  | 'model_id'
  | 'instruction'
  | 'trigger'
  | 'session_mode'
  | 'carry_last_result'
  | 'misfire_policy'
  | 'overlap_policy'
  | 'access_level'
  | 'approval_timeout_minutes'
  | 'limits'
  | 'notify_on_success'
>>

export interface AutomationRun {
  id: string
  automation_id: string
  scheduled_at: string
  trigger_kind: AutomationRunTrigger
  status: AutomationRunStatus
  session_id: string
  user_message_id: string
  assistant_message_id: string
  pending_prompt_id: string
  cancel_requested: boolean
  answer: string
  error: string
  started_at: string | null
  finished_at: string | null
  created_at: string
}

export interface AutomationGrant {
  id: string
  grant_key: string
  label: string
  risk: string
  prompt_id: string
  created_at: string
  last_used_at: string | null
  /** Why the grant no longer applies, e.g. the agent changed. Empty while it does. */
  lapse?: string
}

export interface AutomationGrantCandidate {
  prompt_id: string
  grant_key: string
  label: string
  risk: string
  tool: string
  service_name?: string
  asked_at: string
}

const base = '/api/v1/automations'
const enc = encodeURIComponent

export function listAutomations() {
  return get<{ success: boolean; data: Automation[] }>(base)
}

export function getAutomation(id: string) {
  return get<{ success: boolean; data: Automation }>(`${base}/${enc(id)}`)
}

export function getAutomationBySession(sessionId: string) {
  return get<{ success: boolean; data: Automation }>(`${base}/by-session/${enc(sessionId)}`)
}

export function createAutomation(input: AutomationInput) {
  return post<{ success: boolean; data: Automation }>(base, input)
}

export function updateAutomation(id: string, input: AutomationInput) {
  return put<{ success: boolean; data: Automation }>(`${base}/${enc(id)}`, input)
}

export function deleteAutomation(id: string) {
  return del<{ success: boolean }>(`${base}/${enc(id)}`)
}

export function pauseAutomation(id: string) {
  return post<{ success: boolean; data: Automation }>(`${base}/${enc(id)}/pause`, {})
}

/** Schedule again from now and clear the failure count; missed runs are not caught up. */
export function resumeAutomation(id: string) {
  return post<{ success: boolean; data: Automation }>(`${base}/${enc(id)}/resume`, {})
}

export function runAutomationNow(id: string) {
  return post<{ success: boolean; data: AutomationRun }>(`${base}/${enc(id)}/run`, {})
}

/** The next three times trigger fires, in UTC; rejects an invalid or too frequent trigger. */
export function validateAutomationTrigger(trigger: AutomationTrigger) {
  return post<{ success: boolean; data: { next_runs: string[] } }>(`${base}/validate-trigger`, { trigger })
}

export function listAutomationRuns(id: string, page = 1, pageSize = 20) {
  return get<{ success: boolean; data: AutomationRun[]; total: number }>(
    `${base}/${enc(id)}/runs?page=${page}&page_size=${pageSize}`,
  )
}

export function cancelAutomationRun(runId: string) {
  return post<{ success: boolean; data: AutomationRun }>(`/api/v1/automation-runs/${enc(runId)}/cancel`, {})
}

export function listAutomationGrants(id: string) {
  return get<{ success: boolean; data: AutomationGrant[] }>(`${base}/${enc(id)}/grants`)
}

export function listAutomationGrantCandidates(id: string) {
  return get<{ success: boolean; data: AutomationGrantCandidate[] }>(`${base}/${enc(id)}/grant-candidates`)
}

/** Let later runs make the call a past run asked approval for without asking. */
export function addAutomationGrant(id: string, promptId: string) {
  return post<{ success: boolean; data: AutomationGrant }>(`${base}/${enc(id)}/grants`, { prompt_id: promptId })
}

export function revokeAutomationGrant(grantId: string) {
  return del<{ success: boolean }>(`/api/v1/agent/grants/${enc(grantId)}`)
}

// ---------------------------------------------------------------------------
// In-app notifications.
// ---------------------------------------------------------------------------

export type NotificationKind =
  | 'automation.succeeded'
  | 'automation.failed'
  | 'automation.awaiting_approval'
  | 'automation.paused'

export interface AppNotification {
  id: string
  kind: NotificationKind | string
  title: string
  body: string
  /** In-app route the notification opens. */
  link: string
  source_type: string
  source_id: string
  read_at: string | null
  created_at: string
}

export interface PendingNotification {
  id: string
  title: string
  kind: string
  link: string
  created_at: string
}

export interface NotificationPage {
  items: AppNotification[]
  pending: PendingNotification[]
  unread: number
  read_count: number
  next_cursor: string
}

export function listNotifications(limit = 20, cursor = '') {
  return get<{ success: boolean; data: NotificationPage }>(
    `/api/v1/notifications?limit=${limit}${cursor ? `&cursor=${enc(cursor)}` : ''}`,
  )
}

/** Removes read history only; live approvals and run records are unaffected. */
export function clearReadNotifications() {
  return del<{ success: boolean }>('/api/v1/notifications/read')
}

/** Mark history ids read; no ids marks all history. Pending work is separate. */
export function markNotificationsRead(ids: string[] = []) {
  return post<{ success: boolean }>('/api/v1/notifications/read', { ids })
}
