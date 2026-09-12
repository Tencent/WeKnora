/** Display helpers for skill and sandbox file tools in the agent stream. */

const SKILL_SCRIPT_EXT = /\.(py|sh|bash|js|ts|rb|pl|php)$/i
const SKILL_MD = /(^|\/)SKILL\.md$/i

type ToolEventLike = {
  tool_data?: unknown
  arguments?: unknown
}

export type SkillFileListItem = {
  name: string
  path: string
  size?: number
  modifiedAt?: string
  isScript?: boolean
}

function asRecord(value: unknown): Record<string, unknown> {
  if (!value) return {}
  if (typeof value === 'string') {
    try {
      const parsed = JSON.parse(value)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        return parsed as Record<string, unknown>
      }
    } catch {
      return {}
    }
  }
  if (typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return {}
}

function stringField(record: Record<string, unknown>, key: string): string {
  const value = record[key]
  return typeof value === 'string' ? value.trim() : value == null ? '' : String(value).trim()
}

function eventFields(event: ToolEventLike | null | undefined): Record<string, unknown> {
  return {
    ...asRecord(event?.arguments),
    ...asRecord(event?.tool_data),
  }
}

export function isSkillScriptPath(path: string): boolean {
  return SKILL_SCRIPT_EXT.test(path)
}

export function formatToolTitleWithDetail(baseTitle: string, detail: string): string {
  const trimmed = detail.trim()
  if (!trimmed) return baseTitle
  return `${baseTitle}：「${trimmed}」`
}

export function getEventSkillName(event: ToolEventLike | null | undefined): string {
  return stringField(eventFields(event), 'skill_name')
}

export function getReadSkillTarget(event: ToolEventLike | null | undefined): string {
  const fields = eventFields(event)
  const skill = stringField(fields, 'skill_name')
  const file = stringField(fields, 'file_path')
  if (skill && file) return `${skill}/${file}`
  return skill || file
}

export function getSandboxToolPath(event: ToolEventLike | null | undefined): string {
  return stringField(eventFields(event), 'path')
}

export type SandboxDiffStat = {
  added: number
  removed: number
}

function numberField(record: Record<string, unknown>, key: string): number {
  const value = record[key]
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim()) {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return 0
}

export function getSandboxDiffStat(event: ToolEventLike | null | undefined): SandboxDiffStat | null {
  const fields = eventFields(event)
  const added = Math.max(0, Math.trunc(numberField(fields, 'added_lines')))
  const removed = Math.max(0, Math.trunc(numberField(fields, 'removed_lines')))
  if (added <= 0 && removed <= 0) return null
  return { added, removed }
}

export function getSandboxFilePreview(event: ToolEventLike | null | undefined): string {
  return stringField(eventFields(event), 'preview')
}

export function sandboxPreviewRemaining(event: ToolEventLike | null | undefined): number {
  const stat = getSandboxDiffStat(event)
  const preview = getSandboxFilePreview(event)
  if (!stat || !preview) return 0
  const previewLines = preview.split('\n').filter((line, index, lines) => !(index === lines.length - 1 && line === '')).length
  return Math.max(0, stat.added - previewLines)
}

export function skillScriptTitleCommand(skillName: string, command: string): string {
  const trimmed = command.trim()
  if (!trimmed) return ''
  if (skillName && trimmed.startsWith(`${skillName}/`)) {
    return trimmed.slice(skillName.length + 1)
  }
  return trimmed
}

function basename(path: string): string {
  const trimmed = path.replace(/\/+$/, '')
  const parts = trimmed.split('/').filter(Boolean)
  return parts[parts.length - 1] || trimmed || path
}

function relativeToRoot(path: string, root: string): string {
  if (!root) return path
  if (path === root) return '.'
  const prefix = root.endsWith('/') ? root : `${root}/`
  if (path.startsWith(prefix)) return path.slice(prefix.length)
  return path
}

export function skillFileListItems(data: unknown): SkillFileListItem[] {
  const record = asRecord(data)
  const files = record.files
  if (!Array.isArray(files)) return []
  return files
    .map((file) => String(file || '').trim())
    .filter(Boolean)
    .filter((path) => !SKILL_MD.test(path))
    .map((path) => ({
      name: basename(path),
      path,
      isScript: isSkillScriptPath(path),
    }))
}

export function sandboxFileListItems(data: unknown): SkillFileListItem[] {
  const record = asRecord(data)
  const root = stringField(record, 'root') || stringField(record, 'path')
  const entries = record.entries
  if (!Array.isArray(entries)) return []
  return entries.map((entry) => {
    const item = asRecord(entry)
    const path = stringField(item, 'path')
    const name = stringField(item, 'name') || basename(path)
    const sizeRaw = item.size
    const size = typeof sizeRaw === 'number' && Number.isFinite(sizeRaw) ? sizeRaw : undefined
    return {
      name: relativeToRoot(path, root) || name,
      path,
      size,
      modifiedAt: stringField(item, 'modified_at'),
      isScript: isSkillScriptPath(path || name),
    }
  })
}

export function formatSandboxModifiedAt(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) return ''
  const date = new Date(trimmed)
  if (Number.isNaN(date.getTime())) return trimmed
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
