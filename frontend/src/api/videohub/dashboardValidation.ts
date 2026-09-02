export interface DashboardValidationRequest {
  range: '7d' | '30d' | '90d' | 'custom'
  from?: string
  to?: string
}

export function validateDashboardRequest(req: DashboardValidationRequest) {
  if (req.range === 'custom' && (!req.from || !req.to)) throw new Error('请选择自定义日期范围')
  if (req.range === 'custom' && Date.parse(req.from!) > Date.parse(req.to!)) throw new Error('开始日期不能晚于结束日期')
  if (
    req.range === 'custom' &&
    (Date.parse(`${req.to!}T00:00:00Z`) - Date.parse(`${req.from!}T00:00:00Z`)) / 86400000 + 1 > 90
  ) {
    throw new Error('自定义时间范围最长 90 天')
  }
}
