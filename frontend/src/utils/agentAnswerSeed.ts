export type ChatMessage = Record<string, unknown>

/**
 * Whether a fresh `answer` event may adopt the message's current text as its
 * own starting content.
 *
 * A message rendered outside the agent stream carries its answer in
 * `message.content` alone, so the first agent answer event has to inherit it
 * or that text disappears from the recomposition. Every later event must not:
 * `message.content` is itself recomposed from the answer events, so seeding a
 * second event copies the earlier events' text into it, and the same words
 * then exist twice — once in the event that produced them and once inside the
 * new one, where no later supersede can take them back (#3383).
 *
 * The presence of any other answer event is what separates the two cases,
 * superseded ones included: a superseded event proves the message's text came
 * from the stream rather than from a non-agent render.
 */
export function canSeedAnswerEvent(
  stream: readonly ChatMessage[] | undefined,
  answerEvent: ChatMessage,
  messageContent: unknown,
): boolean {
  if (typeof messageContent !== 'string' || messageContent.trim() === '') return false
  const existing = answerEvent.content
  if (typeof existing === 'string' && existing !== '') return false
  if (stream === undefined) return true
  return !stream.some(event => event !== answerEvent && event.type === 'answer')
}
