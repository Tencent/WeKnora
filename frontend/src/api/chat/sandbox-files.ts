import { del, get, getDown, patch, postUpload } from '@/utils/request'

export type SandboxLiveFileType = 'file' | 'directory' | 'dir' | 'other'

export interface SandboxLiveFileEntry {
  name: string
  path: string
  type: SandboxLiveFileType
  size: number
  mod_time: string
}

export const MAX_SANDBOX_LIVE_FILE_BYTES = 16 * 1024 * 1024

interface SandboxLiveFileListResponse {
  success: boolean
  data: SandboxLiveFileEntry[]
}

const sessionFilesURL = (sessionId: string) =>
  `/api/v1/sessions/${encodeURIComponent(sessionId)}/sandbox/files`

export async function listSandboxLiveFiles(
  sessionId: string,
  relativeDir = '',
): Promise<SandboxLiveFileEntry[]> {
  const query = new URLSearchParams()
  if (relativeDir) query.set('path', relativeDir)
  const suffix = query.size ? `?${query.toString()}` : ''
  const response = await get<SandboxLiveFileListResponse>(`${sessionFilesURL(sessionId)}${suffix}`)
  return response.data
}

export function downloadSandboxLiveFile(sessionId: string, relativePath: string): Promise<Blob> {
  const query = new URLSearchParams({ path: relativePath })
  return getDown(`${sessionFilesURL(sessionId)}/content?${query.toString()}`)
}

export function uploadSandboxLiveFile(
  sessionId: string,
  relativePath: string,
  file: File,
): Promise<unknown> {
  const form = new FormData()
  form.append('path', relativePath)
  form.append('file', file)
  return postUpload(sessionFilesURL(sessionId), form)
}

export function renameSandboxLiveFile(
  sessionId: string,
  source: string,
  target: string,
): Promise<unknown> {
  return patch(sessionFilesURL(sessionId), { source, target })
}

export function deleteSandboxLiveFile(sessionId: string, relativePath: string): Promise<unknown> {
  const query = new URLSearchParams({ path: relativePath })
  return del(`${sessionFilesURL(sessionId)}?${query.toString()}`)
}
