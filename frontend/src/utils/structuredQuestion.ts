export type StructuredQuestionMode =
  | 'single_choice'
  | 'multiple_choice'
  | 'short_text'
  | 'long_text'
  | 'number'
  | 'date'

export type StructuredQuestionStatus = 'answered' | 'skipped' | 'timed_out' | 'canceled'

export interface StructuredQuestionOption {
  id: string
  label: string
  description?: string
}

export interface StructuredQuestionValidation {
  min_length?: number
  max_length?: number
  min_number?: number
  max_number?: number
  min_date?: string
  max_date?: string
}

export interface StructuredAnswer {
  field_key?: string
  schema_version?: number
  selected_option_ids: string[]
  value?: string | number
  other_text: string
  skipped: boolean
}

export interface StructuredQuestionState {
  mode: StructuredQuestionMode
  fieldKey?: string
  schemaVersion?: number
  selectedOptionIds: string[]
  otherSelected: boolean
  otherText: string
  value?: unknown
  validation?: StructuredQuestionValidation
  skipped: boolean
  allowOther: boolean
  allowSkip: boolean
}

export interface StructuredQuestionEvent {
  pending_id: string
  question: string
  mode: StructuredQuestionMode
  field_key?: string
  schema_version?: number
  question_group_id: string
  question_index: number
  question_total: number
  completed_count?: number
  remaining_count?: number
  options: StructuredQuestionOption[]
  validation?: StructuredQuestionValidation
  allow_other: boolean
  allow_skip: boolean
  resolved?: boolean
  status?: StructuredQuestionStatus
  selected_options?: StructuredQuestionOption[]
  value?: unknown
  other_text?: string
  reason?: string
}

export function remainingQuestionCount(index: number, total: number): number {
  return Math.max(0, total - index)
}

export function structuredQuestionProgress(event: Pick<StructuredQuestionEvent,
  'question_index' | 'question_total' | 'completed_count' | 'remaining_count'>,
  currentQuestionCompleted = false,
) {
  const completed = event.completed_count ?? Math.max(0, event.question_index - 1)
  const remaining = event.remaining_count ?? Math.max(0, event.question_total - event.question_index + 1)
  if (!currentQuestionCompleted) return { completed, remaining }
  return { completed: completed + 1, remaining: Math.max(0, remaining - 1) }
}

export function buildStructuredAnswer(state: StructuredQuestionState): StructuredAnswer | null {
  const metadata = state.fieldKey
    ? { field_key: state.fieldKey, schema_version: state.schemaVersion }
    : {}
  const selected = [...new Set(state.selectedOptionIds)]
  const otherText = state.otherSelected ? state.otherText.trim() : ''

  if (state.skipped) {
    if (!state.allowSkip || selected.length > 0 || state.otherSelected || otherText || hasValue(state.value)) return null
    return { ...metadata, selected_option_ids: [], other_text: '', skipped: true }
  }
  if (isChoiceMode(state.mode)) {
    if (state.otherSelected && (!state.allowOther || !otherText)) return null
    if (state.mode === 'single_choice' && (selected.length > 1 || (selected.length === 1 && state.otherSelected))) {
      return null
    }
    if (selected.length === 0 && !otherText) return null
    return { ...metadata, selected_option_ids: selected, other_text: otherText, skipped: false }
  }
  const value = normalizeTypedValue(state.mode, state.value, state.validation)
  if (value === null) return null
  return { ...metadata, selected_option_ids: [], value, other_text: '', skipped: false }
}

function isChoiceMode(mode: StructuredQuestionMode): boolean {
  return mode === 'single_choice' || mode === 'multiple_choice'
}

function hasValue(value: unknown): boolean {
  return value !== undefined && value !== null && value !== ''
}

function normalizeTypedValue(
  mode: StructuredQuestionMode,
  raw: unknown,
  validation: StructuredQuestionValidation = {},
): string | number | null {
  if (mode === 'number') {
    const value = typeof raw === 'number' ? raw : Number(raw)
    if (!Number.isFinite(value)) return null
    if (validation.min_number !== undefined && value < validation.min_number) return null
    if (validation.max_number !== undefined && value > validation.max_number) return null
    return value
  }
  if (typeof raw !== 'string') return null
  const value = raw.trim()
  if (!value) return null
  if (mode === 'date') {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(value) || Number.isNaN(Date.parse(`${value}T00:00:00Z`))) return null
    if (validation.min_date && value < validation.min_date) return null
    if (validation.max_date && value > validation.max_date) return null
    return value
  }
  const length = [...value].length
  if (validation.min_length !== undefined && length < validation.min_length) return null
  if (validation.max_length !== undefined && length > validation.max_length) return null
  const hardMax = mode === 'long_text' ? 5000 : 500
  return length <= hardMax ? value : null
}
