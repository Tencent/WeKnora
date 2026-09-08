import type { ArtifactMeta } from '../api/chat'
import type { AuditLog } from '../api/tenant/audit-log'

export const WORKBENCH_MAX_FILE_BYTES = 8 * 1024 * 1024
export const TERMINAL_OUTPUT_BYTES = 2 * 1024 * 1024

export function workbenchSocketUrl(apiBase: string, path: string, pageUrl: string): string {
  // Match axios baseURL joining, including installations at /app/weknora/.
  if (path !== '/api/v1/sandbox-terminal') throw new Error('Invalid terminal WebSocket path')
  const url = new URL(`${apiBase.replace(/\/+$/, '')}${path}`, pageUrl)
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) {
    throw new Error('Invalid terminal WebSocket URL')
  }
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.href
}

export function validWorkbenchPath(path: string, allowRoot = false): boolean {
  if (!path) return allowRoot
  if (path.startsWith('/') || /[\\\x00-\x1f\x7f]/.test(path)) return false
  return path.split('/').every(part => part !== '' && part !== '.' && part !== '..')
}

export function workbenchChildPath(directory: string, name: string): string {
  return directory ? `${directory}/${name}` : name
}

export function workbenchFileLimit(limit?: number): number {
  return Number.isFinite(limit) && (limit as number) > 0
    ? Math.min(WORKBENCH_MAX_FILE_BYTES, limit as number)
    : WORKBENCH_MAX_FILE_BYTES
}

export function workbenchSnapshotKey(path: string, revision: number): string {
  return JSON.stringify([path, revision])
}

export function artifactTarget(item: ArtifactMeta): { messageId: string; index: number } | null {
  // A flattened session index cannot identify an artifact within a message.
  if (!item.message_id || !Number.isInteger(item.artifact_index) || (item.artifact_index as number) < 0) return null
  return { messageId: item.message_id, index: item.artifact_index as number }
}

export function workbenchError(error: unknown): string {
  const e = error as { message?: string; error?: { code?: string; message?: string } | string; status?: number; code?: string }
  const detail = typeof e?.error === 'string' ? e.error : e?.error?.message
  const code = typeof e?.error === 'object' ? e.error?.code : e?.code
  return [code || e?.status, detail || e?.message].filter(Boolean).join(': ').slice(0, 1000)
}

export function auditDetails(log: Pick<AuditLog, 'details'>): Record<string, unknown> {
  if (log.details && typeof log.details === 'object') return log.details
  if (typeof log.details === 'string') {
    try {
      const parsed = JSON.parse(log.details)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) return parsed
    } catch { /* Older audit rows may contain plain text. */ }
  }
  return {}
}

export function formatWorkbenchBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

export function isWorkbenchDropTarget(target: EventTarget | null): boolean {
  return typeof Element !== 'undefined' && target instanceof Element && !!target.closest('[data-sandbox-workbench], .sandbox-workbench')
}

export function saveWorkbenchBlob(blob: Blob, name: string, signal: AbortSignal) {
  if (signal.aborted) return
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = name
  document.body.appendChild(link)
  link.click()
  link.remove()
  const cleanup = () => {
    clearTimeout(timer)
    URL.revokeObjectURL(url)
    signal.removeEventListener('abort', cleanup)
  }
  const timer = setTimeout(cleanup, 1000)
  signal.addEventListener('abort', cleanup, { once: true })
}
