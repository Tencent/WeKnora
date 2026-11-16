export interface AskUserField {
  name: string
  label?: string
  type: 'text' | 'number' | 'select' | 'multiselect'
  required?: boolean
  options?: string[]
}

/** Request of an ask_user prompt; mirrors types.AskUserPromptRequest. */
export interface AskUserQuestion {
  question: string
  options?: string[]
  multi_select?: boolean
  fields?: AskUserField[]
  allow_free_text?: boolean
}

export interface AskUserDraft {
  selected: string[]
  values: Record<string, unknown>
  text: string
}

const isEmpty = (v: unknown) =>
  v === undefined || v === null || (typeof v === 'string' && !v.trim()) || (Array.isArray(v) && v.length === 0)

/** Builds the response the server validates, dropping untouched inputs. */
export function buildAskUserResponse(q: AskUserQuestion, draft: AskUserDraft): Record<string, unknown> {
  const response: Record<string, unknown> = {}
  if (q.options?.length && draft.selected.length) response.selected = [...draft.selected]
  if (q.fields?.length) {
    const values: Record<string, unknown> = {}
    for (const f of q.fields) {
      const v = draft.values[f.name]
      if (!isEmpty(v)) values[f.name] = typeof v === 'string' ? v.trim() : v
    }
    if (Object.keys(values).length) response.values = values
  }
  const freeTextOnly = !q.options?.length && !q.fields?.length
  const text = draft.text.trim()
  if (text && (freeTextOnly || q.allow_free_text)) response.text = text
  return response
}

/** Whether a response answers the question well enough to send. */
export function canSubmitAskUser(q: AskUserQuestion, response: Record<string, unknown>): boolean {
  const values = (response.values || {}) as Record<string, unknown>
  if (q.fields?.some((f) => f.required && isEmpty(values[f.name]))) return false
  if (q.options?.length) return Boolean((response.selected as string[] | undefined)?.length || response.text)
  if (q.fields?.length) return true
  return Boolean(response.text)
}

/** One line per part of an answer, labelled the way the question was. */
export function summarizeAskUserAnswer(q: AskUserQuestion, answer: Record<string, unknown>): string {
  const lines: string[] = []
  const selected = answer.selected as string[] | undefined
  if (selected?.length) lines.push(selected.join(', '))
  const values = (answer.values || {}) as Record<string, unknown>
  for (const f of q.fields || []) {
    const v = values[f.name]
    if (isEmpty(v)) continue
    lines.push(`${f.label || f.name}: ${Array.isArray(v) ? v.join(', ') : String(v)}`)
  }
  if (typeof answer.text === 'string' && answer.text.trim()) lines.push(answer.text.trim())
  return lines.join('\n')
}
