import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

export const KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH = 224
export const KNOWLEDGE_FOLDER_SIDEBAR_MIN_WIDTH = 224
export const KNOWLEDGE_FOLDER_SIDEBAR_MAX_WIDTH = 600
export const KNOWLEDGE_FOLDER_SIDEBAR_BREAKPOINT = 980
export const KNOWLEDGE_FOLDER_SIDEBAR_STORAGE_KEY = 'WeKnora_knowledge_folder_sidebar_width'

export function getKnowledgeFolderSidebarMaxWidth(viewportWidth: number): number {
  const safeViewportWidth = Number.isFinite(viewportWidth) ? Math.max(0, viewportWidth) : 0
  return Math.max(
    KNOWLEDGE_FOLDER_SIDEBAR_MIN_WIDTH,
    Math.min(KNOWLEDGE_FOLDER_SIDEBAR_MAX_WIDTH, Math.floor(safeViewportWidth * 0.5)),
  )
}

export function clampKnowledgeFolderSidebarWidth(width: number, viewportWidth: number): number {
  const normalized = Number.isFinite(width) ? width : KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH
  return Math.round(Math.min(
    getKnowledgeFolderSidebarMaxWidth(viewportWidth),
    Math.max(KNOWLEDGE_FOLDER_SIDEBAR_MIN_WIDTH, normalized),
  ))
}

export function parseKnowledgeFolderSidebarWidth(value: string | null, viewportWidth: number): number {
  if (value === null || value.trim() === '') return KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) return KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH
  if (parsed < KNOWLEDGE_FOLDER_SIDEBAR_MIN_WIDTH || parsed > KNOWLEDGE_FOLDER_SIDEBAR_MAX_WIDTH) {
    return KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH
  }
  return clampKnowledgeFolderSidebarWidth(parsed, viewportWidth)
}

type Listener = (event: any) => void

export interface ResizeEventTarget {
  addEventListener(type: string, listener: Listener, options?: any): void
  removeEventListener(type: string, listener: Listener, options?: any): void
}

export interface SidebarWidthStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

interface SidebarResizeControllerOptions {
  target: ResizeEventTarget
  storage?: SidebarWidthStorage
  getViewportWidth: () => number
  onWidthChange: (width: number) => void
  onDesktopChange?: (desktop: boolean) => void
  onDraggingChange?: (dragging: boolean) => void
  setTextSelectionDisabled?: (disabled: boolean) => void
}

