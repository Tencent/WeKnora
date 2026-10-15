import { computed, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { get } from '@/utils/request'
import { emitAssistantDataChange } from '@/utils/assistantDataChange'
import { ASSISTANT_TASK_EVENT, tasksFromEvent, taskStatusPath, taskState, type AssistantTask } from '@/utils/assistantTasks'

/** Lives on the panel, so closing it or switching conversations doesn't stop polling. */
export function useAssistantTasks(owner: Ref<string>, sessionId: Ref<string>) {
  const tasks = ref<AssistantTask[]>([])
  let generation = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let active = false
  const controllers = new Set<AbortController>()
  const key = (t: AssistantTask) => `${t.sessionId}:${t.callId}:${t.kind}:${t.id}`
  function clear() {
    generation++
    clearTimeout(timer)
    controllers.forEach(c => c.abort()); controllers.clear()
    tasks.value = []
  }
  watch(owner, clear, {flush: 'sync'})
  const receive = (event: Event) => {
    for (const task of tasksFromEvent((event as CustomEvent).detail, owner.value)) {
      if (tasks.value.some(t => key(t) === key(task))) continue
      if (task.kind === 'parse') {
        // Parse status belongs to a document. A newer request supersedes the old run.
        for (const old of tasks.value.filter(t => t.kind === 'parse' && t.id === task.id && t.state === 'running')) {
          if (old.startedAt <= task.startedAt) old.state = 'unknown'
          else task.state = 'unknown'
        }
      }
      tasks.value.push(task)
    }
    // Bound retained history; do not evict live tasks to make room for old ones.
    const running = tasks.value.filter(t => t.state === 'running')
    tasks.value = [...running.slice(-100), ...tasks.value.filter(t => t.state !== 'running').slice(-50)]
    if (!active) void poll()
  }
  async function poll() {
    clearTimeout(timer)
    active = true
    const epoch = generation
    const pending = tasks.value.filter(t => t.state === 'running')
    // Sequential reads avoid a batch of parse tasks flooding the API.
    for (const task of pending) {
      if (epoch !== generation) break
      const path = taskStatusPath(task)
      if (!path || Date.now() - task.startedAt > 30 * 60_000) { task.state = 'unknown'; continue }
      const controller = new AbortController(); controllers.add(controller)
      try {
        const result = await get(path, {signal: controller.signal, timeout: 10_000})
        if (epoch !== generation) break
        if (task.state !== 'running') continue
        task.state = taskState(task.kind, result)
      } catch {
        if (epoch !== generation) break
        task.state = 'unknown'
      } finally { controllers.delete(controller) }
      if (task.state !== 'running' && task.state !== 'unknown') emitAssistantDataChange(task.change)
    }
    active = false
    if (tasks.value.some(t => t.state === 'running')) timer = setTimeout(() => void poll(), 3000)
  }
  function retry(task: AssistantTask) {
    if (task.kind === 'unknown') return
    task.state = 'running'; task.startedAt = Date.now()
    if (!active) void poll()
  }
  onMounted(() => window.addEventListener(ASSISTANT_TASK_EVENT, receive))
  onUnmounted(() => { window.removeEventListener(ASSISTANT_TASK_EVENT, receive); clear() })
  return {tasks: computed(() => tasks.value.filter(t => t.sessionId === sessionId.value)), retry}
}
