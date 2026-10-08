/**
 * Keyboard behaviours a plain <textarea> does not give a Markdown author:
 * continuing a list on Enter and indenting a block with Tab.
 *
 * Everything here is a pure string transform — the component owns the DOM and
 * only applies the returned value/selection.
 */

export interface EditorState {
  value: string
  start: number
  end: number
}

/** A `null` result means "let the browser handle the key normally". */
export type EditorPatch = EditorState | null

/** Indent unit: two spaces, the width Markdown nests a list item with. */
const INDENT = '  '

/** `- `, `* `, `+ `, `1. `, `1) `, optionally followed by a `[ ]` checkbox. */
const LIST_LINE_RE = /^([ \t]*)([-*+]|\d+[.)])[ \t]+(\[[ xX]\][ \t]+)?(.*)$/

const lineStartAt = (value: string, index: number): number => {
  if (index <= 0) return 0
  const previousBreak = value.lastIndexOf('\n', index - 1)
  return previousBreak === -1 ? 0 : previousBreak + 1
}

const lineEndAt = (value: string, index: number): number => {
  const nextBreak = value.indexOf('\n', index)
  return nextBreak === -1 ? value.length : nextBreak
}

/**
 * Enter inside a list item keeps the list going.
 *
 * An item that is still empty ends the list instead — the same escape hatch
 * every Markdown editor has, so Enter twice gets you back to a paragraph.
 */
export function continueListOnEnter({ value, start, end }: EditorState): EditorPatch {
  // A range selection replaces text; leave that to the browser.
  if (start !== end) return null

  const lineStart = lineStartAt(value, start)
  const match = LIST_LINE_RE.exec(value.slice(lineStart, start))
  if (!match) return null

  const [, indent, marker, checkbox, content] = match
  if (!content.trim()) {
    // Empty item: drop the marker and leave the caret on a clean line.
    const nextValue = value.slice(0, lineStart) + value.slice(start)
    return { value: nextValue, start: lineStart, end: lineStart }
  }

  const nextMarker = /^\d+[.)]$/.test(marker)
    ? `${parseInt(marker, 10) + 1}${marker.slice(-1)}`
    : marker
  const prefix = `\n${indent}${nextMarker} ${checkbox ? '[ ] ' : ''}`
  const caret = start + prefix.length
  return { value: value.slice(0, start) + prefix + value.slice(start), start: caret, end: caret }
}

const hasLineBreak = (value: string, start: number, end: number) =>
  value.slice(start, end).includes('\n')

/** Rewrite every line the selection touches, keeping it selected. */
function mapSelectedLines(
  { value, start, end }: EditorState,
  transform: (line: string) => string,
): EditorState {
  const blockStart = lineStartAt(value, start)
  const blockEnd = lineEndAt(value, end)
  const rewritten = value.slice(blockStart, blockEnd).split('\n').map(transform).join('\n')
  return {
    value: value.slice(0, blockStart) + rewritten + value.slice(blockEnd),
    start: blockStart,
    end: blockStart + rewritten.length,
  }
}

/**
 * Tab indents, Shift+Tab outdents.
 *
 * Only claimed when it would actually mean something — a multi-line selection,
 * or a caret sitting in a list item. Anywhere else Tab still moves focus, which
 * is the only way out of the textarea for keyboard users.
 */
export function indentOnTab(state: EditorState, outdent: boolean): EditorPatch {
  const { value, start, end } = state
  const multiLine = hasLineBreak(value, start, end)
  const lineStart = lineStartAt(value, start)
  const inList = LIST_LINE_RE.test(value.slice(lineStart, lineEndAt(value, end)))
  if (!multiLine && !inList) return null

  if (outdent) {
    return mapSelectedLines(state, (line) => line.replace(/^(?: {1,2}|\t)/, ''))
  }
  if (multiLine) {
    return mapSelectedLines(state, (line) => (line.trim() ? INDENT + line : line))
  }
  // Single list line: indent the line, not the caret position inside it.
  const indented = mapSelectedLines(state, (line) => INDENT + line)
  const caret = start + INDENT.length
  return { ...indented, start: caret, end: caret + (end - start) }
}

/** Reading stats shown next to the draft tag in the footer. */
export function countContent(value: string): { characters: number; lines: number } {
  const text = value ?? ''
  return {
    characters: text.length,
    lines: text ? text.split('\n').length : 0,
  }
}
