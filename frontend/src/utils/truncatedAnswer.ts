// The backend uses this explanatory answer when no partial text exists.
const noPartialAnswer = "Sorry, this answer kept hitting the model's per-response output limit " +
  "before any text was produced. Try narrowing the question, or raise the agent's " +
  "max_completion_tokens setting.";

export function isPartialTruncatedAnswer(answer: { truncated?: boolean; is_fallback?: boolean; content?: unknown } | undefined): boolean {
  return answer?.truncated === true && !answer.is_fallback && String(answer.content || '').trim() !== noPartialAnswer;
}
