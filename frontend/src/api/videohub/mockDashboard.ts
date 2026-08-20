import type { DashboardPayload, DashboardRequest, QuestionCluster, QuestionTrendPoint, VideoCategory } from '@/types/videohub'

const videoSeeds = [
  ['v-01', '企业数字化转型中的组织能力建设'], ['v-02', 'AI 大模型的技术演进与未来'],
  ['v-03', '产品经理如何构建用户洞察'], ['v-04', '销售团队高绩效方法论'],
  ['v-05', '组织学习与知识管理实践'], ['v-06', '数据驱动增长策略'],
] as const
const categories: VideoCategory[] = ['interview', 'training', 'salon', 'general']
const clusterQuestions = ['如何提升转化率？', '产品定位的方法论是什么？', '如何制定用户增长策略？', '团队如何沉淀组织知识？', '如何评估 AI 项目的业务价值？']

function dayNumber(value: string) { const [year, month, day] = value.split('-').map(Number); return Date.UTC(year, month - 1, day) }
function formatDay(value: number) { return new Date(value).toISOString().slice(0, 10) }
function daysFor(req: DashboardRequest) {
  if (req.range !== 'custom') return Number(req.range.slice(0, -1))
  if (!req.from || !req.to) return 7
  return Math.min(90, Math.max(1, Math.floor((dayNumber(req.to) - dayNumber(req.from)) / 86400000) + 1))
}
function makeTrend(days: number, to?: string): QuestionTrendPoint[] {
  const end = dayNumber(to ?? '2026-08-20')
  return Array.from({ length: days }, (_, index) => {
    const date = end - (days - index - 1) * 86400000
    return { date: formatDay(date), count: 120 + (index * 11) % 81, top_videos: videoSeeds.slice(0, 3).map(([video_id, title], rank) => ({ video_id, title, count: 24 - rank * 5 + index % 4 })) }
  })
}

function clustersFor(days: number, to?: string) {
  const end = dayNumber(to ?? '2026-08-20')
  const factor = Math.max(.35, days / 7)
  return mockClusters.map((cluster, index) => ({
    ...cluster,
    count: Math.round(cluster.count * factor),
    related_video_count: cluster.videos.length,
    last_asked_at: `${formatDay(end - index * 86400000)} ${14 + index}:30`,
    videos: cluster.videos.map(video => ({ ...video })),
  }))
}

export const mockClusters: QuestionCluster[] = clusterQuestions.map((question, index) => ({
  id: `cluster-${index + 1}`, representative_question: question, count: 186 - index * 23,
  related_video_count: 2 + index % 3, last_asked_at: `2026-08-${String(20 - index).padStart(2, '0')} ${14 + index}:30`,
  videos: Array.from({ length: 2 + index % 3 }, (_, offset) => {
    const [video_id, title] = videoSeeds[(index + offset) % videoSeeds.length]
    const seconds = 75 + index * 83 + offset * 49
    return { video_id, title, video_category: categories[(index + offset) % categories.length], first_seconds: seconds, first_timestamp: `${String(Math.floor(seconds / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`, deleted: index === 4 && offset === 2 }
  }),
}))

export function getMockDashboard(req: DashboardRequest): DashboardPayload {
  const days = daysFor(req)
  const factor = days / 7
  return {
    range: req.range, from: req.from, to: req.to,
    kpi: {
      total_questions: Math.round(1234 * factor), active_videos: Math.min(36, 18 + Math.floor(days / 15)), cluster_count: 47,
      avg_questions_per_video: 68,
      trend: { total_questions: 12, active_videos: 3, cluster_count: -2, avg_questions_per_video: 5 },
    },
    trend: makeTrend(days, req.to), clusters: clustersFor(days, req.to),
  }
}
