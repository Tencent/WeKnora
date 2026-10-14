import { computed, shallowRef, watch } from 'vue'
import type { MentionItem } from '../types/mention'

interface MentionFileDraftProps {
  readonly visible: boolean
  readonly selectedFileIds?: readonly string[]
  readonly fileSelectionScope?: string
}

/** Keep a file selection independent of the currently loaded search results. */
export function useMentionFileDraft(props: MentionFileDraftProps) {
  const pending = shallowRef(new Map<string, MentionItem>())
  const finished = shallowRef(!props.visible)
  const selectedIds = computed(() => new Set(props.selectedFileIds ?? []))
  const pendingCount = computed(() => pending.value.size)

  const clear = () => {
    pending.value = new Map()
  }

  watch(() => props.visible, (visible) => {
    clear()
    finished.value = !visible
  }, { flush: 'sync' })

  watch(() => props.fileSelectionScope, clear, { flush: 'sync' })

  watch(selectedIds, (ids) => {
    const remaining = new Map(pending.value)
    for (const id of ids) remaining.delete(id)
    if (remaining.size !== pending.value.size) pending.value = remaining
  }, { flush: 'sync' })

  const isAlreadySelected = (id: string) => selectedIds.value.has(id)
  const isChecked = (id: string) => isAlreadySelected(id) || pending.value.has(id)
  const isDisabled = (item: MentionItem) => (
    !props.visible || finished.value ||
    (item.type === 'file' ? isAlreadySelected(item.id) : pendingCount.value > 0)
  )

  const toggleFile = (item: MentionItem) => {
    if (item.type !== 'file' || isDisabled(item)) return
    const next = new Map(pending.value)
    if (next.has(item.id)) next.delete(item.id)
    else next.set(item.id, { ...item })
    pending.value = next
  }

  const confirm = (): MentionItem[] => {
    if (!props.visible || finished.value || !pendingCount.value) return []
    const files = [...pending.value.values()]
    // Finish before emitting so a second click cannot commit the same draft.
    finished.value = true
    clear()
    return files
  }

  const cancel = () => {
    finished.value = true
    clear()
  }

  return { pendingCount, isAlreadySelected, isChecked, isDisabled, toggleFile, confirm, cancel }
}
