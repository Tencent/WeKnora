import { get, getDown, post, postUpload, del } from '@/utils/request'

export interface BackupManifest {
  schema_version: number
  created_at: string
  weknora_version: string
  edition?: string
  db_driver: string
  storage_type: string
  includes: {
    database: boolean
    files: boolean
  }
}

export interface BackupSnapshot {
  id: string
  note: string
  created_at: string
  size_bytes: number
  manifest: BackupManifest
}

export interface RestoreSummary {
  restored_at: string
  source_version: string
  source_created_at: string
  files_restored: boolean
  pre_restore_snapshot: string
  restart_required: boolean
  note: string
}

/** Stream a freshly built full backup archive. */
export async function exportBackup(): Promise<Blob> {
  // 服务端要现场 pg_dump + 打包,关闭全局 30s 超时
  return getDown('/api/v1/backups/export', { timeout: 0 })
}

/** Create a server-side snapshot with an optional note. */
export function createSnapshot(note: string): Promise<{ data: BackupSnapshot }> {
  return post('/api/v1/backups', { note }, { timeout: 0 })
}

/** List server-side snapshots (newest first). */
export function listSnapshots(): Promise<{ data: BackupSnapshot[] }> {
  return get('/api/v1/backups')
}

/** Download a stored snapshot. */
export function downloadSnapshot(id: string): Promise<Blob> {
  return getDown(`/api/v1/backups/${encodeURIComponent(id)}/download`, { timeout: 0 })
}

/** Delete a stored snapshot. */
export function deleteSnapshot(id: string): Promise<void> {
  return del(`/api/v1/backups/${encodeURIComponent(id)}`)
}

/**
 * Restore from a stored snapshot or an uploaded archive file.
 * The backend requires confirm=true; restore replaces the whole database.
 */
export function restoreBackup(payload: { snapshotId?: string; file?: File }): Promise<{ data: RestoreSummary }> {
  const form = new FormData()
  if (payload.snapshotId) form.append('snapshot_id', payload.snapshotId)
  if (payload.file) form.append('file', payload.file)
  form.append('confirm', 'true')
  return postUpload('/api/v1/backups/restore', form, undefined, { timeout: 0 })
}

/** Trigger a browser download for a Blob with the given filename. */
export function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}
