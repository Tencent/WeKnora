/** Presentation projection; never expose runtime IDs or raw tool payloads. */
export interface CollaborationTaskView {
  id: string
  name: string
  sessionId: string
  runId: string
  displayName: string
  status: string
  progress?: { phase?: string; tool_name?: string; iteration?: number }
  latestMessage: string
}
const statuses = new Set(['queued', 'running', 'waiting', 'succeeded', 'failed', 'interrupted', 'cancelled', 'cancelling', 'needs_attention'])
export function collaborationTasks(snapshot: unknown): CollaborationTaskView[] {
  const tasks = (snapshot as any)?.tasks
  if (!Array.isArray(tasks)) return []
  return tasks.filter((task: any) => task && typeof task === 'object').map((task: any, index: number) => {
    const latest = task.latest_message
    const message = typeof latest === 'string' ? latest : latest?.content ?? latest?.message
    return {
      id: String(task.id || task.member_id || task.name || task.task_name || index),
      name: String(task.name || task.task_name || ''),
      sessionId: String(task.session_id || ''),
      runId: String(task.run_id || ''),
      displayName: String(task.display_name || task.name || task.task_name || ''),
      status: task.cancel_requested && ['queued', 'running', 'waiting'].includes(task.status)
        ? 'cancelling' : statuses.has(task.status) ? task.status : 'unknown',
      progress: task.progress && typeof task.progress === 'object' ? task.progress : undefined,
      latestMessage: typeof message === 'string' ? message : '',
    }
  })
}
export function collaborationWaitState(events: any[] = [], settled = false): 'waiting' | 'finished' | 'failed' | undefined {
  const waits = events.filter(event => event?.type === 'tool_call' && event.tool_name === 'wait_agent')
  if (!waits.length) return undefined
  if (!settled && waits.some(event => event.pending && !event.canceled)) return 'waiting'
  return waits[waits.length - 1].success === false ? 'failed' : 'finished'
}
/** Current activity belongs at the live response tail, not the first wait call. */
export function collaborationActivity(events: any[], snapshot: unknown, settled = false): 'waiting' | 'approval' | undefined {
  if (settled || events.some(event => event?.type === 'stop' || event?.type === 'agent_complete')) return undefined
  if (collaborationWaitState(events) !== 'waiting') return undefined
  return events.some(isUnresolvedCollaborationPrompt) || collaborationTasks(snapshot).some(task => task.status === 'waiting')
    ? 'approval' : 'waiting'
}
/** All wait polls share the task panel; they must not multiply timeline rows. */
export function withoutCollaborationWaits(events: any[]): any[] {
  return events.filter(event => !(event?.type === 'tool_call' && event.tool_name === 'wait_agent'))
}

export function isCollaborationTaskActive(status: string): boolean {
  return ['queued', 'running', 'waiting', 'cancelling'].includes(status)
}
export function isUnresolvedCollaborationPrompt(event: any): boolean {
  return ['tool_approval_required', 'agent_prompt_required', 'mcp_oauth_required'].includes(event?.type)
    && !event.resolved && !event.canceled && !event.abandoned
}
/** Count the actual calls, including calls represented by the task panel. */
export function countToolCalls(events: any[]): number {
  const calls = events.filter(event => event?.type === 'tool_call')
  const ids = new Set(calls.map(event => event.tool_call_id).filter(Boolean))
  return ids.size + calls.filter(event => !event.tool_call_id).length
}

/** Keep child rows at their spawn/continuation location, including restored snapshots. */
export function collaborationTimeline(events: any[], snapshot: unknown, settled = false): any[] {
  const tasks = collaborationTasks(snapshot)
  const seen = new Set<string>()
  const result: any[] = []
  const waitState = collaborationWaitState(events, settled)
  const lastWait = events.filter(event => event?.type === 'tool_call' && event.tool_name === 'wait_agent').at(-1)
  for (const event of events) {
    if (event.type === 'tool_call' && (event.tool_name === 'spawn_agent'
      || (event.tool_name === 'followup_task' && event.success !== false && !event.canceled))) {
      const target = event.arguments?.target
      const task = tasks.find(t => event.tool_name === 'followup_task'
        ? t.id === target || t.name === target || `/root/${t.name}` === target
        : t.id === event.tool_data?.member_id
          || t.name === event.arguments?.task_name || t.name === event.arguments?.name)
      if (task) {
        if (!seen.has(task.id)) result.push({ type: 'child_task', event_id: `child-${task.id}`, task, timestamp: event.timestamp })
        seen.add(task.id)
        continue
      }
    }
    if (tasks.length && event.type === 'tool_call' && event.tool_name === 'list_agents') continue
    if (event.type === 'tool_call' && event.tool_name === 'wait_agent') {
      // Successful polls (including timeouts) are plumbing, not milestones.
      // Preserve an actual failure in the normal tool row for inspection.
      if (event === lastWait && waitState === 'failed') result.push(event)
      continue
    }
    result.push(event)
  }
  // A restored segment may lack the original spawn calls. Keep orphan rows in
  // the transcript before its answer, never in a separate top status panel.
  const missing = tasks.filter(t => !seen.has(t.id)).map(task => ({ type: 'child_task', event_id: `child-${task.id}`, task }))
  const answer = result.findIndex(e => e.type === 'answer' && !e.superseded)
  result.splice(answer < 0 ? result.length : answer, 0, ...missing)
  return result
}
