import { del, get, post } from '@/utils/request'

export type TaskJobState = 'queued' | 'processing' | 'finalizing' | 'succeeded' | 'failed' | 'canceled'
export type TaskExecutionState = 'queued' | 'active' | 'retrying' | 'succeeded' | 'failed' | 'canceled'

export interface TaskJobCapabilities {
  can_retry: boolean
  can_cancel: boolean
  can_delete_record: boolean
}

export interface TaskJob {
  job_id: string
  tenant_id: number
  created_by: string
  kind: string
  origin: string
  display_name: string
  scope: string
  scope_id: string
  related_id: string
  process_attempt: number
  state: TaskJobState
  metadata: Record<string, unknown>
  last_error_class: string
  last_error: string
  failed_task_type: string
  failed_task_id: string
  created_at: string
  updated_at: string
  finished_at?: string | null
  capabilities: TaskJobCapabilities
}

export interface TaskExecution {
  execution_id: string
  job_id: string
  process_attempt: number
  task_type: string
  queue: string
  state: TaskExecutionState
  retry_count: number
  error_class: string
  last_error: string
  retry_of: string
  enqueued_at: string
  dispatched_at?: string | null
  started_at?: string | null
  finished_at?: string | null
}

export interface TaskSummary {
  queued: number
  processing: number
  succeeded: number
  failed: number
  canceled: number
}

export interface TaskJobQuery {
  state?: string
  kind?: string
  kb_id?: string
  created_by?: string
  q?: string
  page?: number
  page_size?: number
  sort?: string
}

export interface TaskJobList {
  items: TaskJob[]
  total: number
  page: number
  page_size: number
}

function qs(params?: object) {
  const query = new URLSearchParams()
  Object.entries((params || {}) as Record<string, unknown>).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') {
      query.set(key, String(value))
    }
  })
  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

function unwrap<T>(response: any): T {
  return (response?.data ?? response) as T
}

export async function getTaskSummary(params?: TaskJobQuery): Promise<TaskSummary> {
  return unwrap<TaskSummary>(await get(`/api/v1/tasks/summary${qs(params)}`))
}

export async function listTaskJobs(params?: TaskJobQuery): Promise<TaskJobList> {
  return unwrap<TaskJobList>(await get(`/api/v1/tasks${qs(params)}`))
}

export async function getTaskJob(jobId: string): Promise<TaskJob> {
  return unwrap<TaskJob>(await get(`/api/v1/tasks/${encodeURIComponent(jobId)}`))
}

export async function listTaskExecutions(jobId: string): Promise<TaskExecution[]> {
  return unwrap<TaskExecution[]>(await get(`/api/v1/tasks/${encodeURIComponent(jobId)}/executions`))
}

export async function retryTask(jobId: string): Promise<{ execution_id: string }> {
  return unwrap<{ execution_id: string }>(await post(`/api/v1/tasks/${encodeURIComponent(jobId)}/retry`, {}))
}

export async function retryTaskBatch(params?: TaskJobQuery): Promise<{ retried: number }> {
  return unwrap<{ retried: number }>(await post(`/api/v1/tasks/retry${qs(params)}`, {}))
}

export async function cancelTask(jobId: string): Promise<void> {
  await post(`/api/v1/tasks/${encodeURIComponent(jobId)}/cancel`, {})
}

export async function deleteTaskRecords(olderThanDays = 30): Promise<{ deleted: number }> {
  return unwrap<{ deleted: number }>(await del(`/api/v1/tasks${qs({ older_than_days: olderThanDays })}`))
}
