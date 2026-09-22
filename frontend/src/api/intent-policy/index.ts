import { get, post, put } from '@/utils/request'

// IntentGate 策略 CRUD 客户端（T21 后端 API，T50 管理界面）。
// 与 internal/handler/intent_policy.go 的请求/响应契约一一对应：
// 创建 POST（version=1）、列表 GET（跨 scope/版本，含 disabled）、
// 读取 GET /:id、更新 PUT（= 同谱系插入 version+1 新行，旧版本保留）、
// 启停 POST /:id/enable|disable（原地改，不产生新版本）。

export type PolicyScopeType = 'tool' | 'service' | 'agent' | 'workspace' | 'tenant'
export type PolicyRiskTier = 'low' | 'high'
export type PolicyMode = 'observe' | 'enforce'

export interface IntentPolicy {
  id: string
  tenant_id: number
  scope_type: PolicyScopeType
  scope_ref: string
  arg_path?: string | null
  constraint_text: string
  rule_expr?: string | null
  risk_tier: PolicyRiskTier
  mode: PolicyMode
  version: number
  enabled: boolean
  created_by: string
  created_at: string
  updated_at: string
}

// IntentPolicyPayload 是创建/新版本的表单载荷。scope 字段仅创建使用
// （PUT 的谱系继承目标行，后端会忽略）；mode 两端都必传——后端 PUT 缺省
// 回落 observe（安全方向缺省），漏传会把 enforce 静默降级，所以表单永远
// 显式带上当前选择。
export interface IntentPolicyPayload {
  scope_type: PolicyScopeType
  scope_ref: string
  arg_path: string
  constraint_text: string
  rule_expr: string
  risk_tier: PolicyRiskTier
  mode: PolicyMode
}

export async function listIntentPolicies(): Promise<IntentPolicy[]> {
  const res = await get<{ data: IntentPolicy[] }>('/api/v1/intent-policies')
  return res.data ?? []
}

export async function createIntentPolicy(payload: IntentPolicyPayload): Promise<IntentPolicy> {
  const res = await post<{ data: IntentPolicy }>('/api/v1/intent-policies', payload)
  return res.data
}

export async function updateIntentPolicy(id: string, payload: IntentPolicyPayload): Promise<IntentPolicy> {
  const res = await put<{ data: IntentPolicy }>(`/api/v1/intent-policies/${id}`, payload)
  return res.data
}

export async function setIntentPolicyEnabled(id: string, enabled: boolean): Promise<void> {
  await post(`/api/v1/intent-policies/${id}/${enabled ? 'enable' : 'disable'}`)
}

// policyLineageKey 是谱系标识：同 (scope_type, scope_ref) 的所有版本
// 构成一条谱系（设计 §3.2 修改 = version+1）。列表页按谱系分组展示，
// 版本历史 = 谱系内按 version 倒序的全部行。
export function policyLineageKey(p: Pick<IntentPolicy, 'scope_type' | 'scope_ref'>): string {
  return `${p.scope_type}|${p.scope_ref}`
}
