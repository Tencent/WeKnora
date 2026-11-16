import { computed, shallowRef, type InjectionKey } from 'vue'
import { collaborationTasks, type CollaborationTaskView } from '../utils/collaborationDisplay'

export function createChildAgentTabs() {
  const entries = shallowRef<Array<{ id: string; initial: CollaborationTaskView; latest: () => CollaborationTaskView | undefined }>>([])
  const activeId = shallowRef('')
  const attention = shallowRef(false)
  const tabs = computed(() => entries.value.map(entry => ({ id: entry.id, task: entry.latest() || entry.initial })))
  function open(task: CollaborationTaskView, latest: () => CollaborationTaskView | undefined) {
    const id = `${task.sessionId}:${task.id}`
    const entry = { id, initial: task, latest }
    const index = entries.value.findIndex(item => item.id === id)
    if (index < 0) entries.value = [...entries.value, entry]
    else entries.value = entries.value.map(item => item.id === id ? entry : item)
    activeId.value = id
  }
  function select(id: string) {
    if (!id || entries.value.some(entry => entry.id === id)) activeId.value = id
  }
  function close(id: string) {
    entries.value = entries.value.filter(entry => entry.id !== id)
    if (activeId.value === id) activeId.value = ''
  }
  function reset() { entries.value = []; activeId.value = ''; attention.value = false }
  return { tabs, activeId, attention, open, select, close, reset }
}
export const childAgentTabsKey: InjectionKey<ReturnType<typeof createChildAgentTabs>> = Symbol('child-agent-tabs')

// Steering moves the current snapshot between presentation segments. Resolve
// by durable child identity across the conversation, never by the opened row.
export function findChildAgentTask(messages: readonly { collaborationStatus?: unknown }[], task: CollaborationTaskView) {
  for (let i = messages.length - 1; i >= 0; i--) {
    const current = collaborationTasks(messages[i].collaborationStatus)
      .find(item => item.id === task.id && item.sessionId === task.sessionId)
    if (current) return current
  }
}
export const childAgentTaskLookupKey: InjectionKey<(task: CollaborationTaskView) => CollaborationTaskView | undefined> = Symbol('child-agent-task-lookup')
