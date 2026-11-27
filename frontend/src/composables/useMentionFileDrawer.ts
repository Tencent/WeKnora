import { inject, provide, ref, type InjectionKey, type Ref } from 'vue'

/**
 * Target describing a file mentioned via @-mention whose details should be
 * shown in the side drawer. Both the composer chips (Input-field.vue) and the
 * message bubbles (usermsg.vue) build this from their local mention item.
 */
export type MentionFileDrawerTarget = {
  /** Knowledge document (file) id. */
  fileId: string
  /** Parent knowledge base id, when known (file mentions carry kb_id). */
  kbId?: string
  /** Display name of the file, used as the drawer title fallback. */
  fileName?: string
}

export type MentionFileDrawerContext = {
  visible: Ref<boolean>
  target: Ref<MentionFileDrawerTarget | null>
  open: (target: MentionFileDrawerTarget) => void
  close: () => void
}

const MENTION_FILE_DRAWER_KEY: InjectionKey<MentionFileDrawerContext> = Symbol(
  'mentionFileDrawer',
)

/**
 * Global drawer state shared between the chat page (provider), the composer
 * chips, the message bubbles and the drawer component itself. Mirrors the
 * pattern of useChatReferencesDrawer.
 */
export function provideMentionFileDrawer(): MentionFileDrawerContext {
  const visible = ref(false)
  const target = ref<MentionFileDrawerTarget | null>(null)

  const open = (next: MentionFileDrawerTarget) => {
    if (!next?.fileId) return
    target.value = next
    visible.value = true
  }

  const close = () => {
    visible.value = false
    target.value = null
  }

  const ctx: MentionFileDrawerContext = {
    visible,
    target,
    open,
    close,
  }

  provide(MENTION_FILE_DRAWER_KEY, ctx)
  return ctx
}

export function useMentionFileDrawer(): MentionFileDrawerContext | null {
  return inject(MENTION_FILE_DRAWER_KEY, null)
}