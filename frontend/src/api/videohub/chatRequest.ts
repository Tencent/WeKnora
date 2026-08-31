export type ChatScope = 'global' | 'video'

export interface ChatRequestScope {
  scope: ChatScope
  agent_id?: string
  knowledge_base_ids: string[]
  knowledge_ids: string[]
  tenant_id?: string | number
}

export function normalizeTenantId(value?: string | number | null): string {
  return value === undefined || value === null ? '' : String(value).trim()
}

export function buildChatRequest(
  scope: ChatRequestScope,
  query: string,
  token: string,
  fallbackTenantId?: string | number | null,
) {
  const tenantId = normalizeTenantId(scope.tenant_id) || normalizeTenantId(fallbackTenantId)
  return {
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
      ...(tenantId ? { 'X-Tenant-ID': tenantId } : {}),
    },
    body: {
      query,
      knowledge_base_ids: scope.knowledge_base_ids,
      // Video Agent mode can use the configured agent to fall back to other
      // knowledge in the same resource workspace.
      ...(scope.scope === 'global' || !scope.agent_id ? { knowledge_ids: scope.knowledge_ids } : {}),
      agent_enabled: Boolean(scope.agent_id),
      ...(scope.agent_id ? { agent_id: scope.agent_id } : {}),
      disable_title: true,
      channel: 'web',
    },
  }
}

export function normalizeChatError(error: unknown): Error {
  const payload = error && typeof error === 'object' ? error as {
    status?: number
    message?: string
    error?: string | { message?: string }
  } : {}
  const status = error instanceof Error
    ? (error as Error & { status?: number }).status
    : payload.status
  if (status === 403) {
    return new Error('当前账号没有访问视频知识库所属工作空间的权限，请联系管理员加入该工作空间')
  }
  if (error instanceof Error) return error

  const message = typeof payload.error === 'string'
    ? payload.error
    : payload.error?.message || payload.message
  return new Error(message || '问答生成失败，请稍后重试')
}
