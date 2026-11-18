import { onBeforeUnmount, watch } from 'vue'
import { useAssistantStore, type AssistantPageContext } from '@/stores/assistant'

/**
 * Publishes what this page has open to the WeKnora assistant for as long as
 * the component is mounted. Return null while nothing is open. A component
 * mounted inside another publisher refines it: fields it sets win.
 */
export function useAssistantPageContext(source: () => Partial<AssistantPageContext> | null) {
  const assistant = useAssistantStore()
  const owner = Symbol('assistant-page-context')
  watch(source, (context) => {
    if (context) assistant.publishPageContext(owner, context)
    else assistant.retractPageContext(owner)
  }, { immediate: true, deep: true })
  onBeforeUnmount(() => assistant.retractPageContext(owner))
}
