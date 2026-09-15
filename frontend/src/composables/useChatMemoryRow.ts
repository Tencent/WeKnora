import { computed, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { deleteMemoryEpisode, deleteMemoryNote } from '@/api/memory'
import { useUIStore } from '@/stores/ui'

/** The three stores recall can draw from, as streamed on `used_memories`. */
export type UsedMemoryKind = 'digest' | 'note' | 'episode'

/**
 * `content` differs per kind: a digest row carries a short excerpt of the
 * profile, a note carries the sentence the user asked to keep, and an episode
 * carries the title of a conversation account, not its summary.
 */
export type UsedMemory = { id: string; kind: string; content: string }

/** A digest row stands for the whole profile and has no row of its own to drop. */
export const isForgettableMemoryKind = (kind: string): kind is 'note' | 'episode' =>
  kind === 'note' || kind === 'episode'

/**
 * State for the "memories used by this turn" timeline row.
 *
 * Both timelines (the quick-answer pipeline and the agent stream) show the same
 * row, so the recall list, the expand state and the forget call live here rather
 * than being written twice and drifting apart.
 */
export function useChatMemoryRow(getUsedMemories: () => UsedMemory[] | undefined) {
  const { t } = useI18n()
  const uiStore = useUIStore()

  const expanded = ref(false)
  const forgettingId = ref('')
  const forgottenIds = ref<string[]>([])

  const recalledMemories = computed(() => {
    const used = getUsedMemories()
    if (!Array.isArray(used)) return []
    return used.filter((memory) => memory?.id && !forgottenIds.value.includes(memory.id))
  })

  const hasMemory = computed(() => recalledMemories.value.length > 0)

  const toggle = () => {
    expanded.value = !expanded.value
  }

  // Forgetting from the answer is the shortest path from noticing a wrong memory
  // to it being gone, which is where users actually notice one.
  const forget = async (memory: UsedMemory) => {
    if (!isForgettableMemoryKind(memory.kind)) return
    forgettingId.value = memory.id
    try {
      if (memory.kind === 'note') await deleteMemoryNote(memory.id)
      else await deleteMemoryEpisode(memory.id)
      forgottenIds.value = [...forgottenIds.value, memory.id]
      MessagePlugin.success(t('chat.memoryForgotten'))
    } catch (error: any) {
      MessagePlugin.error(error?.message || t('chat.memoryForgetFailed'))
    } finally {
      forgettingId.value = ''
    }
  }

  /** The profile is edited as a whole document, so a digest row points there. */
  const openProfile = () => {
    uiStore.openSettings('mymemory')
  }

  return { recalledMemories, hasMemory, expanded, forgettingId, toggle, forget, openProfile }
}
