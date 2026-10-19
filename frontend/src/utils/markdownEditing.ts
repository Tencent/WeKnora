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

/** Only a matching, sufficiently long fence with no info string closes a block. */
function isInsideCodeFence(value: string, lineStart: number): boolean {
  let fence = ''
  let fenceIndent = 0
  const listIndents = [0]
  // Tabs advance to the next four-column stop in Markdown container prefixes.
  const expandTabs = (line: string) => {
    let expanded = ''
    for (const char of line) expanded += char === '\t' ? ' '.repeat(4 - (expanded.length % 4)) : char
    return expanded
  }
  const indentation = (line: string) => /^ */.exec(line)![0].length

  for (const rawLine of value.slice(0, lineStart).split(/\r?\n/)) {
    const line = expandTabs(rawLine)
    if (fence) {
      if (line.trim() && indentation(line) < fenceIndent) {
        // An unclosed fence ends when its containing list item ends.
        fence = ''
      } else {
        const closer = /^ {0,3}(`{3,}|~{3,})[ \t]*$/.exec(line.slice(fenceIndent))
        if (closer && closer[1]![0] === fence[0] && closer[1]!.length >= fence.length) {
          fence = ''
        }
        // List markers inside code are literal, never container prefixes.
        continue
      }
    }
    while (listIndents.length > 1 && line.trim() && indentation(line) < listIndents.at(-1)!) {
      listIndents.pop()
    }
    let offset = listIndents.at(-1)!
    let content = line.slice(offset)
    let prefix: RegExpExecArray | null
    while ((prefix = /^ {0,3}(?:[-*+]|\d{1,9}[.)]) {1,4}(?=\S|$)/.exec(content))) {
      offset += prefix[0].length
      listIndents.push(offset)
      content = line.slice(offset)
    }
    const match = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(content)
    if (!match) continue
    const [, delimiter = '', rest = ''] = match
    if (delimiter[0] !== '`' || !rest.includes('`')) {
      // Backtick fences cannot have backticks in their info string.
      fence = delimiter
      fenceIndent = offset
    }
  }
  const currentLine = expandTabs(value.slice(lineStart, lineEndAt(value, lineStart)))
  return fence !== '' && (!currentLine.trim() || indentation(currentLine) >= fenceIndent)
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
  if (isInsideCodeFence(value, lineStart)) return null

  const [, indent, marker, checkbox, content] = match
  const remainingContent = value.slice(start, lineEndAt(value, start))
  if (!content.trim() && !remainingContent.trim()) {
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
  // Textarea selections exclude `end`: a selection ending just after a
  // newline does not include the following line. Keep that newline selected
  // after rewriting so another Tab still operates on the same block.
  const endsAtLineStart = end > start && value[end - 1] === '\n'
  const blockEnd = lineEndAt(value, endsAtLineStart ? end - 1 : end)
  const rewritten = value.slice(blockStart, blockEnd).split('\n').map(transform).join('\n')
  return {
    value: value.slice(0, blockStart) + rewritten + value.slice(blockEnd),
    start: blockStart,
    end: blockStart + rewritten.length + (endsAtLineStart ? 1 : 0),
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
    const outdented = mapSelectedLines(state, (line) => line.replace(/^(?: {1,2}|\t)/, ''))
    if (start !== end) return outdented
    // A caret must stay collapsed so the next keystroke does not replace the item.
    const removedIndent = value.length - outdented.value.length
    const caret = Math.max(lineStart, start - removedIndent)
    return { ...outdented, start: caret, end: caret }
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
