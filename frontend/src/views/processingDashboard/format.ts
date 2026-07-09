import type { ProcessingLogicalStage, ProcessingStageItem, ProcessingStageProgress, ProcessingStageState } from '../../types/processingDashboard.ts'
import { PROCESSING_STAGE_ORDER } from '../../types/processingDashboard.ts'

export function stageSortValue(stage: ProcessingLogicalStage): number {
  const index = PROCESSING_STAGE_ORDER.indexOf(stage)
  return index < 0 ? 999 : index
}

export function sortStages<T extends { key: ProcessingLogicalStage; order?: number }>(stages: T[]): T[] {
  return [...stages].sort((a, b) => (a.order ?? stageSortValue(a.key)) - (b.order ?? stageSortValue(b.key)))
}

export function previewItems(items: ProcessingStageItem[] = [], limit = 5): ProcessingStageItem[] {
  return items.slice(0, Math.max(0, limit))
}

export function formatProgress(progress?: ProcessingStageProgress | null, fallback = ''): string {
  if (!progress || !progress.reliable || !progress.total) return fallback
  return `${progress.completed}/${progress.total} ${unitLabel(progress.unit)}`
}

export function unitLabel(unit: string): string {
  switch (unit) {
    case 'image':
      return '张'
    case 'batch':
      return '批'
    case 'chunk':
      return 'Chunk'
    case 'page':
      return '页'
    default:
      return unit || ''
  }
}

export function formatElapsed(ms?: number): string {
  if (!ms || ms < 0) return ''
  const totalSeconds = Math.floor(ms / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  if (hours > 0) return `${hours}h${String(minutes).padStart(2, '0')}m`
  if (minutes > 0) return `${minutes}m${String(seconds).padStart(2, '0')}s`
  return `${seconds}s`
}

export function stateTone(state: ProcessingStageState): 'success' | 'warning' | 'danger' | 'primary' | 'default' {
  switch (state) {
    case 'running':
      return 'primary'
    case 'queued':
    case 'retrying':
      return 'warning'
    case 'failed':
    case 'done_with_errors':
      return 'danger'
    case 'done':
    case 'skipped':
      return 'success'
    default:
      return 'default'
  }
}

export function shouldShowRetryingCount(observable: boolean): boolean {
  return observable
}

export function formatStageCount(value: number, reliable = true): string {
  return reliable ? String(value) : `>=${value}`
}

export function autoRefreshDelay(value: number): number | null {
  return value > 0 ? value * 1000 : null
}
