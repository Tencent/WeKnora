import { get, post } from '@/utils/request'
import { parseMeetingProjection, type MeetingEvidenceRef, type MeetingProjection, type MeetingSession, type MeetingVideoContribution } from './meetingOrchestrationContract'

export type { MeetingEvidenceRef, MeetingProjection, MeetingSession, MeetingVideoContribution }

export type MeetingJob = { id: string; status: 'queued'|'running'|'succeeded'|'failed'; stage?: string; progress: number; error_code?: string; error_message?: string }

function parseJob(value: unknown): MeetingJob {
  const job = value as MeetingJob
  if (!job || typeof job.id !== 'string' || !['queued', 'running', 'succeeded', 'failed'].includes(job.status) || !Number.isFinite(job.progress)) throw new Error('会议主题簇任务状态不可用')
  return job
}

export async function fetchCurrentMeetingProjection(): Promise<MeetingProjection | null> {
  const response: any = await get('/api/custom/meeting-orchestration/current')
  const value = response?.data
  return value ? parseMeetingProjection(value) : null
}
export async function generateMeetingProjection(): Promise<MeetingJob> {
  const response: any = await post('/api/custom/meeting-orchestration/generate')
  return parseJob(response?.data)
}
export async function fetchMeetingJob(id: string): Promise<MeetingJob> {
  const response: any = await get(`/api/custom/meeting-orchestration/jobs/${encodeURIComponent(id)}`)
  return parseJob(response?.data)
}
