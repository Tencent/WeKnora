import { del, get, patch, post } from '@/utils/request'

export interface WorkspaceWebhookEndpoint {
  id: string
  tenant_id: number
  name: string
  url: string
  events: string[]
  enabled: boolean
  description: string
  has_secret: boolean
  created_by: string
  created_at: string
  updated_at: string
}

export interface WorkspaceWebhookDelivery {
  delivery_id: string
  endpoint_id: string
  event_id: string
  event_type: string
  status: string
  http_status: number
  attempts: number
  error: string
  duration_ms: number
  created_at: string
  finished_at?: string | null
}

export interface CreateWorkspaceWebhookPayload {
  name: string
  url: string
  secret: string
  events: string[]
  enabled?: boolean
  description?: string
}

export interface UpdateWorkspaceWebhookPayload {
  name?: string
  url?: string
  secret?: string
  events?: string[]
  enabled?: boolean
  description?: string
}

function unwrap<T>(resp: unknown): T {
  const body = resp as { success?: boolean; data?: T }
  return (body?.data ?? resp) as T
}

export async function listWorkspaceWebhooks(tenantId: number): Promise<WorkspaceWebhookEndpoint[]> {
  const resp = await get(`/api/v1/tenants/${tenantId}/event/webhooks`)
  const data = unwrap<WorkspaceWebhookEndpoint[] | null>(resp)
  return data ?? []
}

export async function listWorkspaceWebhookEventTypes(tenantId: number): Promise<string[]> {
  const resp = await get(`/api/v1/tenants/${tenantId}/event/types`)
  const data = unwrap<string[] | null>(resp)
  return data ?? []
}

export async function createWorkspaceWebhook(
  tenantId: number,
  payload: CreateWorkspaceWebhookPayload,
): Promise<WorkspaceWebhookEndpoint> {
  const resp = await post(`/api/v1/tenants/${tenantId}/event/webhooks`, payload)
  return unwrap<WorkspaceWebhookEndpoint>(resp)
}

export async function updateWorkspaceWebhook(
  tenantId: number,
  hookId: string,
  payload: UpdateWorkspaceWebhookPayload,
): Promise<WorkspaceWebhookEndpoint> {
  const resp = await patch(`/api/v1/tenants/${tenantId}/event/webhooks/${hookId}`, payload)
  return unwrap<WorkspaceWebhookEndpoint>(resp)
}

export async function deleteWorkspaceWebhook(tenantId: number, hookId: string): Promise<void> {
  await del(`/api/v1/tenants/${tenantId}/event/webhooks/${hookId}`)
}

export async function testWorkspaceWebhook(tenantId: number, hookId: string): Promise<void> {
  await post(`/api/v1/tenants/${tenantId}/event/webhooks/${hookId}/test`)
}

export async function listWorkspaceWebhookDeliveries(
  tenantId: number,
  hookId: string,
  limit = 50,
): Promise<WorkspaceWebhookDelivery[]> {
  const resp = await get(
    `/api/v1/tenants/${tenantId}/event/webhooks/${hookId}/deliveries?limit=${limit}`,
  )
  const data = unwrap<WorkspaceWebhookDelivery[] | null>(resp)
  return data ?? []
}
