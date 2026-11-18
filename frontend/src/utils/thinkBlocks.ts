/**
 * Split inline `<think>…</think>` reasoning from user-facing answer text.
 *
 * Some models (DeepSeek, Qwen, …) put reasoning in the plain `content` field
 * instead of a separate `reasoning_content` field. A response may contain
 * several reasoning rounds, so both the opening and closing markers must be
 * handled globally. A tag is only a protocol marker outside Markdown code:
 * answers that merely show the markup in a fenced block or inline code span
 * must render verbatim (#3132).
 *
 * This mirrors the code-region rules in `internal/agent/tools/think_stream.go`:
 * backtick/tilde runs toggle a code region, and a backslash escapes the next
 * character outside code. It is a rendering heuristic, not a full CommonMark
 * parser; indented code blocks and tags split across separately parsed strings
 * are intentionally out of scope (callers parse the accumulated content).
 */

const THINK_OPEN = '<think>'
const THINK_CLOSE = '</think>'

export interface ThinkBlockParseResult {
  /** Reasoning text from every real block, tags removed and segments joined. */
  think: string
  /** User-facing answer text with real think blocks removed. */
  answer: string
  /** True when at least one real `<think>` opener was found. */
  found: boolean
  /** True when the content ends inside an unterminated real think block. */
  open: boolean
}

/**
 * Track Markdown code delimiters while scanning answer text.
 *
 * A run of backticks is an inline code span when it is shorter than three
 * characters; longer backtick runs and runs of at least three tildes are
 * fenced code blocks. The same character with an equal (inline) or longer
 * (fence) run closes the region. Returns true when the consumed character is
 * literal code content or escaped, so callers must not treat it as a tag.
 */
class MarkdownCodeState {
  private ticks = 0
  private marker = ''
  private run = 0
  private runMarker = ''
  private escaped = false

  consume(char: string): boolean {
    if (this.escaped) {
      this.escaped = false
      return true
    }
    if (char === '`' || char === '~') {
      if (this.run > 0 && this.runMarker !== char) this.finishRun()
      this.runMarker = char
      this.run += 1
      return true
    }
    this.finishRun()
    if (char === '\\' && this.ticks === 0) {
      this.escaped = true
      return true
    }
    return this.ticks !== 0
  }

  private finishRun(): void {
    if (this.run === 0) return
    if (this.ticks === 0 && (this.runMarker === '`' || this.run >= 3)) {
      this.ticks = this.run
      this.marker = this.runMarker
    } else if (
      this.marker === this.runMarker &&
      (this.run === this.ticks || (this.ticks >= 3 && this.run >= this.ticks))
    ) {
      this.ticks = 0
      this.marker = ''
    }
    this.run = 0
  }
}

/**
 * Split accumulated content into reasoning and answer text.
 *
 * - Text before, between and after terminated blocks is answer text, in order.
 * - Text inside a block (including an unterminated trailing block) is
 *   reasoning text. Reasoning segments are trimmed and joined with a newline.
 * - A stray close tag with no open tag stays in the answer as literal text.
 * - Markup inside fenced code blocks or inline code spans is literal content.
 * - A backslash-escaped opener is literal too.
 */
export function parseThinkBlocks(raw: string): ThinkBlockParseResult {
  const source = String(raw ?? '')
  if (!source.includes(THINK_OPEN)) {
    return { think: '', answer: source, found: false, open: false }
  }

  const markdown = new MarkdownCodeState()
  const thinkParts: string[] = []
  let answer = ''
  let answerStart = 0
  let inThink = false
  let found = false
  let i = 0

  while (i < source.length) {
    if (inThink) {
      const close = source.indexOf(THINK_CLOSE, i)
      if (close === -1) {
        thinkParts.push(source.slice(i))
        return finish(answer, true)
      }
      thinkParts.push(source.slice(i, close))
      i = close + THINK_CLOSE.length
      inThink = false
      answerStart = i
      continue
    }

    const literal = markdown.consume(source[i])
    if (!literal && source[i] === '<' && source.startsWith(THINK_OPEN, i)) {
      answer += source.slice(answerStart, i)
      i += THINK_OPEN.length
      inThink = true
      found = true
      answerStart = i
      continue
    }
    i += 1
  }

  answer += source.slice(answerStart)
  return finish(answer, false, found)

  function finish(
    finalAnswer: string | null,
    open: boolean,
    hasOpener = false
  ): ThinkBlockParseResult {
    return {
      think: thinkParts.map((part) => part.trim()).filter(Boolean).join('\n'),
      answer: finalAnswer ?? '',
      found: hasOpener || open,
      open,
    }
  }
}
