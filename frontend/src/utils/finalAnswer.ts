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
