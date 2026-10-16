/**
 * Reads the WeKnora page beside the assistant the way a screen reader would:
 * an indented outline of headings, text, tables, buttons and form fields with
 * their current values. Fields the assistant may fill get a ref (e12) that
 * stays with the element while it lives. Credentials are never read.
 */
import { isSecretLabel, lineOf, markdownOutline, PageFillError, readLines } from './pageText'

export type PageFieldKind =
  | 'text' | 'multiline' | 'number' | 'select' | 'multiselect' | 'native-select' | 'checkbox' | 'radio' | 'switch'

export interface PageField {
  kind: PageFieldKind
  /** The element a ref names: the native control or the component root. */
  control: HTMLElement
  /** The component as the user sees it; highlighted after a fill. */
  box: HTMLElement
  label: string
  disabled: boolean
  readonly: boolean
  secret: boolean
}

export interface PageSnapshot {
  location: string
  snapshot: string
}

// The assistant itself and anything a page opts out.
const IGNORED = '.assistant-panel, .assistant-launcher, [data-assistant-ignore]'
const OVERLAYS = '.t-drawer.t-drawer--open > .t-drawer__content-wrapper, .t-dialog__ctx, [role="dialog"], [aria-modal="true"]'
const SKIPPED_TAGS = new Set(['SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE', 'SVG', 'CANVAS', 'IFRAME', 'VIDEO', 'AUDIO', 'OBJECT', 'LINK', 'META'])
// What a container must be descended into for, instead of read as one text.
const SIGNIFICANT = [
  'input', 'textarea', 'select', 'button', 'a[href]', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'table', 'ul', 'ol',
  'img[alt]', '[role]', '[data-assistant-ref]', '[data-assistant-ignore]', '.t-select', '.t-switch',
  '.t-checkbox', '.t-radio', '.t-radio-button', '.t-input-number', '.t-alert', '.t-tabs__nav-item',
].join(',')
const CONTAINER_ROLES = new Set(['dialog', 'alertdialog', 'form', 'region', 'navigation', 'list', 'tablist', 'menu', 'grid', 'tree'])
const ALERT_ROLES = new Set(['alert', 'status'])
const MAX_ELEMENTS = 6000
const MAX_TABLE_ROWS = 80
const MULTILINE_PREVIEW_LINES = 40
const MULTILINE_PREVIEW_CHARS = 2000
const DEFAULT_BUDGET = 24000

const collapse = (s: string | null | undefined) => (s || '').replace(/\s+/g, ' ').trim()
const quote = (s: string) => JSON.stringify(s)
const clip = (s: string, max: number) => (s.length > max ? `${s.slice(0, max - 1)}…` : s)

// ---------- refs ----------

const refOf = new WeakMap<Element, string>()
const byRef = new Map<string, WeakRef<HTMLElement>>()
let nextRef = 1

function refFor(el: HTMLElement): string {
  let ref = refOf.get(el)
  if (!ref) {
    ref = `e${nextRef++}`
    refOf.set(el, ref)
    byRef.set(ref, new WeakRef(el))
    if (byRef.size > 4000) {
      for (const [key, weak] of byRef) if (!weak.deref()?.isConnected) byRef.delete(key)
    }
  }
  return ref
}

function elementByRef(ref: string): HTMLElement | null {
  const el = byRef.get(ref)?.deref()
  return el?.isConnected ? el : null
}

// ---------- visibility ----------

function isHidden(el: Element): boolean {
  if (el.hasAttribute('hidden') || el.getAttribute('aria-hidden') === 'true' || el.hasAttribute('inert')) return true
  const style = getComputedStyle(el)
  if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse') return true
  return style.display !== 'contents' && el.getClientRects().length === 0
}

function isShown(el: Element): boolean {
  for (let node: Element | null = el; node && node !== document.body; node = node.parentElement) {
    if (isHidden(node)) return false
  }
  return true
}

// ---------- fields ----------

const NON_FIELD_INPUTS = new Set(['hidden', 'file', 'submit', 'button', 'reset', 'image', 'color', 'range'])

function textOf(el: Element | null | undefined): string {
  return collapse((el as HTMLElement | null)?.innerText ?? el?.textContent)
}

