/** Presentation only: keep reasoning and progress separate in the stored stream. */
export function foldProgressIntoThinking(events: any[]): any[] {
  const result: any[] = []
  for (const event of events) {
    const progress = event.type === 'progress' || (event.type === 'answer' && event.superseded)
    if (!progress || typeof event.content !== 'string' || !event.content.trim()) {
      result.push(event)
      continue
    }
    const previous = result[result.length - 1]
    // Only adjacent events belong together. Tools, approvals and user input
    // remain boundaries, even when a later presentation filter hides them.
    const sameRound = previous?.iteration == null || event.iteration == null
      || previous.iteration === event.iteration
    if (previous?.type === 'thinking' && !previous.title && sameRound) {
      result[result.length - 1] = { ...previous, title: event.content }
    } else {
      result.push(event.type === 'progress' ? event : { ...event, type: 'progress', done: true })
    }
  }
  return result
}

/** Compact adjacent visible updates without crossing tools, prompts or answers. */
export function groupProgressUpdates(events: any[]): any[] {
  const result: any[] = []
  for (const event of events) {
    const previous = result.at(-1)
    if (event.type === 'progress' && typeof event.content === 'string' && event.content.trim()
      && previous?.type === 'progress' && typeof previous.content === 'string' && previous.content.trim()) {
      result[result.length - 1] = {
        ...event,
        // Keep the first row's identity so expansion survives streamed updates.
        event_id: previous.event_id,
        timestamp: previous.timestamp,
        progress_history: [...(previous.progress_history || []), previous.content],
      }
    } else {
      result.push(event)
    }
  }
  return result
}
