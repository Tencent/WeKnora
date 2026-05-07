// finalAnswer.ts
//
// Helpers for normalising LLM-emitted final answers before rendering.
//
// Background: not every model reliably calls our `final_answer` tool. Many
// models — especially smaller ones or those SFT'd on different conventions —
// instead embed the answer inside <answer>…</answer>, <final_answer>…</final_answer>,
// or prefix it with "Final Answer:" / "最终答案：". When the agent loop accepts
// such a natural-stop response as the final answer, those wrappers leak into
// the rendered output. This module provides a single helper to strip them
// before the markdown renderer sees the text.
//
// The function is intentionally conservative: it only strips a wrapper when
// it covers the *entire* trimmed content. We don't want to corrupt user-
// authored markdown that happens to contain the word "Final Answer:" or an
// XML-style tag in the middle of a sentence.

const ANSWER_TAG_RE =
  /^\s*<(answer|final_answer|final-answer)\b[^>]*>([\s\S]*?)<\/\1>\s*$/i;

const FENCED_ANSWER_RE =
  /^\s*```(?:final_answer|answer)\s*\n?([\s\S]*?)\n?```\s*$/i;

const ANSWER_PREFIX_RE =
  /^\s*(?:final\s*answer|最终答案|答案|答)\s*[:：]\s*/i;

/**
 * Remove common "final answer" wrappers that some models emit instead of
 * calling the structured `final_answer` tool. Returns the original string
 * (trimmed only when stripping happens) when no wrapper is detected.
 *
 * Recognised wrappers (must cover the entire trimmed content):
 *  - `<answer>…</answer>` / `<final_answer>…</final_answer>` (case-insensitive)
 *  - ```` ```final_answer\n…\n``` ```` fenced code block
 *  - Leading `Final Answer:` / `最终答案：` / `答：` prefix
 */
export function unwrapFinalAnswerWrappers(content: string): string {
  if (!content || typeof content !== 'string') {
    return content ?? '';
  }

  let result = content;
  let changed = false;

  // Strip outer XML-style answer tags. Loop in case the model nested them
  // (e.g. <final_answer><answer>…</answer></final_answer>), but cap iterations
  // to avoid pathological inputs.
  for (let i = 0; i < 3; i++) {
    const tagMatch = result.match(ANSWER_TAG_RE);
    if (!tagMatch) break;
    result = tagMatch[2];
    changed = true;
  }

  // Strip fenced "```final_answer" code block wrappers.
  const fencedMatch = result.match(FENCED_ANSWER_RE);
  if (fencedMatch) {
    result = fencedMatch[1];
    changed = true;
  }

  // Strip a leading "Final Answer:" / "最终答案：" prefix when it is the very
  // first non-whitespace token. Only applied once.
  const prefixMatch = result.match(ANSWER_PREFIX_RE);
  if (prefixMatch) {
    result = result.slice(prefixMatch[0].length);
    changed = true;
  }

  return changed ? result.trim() : result;
}

const THINK_BLOCK_RE = /<think\b[^>]*>[\s\S]*?<\/think>/gi;

/**
 * Normalise content for cross-event comparison. Strips <think>…</think>
 * blocks (which appear in raw thinking chunks but not in the final answer
 * event), strips final-answer wrappers, and collapses whitespace. Used to
 * detect when the natural-stop path emits the same content twice (once as
 * streaming thinking chunks and once as a final answer event).
 */
export function normaliseForComparison(content: string): string {
  if (!content || typeof content !== 'string') return '';
  const stripped = content.replace(THINK_BLOCK_RE, '');
  const unwrapped = unwrapFinalAnswerWrappers(stripped);
  return unwrapped.replace(/\s+/g, ' ').trim();
}

/**
 * Returns true when `thinking` and `answer` represent the same final answer
 * after normalisation. Tolerates small differences (≤2% length delta with
 * matching head/tail) to absorb chunk-boundary whitespace noise.
 */
export function thinkingEqualsAnswer(thinking: string, answer: string): boolean {
  const a = normaliseForComparison(thinking);
  const b = normaliseForComparison(answer);
  if (!a || !b) return false;
  if (a === b) return true;

  const maxLen = Math.max(a.length, b.length);
  // Require at least ~80 chars of signal to avoid matching trivial prefixes.
  if (maxLen < 80) return false;

  const lenDelta = Math.abs(a.length - b.length);
  if (lenDelta > maxLen * 0.02) return false;

  const head = 50;
  const tail = 50;
  return (
    a.slice(0, head) === b.slice(0, head) &&
    a.slice(-tail) === b.slice(-tail)
  );
}
