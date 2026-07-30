import { get, post } from '@/utils/request'

export type DependencyState = 'ok' | 'failed' | 'disabled' | string

export interface OperationsStatus {
  status: 'ready' | 'not_ready' | string
  uptime_seconds: number
  dependencies: Record<string, DependencyState>
  database: {
    driver: 'mysql' | 'postgres' | 'sqlite' | 'unknown' | string
    open_connections: number
    in_use_connections: number
    wait_count: number
  }
  file_log: {
    enabled: boolean
    size_bytes: number
    disk_free_bytes: number
    disk_total_bytes: number
    disk_state: 'normal' | 'warning' | 'critical' | 'disabled' | string
  }
  migration: {
    known: boolean
    version: number
    dirty: boolean
  }
  backup: {
    scheduled: boolean
    configuration_error: boolean
    schedule?: string
    retention_days: number
    min_free_gb: number
    last_success_at?: string
    last_failure_at?: string
    last_failure_kind?: string
    last_retention_at?: string
    last_retention_failure_at?: string
    last_retention_deleted: number
  }
}

export interface ManualBackupResult {
  backup_id: string
  created_at: string
  archive_file: string
  manifest_file: string
  size_bytes: number
  sha256: string
  migration_known: boolean
  migration_version: number
  files_archive_file?: string
  files_inventory_file?: string
  files_count?: number
  qdrant_snapshot_count?: number
}

export function getOperationsStatus(): Promise<OperationsStatus> {
  return get<OperationsStatus>('/api/v1/admin/operations/status')
}

export function createManualBackup(reason: string): Promise<ManualBackupResult> {
  return post<ManualBackupResult>('/api/v1/admin/operations/backups', { reason })
}
