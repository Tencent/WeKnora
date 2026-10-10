// The backend uses this explanatory answer when no partial text exists.
const noPartialAnswer = "Sorry, this answer kept hitting the model's per-response output limit " +
  "before any text was produced. Try narrowing the question, or raise the agent's " +
  "max_completion_tokens setting.";

interface Answer {
  type?: string;
  superseded?: boolean;
  truncated?: boolean;
  is_fallback?: boolean;
  content?: unknown;
}

export function isPartialTruncatedAnswer(answer: Answer | undefined): boolean {
  const content = String(answer?.content || '').trim();
  return answer?.truncated === true && !answer.is_fallback && !!content && content !== noPartialAnswer;
}

export function isFinalPartialTruncatedAnswer(answer: Answer, stream: Answer[] = []): boolean {
  return answer === stream.filter(event => event.type === 'answer' && !event.superseded).at(-1)
    && isPartialTruncatedAnswer(answer);
}
