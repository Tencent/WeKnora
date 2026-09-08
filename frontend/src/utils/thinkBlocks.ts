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
 */
export function parseThinkBlocks(raw: string): ThinkBlockParseResult {
  const thinkParts: string[] = []
  let answer = ''
  let rest = String(raw ?? '')
  let inThink = false

  while (rest) {
    if (inThink) {
      const close = rest.indexOf(THINK_CLOSE)
      if (close === -1) {
        thinkParts.push(rest)
        rest = ''
      } else {
        thinkParts.push(rest.slice(0, close))
        rest = rest.slice(close + THINK_CLOSE.length)
        inThink = false
      }
      continue
    }
    const open = rest.indexOf(THINK_OPEN)
    if (open === -1) {
      answer += rest
      rest = ''
      break
    }
    answer += rest.slice(0, open)
    rest = rest.slice(open + THINK_OPEN.length)
    inThink = true
  }

  return {
    think: thinkParts.join('\n').trim(),
    answer,
    thinking: inThink,
  }
}

/** True when the content contains any think-tag markup at all. */
export function hasThinkMarkup(raw: string): boolean {
  const s = String(raw ?? '')
  return s.includes(THINK_OPEN) || s.includes(THINK_CLOSE)
}
