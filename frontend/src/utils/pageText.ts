/**
 * Text helpers behind the assistant's read_page / fill_page tools: patching a
 * field's text by content, reading it by line, and spotting credential fields.
 * Nothing here touches the DOM.
 */

export type PageTextEditOp = 'replace' | 'insert_before' | 'insert_after' | 'replace_selection' | 'append'

export interface PageTextEdit {
  op: PageTextEditOp
  old_text?: string
  anchor?: string
  text: string
}

export interface TextRange {
  start: number
  end: number
}

/** A fill that cannot be applied; its message is shown to the model. */
export class PageFillError extends Error {}

const normalize = (s: string | undefined) => (s || '').replace(/\r\n?/g, '\n')

export function preview(text: string, max = 60): string {
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
}

function findOnce(value: string, needle: string, what: string): number {
  const first = value.indexOf(needle)
  if (first < 0) {
    throw new PageFillError(`${what} "${preview(needle)}" was not found; read the field again, the text may differ in spacing or have changed`)
  }
  let count = 1
  for (let at = value.indexOf(needle, first + needle.length); at >= 0; at = value.indexOf(needle, at + needle.length)) {
    count++
  }
  if (count > 1) {
    throw new PageFillError(`${what} "${preview(needle)}" occurs ${count} times; include more surrounding text so it occurs once`)
  }
  return first
}

/**
 * Applies edits in order. The selection, when given, is the user's selection
 * in the field; it follows the text as earlier edits shift it.
 */
export function applyTextEdits(value: string, edits: PageTextEdit[], selection?: TextRange | null): string {
  let out = value
  let sel = selection && selection.end > selection.start ? { ...selection } : null
  edits.forEach((edit, index) => {
    const text = normalize(edit.text)
    let start: number
    let end: number
    try {
      switch (edit.op) {
        case 'replace': {
          const old = normalize(edit.old_text)
          if (!old) throw new PageFillError('replace needs old_text')
          start = findOnce(out, old, 'old_text')
          end = start + old.length
          break
        }
        case 'insert_before':
        case 'insert_after': {
          const anchor = normalize(edit.anchor)
          if (!anchor) throw new PageFillError(`${edit.op} needs anchor`)
          const at = findOnce(out, anchor, 'anchor')
          start = end = edit.op === 'insert_before' ? at : at + anchor.length
          break
        }
        case 'replace_selection': {
          if (!sel) throw new PageFillError('nothing is selected in this field')
          ;({ start, end } = sel)
          // Selecting whole lines takes in their line breaks, which a rewrite
          // of the words leaves out; keep the selection's outer whitespace
          // unless the new text brings its own.
          const selected = out.slice(start, end)
          if (selected.trim()) {
            if (!/^\s/.test(text)) start += /^\s*/.exec(selected)![0].length
            if (!/\s$/.test(text)) end -= /\s*$/.exec(selected)![0].length
          }
          break
        }
        case 'append':
          start = end = out.length
          break
        default:
          throw new PageFillError(`unknown op "${(edit as PageTextEdit).op}"`)
      }
    } catch (err) {
      if (err instanceof PageFillError && edits.length > 1) {
        throw new PageFillError(`edit ${index + 1}: ${err.message}`)
      }
      throw err
    }
    out = out.slice(0, start) + text + out.slice(end)
    if (sel) {
      const delta = text.length - (end - start)
      if (edit.op === 'replace_selection') sel = null
      else if (end <= sel.start) sel = { start: sel.start + delta, end: sel.end + delta }
      else if (start < sel.end) sel = null
    }
  })
  return out
}

/** Text lines start_line .. start_line+count-1, numbered like an editor. */
export function readLines(value: string, startLine: number, count: number): { text: string; total: number; last: number } {
  const lines = value.split('\n')
  const total = lines.length
  const first = Math.max(1, Math.min(startLine, total))
  const last = Math.min(total, first + Math.max(1, count) - 1)
  const width = String(last).length
  const text = lines
    .slice(first - 1, last)
    .map((line, i) => `${String(first + i).padStart(width)}| ${line.length > 2000 ? `${line.slice(0, 2000)}…` : line}`)
    .join('\n')
  return { text, total, last }
}

/** 1-based line of a character offset. */
export function lineOf(value: string, offset: number): number {
  let line = 1
  for (let i = 0; i < offset && i < value.length; i++) if (value[i] === '\n') line++
  return line
}

export interface OutlineEntry {
  line: number
  level: number
  text: string
}

/** Markdown headings outside fenced code, with their lines. */
export function markdownOutline(value: string, limit = 60): OutlineEntry[] {
  const out: OutlineEntry[] = []
  let fence = ''
  value.split('\n').forEach((line, i) => {
    const marker = /^\s*(`{3,}|~{3,})/.exec(line)?.[1]
    if (marker) {
      if (!fence) fence = marker[0]
      else if (marker[0] === fence) fence = ''
      return
    }
    if (fence || out.length >= limit) return
    const heading = /^(#{1,6})\s+(.+?)\s*#*\s*$/.exec(line)
    if (heading) out.push({ line: i + 1, level: heading[1].length, text: preview(heading[2], 80) })
  })
  return out
}

// Labels of fields that hold credentials. "token" alone is left out: model
// settings have "max tokens".
const SECRET_LABEL =
  /\b(api[\s_-]?key|secret|passwords?|passwd|passphrase|access[\s_-]?key|private[\s_-]?key|client[\s_-]?secret|bearer|(auth|access|refresh|api|bot|app)[\s_-]?token|credentials?|cookie)\b|密钥|秘钥|密码|口令|令牌|凭证|凭据/i

export function isSecretLabel(...texts: Array<string | null | undefined>): boolean {
  return texts.some(text => !!text && SECRET_LABEL.test(text))
}

/** How a fill changed a text field, for the model and the undo card. */
export function describeTextChange(before: string, after: string, edits?: PageTextEdit[]): string {
  if (edits?.length) {
    const counts = new Map<string, number>()
    for (const e of edits) counts.set(e.op, (counts.get(e.op) || 0) + 1)
    const parts = [...counts].map(([op, n]) => `${n} ${op.replace('_', ' ')}`)
    return `${parts.join(', ')} (${before.length} → ${after.length} characters)`
  }
  if (!after) return 'cleared'
  return after.length > 80 || after.includes('\n')
    ? `replaced the text (${before.length} → ${after.length} characters)`
    : `set to "${after}"`
}
