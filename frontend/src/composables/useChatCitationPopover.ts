import { onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getChunkByIdOnly } from '@/api/knowledge-base'
import { getEmbedChunkById } from '@/api/embed'
import { resolveCitationChunkId, type CitationKnowledgeRef } from '@/utils/citationMarkdown'
import {
  getCitationChunkCache,
  setCitationChunkCache,
} from '@/utils/citationChunkCache'

export { clearCitationChunkCache } from '@/utils/citationChunkCache'

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

export type CitationFloatState = FloatState

export type ChatCitationPopoverOptions = {
  getKnowledgeReferences?: () => CitationKnowledgeRef[] | null | undefined
  embedChannelId?: () => string | undefined
  embedToken?: () => string | undefined
  sessionId?: () => string | undefined
}

export function useChatCitationPopover(
  rootRef: Ref<HTMLElement | null>,
  options?: ChatCitationPopoverOptions,
) {
  const { t } = useI18n()

  const getCacheScope = () => {
    const channelId = options?.embedChannelId?.()
    const token = options?.embedToken?.()
    if (channelId && token) return `embed:${channelId}:${token}`
    return options?.sessionId?.() || 'default'
  }

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

  const fetchChunkContent = async (chunkId: string) => {
    const channelId = options?.embedChannelId?.()
    const token = options?.embedToken?.()
    if (channelId && token) {
      return getEmbedChunkById(channelId, token, chunkId)
    }
    return getChunkByIdOnly(chunkId)
  }

  const openKb = async (el: HTMLElement) => {
    const rawChunkId = el.getAttribute('data-chunk-id') || ''
    const title = el.getAttribute('data-doc') || ''
    const kbId = el.getAttribute('data-kb-id') || ''
    const chunkId = resolveCitationChunkId(
      rawChunkId,
      { doc: title, kbId },
      options?.getKnowledgeReferences?.(),
    ) || rawChunkId
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
      const res = await fetchChunkContent(chunkId)
      const content = String(res?.data?.content || '').trim()
      if (!content) {
        const msg = t('agentStream.citation.notFound')
        setCitationChunkCache(scope, chunkId, { content: '', error: msg })
        float.value.error = msg
        return
      }
      setCitationChunkCache(scope, chunkId, { content })
      float.value.content = content
    } catch {
      const msg = t('agentStream.citation.loadFailed')
      setCitationChunkCache(scope, chunkId, { content: '', error: msg })
      float.value.error = msg
    } finally {
      float.value.loading = false
    }
  }

  const scheduleClose = () => {
    if (closeTimer) window.clearTimeout(closeTimer)
    closeTimer = window.setTimeout(() => {
      const hoveredCitation = document.querySelector('.citation-kb:hover, .citation-web:hover')
      const hoveredPopup = document.querySelector('.chat-citation-float:hover')
      if (!hoveredCitation && !hoveredPopup) {
        float.value.visible = false
      }
    }, 120)
  }

  const cancelClose = () => {
    if (closeTimer) {
      window.clearTimeout(closeTimer)
      closeTimer = null
    }
  }

  const onMouseOver = (e: Event) => {
    const target = e.target as HTMLElement
    const kbEl = target.closest?.('.citation-kb') as HTMLElement | null
    const webEl = target.closest?.('.citation-web') as HTMLElement | null
    if (!kbEl && !webEl) return
    cancelClose()
    if (hoverTimer) window.clearTimeout(hoverTimer)
    hoverTimer = window.setTimeout(() => {
      if (kbEl) void openKb(kbEl)
      else if (webEl) openWeb(webEl)
    }, kbEl ? 80 : 40)
  }

  const onMouseOut = (e: Event) => {
    const rt = (e as MouseEvent).relatedTarget as HTMLElement | null
    if (rt?.closest?.('.citation-kb, .citation-web, .chat-citation-float')) return
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
    }
  }

  const onViewportChange = () => {
    if (float.value.visible) scheduleClose()
  }

  const bind = () => {
    const root = rootRef.value
    if (!root) return
    root.addEventListener('mouseover', onMouseOver, true)
    root.addEventListener('mouseout', onMouseOut, true)
    root.addEventListener('click', onClick, true)
    window.addEventListener('scroll', onViewportChange, true)
    window.addEventListener('resize', onViewportChange, true)
  }

  const unbind = () => {
    const root = rootRef.value
    if (root) {
      root.removeEventListener('mouseover', onMouseOver, true)
      root.removeEventListener('mouseout', onMouseOut, true)
      root.removeEventListener('click', onClick, true)
    }
    window.removeEventListener('scroll', onViewportChange, true)
    window.removeEventListener('resize', onViewportChange, true)
  }

  watch(rootRef, () => {
    unbind()
    bind()
  }, { flush: 'post' })

  onMounted(() => {
    bind()
  })

  onBeforeUnmount(() => {
    unbind()
    if (hoverTimer) window.clearTimeout(hoverTimer)
    if (closeTimer) window.clearTimeout(closeTimer)
  })

  return { float, rebind: () => { unbind(); bind() }, cancelClose, scheduleClose }
}
