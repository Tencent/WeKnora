import { onBeforeUnmount, ref, unref, watch, type MaybeRef, type Ref } from 'vue'
import { getEmbedChunkById } from '@/api/embed'
import {
  getCitationChunkCache,
  setCitationChunkCache,
} from '@/utils/citationChunkCache'

type FloatState = {
  visible: boolean
  type: 'kb' | 'web'
  top: number
  left: number
  title: string
  content: string
  url: string
  loading: boolean
  error: string
}

export function useEmbedCitationPopover(
  rootRef: Ref<HTMLElement | null>,
  channelId: MaybeRef<string>,
  token: MaybeRef<string>,
) {
  const float = ref<FloatState>({
    visible: false,
    type: 'kb',
    top: 0,
    left: 0,
    title: '',
    content: '',
    url: '',
    loading: false,
    error: '',
  })

  let hoverTimer: number | null = null
  let closeTimer: number | null = null

  const getCacheScope = () => `embed:${unref(channelId)}:${unref(token)}`

  const positionFor = (el: HTMLElement, offsetY = 0) => {
    const rect = el.getBoundingClientRect()
    float.value.top = rect.bottom + window.scrollY + 6 + offsetY
    float.value.left = Math.min(rect.left + window.scrollX, window.innerWidth - 320)
  }

  const openWeb = (el: HTMLElement) => {
    const url = el.getAttribute('data-url') || ''
    float.value.type = 'web'
    float.value.url = url
    float.value.title = el.querySelector('.tip-title')?.textContent || ''
    float.value.content = ''
    float.value.loading = false
    float.value.error = ''
    float.value.visible = true
    positionFor(el)
  }

  const openKb = async (el: HTMLElement) => {
    const chunkId = el.getAttribute('data-chunk-id') || ''
    const title = el.getAttribute('data-doc') || ''
    if (!chunkId) return
    float.value.type = 'kb'
    float.value.title = title
    float.value.url = ''
    float.value.visible = true
    positionFor(el, 4)

    const scope = getCacheScope()
    const cached = getCitationChunkCache(scope, chunkId)
    if (cached) {
      float.value.content = cached.content
      float.value.error = cached.error || ''
      float.value.loading = false
      return
    }

    float.value.loading = true
    float.value.error = ''
    float.value.content = ''
    try {
      const res = await getEmbedChunkById(unref(channelId), unref(token), chunkId)
      const content = String(res?.data?.content || '').trim()
      setCitationChunkCache(scope, chunkId, { content })
      float.value.content = content
    } catch {
      const msg = 'Failed to load'
      setCitationChunkCache(scope, chunkId, { content: '', error: msg })
      float.value.error = msg
    } finally {
      float.value.loading = false
    }
  }

  const scheduleClose = () => {
    if (closeTimer) window.clearTimeout(closeTimer)
    closeTimer = window.setTimeout(() => {
      float.value.visible = false
    }, 120)
  }

  const onMouseOver = (e: Event) => {
    const target = e.target as HTMLElement
    const kbEl = target.closest?.('.citation-kb') as HTMLElement | null
    const webEl = target.closest?.('.citation-web') as HTMLElement | null
    if (!kbEl && !webEl) return
    if (closeTimer) {
      window.clearTimeout(closeTimer)
      closeTimer = null
    }
    if (hoverTimer) window.clearTimeout(hoverTimer)
    hoverTimer = window.setTimeout(() => {
      if (kbEl) void openKb(kbEl)
      else if (webEl) openWeb(webEl)
    }, kbEl ? 80 : 40)
  }

  const onMouseOut = (e: Event) => {
    const rt = (e as MouseEvent).relatedTarget as HTMLElement | null
    if (rt?.closest?.('.citation-kb, .citation-web, .embed-citation-float')) return
    if (hoverTimer) {
      window.clearTimeout(hoverTimer)
      hoverTimer = null
    }
    scheduleClose()
  }

  const onClick = (e: Event) => {
    const target = e.target as HTMLElement
    const kbEl = target.closest?.('.citation-kb') as HTMLElement | null
    if (kbEl) {
      e.preventDefault()
      e.stopPropagation()
      void openKb(kbEl)
      return
    }
    const wikiEl = target.closest?.('.citation-wiki') as HTMLElement | null
    if (wikiEl) {
      e.preventDefault()
      e.stopPropagation()
    }
  }

  const bind = () => {
    const root = rootRef.value
    if (!root) return
    root.addEventListener('mouseover', onMouseOver, true)
    root.addEventListener('mouseout', onMouseOut, true)
    root.addEventListener('click', onClick, true)
  }

  const unbind = () => {
    const root = rootRef.value
    if (!root) return
    root.removeEventListener('mouseover', onMouseOver, true)
    root.removeEventListener('mouseout', onMouseOut, true)
    root.removeEventListener('click', onClick, true)
  }

  watch(rootRef, () => {
    unbind()
    bind()
  }, { flush: 'post' })

  onBeforeUnmount(() => {
    unbind()
    if (hoverTimer) window.clearTimeout(hoverTimer)
    if (closeTimer) window.clearTimeout(closeTimer)
  })

  return { float, rebind: () => { unbind(); bind() } }
}