function labelOf(control: HTMLElement, root: HTMLElement): string {
  const named = root.closest('[data-assistant-field]')?.getAttribute('data-assistant-field')
  if (named) return named.trim()
  for (const el of [control, root]) {
    const aria = el.getAttribute('aria-label')
    if (aria?.trim()) return aria.trim()
    const ids = el.getAttribute('aria-labelledby')
    if (ids) {
      const text = collapse(ids.split(/\s+/).map(id => textOf(document.getElementById(id))).join(' '))
      if (text) return text
    }
  }
  const labels = (control as HTMLInputElement).labels
  if (labels?.length) {
    const text = textOf(labels[0])
    if (text) return text
  }
  const formLabel = textOf(root.closest('.t-form__item')?.querySelector('.t-form__label'))
  if (formLabel) return formLabel
  // A short text just before the control or one of its near ancestors, as
  // settings rows lay out a label beside the control.
  let el: HTMLElement | null = root
  for (let depth = 0; el && depth < 4; depth++, el = el.parentElement) {
    let sibling = el.previousElementSibling
    for (let hops = 0; sibling && hops < 2; hops++, sibling = sibling.previousElementSibling) {
      if (sibling.querySelector('input, textarea, select, .t-select, .t-switch')) break
      const text = textOf(sibling)
      if (text && text.length <= 60) return text
    }
  }
  return collapse(control.getAttribute('placeholder') || control.getAttribute('title') || control.getAttribute('name'))
}

function isDisabled(control: HTMLElement, root: HTMLElement): boolean {
  return !!((control as HTMLInputElement).disabled ||
    root.classList.contains('t-is-disabled') ||
    root.getAttribute('aria-disabled') === 'true' ||
    control.closest('fieldset:disabled'))
}

function makeField(kind: PageFieldKind, control: HTMLElement, box: HTMLElement): PageField {
  const label = labelOf(control, box)
  const input = control as HTMLInputElement
  const autocomplete = control.getAttribute('autocomplete') || ''
  const secret = input.type === 'password' || /password|cc-/.test(autocomplete) ||
    !!box.closest('[data-assistant-redact]') ||
    isSecretLabel(label, control.getAttribute('name'), control.id, control.getAttribute('placeholder'))
  const readonly = (kind === 'text' || kind === 'multiline' || kind === 'number') && input.readOnly
  return { kind, control, box, label, disabled: isDisabled(control, box), readonly, secret }
}

/** The field an element is, if it is one; descendants of a field are not fields. */
function fieldOf(el: HTMLElement): PageField | null {
  const cls = el.classList
  if (cls.contains('t-select')) {
    return makeField(el.querySelector('.t-tag-input') ? 'multiselect' : 'select', el, el)
  }
  if (cls.contains('t-switch')) return makeField('switch', el, el)
  if (cls.contains('t-input-number')) {
    const input = el.querySelector('input')
    return input ? makeField('number', input, el) : null
  }
  if (cls.contains('t-checkbox') || cls.contains('t-radio') || cls.contains('t-radio-button')) {
    const input = el.querySelector<HTMLInputElement>('input[type="checkbox"], input[type="radio"]')
    return input ? makeField(input.type === 'radio' ? 'radio' : 'checkbox', input, el) : null
  }
  if (el instanceof HTMLInputElement) {
    if (NON_FIELD_INPUTS.has(el.type)) return null
    if (el.type === 'checkbox' || el.type === 'radio') return makeField(el.type, el, el)
    return makeField(el.type === 'number' ? 'number' : 'text', el, el)
  }
  if (el instanceof HTMLTextAreaElement) return makeField('multiline', el, el)
  if (el instanceof HTMLSelectElement) return makeField(el.multiple ? 'multiselect' : 'native-select', el, el)
  return null
}

function fieldForControl(control: HTMLElement): PageField | null {
  const root = control.closest<HTMLElement>('.t-input-number, .t-checkbox, .t-radio, .t-radio-button')
  return fieldOf(root && root !== control ? root : control)
}