export function createKnowledgeFolderSidebarResizeController(options: SidebarResizeControllerOptions) {
  let width = parseKnowledgeFolderSidebarWidth(
    safeStorageRead(options.storage),
    options.getViewportWidth(),
  )
  let desktop = options.getViewportWidth() > KNOWLEDGE_FOLDER_SIDEBAR_BREAKPOINT
  let dragging = false
  let startX = 0
  let startWidth = width

  const publish = () => {
    options.onWidthChange(width)
    options.onDesktopChange?.(desktop)
  }

  const persist = () => {
    try {
      options.storage?.setItem(KNOWLEDGE_FOLDER_SIDEBAR_STORAGE_KEY, String(width))
    } catch {
      // Storage may be disabled by browser policy; resizing must still work.
    }
  }

  const stop = () => {
    if (!dragging) return
    dragging = false
    options.target.removeEventListener('mousemove', onMove)
    options.target.removeEventListener('mouseup', stop)
    options.setTextSelectionDisabled?.(false)
    options.onDraggingChange?.(false)
    persist()
  }

  const onMove = (event: { clientX?: number }) => {
    if (!dragging || typeof event.clientX !== 'number') return
    width = clampKnowledgeFolderSidebarWidth(
      startWidth + event.clientX - startX,
      options.getViewportWidth(),
    )
    options.onWidthChange(width)
  }

  const start = (event: {
    clientX: number
    button?: number
    preventDefault?: () => void
    stopPropagation?: () => void
  }) => {
    if (!desktop || (typeof event.button === 'number' && event.button !== 0)) return false
    event.preventDefault?.()
    event.stopPropagation?.()
    startX = event.clientX
    startWidth = width
    dragging = true
    options.setTextSelectionDisabled?.(true)
    options.onDraggingChange?.(true)
    options.target.addEventListener('mousemove', onMove)
    options.target.addEventListener('mouseup', stop)
    return true
  }

  const adjust = (delta: number) => {
    if (!desktop || !Number.isFinite(delta)) return
    width = clampKnowledgeFolderSidebarWidth(width + delta, options.getViewportWidth())
    options.onWidthChange(width)
    persist()
  }

  const reset = () => {
    width = clampKnowledgeFolderSidebarWidth(
      KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH,
      options.getViewportWidth(),
    )
    options.onWidthChange(width)
    persist()
  }

  const syncViewport = () => {
    desktop = options.getViewportWidth() > KNOWLEDGE_FOLDER_SIDEBAR_BREAKPOINT
    if (!desktop) stop()
    width = clampKnowledgeFolderSidebarWidth(width, options.getViewportWidth())
    publish()
  }

  const mount = () => {
    publish()
    options.target.addEventListener('resize', syncViewport)
  }

  const destroy = () => {
    stop()
    options.target.removeEventListener('resize', syncViewport)
    options.target.removeEventListener('mousemove', onMove)
    options.target.removeEventListener('mouseup', stop)
    options.setTextSelectionDisabled?.(false)
  }

  return {
    mount,
    destroy,
    start,
    stop,
    adjust,
    reset,
    syncViewport,
    getWidth: () => width,
    isDesktop: () => desktop,
    isDragging: () => dragging,
  }
}

function safeStorageRead(storage?: SidebarWidthStorage): string | null {
  try {
    return storage?.getItem(KNOWLEDGE_FOLDER_SIDEBAR_STORAGE_KEY) ?? null
  } catch {
    return null
  }
}

export function useResizableKnowledgeFolderSidebar() {
  const width = ref(KNOWLEDGE_FOLDER_SIDEBAR_DEFAULT_WIDTH)
  const desktop = ref(false)
  const dragging = ref(false)
  let previousUserSelect = ''
  let controller: ReturnType<typeof createKnowledgeFolderSidebarResizeController> | null = null

  onMounted(() => {
    controller = createKnowledgeFolderSidebarResizeController({
      target: window,
      storage: window.localStorage,
      getViewportWidth: () => window.innerWidth,
      onWidthChange: value => { width.value = value },
      onDesktopChange: value => { desktop.value = value },
      onDraggingChange: value => { dragging.value = value },
      setTextSelectionDisabled: disabled => {
        if (disabled) {
          previousUserSelect = document.body.style.userSelect
          document.body.style.userSelect = 'none'
        } else {
          document.body.style.userSelect = previousUserSelect
        }
      },
    })
    controller.mount()
  })

  onBeforeUnmount(() => {
    controller?.destroy()
    controller = null
  })

  const maxWidth = computed(() => {
    if (typeof window === 'undefined') return KNOWLEDGE_FOLDER_SIDEBAR_MAX_WIDTH
    return getKnowledgeFolderSidebarMaxWidth(window.innerWidth)
  })

  const sidebarStyle = computed(() => desktop.value
    ? { width: `${width.value}px`, minWidth: `${width.value}px` }
    : undefined)

  const startResize = (event: MouseEvent) => controller?.start(event)
  const resetWidth = () => controller?.reset()
  const handleResizeKeydown = (event: KeyboardEvent) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
    event.preventDefault()
    event.stopPropagation()
    const step = event.shiftKey ? 40 : 8
    controller?.adjust(event.key === 'ArrowLeft' ? -step : step)
  }

  return {
    width,
    maxWidth,
    desktop,
    dragging,
    sidebarStyle,
    startResize,
    resetWidth,
    handleResizeKeydown,
  }
}
