// Completion is authoritative after sandbox names and version references have
// been reconciled. Update both plain content and the Agent timeline.
export function applyFinalArtifactContent(message: any, content: unknown): void {
  if (typeof content !== 'string' || content.trim() === '') return;
  message.content = content;
  if (!Array.isArray(message.agentEventStream)) return;
  const answers = message.agentEventStream.filter((e: any) => e.type === 'answer' && !e.superseded);
  const last = answers.pop();
  answers.forEach((e: any) => { e.superseded = true; });
  if (last) {
    last.content = content;
    last.done = true;
  } else if (content) {
    message.agentEventStream.push({ type: 'answer', event_id: 'completed-answer', content, done: true });
  }
}