/** The checked radio of the group a radio field belongs to, if any. */
export function checkedRadioOf(field: PageField): PageField | null {
  const input = field.control as HTMLInputElement
  const group = field.box.closest('.t-radio-group, [role="radiogroup"]')
  if (!group && !input.name) return null
  const radios = group
    ? [...group.querySelectorAll<HTMLInputElement>('input[type="radio"]')]
    : [...(input.form || document).querySelectorAll<HTMLInputElement>('input[type="radio"]')]
      .filter(other => other.name === input.name)
  for (const other of radios) {
    const candidate = fieldForControl(other)
    if (candidate && readFieldValue(candidate) === true) return candidate
  }
  return null
}

/** The live field a ref names, or why it cannot be used. */
export function resolveField(ref: string, forWriting: boolean): PageField {
  const el = elementByRef(ref)
  const field = el && isShown(el) ? fieldForControl(el) : null
  if (!field) throw new PageFillError(`${ref} is no longer on the page; read the page again for current refs`)
  if (field.secret) throw new PageFillError(`${ref} holds a credential; the user enters it themselves`)
  if (forWriting && (field.disabled || field.readonly)) {
    throw new PageFillError(`${ref} (${field.label || field.kind}) cannot be edited right now`)
  }
  return field
}

export function isSwitchOn(field: PageField): boolean {
  return field.box.classList.contains('t-is-checked') || field.box.getAttribute('aria-checked') === 'true'
}

/** The labels a TDesign select shows as chosen. */
function selectLabels(field: PageField): string[] {
  if (field.kind === 'multiselect' && field.control instanceof HTMLSelectElement) {
    return [...field.control.selectedOptions].map(o => collapse(o.text))
  }
  if (field.kind === 'multiselect') {
    return [...field.box.querySelectorAll('.t-tag')]
      .map(tag => textOf(tag))
      .filter(text => text && !/^\+\s*\d+$/.test(text))
  }
  const input = field.box.querySelector('input')
  const text = collapse(input?.value)
  return text ? [text] : []
}

export type PageFieldValue = string | boolean | string[]

/** A field's current value as fill_page sets it. */
export function readFieldValue(field: PageField): PageFieldValue {
  switch (field.kind) {
    case 'checkbox':
    case 'radio':
      // TDesign's wrapper class is the component state; the native input can
      // lag behind it.
      return field.box !== field.control
        ? field.box.classList.contains('t-is-checked')
        : (field.control as HTMLInputElement).checked
    case 'switch':
      return isSwitchOn(field)
    case 'select':
      return selectLabels(field)[0] || ''
    case 'multiselect':
      return selectLabels(field)
    case 'native-select': {
      const select = field.control as HTMLSelectElement
      return collapse(select.selectedOptions[0]?.text)
    }
    default:
      return (field.control as HTMLInputElement).value
  }
}

const ROLE_NAMES: Record<PageFieldKind, string> = {
  text: 'textbox', multiline: 'textbox', number: 'spinbutton', select: 'combobox', multiselect: 'combobox',
  'native-select': 'combobox', checkbox: 'checkbox', radio: 'radio', switch: 'switch',
}

function selectionOf(control: HTMLElement): { start: number; end: number } | null {
  const el = control as HTMLTextAreaElement
  if (typeof el.selectionStart !== 'number' || typeof el.selectionEnd !== 'number') return null
  return el.selectionEnd > el.selectionStart ? { start: el.selectionStart, end: el.selectionEnd } : null
}

export function fieldSelection(field: PageField): { start: number; end: number } | null {
  return field.kind === 'text' || field.kind === 'multiline' ? selectionOf(field.control) : null
}

function errorOf(field: PageField): string {
  const item = field.box.closest('.t-form__item')
  const tips = item?.querySelector('.t-input__extra, .t-form__controls .t-input__tips, .t-input__tips--error')
  return tips && isShown(tips) ? textOf(tips) : ''
}

