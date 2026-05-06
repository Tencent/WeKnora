import { useRouter } from 'vue-router'
import { useMenuStore } from '@/stores/menu'
import { useSettingsStore } from '@/stores/settings'

/**
 * Shared "start a new chat with query + KB/file preselected" helper.
 *
 * Used from:
 *   - Global command palette (⌘K) when user presses ⌘↵ on a result
 *   - KB-scoped search bar on the KB detail page
 *   - Empty-state "Ask AI directly" button in the palette
 *
 * Mirrors the legacy behavior of KnowledgeSearch.vue#startChat.
 */
export function useStartChat() {
  const router = useRouter()
  const menuStore = useMenuStore()
  const settingsStore = useSettingsStore()

  /**
   * @param query The user's query; it becomes the pre-filled first message
   * @param kbIds Knowledge bases to scope the new chat to; empty means no constraint
   * @param fileIds Specific knowledge/file ids to attach as context
   */
  const startChat = (query: string, kbIds: string[] = [], fileIds: string[] = []) => {
    const q = (query || '').trim()
    if (!q) return

    if (kbIds.length > 0) {
      settingsStore.selectKnowledgeBases(kbIds)
    }
    for (const fid of fileIds) {
      settingsStore.addFile(fid)
    }

    menuStore.setPrefillQuery(q)
    router.push('/platform/creatChat')
  }

  return { startChat }
}
