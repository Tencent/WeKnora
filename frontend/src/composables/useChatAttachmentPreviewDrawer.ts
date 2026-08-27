import { inject, provide, ref, type InjectionKey, type Ref } from 'vue'

type ChatPreviewFile = {
  fileName: string
  fileType: string
}

export type ChatAttachmentPreviewTarget = ChatPreviewFile & (
  | { knowledgeId: string; sessionId?: never; attachmentId?: never }
  | { knowledgeId?: never; sessionId: string; attachmentId: string }
)

export type ChatAttachmentPreviewDrawerContext = {
  visible: Ref<boolean>
  target: Ref<ChatAttachmentPreviewTarget | null>
  open: (target: ChatAttachmentPreviewTarget) => void
  close: () => void
}

const CHAT_ATTACHMENT_PREVIEW_DRAWER_KEY: InjectionKey<ChatAttachmentPreviewDrawerContext> = Symbol(
  'chatAttachmentPreviewDrawer',
)

export function provideChatAttachmentPreviewDrawer(): ChatAttachmentPreviewDrawerContext {
  const visible = ref(false)
  const target = ref<ChatAttachmentPreviewTarget | null>(null)

  const open = (next: ChatAttachmentPreviewTarget) => {
    target.value = next
    visible.value = true
  }

  const close = () => {
    visible.value = false
    target.value = null
  }

  const ctx: ChatAttachmentPreviewDrawerContext = {
    visible,
    target,
    open,
    close,
  }

  provide(CHAT_ATTACHMENT_PREVIEW_DRAWER_KEY, ctx)
  return ctx
}

export function useChatAttachmentPreviewDrawer(): ChatAttachmentPreviewDrawerContext | null {
  return inject(CHAT_ATTACHMENT_PREVIEW_DRAWER_KEY, null)
}
