import { inject, provide, ref, type InjectionKey, type Ref } from 'vue'
import type { KnowledgeReferenceLike, ReferenceHighlightTarget } from '@/utils/referenceSources'

export type ChatReferencesDrawerOpenOptions = {
  references: KnowledgeReferenceLike[]
  highlight?: ReferenceHighlightTarget | null
  messageId?: string
  sourceKey?: string
}

export type ChatReferencesDrawerContext = {
  visible: Ref<boolean>
  references: Ref<KnowledgeReferenceLike[]>
  highlight: Ref<ReferenceHighlightTarget | null>
  messageId: Ref<string>
  sourceKey: Ref<string>
  open: (options: ChatReferencesDrawerOpenOptions) => void
  toggle: (options: ChatReferencesDrawerOpenOptions) => boolean
  close: () => void
  setHighlight: (highlight: ReferenceHighlightTarget | null) => void
}

const CHAT_REFERENCES_DRAWER_KEY: InjectionKey<ChatReferencesDrawerContext> = Symbol(
  'chatReferencesDrawer',
)

export function provideChatReferencesDrawer(): ChatReferencesDrawerContext {
  const visible = ref(false)
  const references = ref<KnowledgeReferenceLike[]>([])
  const highlight = ref<ReferenceHighlightTarget | null>(null)
  const messageId = ref('')
  const sourceKey = ref('')

  const getFallbackSourceKey = (options: ChatReferencesDrawerOpenOptions) => {
    if (options.sourceKey) return options.sourceKey
    if (options.messageId) return `message:${options.messageId}`
    return options.references
      .map((item) => item.id || item.knowledge_id || item.knowledge_title || item.metadata?.url || '')
      .filter(Boolean)
      .join('|')
  }

  const open = (options: ChatReferencesDrawerOpenOptions) => {
    references.value = Array.isArray(options.references) ? options.references : []
    highlight.value = options.highlight ?? null
    messageId.value = options.messageId || ''
    sourceKey.value = getFallbackSourceKey(options)
    visible.value = true
  }

  const toggle = (options: ChatReferencesDrawerOpenOptions) => {
    const nextSourceKey = getFallbackSourceKey(options)
    if (visible.value && sourceKey.value && sourceKey.value === nextSourceKey) {
      close()
      return false
    }
    open(options)
    return true
  }

  const close = () => {
    visible.value = false
    highlight.value = null
    sourceKey.value = ''
  }

  const setHighlight = (next: ReferenceHighlightTarget | null) => {
    highlight.value = next
    if (next && references.value.length) {
      visible.value = true
    }
  }

  const ctx: ChatReferencesDrawerContext = {
    visible,
    references,
    highlight,
    messageId,
    sourceKey,
    open,
    toggle,
    close,
    setHighlight,
  }

  provide(CHAT_REFERENCES_DRAWER_KEY, ctx)
  return ctx
}

export function useChatReferencesDrawer(): ChatReferencesDrawerContext | null {
  return inject(CHAT_REFERENCES_DRAWER_KEY, null)
}
