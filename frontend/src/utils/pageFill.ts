/**
 * Types into the WeKnora page beside the assistant the way the user would:
 * native setters plus input events for text, clicks for checkboxes, switches
 * and TDesign selects. Nothing is saved. A fill applies all its changes or
 * none, and can be undone from the chat while the fields keep its values.
 */
import { shallowReactive, shallowRef } from 'vue'
import {
  applyTextEdits,
  describeTextChange,
  PageFillError,
  preview,
  type PageTextEdit,
} from './pageText'
import {
  checkedRadioOf,
  fieldSelection,
  readFieldValue,
  resolveField,
  type PageField,
  type PageFieldValue,
} from './pageSnapshot'
import { revealTextChange } from './pageReveal'

export interface PageFillChange {
  ref: string
  value?: unknown
  edits?: PageTextEdit[]
}

export interface PageFillApplied {
  ref: string
  label: string
  summary: string
}

interface UndoEntry {
  field: PageField
  before: PageFieldValue
  after: PageFieldValue
}

const OPTION_WAIT_MS = 1500
const HIGHLIGHT_CLASS = 'assistant-page-filled'

const undoEntries = new Map<string, UndoEntry[]>()
const undoable = shallowReactive(new Set<string>())

/** The latest fill, for the notice shown beside the field it changed. */
export interface PageFillNotice {
  id: number
  key: string
  count: number
  anchor: WeakRef<HTMLElement>
}
export const lastPageFill = shallowRef<PageFillNotice | null>(null)
let noticeSeq = 0

const sleep = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms))
const collapse = (s: string | null | undefined) => (s || '').replace(/\s+/g, ' ').trim()

async function waitFor<T>(probe: () => T | null | undefined, timeout = OPTION_WAIT_MS): Promise<T | null> {
  const until = Date.now() + timeout
  for (;;) {
    const found = probe()
    if (found) return found
    if (Date.now() > until) return null
    await sleep(30)
  }
}

// Vue applies v-model updates on the next tick; the DOM is checked after it.
const settle = () => sleep(30)

function toBoolean(value: unknown): boolean | null {
  if (typeof value === 'boolean') return value
  const text = String(value ?? '').trim().toLowerCase()
  if (['true', 'on', 'yes', 'checked', '1'].includes(text)) return true
  if (['false', 'off', 'no', 'unchecked', '0'].includes(text)) return false
  return null
}

function toText(value: unknown, field: PageField): string {
  if (typeof value === 'string') return value.replace(/\r\n?/g, '\n')
  if (typeof value === 'number' && Number.isFinite(value)) return String(value)
  throw new PageFillError(`${fieldName(field)} takes text`)
}

function fieldName(field: PageField): string {
  return field.label ? `"${preview(field.label, 40)}"` : `this ${field.kind} field`
}

/** What a change sets its field to, checked before anything is touched. */
function plan(field: PageField, change: PageFillChange): PageFieldValue {
  const isText = field.kind === 'text' || field.kind === 'multiline'
  if (change.edits?.length) {
    if (!isText) throw new PageFillError(`${fieldName(field)} is a ${field.kind}; edits apply to text fields, use value`)
    return applyTextEdits(String(readFieldValue(field)), change.edits, fieldSelection(field))
  }
  const value = change.value
  switch (field.kind) {
    case 'text':
    case 'multiline':
      return toText(value, field)
    case 'number': {
      const text = toText(value, field).trim()
      if (text !== '' && !Number.isFinite(Number(text))) throw new PageFillError(`${fieldName(field)} takes a number`)
      return text
    }
    case 'checkbox':
    case 'switch':
    case 'radio': {
      const on = toBoolean(value)
      if (on === null) throw new PageFillError(`${fieldName(field)} takes true or false`)
      if (field.kind === 'radio' && !on) {
        throw new PageFillError(`${fieldName(field)} is a radio button: choose another option to clear it`)
      }
      return on
    }
    case 'multiselect': {
      const labels = Array.isArray(value) ? value : [value]
      if (!labels.every(label => typeof label === 'string')) {
        throw new PageFillError(`${fieldName(field)} takes a list of option labels`)
      }
      return labels.map(label => collapse(label as string))
    }
    default:
      if (typeof value !== 'string' && typeof value !== 'number') {
        throw new PageFillError(`${fieldName(field)} takes one option label`)
      }
      return collapse(String(value))
  }
}

