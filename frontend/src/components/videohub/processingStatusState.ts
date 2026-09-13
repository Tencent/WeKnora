import type { VideoProcessingJobStatus, VideoProcessingStatus } from '@/types/videohub'

const transcriptionStages = new Set(['transcription', 'subtitle_generate', 'index'])
const aiContentStages = new Set(['outline', 'summary', 'summary_enhance', 'graph', 'related_knowledge'])

export type ProcessingBannerState = 'transcribing' | 'transcription_failed' | 'ai_generating' | null

export function getNewlyCompletedStages(
  previousStates: ReadonlyMap<string, string>,
  jobs: VideoProcessingJobStatus[],
): string[] {
  return jobs
    .filter(job => job.status === 'succeeded' && previousStates.get(job.job_id) !== 'succeeded')
    .map(job => job.job_type)
}

export function getProcessingStages(jobs: VideoProcessingJobStatus[]): string[] {
  return jobs
    .filter(job => job.status === 'pending' || job.status === 'running')
    .map(job => job.job_type)
}

export function getTranscriptionProgress(jobs: VideoProcessingJobStatus[]): number | undefined {
  const job = jobs.find(item => item.job_type === 'transcription' && item.status === 'running' && item.provider === 'tencent_mps' && item.phase === 'mps_running')
  const progress = job?.progress
  return typeof progress === 'number' && Number.isInteger(progress) && progress >= 1 && progress <= 100 ? progress : undefined
}

export function getFailedProcessingStages(jobs: VideoProcessingJobStatus[]): string[] {
  return jobs
    .filter(job => job.status === 'failed')
    .map(job => job.job_type)
}

export function getProcessingBannerState(jobs: VideoProcessingJobStatus[]): ProcessingBannerState {
  const activeStages = new Set(getProcessingStages(jobs))
  if ([...activeStages].some(stage => transcriptionStages.has(stage))) return 'transcribing'

  const failedStages = new Set(getFailedProcessingStages(jobs))
  if ([...failedStages].some(stage => transcriptionStages.has(stage))) return 'transcription_failed'
  if ([...activeStages].some(stage => aiContentStages.has(stage))) return 'ai_generating'
  return null
}

export function shouldPollProcessingStatus(value: VideoProcessingStatus | null): boolean {
  return value === null || getProcessingStages(value.jobs).length > 0
}
