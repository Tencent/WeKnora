import { get } from '@/utils/request'

// IntentGate 判定报表客户端（T51 后端 API）。分布数字的验收口径：
// summary 与 verdict 表直查一致（e2e 用 sqlite 对账）。

export type VerdictValue = 'allow' | 'deny' | 'require_approval' | 'uncertain'

export interface IntentVerdict {
  id: string
  tenant_id: number
  session_id: string
  tool_call_id: string
  policy_id?: string | null
  policy_version?: number | null
  tool_name: string
  layer: string
  verdict: VerdictValue
  reason: string
  mode_at_decision: string
  latency_ms: number
  judge_tokens: number
  human_override: string
  created_at: string
}

export interface VerdictSummary {
  counts: Partial<Record<VerdictValue, number>>
  total: number
}

export async function listIntentVerdicts(policyId: string, limit = 100): Promise<IntentVerdict[]> {
  const res = await get<{ data: IntentVerdict[] }>(
    `/api/v1/intent-verdicts?policy_id=${encodeURIComponent(policyId)}&limit=${limit}`,
  )
  return res.data ?? []
}

export async function intentVerdictSummary(policyId: string): Promise<VerdictSummary> {
  const res = await get<{ data: VerdictSummary }>(
    `/api/v1/intent-verdicts/summary?policy_id=${encodeURIComponent(policyId)}`,
  )
  return res.data ?? { counts: {}, total: 0 }
}
