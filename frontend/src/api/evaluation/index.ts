import { get } from '../../utils/request'

export interface EvaluationUsage {
  call_count: number
  successful_calls: number
  failed_calls: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  cache_miss_tokens: number
  cache_reported_calls: number
  cache_hit_calls: number
  cache_hit_rate: number
  model_duration_ms: number
  average_model_latency_ms: number
  priced_calls: number
  unpriced_calls: number
  cost_by_currency: Record<string, number>
}

export interface EvaluationModelUsageStat {
  model_id: string
  model_name: string
  usage: EvaluationUsage
}

export interface EvaluationModelUsageRange {
  startTime?: string
  endTime?: string
}

export async function getEvaluationModelUsage(range: EvaluationModelUsageRange = {}): Promise<EvaluationModelUsageStat[]> {
  const response: any = await get('/api/v1/evaluation/model-usage', {
    params: {
      ...(range.startTime ? { start_time: range.startTime } : {}),
      ...(range.endTime ? { end_time: range.endTime } : {}),
    },
  })
  if (!response?.success || !Array.isArray(response.data)) return []
  return response.data
}
