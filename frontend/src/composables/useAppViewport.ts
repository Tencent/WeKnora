import { nextTick, onMounted, onUnmounted, watch } from 'vue'
import { useFont } from './useFont'
import { getRootZoom } from '@/utils/zoom'

/** Keep the composer above the software keyboard without overriding pinch zoom. */
export function useAppViewport() {
  const { currentSize } = useFont()
  function update() {
    const viewport = window.visualViewport
    if (viewport && viewport.scale !== 1) return
    document.documentElement.style.setProperty('--app-viewport-height', `${(viewport?.height || window.innerHeight) / getRootZoom()}px`)
  }
  watch(currentSize, () => nextTick(update))
  onMounted(() => {
    update()
    window.visualViewport?.addEventListener('resize', update)
    window.addEventListener('resize', update)
  })
  onUnmounted(() => {
    window.visualViewport?.removeEventListener('resize', update)
    window.removeEventListener('resize', update)
    document.documentElement.style.removeProperty('--app-viewport-height')
  })
}
