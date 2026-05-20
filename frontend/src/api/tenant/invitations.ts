import { get, post, del } from '@/utils/request'
import type { TenantRole } from '@/api/tenant/members'

// TenantInvitationStatus mirrors internal/types/tenant_invitation.go's
// five-state machine. pending is the only non-terminal state; the rest
// are recorded for the audit trail.
export type TenantInvitationStatus =
  | 'pending'
  | 'accepted'
  | 'declined'
  | 'revoked'
  | 'expired'

// TenantInvitation is the API projection of a tenant_invitations row,
// hydrated with the inviter / invitee user fields and the tenant name
// when the backend has them. Missing optional fields render as the
// raw id in the UI rather than dropping the row.
export interface TenantInvitation {
  id: number
  tenant_id: number
  tenant_name?: string
  invitee_user_id: string
  invitee_email?: string
  invitee_name?: string
  invited_by?: string | null
  inviter_email?: string
  inviter_name?: string
  role: TenantRole
  status: TenantInvitationStatus
  message?: string
  expires_at: string
  responded_at?: string | null
  created_at: string
}

export interface ListInvitationsResponse {
  success: boolean
  data?: {
    invitations: TenantInvitation[]
    total: number
    page?: number
    page_size?: number
  }
  message?: string
}

export interface ListTenantInvitationsParams {
  includeTerminal?: boolean
  page?: number
  page_size?: number
}

function buildTenantInvitationsQuery(options: ListTenantInvitationsParams): string {
  const u = new URLSearchParams()
  if (options.includeTerminal) u.set('include_terminal', 'true')
  if (options.page != null && options.page > 0) u.set('page', String(options.page))
  if (options.page_size != null && options.page_size > 0) u.set('page_size', String(options.page_size))
  const qs = u.toString()
  return qs ? `?${qs}` : ''
}

export interface CreateInvitationRequest {
  email: string
  role: TenantRole
  message?: string
}

export interface CreateInvitationResponse {
  success: boolean
  data?: TenantInvitation
  message?: string
}

export interface SimpleResponse {
  success: boolean
  message?: string
}

export interface AcceptInvitationResponse {
  success: boolean
  data?: {
    membership: {
      tenant_id: number
      role: TenantRole
      status: string
      joined_at: string
    }
  }
  message?: string
}

export interface PendingCountResponse {
  success: boolean
  data?: { pending_count: number }
  message?: string
}

/**
 * List invitations for a tenant. Defaults to pending only; pass
 * includeTerminal=true to also surface accepted/declined/revoked/
 * expired rows for the history view. Supports `page` / `page_size`.
 * Backend: GET /api/v1/tenants/:id/invitations (Viewer+).
 */
export async function listTenantInvitations(
  tenantId: number,
  options: ListTenantInvitationsParams = {},
): Promise<ListInvitationsResponse> {
  const qs = buildTenantInvitationsQuery(options)
  return (await get(
    `/api/v1/tenants/${tenantId}/invitations${qs}`,
  )) as unknown as ListInvitationsResponse
}

/**
 * Send a new invitation. The invitee will see it in /me/invitations
 * and must accept before they actually become a member.
 * Backend: POST /api/v1/tenants/:id/invitations (Owner+).
 *
 * 404 when the email is not a registered user (ask them to register).
 * 409 when an existing pending invitation already covers this pair, or
 * when the invitee is already an active member.
 */
export async function createInvitation(
  tenantId: number,
  body: CreateInvitationRequest,
): Promise<CreateInvitationResponse> {
  return (await post(
    `/api/v1/tenants/${tenantId}/invitations`,
    body,
  )) as unknown as CreateInvitationResponse
}

/**
 * Revoke a still-pending invitation. Already-finalised rows return
 * 409; rows from another tenant render as 404 to avoid existence
 * leaks across tenants.
 * Backend: DELETE /api/v1/tenants/:id/invitations/:inv_id (Owner+).
 */
export async function revokeInvitation(
  tenantId: number,
  invId: number,
): Promise<SimpleResponse> {
  return (await del(
    `/api/v1/tenants/${tenantId}/invitations/${invId}`,
  )) as unknown as SimpleResponse
}

/**
 * List MY invitations. Defaults to pending only — the inbox page
 * filters terminal rows by default; pass includeTerminal=true for a
 * history view if/when the UI grows one.
 * Backend: GET /api/v1/me/invitations (authenticated).
 */
export async function listMyInvitations(
  options: { includeTerminal?: boolean } = {},
): Promise<ListInvitationsResponse> {
  const qs = options.includeTerminal ? '?include_terminal=true' : ''
  return (await get(`/api/v1/me/invitations${qs}`)) as unknown as ListInvitationsResponse
}

/**
 * Lightweight pending-count endpoint used by the avatar-row badge
 * poller. Separate from the list endpoint so polling doesn't transfer
 * the full payload every cycle.
 * Backend: GET /api/v1/me/invitations/pending-count (authenticated).
 */
export async function getMyPendingInvitationCount(): Promise<PendingCountResponse> {
  return (await get(
    `/api/v1/me/invitations/pending-count`,
  )) as unknown as PendingCountResponse
}

/**
 * Accept one of MY pending invitations. On success the backend also
 * creates the tenant_members row in the same flow; the caller should
 * then refresh memberships in the auth store.
 * Backend: POST /api/v1/me/invitations/:inv_id/accept (authenticated).
 */
export async function acceptInvitation(invId: number): Promise<AcceptInvitationResponse> {
  return (await post(
    `/api/v1/me/invitations/${invId}/accept`,
  )) as unknown as AcceptInvitationResponse
}

/**
 * Decline one of MY pending invitations.
 * Backend: POST /api/v1/me/invitations/:inv_id/decline (authenticated).
 */
export async function declineInvitation(invId: number): Promise<SimpleResponse> {
  return (await post(
    `/api/v1/me/invitations/${invId}/decline`,
  )) as unknown as SimpleResponse
}
