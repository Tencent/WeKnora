import { inject, provide, ref, type InjectionKey, type Ref } from 'vue'
import {
  resolveReferenceSource,
  type KnowledgeReferenceLike,
  type ReferenceHighlightTarget,
  type ReferenceSourceTarget,
} from '@/utils/referenceSources'

export type ChatReferencesDrawerOpenOptions = {
  references: KnowledgeReferenceLike[]
  highlight?: ReferenceHighlightTarget | null
  messageId?: string
  sourceKey?: string
}

/** Default width of the references panel, which the chat column makes room for. */
export const REFERENCES_PANEL_WIDTH = 420

export type ChatReferencesDrawerContext = {
  visible: Ref<boolean>
  references: Ref<KnowledgeReferenceLike[]>
  highlight: Ref<ReferenceHighlightTarget | null>
  messageId: Ref<string>
  sourceKey: Ref<string>
  /** The cited chunk shown inside its original document, or null for the list. */
  source: Ref<ReferenceSourceTarget | null>
  /** Current panel width in px; the source view is wider than the list. */
  panelWidth: Ref<number>
  open: (options: ChatReferencesDrawerOpenOptions) => void
  toggle: (options: ChatReferencesDrawerOpenOptions) => boolean
  close: () => void
  setHighlight: (highlight: ReferenceHighlightTarget | null) => void
  openSource: (target: ReferenceSourceTarget) => void
  closeSource: () => void
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
  const source = ref<ReferenceSourceTarget | null>(null)
  const panelWidth = ref(REFERENCES_PANEL_WIDTH)

  const getFallbackSourceKey = (options: ChatReferencesDrawerOpenOptions) => {
    if (options.sourceKey) return options.sourceKey
    if (options.messageId) return `message:${options.messageId}`
    return options.references
      .map((item) => item.id || item.knowledge_id || item.knowledge_title || item.metadata?.url || '')
      .filter(Boolean)
      .join('|')
  }

  // Citation clicks ask for the original document; other openings show the list.
  const sourceFor = (next: ReferenceHighlightTarget | null | undefined) =>
    next?.openSource ? resolveReferenceSource(references.value, next) : null

  const open = (options: ChatReferencesDrawerOpenOptions) => {
    references.value = Array.isArray(options.references) ? options.references : []
    highlight.value = options.highlight ?? null
    messageId.value = options.messageId || ''
    sourceKey.value = getFallbackSourceKey(options)
    source.value = sourceFor(options.highlight)
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
    source.value = null
  }

  const setHighlight = (next: ReferenceHighlightTarget | null) => {
    highlight.value = next
    if (next?.openSource) source.value = sourceFor(next)
    if (next && references.value.length) {
      visible.value = true
    }
  }

  const openSource = (target: ReferenceSourceTarget) => {
    source.value = target
    visible.value = true
  }

  const closeSource = () => {
    source.value = null
  }

  const ctx: ChatReferencesDrawerContext = {
    visible,
    references,
    highlight,
    messageId,
    sourceKey,
    source,
    panelWidth,
    open,
    toggle,
    close,
    setHighlight,
    openSource,
    closeSource,
  }

  provide(CHAT_REFERENCES_DRAWER_KEY, ctx)
  return ctx
}

export function useChatReferencesDrawer(): ChatReferencesDrawerContext | null {
  return inject(CHAT_REFERENCES_DRAWER_KEY, null)
}
