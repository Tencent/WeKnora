import { get, post, put, del } from '@/utils/request'

// TenantRole mirrors internal/types/tenant_member.go's four-role enum.
// Keep the string values aligned with the Go constants.
export type TenantRole = 'owner' | 'admin' | 'contributor' | 'viewer'

export type TenantMemberStatus = 'active' | 'invited' | 'suspended'

// TenantMember is the API projection of a (user, tenant) membership row,
// already joined with the user's email/username/avatar by the backend.
export interface TenantMember {
  user_id: string
  email: string
  username: string
  avatar?: string
  role: TenantRole
  status: TenantMemberStatus
  invited_by?: string | null
  joined_at: string
}

export interface ListMembersResponse {
  success: boolean
  data?: {
    members: TenantMember[]
    total: number
  }
  message?: string
}

export interface AddMemberRequest {
  email: string
  role: TenantRole
}

export interface AddMemberResponse {
  success: boolean
  data?: TenantMember
  message?: string
}

export interface SimpleResponse {
  success: boolean
  message?: string
}

/**
 * List all active members of the given tenant.
 * Backend: GET /api/v1/tenants/:id/members (Viewer+).
 */
export async function listMembers(tenantId: number): Promise<ListMembersResponse> {
  return (await get(`/api/v1/tenants/${tenantId}/members`)) as unknown as ListMembersResponse
}

/**
 * Invite an existing user (by email) to the tenant with the given role.
 * Backend: POST /api/v1/tenants/:id/members (Owner+).
 *
 * Returns 404 when the email does not match any registered user — the
 * caller should ask the invitee to register first. PR 3 does not yet
 * support email-based invites for users who don't have an account.
 */
export async function addMember(
  tenantId: number,
  body: AddMemberRequest,
): Promise<AddMemberResponse> {
  return (await post(`/api/v1/tenants/${tenantId}/members`, body)) as unknown as AddMemberResponse
}

/**
 * Change an existing member's role.
 * Backend: PUT /api/v1/tenants/:id/members/:user_id (Owner+).
 *
 * Returns 409 when this would demote the last active Owner of the tenant.
 */
export async function updateMemberRole(
  tenantId: number,
  userId: string,
  role: TenantRole,
): Promise<SimpleResponse> {
  return (await put(`/api/v1/tenants/${tenantId}/members/${userId}`, { role })) as unknown as SimpleResponse
}

/**
 * Remove a member from the tenant.
 * Backend: DELETE /api/v1/tenants/:id/members/:user_id (Owner+).
 *
 * Returns 409 when this would remove the last active Owner.
 */
export async function removeMember(
  tenantId: number,
  userId: string,
): Promise<SimpleResponse> {
  return (await del(`/api/v1/tenants/${tenantId}/members/${userId}`)) as unknown as SimpleResponse
}
