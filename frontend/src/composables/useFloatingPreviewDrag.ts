import { computed, ref, watch, type Ref } from 'vue'

/** Move an absolutely positioned preview within its containing panel. */
export function useFloatingPreviewDrag(element: Ref<HTMLElement | null>) {
  const position = ref<{ x: number; y: number } | null>(null)
  const dragging = ref(false)
  let drag: { pointerId: number; handle: HTMLElement; x: number; y: number; left: number; top: number } | null = null

  const positionStyle = computed(() => position.value
    ? { left: `${position.value.x}px`, top: `${position.value.y}px`, right: 'auto', bottom: 'auto' }
    : {})

  function constrain(x: number, y: number) {
    const preview = element.value
    const container = preview?.offsetParent as HTMLElement | null
    if (!preview || !container) return null
    const maxX = Math.max(0, container.clientWidth - preview.offsetWidth)
    const maxY = Math.max(0, container.clientHeight - preview.offsetHeight)
    const marginX = Math.min(12, maxX / 2)
    const marginY = Math.min(12, maxY / 2)
    return {
      x: Math.min(Math.max(x, marginX), maxX - marginX),
      y: Math.min(Math.max(y, marginY), maxY - marginY),
    }
  }

  function stopDrag(event?: PointerEvent) {
    if (!drag || (event && event.pointerId !== drag.pointerId)) return
    const { handle, pointerId } = drag
    drag = null
    dragging.value = false
    if (handle.hasPointerCapture(pointerId)) handle.releasePointerCapture(pointerId)
  }

  function startDrag(event: PointerEvent) {
    const preview = element.value
    if (!preview || !event.isPrimary || event.button !== 0 || drag) return
    const handle = event.currentTarget as HTMLElement
    handle.setPointerCapture(event.pointerId)
    drag = {
      pointerId: event.pointerId, handle,
      x: event.clientX, y: event.clientY,
      left: preview.offsetLeft, top: preview.offsetTop,
    }
    dragging.value = true
    event.preventDefault()
  }

  function moveDrag(event: PointerEvent) {
    if (!drag || event.pointerId !== drag.pointerId) return
    position.value = constrain(drag.left + event.clientX - drag.x, drag.top + event.clientY - drag.y)
  }

  watch(element, (preview, _, onCleanup) => {
    if (!preview) return
    const keepInBounds = () => {
      const x = position.value?.x ?? preview.offsetLeft
      const y = position.value?.y ?? preview.offsetTop
      const bounded = constrain(x, y)
      if (bounded && (position.value || bounded.x !== x || bounded.y !== y)) position.value = bounded
    }
    const observer = new ResizeObserver(keepInBounds)
    observer.observe(preview)
    if (preview.offsetParent) observer.observe(preview.offsetParent)
    keepInBounds()
    onCleanup(() => { observer.disconnect(); stopDrag() })
  }, { flush: 'post' })

  return { positionStyle, dragging, startDrag, moveDrag, stopDrag }
}
