import { computed, onScopeDispose, shallowRef } from 'vue'

// Multiple assistant messages can contain child entry points, but the page has
// one detail viewer. Opening another entry releases the previous subscription.
const active = shallowRef<{ owner: symbol; taskId: string } | null>(null)
export function useChildAgentViewer() {
  const owner = Symbol('child-viewer')
  const selectedTaskId = computed({
    get: () => active.value?.owner === owner ? active.value.taskId : '',
    set: (taskId: string) => {
      if (taskId) active.value = { owner, taskId }
      else if (active.value?.owner === owner) active.value = null
    },
  })
  onScopeDispose(() => { selectedTaskId.value = '' })
  return selectedTaskId
}