function describeField(field: PageField, ref: string): { line: string; block: string[] } {
  const parts = [ROLE_NAMES[field.kind]]
  if (field.label) parts.push(quote(clip(field.label, 80)))
  if (ref) parts.push(`[ref=${ref}]`)
  const block: string[] = []
  const value = readFieldValue(field)
  if (field.secret) {
    parts.push('value hidden')
  } else if (field.kind === 'checkbox' || field.kind === 'radio') {
    parts.push(value ? 'checked' : 'unchecked')
  } else if (field.kind === 'switch') {
    parts.push(value ? 'on' : 'off')
  } else if (Array.isArray(value)) {
    parts.push(value.length ? `values=${quote(value.join(', '))}` : 'nothing chosen')
  } else if (field.kind === 'multiline' && value) {
    const text = value as string
    const lines = text.split('\n').length
    parts.push(`(${lines} lines, ${text.length} characters)`)
    let shown = 0
    let chars = 0
    for (const line of text.split('\n')) {
      if (shown >= MULTILINE_PREVIEW_LINES || chars + line.length > MULTILINE_PREVIEW_CHARS) break
      shown++
      chars += line.length + 1
    }
    shown = Math.max(shown, 1)
    block.push(...readLines(text, 1, shown).text.split('\n'))
    if (shown < lines) {
      block.push(`… lines ${shown + 1}-${lines} not shown${ref ? `; read them with read_page ref=${ref} start_line line_count` : ''}`)
      const outline = markdownOutline(text, 40)
      if (outline.length) {
        block.push('outline:')
        block.push(...outline.map(h => `  L${h.line} ${'#'.repeat(h.level)} ${h.text}`))
      }
    }
    const sel = selectionOf(field.control)
    if (sel) {
      const from = lineOf(text, sel.start)
      const to = lineOf(text, sel.end)
      block.push(`selected (lines ${from}-${to}): ${quote(clip(text.slice(sel.start, sel.end), 500))}`)
    }
  } else if (value) {
    parts.push(`value=${quote(clip(value as string, 300))}`)
  } else {
    const placeholder = collapse(field.kind === 'select'
      ? field.box.querySelector('input')?.getAttribute('placeholder')
      : field.control.getAttribute('placeholder'))
    parts.push(placeholder ? `empty (placeholder ${quote(clip(placeholder, 80))})` : 'empty')
  }
  if (field.kind === 'native-select') {
    const options = [...(field.control as HTMLSelectElement).options].map(o => collapse(o.text)).filter(Boolean)
    if (options.length) parts.push(`options=${quote(clip(options.slice(0, 30).join(' | '), 400))}`)
  }
  if (field.disabled) parts.push('[disabled]')
  else if (field.readonly) parts.push('[read-only]')
  if (field.control.hasAttribute('required') || field.control.getAttribute('aria-required') === 'true') {
    parts.push('[required]')
  }
  const error = errorOf(field)
  if (error) parts.push(`error=${quote(clip(error, 200))}`)
  return { line: parts.join(' '), block }
}

// ---------- walking ----------

interface WalkState {
  lines: string[]
  chars: number
  budget: number
  elements: number
  truncated: boolean
}

function emit(state: WalkState, depth: number, text: string) {
  if (state.truncated || !text) return
  const line = `${'  '.repeat(depth)}${text}`
  if (state.chars + line.length > state.budget) {
    state.truncated = true
    return
  }
  if (state.lines[state.lines.length - 1] === line) return
  state.lines.push(line)
  state.chars += line.length + 1
}

const bareLabel = (text: string) => text.trim().replace(/^\*\s*/, '').replace(/\s*[:：]$/, '').toLowerCase()

/** The label printed just before a field repeats what the field line says. */
function dropLabelLine(state: WalkState, label: string) {
  const last = state.lines[state.lines.length - 1]
  if (!label || last === undefined || bareLabel(last) !== bareLabel(label)) return
  state.lines.pop()
  state.chars -= last.length + 1
}

function roleOf(el: HTMLElement): string {
  const explicit = el.getAttribute('role')
  if (explicit && explicit !== 'presentation' && explicit !== 'none' && explicit !== 'group') return explicit
  if (el.hasAttribute('data-assistant-ref')) return 'item'
  const cls = el.classList
  if (cls.contains('t-alert') || cls.contains('t-message')) return 'alert'
  if (cls.contains('t-tabs__nav-item')) return 'tab'
  switch (el.tagName) {
    case 'H1': case 'H2': case 'H3': case 'H4': case 'H5': case 'H6': return 'heading'
    case 'BUTTON': return 'button'
    case 'A': return el.hasAttribute('href') ? 'link' : ''
    case 'IMG': return 'img'
    case 'TABLE': return 'table'
    case 'UL': case 'OL': return 'list'
    case 'LI': return 'listitem'
    case 'DIALOG': return 'dialog'
    case 'NAV': return 'navigation'
    case 'FORM': return el.getAttribute('aria-label') ? 'form' : ''
    default: return ''
  }
}

