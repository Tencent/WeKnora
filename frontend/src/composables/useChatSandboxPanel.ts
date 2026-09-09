import { inject, provide, ref, type InjectionKey, type Ref } from 'vue'

export type SandboxPanelTab = 'artifacts' | 'terminal' | 'desktop'

export type ArtifactPanelFocus = {
  messageId: string
  previewIndex?: number | null
}

export type ArtifactPanelFocusState = ArtifactPanelFocus & {
  nonce: number
}

// 面板宽度可拖拽调整，持久化到 localStorage。
export const SANDBOX_PANEL_MIN_WIDTH = 320
export const SANDBOX_PANEL_MAX_WIDTH = 1200
export const SANDBOX_PANEL_DEFAULT_WIDTH = 420

function clampPanelWidth(width: number): number {
  if (typeof window === 'undefined') return SANDBOX_PANEL_DEFAULT_WIDTH
  const viewportCap = Math.max(SANDBOX_PANEL_MIN_WIDTH, window.innerWidth - 480)
  return Math.min(
    SANDBOX_PANEL_MAX_WIDTH,
    viewportCap,
    Math.max(SANDBOX_PANEL_MIN_WIDTH, Math.round(width)),
  )
}

function initialPanelWidth(): number {
  const raw = Number(localStorage.getItem('sandbox_panel_width'))
  return Number.isFinite(raw) && raw > 0 ? clampPanelWidth(raw) : SANDBOX_PANEL_DEFAULT_WIDTH
}

export type ChatSandboxPanelContext = {
  visible: Ref<boolean>
  activeTab: Ref<SandboxPanelTab>
  width: Ref<number>
  setWidth: (width: number) => void
  artifactFocus: Ref<ArtifactPanelFocusState | null>
  open: (tab?: SandboxPanelTab, focus?: ArtifactPanelFocus) => void
  close: () => void
  clearArtifactFocus: () => void
}

const CHAT_SANDBOX_PANEL_KEY: InjectionKey<ChatSandboxPanelContext> = Symbol(
  'chatSandboxPanel',
)

export function provideChatSandboxPanel(): ChatSandboxPanelContext {
  const visible = ref(false)
  const activeTab = ref<SandboxPanelTab>('artifacts')
  const width = ref(initialPanelWidth())
  const artifactFocus = ref<ArtifactPanelFocusState | null>(null)
  let focusNonce = 0

  const setWidth = (next: number) => {
    width.value = clampPanelWidth(next)
    localStorage.setItem('sandbox_panel_width', String(width.value))
  }

  const open = (tab?: SandboxPanelTab, focus?: ArtifactPanelFocus) => {
    if (tab) activeTab.value = tab
    if (focus) {
      artifactFocus.value = { ...focus, nonce: ++focusNonce }
    }
    visible.value = true
  }

  const close = () => {
    visible.value = false
    artifactFocus.value = null
  }

  const clearArtifactFocus = () => {
    artifactFocus.value = null
  }

  const ctx: ChatSandboxPanelContext = {
    visible,
    activeTab,
    width,
    setWidth,
    artifactFocus,
    open,
    close,
    clearArtifactFocus,
  }

  provide(CHAT_SANDBOX_PANEL_KEY, ctx)
  return ctx
}

export function useChatSandboxPanel(): ChatSandboxPanelContext | null {
  return inject(CHAT_SANDBOX_PANEL_KEY, null)
}
