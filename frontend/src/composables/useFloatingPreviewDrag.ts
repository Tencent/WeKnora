import { computed, ref, watch, type Ref } from 'vue'

/** Move a preview within its parent, or a right-anchored floating window within the viewport. */
export function useFloatingPreviewDrag(element: Ref<HTMLElement | null>, boundary: 'parent' | 'viewport' = 'parent') {
  const position = ref<{ x: number; y: number } | null>(null)
  const dragging = ref(false)
  let drag: { pointerId: number; handle: HTMLElement; x: number; y: number; horizontal: number; top: number; scale: number } | null = null

  const positionStyle = computed(() => position.value
    ? {
      left: boundary === 'viewport' ? 'auto' : `${position.value.x}px`,
      right: boundary === 'viewport' ? `${position.value.x}px` : 'auto',
      top: `${position.value.y}px`, bottom: 'auto',
    }
    : {})

  function constrain(x: number, y: number) {
    const preview = element.value
    const container = preview?.offsetParent as HTMLElement | null
    if (!preview?.offsetWidth || !preview.offsetHeight || (boundary === 'parent' && !container)) return null
    const scale = preview.getBoundingClientRect().width / preview.offsetWidth || 1
    const maxX = Math.max(0, (boundary === 'viewport' ? window.innerWidth / scale : container!.clientWidth) - preview.offsetWidth)
    const maxY = Math.max(0, (boundary === 'viewport' ? window.innerHeight / scale : container!.clientHeight) - preview.offsetHeight)
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
    if (!preview?.offsetWidth || !event.isPrimary || event.button !== 0 || drag) return
    const handle = event.currentTarget as HTMLElement
    handle.setPointerCapture(event.pointerId)
    // Pointer coordinates include the app's font-size zoom; offsets use CSS pixels.
    const scale = preview.getBoundingClientRect().width / preview.offsetWidth || 1
    drag = {
      pointerId: event.pointerId, handle,
      x: event.clientX, y: event.clientY,
      horizontal: boundary === 'viewport' ? window.innerWidth / scale - preview.offsetLeft - preview.offsetWidth : preview.offsetLeft,
      top: preview.offsetTop, scale,
    }
    dragging.value = true
    event.preventDefault()
  }

  function moveDrag(event: PointerEvent) {
    if (!drag || event.pointerId !== drag.pointerId) return
    const direction = boundary === 'viewport' ? -1 : 1
    position.value = constrain(drag.horizontal + direction * (event.clientX - drag.x) / drag.scale,
      drag.top + (event.clientY - drag.y) / drag.scale)
  }

  watch(element, (preview, _, onCleanup) => {
    if (!preview) return
    const keepInBounds = () => {
      if (!preview.offsetWidth || !preview.offsetHeight) return
      const scale = preview.getBoundingClientRect().width / preview.offsetWidth || 1
      const x = position.value?.x ?? (boundary === 'viewport'
        ? window.innerWidth / scale - preview.offsetLeft - preview.offsetWidth : preview.offsetLeft)
      const y = position.value?.y ?? preview.offsetTop
      const bounded = constrain(x, y)
      if (bounded && (position.value || bounded.x !== x || bounded.y !== y)) position.value = bounded
    }
    const observer = new ResizeObserver(keepInBounds)
    observer.observe(preview)
    if (boundary === 'viewport') window.addEventListener('resize', keepInBounds)
    else if (preview.offsetParent) observer.observe(preview.offsetParent)
    keepInBounds()
    onCleanup(() => {
      observer.disconnect()
      if (boundary === 'viewport') window.removeEventListener('resize', keepInBounds)
      stopDrag()
    })
  }, { flush: 'post' })

  return { positionStyle, dragging, startDrag, moveDrag, stopDrag }
}
