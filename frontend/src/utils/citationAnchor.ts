/**
 * The answer sentence a citation marker supports: the text of the marker's
 * block (paragraph, list item, table cell) up to the marker, without other
 * citation markers, trimmed to its last sentence. A marker on a line of its
 * own ("依据：[1]") borrows the blocks just before it.
 */

const BLOCK_SELECTOR = 'li, p, td, th, blockquote, h1, h2, h3, h4, h5, h6, dd, dt'
const CITATION_SELECTOR = '.citation, .citation-kb, .citation-web, .citation-wiki'
const MIN_SENTENCE = 8
const MAX_LOOKBACK = 3
// Lookback never leaves the rendered answer.
const ANSWER_ROOT_SELECTOR = '.markdown-content, body, [contenteditable]'

function letterCount(text: string): number {
  return text.replace(/[\s\p{P}\p{S}]/gu, '').length
}

/** Last sentence of `text`, extended backwards while it is too short to align. */
export function lastSentence(text: string): string {
  const pieces = String(text || '')
    .replace(/\s+/g, ' ')
    .split(/(?<=[。！？!?；;])/u)
    .map((s) => s.trim())
    .filter(Boolean)
  let out = ''
  for (let i = pieces.length - 1; i >= 0; i--) {
    out = pieces[i] + out
    if (letterCount(out) >= MIN_SENTENCE) break
  }
  return out.slice(-300)
}

/**
 * Anchor for a marker whose own text is `own`, prepending the texts of the
 * blocks before it (nearest first) while the sentence is too short to align.
 */
export function anchorWithContext(own: string, previous: string[]): string {
  let text = own
  for (const prev of previous) {
    if (letterCount(lastSentence(text)) >= MIN_SENTENCE) break
    text = `${prev} ${text}`
  }
  return lastSentence(text)
}

function textWithoutCitations(node: Node): string {
  const clone = node.cloneNode(true) as Element | DocumentFragment
  clone.querySelectorAll?.(CITATION_SELECTOR).forEach((n) => n.remove())
  return clone.textContent || ''
}

/**
 * Text preceding `block` at each enclosing level, nearest first: earlier
 * siblings, and for a nested list the text of the item that holds it.
 */
function previousBlockTexts(block: Element): string[] {
  const out: string[] = []
  for (let node: Element | null = block; node?.parentElement && out.length < MAX_LOOKBACK; node = node.parentElement) {
    if (node.matches(ANSWER_ROOT_SELECTOR)) break
    const range = node.ownerDocument.createRange()
    range.setStart(node.parentElement, 0)
    range.setEndBefore(node)
    const text = textWithoutCitations(range.cloneContents()).trim()
    if (text) out.push(text)
  }
  return out
}

export function citationAnchorText(marker: Element): string {
  try {
    const block = marker.closest(BLOCK_SELECTOR) || marker.parentElement
    if (!block) return ''
    const range = marker.ownerDocument.createRange()
    range.setStart(block, 0)
    range.setEndBefore(marker)
    const own = textWithoutCitations(range.cloneContents())
    const enough = letterCount(lastSentence(own)) >= MIN_SENTENCE
    return anchorWithContext(own, enough ? [] : previousBlockTexts(block))
  } catch {
    // Alignment only narrows the highlight; without a sentence the whole chunk is shown.
    return ''
  }
}
