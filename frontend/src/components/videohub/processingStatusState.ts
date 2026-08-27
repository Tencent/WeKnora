import type { VideoProcessingJobStatus } from '@/types/videohub'

export function getNewlyCompletedStages(
  previousStates: ReadonlyMap<string, string>,
  jobs: VideoProcessingJobStatus[],
): string[] {
  return jobs
    .filter(job => job.status === 'succeeded' && previousStates.get(job.job_id) !== 'succeeded')
    .map(job => job.job_type)
}
