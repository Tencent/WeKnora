import type { AuditLog } from './tenant/audit-log'

export interface WorkbenchStatus {
  available: boolean
  state: string
  provider: string
  config_id: string
  root: '/workspace/output'
  capabilities: { terminal: boolean; files: boolean }
  limits: {
    command_timeout_seconds: number
    session_timeout_seconds: number
    cpu_seconds: number
    memory_bytes: number
    memory_enforcement?: 'per_process_as_and_aggregate_rss_sampled'
    max_file_bytes: number
  }
  reason?: string
}

export interface TerminalTicket {
  ticket: string
  expires_in: number
  websocket_path: string
}

export interface WorkbenchFile {
  name: string
  path: string
  type: 'file' | 'dir'
  size: number
  modified_at: string
}

export interface WorkbenchDirectory {
  path: string
  entries: WorkbenchFile[]
}

type Envelope<T> = { success: boolean; data: T; error?: { code?: string; message?: string }; message?: string }
type RequestOptions = { signal?: AbortSignal; responseType?: 'blob' }
export interface WorkbenchTransport {
  get<T>(url: string, options?: RequestOptions): Promise<T>
  post<T>(url: string, body: object, options?: RequestOptions): Promise<T>
  patch<T>(url: string, body: object, options?: RequestOptions): Promise<T>
  del<T>(url: string, body?: unknown, options?: RequestOptions): Promise<T>
  postUpload(url: string, body: FormData, progress?: undefined, options?: RequestOptions): Promise<unknown>
}

// Test the contract without initializing the app's auth/i18n singletons.
export function createWorkbenchApi(sessionId: string, transport: WorkbenchTransport, signal?: AbortSignal) {
  if (!sessionId) throw new Error('Missing session ID')
  const base = `/api/v1/sessions/${encodeURIComponent(sessionId)}/sandbox`
  const options = { signal }
  const pathQuery = (path: string) => new URLSearchParams({ path }).toString()
  async function data<T>(request: Promise<Envelope<T>>): Promise<T> {
    const response = await request
    signal?.throwIfAborted()
    if (response.success !== true) throw response
    return response.data
  }
  async function mutation(request: Promise<unknown>): Promise<void> {
    const response = await request as Envelope<unknown>
    signal?.throwIfAborted()
    if (response.success !== true) throw response
  }
  return {
    status: () => data(transport.get<Envelope<WorkbenchStatus>>(`${base}/workbench`, options)),
    bind: (configId: string) => data(transport.post<Envelope<WorkbenchStatus>>(`${base}/workbench`, { config_id: configId }, options)),
    ticket: () => data(transport.post<Envelope<TerminalTicket>>(`${base}/command-ticket`, {}, options)),
    files: (path = '') => data(transport.get<Envelope<WorkbenchDirectory>>(`${base}/files?${pathQuery(path)}`, options)),
    download: async (path: string) => {
      const blob = await transport.get<Blob>(`${base}/files/download?${pathQuery(path)}`, { ...options, responseType: 'blob' })
      signal?.throwIfAborted()
      return blob
    },
    upload: (path: string, file: File) => {
      const form = new FormData()
      form.append('path', path)
      form.append('file', file)
      return mutation(transport.postUpload(`${base}/files`, form, undefined, options))
    },
    mkdir: (path: string) => mutation(transport.post(`${base}/directories`, { path }, options)),
    rename: (path: string, newPath: string) => mutation(transport.patch(`${base}/files`, { path, new_path: newPath }, options)),
    remove: (path: string) => mutation(transport.del(`${base}/files?${pathQuery(path)}`, undefined, options)),
    audit: async (afterId = 0) => {
      const query = afterId ? `?${new URLSearchParams({ after_id: String(afterId) })}` : ''
      const response = await transport.get<Envelope<AuditLog[]> & { next_cursor?: number }>(`${base}/audit${query}`, options)
      signal?.throwIfAborted()
      if (response.success !== true || !Array.isArray(response.data)) throw response
      return { data: response.data, next_cursor: response.next_cursor || 0 }
    },
  }
}

export type WorkbenchApi = ReturnType<typeof createWorkbenchApi>
