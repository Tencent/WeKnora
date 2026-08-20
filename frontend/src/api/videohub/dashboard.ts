import { getMockDashboard } from './mockDashboard'
import type { DashboardPayload, DashboardRequest } from '@/types/videohub'

export async function fetchDashboard(req: DashboardRequest): Promise<DashboardPayload> {
  if (req.range === 'custom' && (!req.from || !req.to)) return Promise.reject(new Error('请选择自定义日期范围'))
  if (req.range === 'custom' && Date.parse(req.from!) > Date.parse(req.to!)) return Promise.reject(new Error('开始日期不能晚于结束日期'))
  if (req.range === 'custom' && (Date.parse(`${req.to!}T00:00:00Z`) - Date.parse(`${req.from!}T00:00:00Z`)) / 86400000 >= 90) return Promise.reject(new Error('自定义时间范围最长 90 天'))
  await Promise.resolve()
  return getMockDashboard(req)
}