function nameOf(el: HTMLElement): string {
  return collapse(el.getAttribute('aria-label') || el.getAttribute('title')) || textOf(el)
}

function isPageTab(el: HTMLElement): boolean {
  return el.getAttribute('role') === 'tab' || el.classList.contains('t-tabs__nav-item')
}

function tabDisabled(el: HTMLElement): boolean {
  return (el as HTMLButtonElement).disabled || el.getAttribute('aria-disabled') === 'true' ||
    el.classList.contains('t-is-disabled')
}

function describeTab(el: HTMLElement, name: string): string {
  const disabled = tabDisabled(el)
  const parts = [`tab ${quote(name)}`]
  if (!disabled) parts.push(`[ref=${refFor(el)}]`)
  if (isSelected(el)) parts.push('[selected]')
  if (disabled) parts.push('[disabled]')
  return parts.join(' ')
}

/**
 * Opens a tab from read_page. Returns whether the tab was clicked; an
 * already selected tab is left as it is. Anything that is not a tab is refused.
 */
export function activateTab(ref: string): boolean {
  const el = elementByRef(ref)
  if (!el || !isShown(el) || isAssistantUI(el)) {
    throw new PageFillError(`${ref} is no longer on the page; read the page again for current refs`)
  }
  if (!isPageTab(el)) {
    throw new PageFillError(`${ref} is not a tab. select_tab only opens a tab; buttons stay with the user`)
  }
  if (tabDisabled(el)) throw new PageFillError(`${ref} is disabled`)
  if (el instanceof HTMLAnchorElement && /^(https?:|\/\/)/i.test(el.getAttribute('href') || '')) {
    throw new PageFillError(`${ref} opens another site; ask the user to open it`)
  }
  if (el instanceof HTMLButtonElement && el.type === 'submit') {
    throw new PageFillError(`${ref} submits a form; ask the user to use it`)
  }
  if (isSelected(el)) return false
  el.click()
  return true
}

function isSelected(el: HTMLElement): boolean {
  return el.getAttribute('aria-selected') === 'true' || el.getAttribute('aria-checked') === 'true' ||
    el.classList.contains('t-is-active') || el.classList.contains('t-is-checked') ||
    el.classList.contains('is-selected') || el.classList.contains('selected') ||
    !!el.querySelector('input[type="checkbox"]:checked, .t-checkbox.t-is-checked')
}

function emitTable(table: HTMLTableElement, depth: number, state: WalkState) {
  const caption = collapse(table.getAttribute('aria-label')) || textOf(table.caption)
  emit(state, depth, caption ? `table ${quote(caption)}` : 'table')
  const rows = [...table.rows].filter(row => !isHidden(row))
  rows.slice(0, MAX_TABLE_ROWS).forEach(row => {
    const cells = [...row.cells].map(cell => clip(textOf(cell), 120))
    if (!cells.some(Boolean)) return
    const header = [...row.cells].every(cell => cell.tagName === 'TH')
    const ref = row.getAttribute('data-assistant-ref') || row.closest('[data-assistant-ref]')?.getAttribute('data-assistant-ref')
    const tags = [header ? 'columns:' : 'row:']
    if (!header && isSelected(row)) tags.push('[selected]')
    if (ref) tags.push(`(${ref})`)
    emit(state, depth + 1, `${tags.join(' ')} ${cells.join(' | ')}`)
  })
  if (rows.length > MAX_TABLE_ROWS) emit(state, depth + 1, `… ${rows.length - MAX_TABLE_ROWS} more rows`)
}

function walkChildren(parent: Node, depth: number, state: WalkState) {
  for (const child of parent.childNodes) {
    if (state.truncated) return
    if (child.nodeType === Node.TEXT_NODE) {
      emit(state, depth, clip(collapse(child.textContent), 500))
    } else if (child instanceof HTMLElement) {
      walkElement(child, depth, state)
    }
  }
}

