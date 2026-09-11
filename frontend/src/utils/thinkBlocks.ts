/**
 * Parse inline <think>…</think> blocks that some thinking models embed in the
 * plain content channel (providers whose thinking toggle cannot be disabled
 * server-side, e.g. Minimax-M2 or several vLLM/Ollama Qwen deployments).
 *
 * A model may emit MORE than one block — one per reasoning round — so both
 * the open and the close side must be handled globally. Parsing with a single
 * `indexOf('<think>')` / `lastIndexOf('</think>')` / one-shot `replace` pair
 * leaks the inner tags back into the thinking card (#3099).
 */

const THINK_OPEN = '<think>'
const THINK_CLOSE = '</think>'

export interface ThinkBlockParseResult {
  /** All reasoning text with the tags stripped ('' when there is none). */
  think: string
  /** Text outside any think block (partial while `thinking` is true). */
  answer: string
  /** True when the content ends inside an unterminated <think> block. */
  thinking: boolean
}

/**
 * Split `raw` into think-block reasoning and answer text.
 *
 * - Text before/after terminated blocks is answer text, in order.
 * - Text inside blocks (including an unterminated trailing block) is think
 *   text; segments are joined with '\n' and trimmed.
 * - A stray close tag without a matching open tag has no block to close and
 *   stays in the answer text as-is.
 * - Think-tag markup inside fenced code blocks (``` / ~~~) or inline code
 *   spans (`` ` `` runs) is literal document content, not a block boundary:
 *   a long markdown answer that merely MENTIONS the tags must not get its
 *   head chopped into the thinking card (#3132). Real think blocks live at
 *   the top level, so skipping code regions cannot misplace reasoning.
 *
 * The fence/span handling is a chat-rendering heuristic, not a full
 * CommonMark parser: tabs, info strings containing backticks, setext
 * headings and 4-space indented code blocks are not modelled.
 */
export function parseThinkBlocks(raw: string): ThinkBlockParseResult {
  const s = String(raw ?? '')
  const thinkParts: string[] = []
  let answer = ''
  let inThink = false
  let i = 0
  let chunkStart = 0

  while (i < s.length) {
    if (inThink) {
      // Inside a real block only the close tag matters: fences and backticks
      // in the reasoning itself are just text.
      const close = s.indexOf(THINK_CLOSE, i)
      if (close === -1) {
        thinkParts.push(s.slice(i))
        return { think: thinkParts.join('\n').trim(), answer, thinking: true }
      }
      thinkParts.push(s.slice(i, close))
      i = close + THINK_CLOSE.length
      inThink = false
      chunkStart = i
      continue
    }

    if (isLineStart(s, i)) {
      const fence = matchFenceOpen(s, i)
      if (fence) {
        answer += s.slice(chunkStart, i)
        const closeEnd = findFenceClose(s, fence.end, fence.char, fence.len)
        if (closeEnd === -1) {
          // Unterminated fence consumes the rest — matching how a markdown
          // renderer treats an unclosed fence — so tags after it stay literal.
          answer += s.slice(i)
          return { think: thinkParts.join('\n').trim(), answer, thinking: false }
        }
        answer += s.slice(i, closeEnd)
        i = closeEnd
        chunkStart = i
        continue
      }
    }

    if (s.startsWith(THINK_OPEN, i)) {
      answer += s.slice(chunkStart, i)
      i += THINK_OPEN.length
      inThink = true
      continue
    }
    if (s.startsWith(THINK_CLOSE, i)) {
      answer += s.slice(chunkStart, i) + THINK_CLOSE
      i += THINK_CLOSE.length
      chunkStart = i
      continue
    }
    if (s[i] === '`') {
      const run = backtickRunLen(s, i)
      const closeAt = findBacktickRun(s, i + run, run)
      answer += s.slice(chunkStart, i)
      if (closeAt === -1) {
        // Unterminated inline span consumes the rest, for the same reason as
        // an unterminated fence.
        answer += s.slice(i)
        return { think: thinkParts.join('\n').trim(), answer, thinking: false }
      }
      answer += s.slice(i, closeAt + run)
      i = closeAt + run
      chunkStart = i
      continue
    }

    i += 1
  }
  answer += s.slice(chunkStart)
  return { think: thinkParts.join('\n').trim(), answer, thinking: false }
}

function isLineStart(s: string, idx: number): boolean {
  return idx === 0 || s[idx - 1] === '\n'
}

/** Length of the backtick run starting at s[idx]. */
function backtickRunLen(s: string, idx: number): number {
  let n = 0
  while (idx + n < s.length && s[idx + n] === '`') n += 1
  return n
}

/** Index of the next backtick run of exactly `len` backticks, or -1. */
function findBacktickRun(s: string, from: number, len: number): number {
  let i = from
  for (;;) {
    const at = s.indexOf('`', i)
    if (at === -1) return -1
    if (backtickRunLen(s, at) === len) return at
    i = at + 1
  }
}

/**
 * Match a fenced code block opener at a line start: up to 3 leading spaces,
 * then >= 3 '`' or '~' characters. Returns the fence character/length and the
 * index right after the fence line (including its newline), or null.
 */
function matchFenceOpen(
  s: string,
  idx: number
): { char: string; len: number; end: number } | null {
  let i = idx
  let spaces = 0
  while (i < s.length && s[i] === ' ' && spaces < 3) {
    i += 1
    spaces += 1
  }
  const ch = s[i]
  if (ch !== '`' && ch !== '~') return null
  let len = 0
  while (i + len < s.length && s[i + len] === ch) len += 1
  if (len < 3) return null
  let end = i + len
  while (end < s.length && s[end] !== '\n') end += 1
  if (end < s.length) end += 1
  return { char: ch, len, end }
}

/**
 * Find the end (index after the closing line) of the fence opened with
 * `char`×`len`. A closing fence is a line with up to 3 leading spaces, a run
 * of at least `len` of the same character, and nothing else. -1 when the
 * fence is never closed.
 */
function findFenceClose(s: string, from: number, char: string, len: number): number {
  let i = from
  for (;;) {
    let j = i
    let spaces = 0
    while (j < s.length && s[j] === ' ' && spaces < 3) {
      j += 1
      spaces += 1
    }
    let k = j
    while (k < s.length && s[k] === char) k += 1
    if (k - j >= len) {
      let e = k
      while (e < s.length && (s[e] === ' ' || s[e] === '\t')) e += 1
      if (e >= s.length) return s.length
      if (s[e] === '\n') return e + 1
    }
    const nl = s.indexOf('\n', i)
    if (nl === -1) return -1
    i = nl + 1
  }
}

/** True when the content contains any think-tag markup at all. */
export function hasThinkMarkup(raw: string): boolean {
  const s = String(raw ?? '')
  return s.includes(THINK_OPEN) || s.includes(THINK_CLOSE)
}
