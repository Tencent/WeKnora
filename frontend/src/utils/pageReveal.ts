/**
 * Shows the user where the assistant changed a text field: scrolls the change
 * into view and lays a fading highlight over its lines. The field is not
 * focused, so whatever the user types next still goes where they were typing.
 */

const RANGE_CLASS = 'assistant-page-range'
const RANGE_MS = 2600

/** The span of after that differs from before. */
export function changedRange(before: string, after: string): { start: number; end: number } | null {
  if (before === after) return null
  let start = 0
  const max = Math.min(before.length, after.length)
  while (start < max && before[start] === after[start]) start++
  let tail = 0
  while (tail < max - start && before[before.length - 1 - tail] === after[after.length - 1 - tail]) tail++
  return { start, end: Math.max(start, after.length - tail) }
}

const MIRRORED = [
  'paddingLeft', 'paddingRight', 'borderLeftWidth', 'borderRightWidth',
  'fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'letterSpacing', 'lineHeight', 'tabSize',
  'whiteSpace', 'wordBreak', 'overflowWrap', 'wordSpacing', 'textIndent',
] as const

/** Height of a text laid out as the textarea lays it out. */
function textHeight(textarea: HTMLTextAreaElement, text: string): number {
  if (!text) return 0
  const style = getComputedStyle(textarea)
  const mirror = document.createElement('textarea')
  for (const key of MIRRORED) mirror.style[key] = style[key]
  // Border-box at the textarea's inner width, so a scrollbar narrows both alike.
  const borders = (parseFloat(style.borderLeftWidth) || 0) + (parseFloat(style.borderRightWidth) || 0)
  mirror.style.width = `${textarea.clientWidth + borders}px`
  Object.assign(mirror.style, {
    boxSizing: 'border-box',
    position: 'fixed', top: '0', left: '-10000px', height: '0', minHeight: '0',
    paddingTop: '0', paddingBottom: '0', borderTopWidth: '0', borderBottomWidth: '0',
    overflow: 'hidden', visibility: 'hidden',
  })
  mirror.value = text
  document.body.appendChild(mirror)
  const height = mirror.scrollHeight
  mirror.remove()
  return height
}

function scrollParent(el: HTMLElement): HTMLElement | null {
  for (let node = el.parentElement; node && node !== document.body; node = node.parentElement) {
    const overflow = getComputedStyle(node).overflowY
    if ((overflow === 'auto' || overflow === 'scroll') && node.scrollHeight > node.clientHeight) return node
  }
  return null
}

/** Scrolls a textarea's change into view and highlights its lines. */
export function revealTextChange(textarea: HTMLTextAreaElement, before: string, after: string) {
  const range = changedRange(before, after)
  if (!range || !textarea.isConnected) return
  const value = textarea.value
  const lineStart = value.lastIndexOf('\n', range.start - 1) + 1
  const nextBreak = value.indexOf('\n', Math.max(range.start, range.end - 1))
  const lineEnd = nextBreak < 0 ? value.length : nextBreak
  const top = lineStart > 0 ? textHeight(textarea, value.slice(0, lineStart - 1)) : 0
  const bottom = Math.max(top + 1, textHeight(textarea, value.slice(0, lineEnd) || ' '))

  const style = getComputedStyle(textarea)
  const padTop = parseFloat(style.paddingTop) || 0
  const borderTop = parseFloat(style.borderTopWidth) || 0
  if (textarea.scrollHeight > textarea.clientHeight + 1) {
    textarea.scrollTop = Math.max(0, top - textarea.clientHeight / 3)
  } else {
    // The textarea grows with its text; whatever holds it scrolls.
    const holder = scrollParent(textarea)
    if (holder) {
      const offset = textarea.getBoundingClientRect().top + borderTop + padTop + top - holder.getBoundingClientRect().top
      holder.scrollTop += offset - holder.clientHeight / 3
    }
  }
  textarea.scrollIntoView({ block: 'nearest' })

  const box = textarea.getBoundingClientRect()
  const contentTop = box.top + borderTop + padTop - textarea.scrollTop
  const y1 = Math.max(box.top, contentTop + top)
  const y2 = Math.min(box.bottom, contentTop + bottom)
  if (y2 <= y1) return
  const mark = document.createElement('div')
  mark.className = RANGE_CLASS
  mark.setAttribute('data-assistant-ignore', '')
  Object.assign(mark.style, {
    left: `${box.left + (parseFloat(style.borderLeftWidth) || 0)}px`,
    width: `${textarea.clientWidth}px`,
    top: `${y1}px`,
    height: `${y2 - y1}px`,
  })
  document.body.appendChild(mark)
  setTimeout(() => mark.remove(), RANGE_MS)
}