function walkElement(el: HTMLElement, depth: number, state: WalkState) {
  if (++state.elements > MAX_ELEMENTS) {
    state.truncated = true
    return
  }
  if (SKIPPED_TAGS.has(el.tagName) || el.matches(IGNORED) || isHidden(el)) return
  const field = fieldOf(el)
  if (field) {
    const ref = field.disabled || field.readonly || field.secret ? '' : refFor(field.control)
    const { line, block } = describeField(field, ref)
    dropLabelLine(state, field.label)
    emit(state, depth, line)
    block.forEach(text => emit(state, depth + 1, text))
    return
  }
  const role = roleOf(el)
  switch (role) {
    case 'heading': {
      const level = /^H(\d)$/.exec(el.tagName)?.[1] || el.getAttribute('aria-level') || ''
      emit(state, depth, `heading${level ? ` [${level}]` : ''} ${quote(clip(nameOf(el), 200))}`)
      return
    }
    case 'button':
    case 'link':
    case 'menuitem':
    case 'option': {
      const name = clip(nameOf(el), 120)
      const disabled = (el as HTMLButtonElement).disabled || el.getAttribute('aria-disabled') === 'true'
      if (name) emit(state, depth, `${role} ${quote(name)}${disabled ? ' [disabled]' : ''}`)
      return
    }
    case 'img': {
      const alt = collapse(el.getAttribute('alt') || el.getAttribute('aria-label'))
      if (alt) emit(state, depth, `img ${quote(clip(alt, 120))}`)
      return
    }
    case 'tab': {
      const name = clip(nameOf(el), 80)
      if (name) emit(state, depth, describeTab(el, name))
      return
    }
    case 'table':
      if (el instanceof HTMLTableElement) {
        emitTable(el, depth, state)
        return
      }
      break
    case 'item': {
      const tags = ['item', `(${el.getAttribute('data-assistant-ref')})`]
      if (isSelected(el)) tags.push('[selected]')
      emit(state, depth, `${tags.join(' ')} ${quote(clip(textOf(el), 300))}`)
      return
    }
    case 'listitem':
      if (!el.querySelector(SIGNIFICANT)) {
        emit(state, depth, `- ${clip(textOf(el), 500)}`)
        return
      }
      emit(state, depth, '-')
      walkChildren(el, depth + 1, state)
      return
  }
  if (ALERT_ROLES.has(role)) {
    const text = clip(textOf(el), 500)
    if (text) emit(state, depth, `${role} ${quote(text)}`)
    return
  }
  if (CONTAINER_ROLES.has(role)) {
    const label = collapse(el.getAttribute('aria-label'))
    emit(state, depth, label ? `${role} ${quote(clip(label, 120))}` : role)
    walkChildren(el, depth + 1, state)
    return
  }
  if (!el.querySelector(SIGNIFICANT)) {
    emit(state, depth, clip(textOf(el), 500))
    return
  }
  walkChildren(el, depth, state)
}

// ---------- page ----------

function zIndexOf(el: Element): number {
  let top = 0
  for (let node: Element | null = el; node && node !== document.body; node = node.parentElement) {
    const z = Number.parseInt(getComputedStyle(node).zIndex, 10)
    if (Number.isFinite(z)) top = Math.max(top, z)
  }
  return top
}

/** The topmost open dialog or drawer, else the routed page. */
function pageRoot(): { root: HTMLElement; overlay: boolean } {
  const overlays = [...document.querySelectorAll<HTMLElement>(OVERLAYS)]
    .filter(el => !el.closest(IGNORED) && !el.querySelector('.assistant-panel') && isShown(el))
  const outermost = overlays.filter(el => !overlays.some(other => other !== el && other.contains(el)))
  if (outermost.length) {
    let top = outermost[0]
    let topZ = zIndexOf(top)
    for (const el of outermost.slice(1)) {
      const z = zIndexOf(el)
      if (z >= topZ) {
        top = el
        topZ = z
      }
    }
    return { root: top, overlay: true }
  }
  const outlet = document.querySelector<HTMLElement>('.platform-route-outlet') ||
    document.querySelector<HTMLElement>('main') || document.body
  return { root: outlet, overlay: false }
}

const TITLE_SELECTORS = ['.setting-drawer__title', '.t-dialog__header', '.t-drawer__header', 'h1', 'h2', '[role="heading"]']

