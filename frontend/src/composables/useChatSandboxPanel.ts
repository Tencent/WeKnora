import { inject, provide, ref, type InjectionKey, type Ref } from 'vue'

export type SandboxPanelTab = 'terminal' | 'desktop'

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
  open: (tab?: SandboxPanelTab) => void
  close: () => void
}

const CHAT_SANDBOX_PANEL_KEY: InjectionKey<ChatSandboxPanelContext> = Symbol(
  'chatSandboxPanel',
)

export function provideChatSandboxPanel(): ChatSandboxPanelContext {
  const visible = ref(false)
  const activeTab = ref<SandboxPanelTab>('terminal')
  const width = ref(initialPanelWidth())

  const setWidth = (next: number) => {
    width.value = clampPanelWidth(next)
    localStorage.setItem('sandbox_panel_width', String(width.value))
  }

  const open = (tab?: SandboxPanelTab) => {
    if (tab) activeTab.value = tab
    visible.value = true
  }

  const close = () => {
    visible.value = false
  }

  const ctx: ChatSandboxPanelContext = {
    visible,
    activeTab,
    width,
    setWidth,
    open,
    close,
  }

  provide(CHAT_SANDBOX_PANEL_KEY, ctx)
  return ctx
}

export function useChatSandboxPanel(): ChatSandboxPanelContext | null {
  return inject(CHAT_SANDBOX_PANEL_KEY, null)
}
