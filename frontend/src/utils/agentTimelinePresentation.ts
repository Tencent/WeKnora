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

/**
 * Move finished, untitled reasoning onto the tool call it led to, so the
 * timeline shows one row per action instead of alternating thought/tool rows.
 * Titled reasoning (a relocated preamble) is user-facing narration and stays.
 */
export function attachReasoningToTools(events: any[], canAttach: (event: any) => boolean): any[] {
  const result: any[] = []
  for (let i = 0; i < events.length; i++) {
    const event = events[i]
    const next = events[i + 1]
    const content = typeof event?.content === 'string' ? event.content.trim() : ''
    if (event?.type === 'thinking' && !event.title && content && event.event_id
      && next?.type === 'tool_call' && next.tool_name !== 'thinking' && !next.reasoning
      && canAttach(event)) {
      result.push({ ...next, reasoning: { id: `reasoning-${event.event_id}`, content: event.content } })
      i++
      continue
    }
    result.push(event)
  }
  return result
}

/**
 * Keys of early timeline rows to fold while a run streams. The most recent
 * rows and anything still demanding attention stay visible; folding fewer
 * than `minFolded` rows would cost a row without saving one.
 */
export function earlierStepKeys(
  rows: { key: string, step: boolean, sticky: boolean }[],
  keepRecent = 3,
  minFolded = 2,
): string[] {
  const steps = rows.filter(row => row.step)
  const keys = steps.slice(0, Math.max(0, steps.length - keepRecent))
    .filter(row => !row.sticky)
    .map(row => row.key)
  return keys.length >= minFolded ? keys : []
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