function titleOf(root: HTMLElement): string {
  const label = collapse(root.getAttribute('aria-label'))
  if (label) return clip(label, 120)
  for (const selector of TITLE_SELECTORS) {
    const text = textOf(root.querySelector(selector))
    if (text) return clip(text, 120)
  }
  return ''
}

function locationOf(root: HTMLElement, overlay: boolean): string {
  const path = `${window.location.pathname}${window.location.hash}`
  const pageTitle = titleOf(document.querySelector<HTMLElement>('.platform-route-outlet') || document.body) || document.title
  if (overlay) {
    const title = titleOf(root)
    return clip(`${title ? `${title}, over ` : 'A dialog over '}${pageTitle} (${path})`, 400)
  }
  return clip(`${pageTitle} (${path})`, 400)
}

/** The title of the dialog or drawer open over the page; '' when none is. */
export function currentView(): string {
  const { root, overlay } = pageRoot()
  if (!overlay) return ''
  const title = titleOf(root)
  // Settings-style dialogs name the part in view in their side navigation.
  const section = textOf(root.querySelector('.settings-nav .nav-item.active .nav-label'))
  return section && section !== title ? clip(title ? `${title} / ${section}` : section, 120) : title
}

/** Whether a node is part of the assistant's own UI rather than the page. */
export function isAssistantUI(node: Node | null): boolean {
  const el = node instanceof Element ? node : node?.parentElement
  return !!el?.closest(IGNORED)
}

/**
 * The page text field an element is, when the assistant may write to it,
 * with the ref read_page gives it.
 */
export function editableTextField(el: Element | null): { field: PageField; ref: string } | null {
  if (!(el instanceof HTMLTextAreaElement || el instanceof HTMLInputElement) || isAssistantUI(el)) return null
  const field = fieldForControl(el)
  if (!field || (field.kind !== 'text' && field.kind !== 'multiline')) return null
  if (field.secret || field.disabled || field.readonly) return null
  return { field, ref: refFor(field.control) }
}

/** Where the user is: the open dialog or the page, and its path. */
export function currentLocation(): string {
  const { root, overlay } = pageRoot()
  return locationOf(root, overlay)
}

/** The page as the user sees it now. */
export function snapshotPage(budget = DEFAULT_BUDGET): PageSnapshot {
  const { root, overlay } = pageRoot()
  const state: WalkState = { lines: [], chars: 0, budget, elements: 0, truncated: false }
  if (overlay) {
    const title = titleOf(root)
    emit(state, 0, title ? `dialog ${quote(title)}` : 'dialog')
    walkChildren(root, 1, state)
  } else {
    walkChildren(root, 0, state)
  }
  const truncated = state.truncated
  const messages = [...document.querySelectorAll<HTMLElement>('.t-message, .t-notification')]
    .filter(el => !root.contains(el) && isShown(el))
    .map(el => clip(textOf(el), 300))
    .filter(Boolean)
  state.truncated = false
  state.budget += 2000
  if (messages.length) {
    emit(state, 0, 'messages on screen:')
    messages.forEach(text => emit(state, 1, `alert ${quote(text)}`))
  }
  if (truncated) emit(state, 0, '… the page goes on; only the part above was read')
  if (!state.lines.length) emit(state, 0, '(nothing readable is shown)')
  return { location: locationOf(root, overlay), snapshot: state.lines.join('\n') }
}

/** Lines of a text field, numbered, for reading a long text in parts. */
export function readFieldText(ref: string, startLine: number, lineCount: number): PageSnapshot {
  const field = resolveField(ref, false)
  const { root, overlay } = pageRoot()
  const location = locationOf(root, overlay)
  if (field.kind !== 'text' && field.kind !== 'multiline') {
    return { location, snapshot: describeField(field, ref).line }
  }
  const value = String(readFieldValue(field))
  const { text, total, last } = readLines(value, startLine, lineCount)
  const head = `${ROLE_NAMES[field.kind]}${field.label ? ` ${quote(clip(field.label, 80))}` : ''} [ref=${ref}] ` +
    `lines ${Math.min(startLine, total)}-${last} of ${total}`
  const tail = last < total ? `\n… ${total - last} more lines` : ''
  return { location, snapshot: `${head}\n${text}${tail}` }
}