function setNativeValue(el: HTMLElement, value: string) {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(proto, 'value')?.set?.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
  el.dispatchEvent(new Event('change', { bubbles: true }))
}

const sameLabel = (a: string, b: string) => collapse(a).toLowerCase() === collapse(b).toLowerCase()

// Displayed is enough: an opening popup stays visibility:hidden until its
// enter transition ends, and its options take clicks meanwhile.
function openDropdowns(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>('.t-select__dropdown')]
    .filter(el => el.getClientRects().length > 0)
}

/** Opens a TDesign select and clicks options until the chosen labels match. */
async function chooseOptions(field: PageField, labels: string[]) {
  const multiple = field.kind === 'multiselect'
  const trigger = field.box.querySelector<HTMLElement>('.t-input') || field.box
  const before = new Set(openDropdowns())
  trigger.click()
  // Popups are teleported to body; the one this click opened is the new one.
  const panel = await waitFor(() =>
    openDropdowns().find(el => !before.has(el) && el.querySelector('.t-select-option')))
  if (!panel) {
    if (field.box.classList.contains('t-select-input--popup-visible')) trigger.click()
    throw new PageFillError(`the options of ${fieldName(field)} did not open`)
  }
  const gone = () => waitFor(() => !openDropdowns().includes(panel))
  const close = async () => {
    if (field.box.classList.contains('t-select-input--popup-visible')) trigger.click()
    await gone()
  }
  const options = [...panel.querySelectorAll<HTMLElement>('.t-select-option')]
    .filter(li => !li.classList.contains('t-is-disabled'))
  const textOf = (li: HTMLElement) => collapse(li.innerText || li.textContent)
  const missing = labels.filter(label => !options.some(li => sameLabel(textOf(li), label)))
  if (missing.length) {
    await close()
    const available = options.map(textOf).filter(Boolean).slice(0, 30).join(', ')
    throw new PageFillError(`${missing.map(l => `"${l}"`).join(', ')} is not an option of ${fieldName(field)}` +
      (available ? `; options: ${available}` : '; it has no options to choose from'))
  }
  if (!multiple) {
    const li = options.find(option => sameLabel(textOf(option), labels[0]))!
    if (li.classList.contains('t-is-selected')) await close()
    else {
      li.click()
      await gone()
    }
    return
  }
  for (const li of options) {
    const want = labels.some(label => sameLabel(textOf(li), label))
    if (want !== li.classList.contains('t-is-selected')) {
      li.click()
      await settle()
    }
  }
  await close()
}

async function write(field: PageField, value: PageFieldValue) {
  const control = field.control
  switch (field.kind) {
    case 'text':
    case 'multiline':
    case 'number':
      setNativeValue(control, String(value))
      break
    case 'checkbox':
    case 'radio':
      // TDesign handles clicks on its label and ignores those on the input;
      // a label click reaches a plain input too.
      if (readFieldValue(field) !== value) field.box.click()
      break
    case 'switch':
      if (readFieldValue(field) !== value) field.box.click()
      break
    case 'native-select': {
      const select = control as HTMLSelectElement
      const option = [...select.options].find(o => sameLabel(o.text, String(value)) || o.value === value)
      if (!option) {
        const available = [...select.options].map(o => collapse(o.text)).filter(Boolean).slice(0, 30).join(', ')
        throw new PageFillError(`"${value}" is not an option of ${fieldName(field)}; options: ${available}`)
      }
      select.value = option.value
      select.dispatchEvent(new Event('input', { bubbles: true }))
      select.dispatchEvent(new Event('change', { bubbles: true }))
      break
    }
    case 'select':
      await chooseOptions(field, [String(value)])
      break
    case 'multiselect':
      await chooseOptions(field, value as string[])
      break
  }
  await settle()
}

function sameValue(a: PageFieldValue, b: PageFieldValue): boolean {
  if (Array.isArray(a) || Array.isArray(b)) {
    const x = (Array.isArray(a) ? a : [a]).map(v => collapse(String(v)).toLowerCase()).sort()
    const y = (Array.isArray(b) ? b : [b]).map(v => collapse(String(v)).toLowerCase()).sort()
    return x.length === y.length && x.every((v, i) => v === y[i])
  }
  if (typeof a === 'string' && typeof b === 'string') return a === b || sameLabel(a, b)
  return a === b
}

function summarize(field: PageField, before: PageFieldValue, after: PageFieldValue, edits?: PageTextEdit[]): string {
  switch (field.kind) {
    case 'text':
    case 'multiline':
      return describeTextChange(String(before), String(after), edits)
    case 'checkbox':
    case 'radio':
      return after ? 'checked' : 'unchecked'
    case 'switch':
      return after ? 'turned on' : 'turned off'
    case 'multiselect':
      return (after as string[]).length ? `chose ${(after as string[]).join(', ')}` : 'cleared'
    default:
      return after === '' ? 'cleared' : `set to "${preview(String(after), 80)}"`
  }
}

function highlight(field: PageField) {
  const box = field.box
  box.classList.remove(HIGHLIGHT_CLASS)
  void box.offsetWidth
  box.classList.add(HIGHLIGHT_CLASS)
  setTimeout(() => box.classList.remove(HIGHLIGHT_CLASS), 2000)
}

/**
 * Applies a fill. key names the fill for undo (the tool call). Throws
 * PageFillError, after putting back whatever was already changed.
 */
export async function fillPage(changes: PageFillChange[], key: string): Promise<PageFillApplied[]> {
  const planned = changes.map(change => {
    const field = resolveField(change.ref, true)
    return { change, field, target: plan(field, change) }
  })
  const done: UndoEntry[] = []
  const applied: PageFillApplied[] = []
  try {
    for (const { change, field, target } of planned) {
      const before = readFieldValue(field)
      // A radio is cleared by checking the one it replaced, so that one is
      // what undo puts back.
      const replaced = field.kind === 'radio' && !before ? checkedRadioOf(field) : null
      await write(field, target)
      const after = readFieldValue(field)
      done.push(replaced ? { field: replaced, before: true, after: false } : { field, before, after })
      if ((field.kind !== 'text' && field.kind !== 'multiline') && !sameValue(after, target)) {
        throw new PageFillError(`${change.ref} (${fieldName(field)}) did not take the value`)
      }
      applied.push({ ref: change.ref, label: field.label, summary: summarize(field, before, after, change.edits) })
    }
  } catch (err) {
    for (const entry of done.reverse()) {
      await write(entry.field, entry.before).catch(() => undefined)
    }
    throw err
  }
  planned.forEach(({ field }) => highlight(field))
  reveal(done, planned[0]?.field)
  if (key) {
    undoEntries.set(key, done)
    undoable.add(key)
    if (planned.length) {
      lastPageFill.value = { id: ++noticeSeq, key, count: applied.length, anchor: new WeakRef(planned[0].field.box) }
    }
  }
  return applied
}

/** Brings the change into view: the edited text where it can, else the field. */
function reveal(done: UndoEntry[], first?: PageField) {
  const text = done.find(entry => entry.field.kind === 'multiline' && entry.before !== entry.after)
  if (text) {
    revealTextChange(text.field.control as HTMLTextAreaElement, String(text.before), String(text.after))
    return
  }
  first?.box.scrollIntoView({ block: 'nearest' })
}

export function canUndoFill(key: string): boolean {
  return undoable.has(key)
}

/**
 * Puts back what a fill changed. A field the user edited since, or one no
 * longer on the page, is left as it is.
 */
export async function undoFill(key: string): Promise<{ restored: number; skipped: number }> {
  const entries = undoEntries.get(key) || []
  undoEntries.delete(key)
  undoable.delete(key)
  let restored = 0
  let skipped = 0
  for (const entry of [...entries].reverse()) {
    const { field } = entry
    if (!field.control.isConnected || !sameValue(readFieldValue(field), entry.after)) {
      skipped++
      continue
    }
    try {
      await write(field, entry.before)
      highlight(field)
      restored++
    } catch {
      skipped++
    }
  }
  return { restored, skipped }
}
