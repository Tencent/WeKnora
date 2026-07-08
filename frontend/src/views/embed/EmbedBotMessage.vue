<template>
  <div class="embed-bot-msg" :class="{ 'is-embedded': embeddedMode }">
    <div v-if="session?.isRagMode" class="rag-answer-stack">
      <RagPipelineProgress :session="session" embedded-mode />
      <AgentStreamDisplay v-if="session?.isAgentMode" :session="session" :session-id="sessionId" :user-query="userQuery"
        rag-mode :embedded-mode="embeddedMode" :embed-channel-id="embedChannelId" :embed-token="embedToken"
        :embed-session-sig="embedSessionSig" :embed-visitor-id="embedVisitorId" />
    </div>
    <template v-else>
      <DocInfo v-if="session?.knowledge_references?.length" :session="session" embedded-mode />
      <AgentStreamDisplay v-if="session?.isAgentMode" :session="session" :session-id="sessionId" :user-query="userQuery"
        :embedded-mode="embeddedMode" :embed-channel-id="embedChannelId" :embed-token="embedToken"
        :embed-session-sig="embedSessionSig" :embed-visitor-id="embedVisitorId" />
    </template>
    <DeepThink v-if="session?.showThink && !session?.isAgentMode" :deep-session="session" />
    <div v-if="!session?.hideContent && !session?.isAgentMode" ref="parentMd">
      <div v-if="hasActualContent" class="content-wrapper">
        <div class="ai-markdown-template markdown-content" v-stable-html="renderedHTML" />
      </div>
      <div v-if="hasActualContent && !session?.is_completed" class="loading-indicator">
        <div class="loading-typing">
          <span></span>
          <span></span>
          <span></span>
        </div>
      </div>
    </div>
    <Teleport to="body">
      <div v-if="citationFloat.visible" ref="citationFloatRef" class="embed-citation-float"
        :style="citationFloatStyle" @mouseenter="cancelCitationClose"
        @mouseleave="scheduleCitationClose">
        <template v-if="citationFloat.type === 'web'">
          <div class="embed-citation-float__title">{{ citationFloat.title || citationFloat.url }}</div>
          <a v-if="citationFloat.url" class="embed-citation-float__link" :href="citationFloat.url" target="_blank"
            rel="noopener noreferrer">{{ citationFloat.url }}</a>
        </template>
        <template v-else>
          <div class="embed-citation-float__title">{{ citationFloat.title }}</div>
          <div v-if="citationFloat.loading" class="embed-citation-float__muted">…</div>
          <div v-else-if="citationFloat.error" class="embed-citation-float__error">{{ citationFloat.error }}</div>
          <div
            v-else
            ref="citationFloatBody"
            class="embed-citation-float__body markdown-content"
            v-stable-html="citationPreviewHTML"
          />
        </template>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUpdated, ref, watch } from 'vue'
import 'katex/dist/katex.min.css'
import {
  sanitizeMarkdownHTML,
  safeMarkdownToHTML,
  createSafeImage,
  isValidImageURL,
  hydrateProtectedFileImages,
} from '@/utils/security'
import {
  createChatMarkdownRenderer,
  renderChatMarkdown,
} from '@/utils/chatMarkdownRenderer'
import {
  createMermaidCodeRenderer,
  ensureMermaidInitialized,
  enhanceMarkdownContainer,
} from '@/utils/mermaidShared'
import { useEmbedCitationPopover } from '@/composables/useEmbedCitationPopover'
import { useTypewriter } from '@/composables/useTypewriter'
import { vStableHtml } from '@/directives/stableHtml'
import { renderCitationPreviewContent } from '@/utils/citationPreview'
import {
  computeCitationFloatPosition,
  currentCitationViewport,
} from '@/utils/citationFloatPosition'

const RagPipelineProgress = defineAsyncComponent(
  () => import('@/views/chat/components/RagPipelineProgress.vue'),
)
const AgentStreamDisplay = defineAsyncComponent(
  () => import('@/views/chat/components/AgentStreamDisplay.vue'),
)
const DocInfo = defineAsyncComponent(
  () => import('@/views/chat/components/docInfo.vue'),
)
const DeepThink = defineAsyncComponent(
  () => import('@/views/chat/components/deepThink.vue'),
)

ensureMermaidInitialized()

const markdownRenderer = createChatMarkdownRenderer({
  codeRenderer: createMermaidCodeRenderer('mermaid-embed-botmsg'),
  imageRenderer: ({ href, title, text }) => createSafeImage(href, text || '', title || ''),
  isValidImageUrl: isValidImageURL,
})

type EmbedSession = {
  content?: string
  isRagMode?: boolean
  isAgentMode?: boolean
  showThink?: boolean
  hideContent?: boolean
  is_completed?: boolean
  agentEventStream?: Array<Record<string, unknown>>
  knowledge_references?: Array<{ chunk_type?: string; knowledge_id?: string; knowledge_title?: string }>
}

const props = withDefaults(
  defineProps<{
    content?: string
    session?: EmbedSession
    sessionId?: string
    userQuery?: string
    embeddedMode?: boolean
    embedChannelId?: string
    embedToken?: string
    embedSessionSig?: string
    embedVisitorId?: string
  }>(),
  {
    content: '',
    session: () => ({}),
    sessionId: '',
    userQuery: '',
    embeddedMode: true,
    embedChannelId: '',
    embedToken: '',
    embedSessionSig: '',
    embedVisitorId: '',
  },
)

const parentMd = ref<HTMLElement | null>(null)
const citationFloatRef = ref<HTMLElement | null>(null)
const citationFloatBody = ref<HTMLElement | null>(null)

const embedChannelIdRef = computed(() => props.embedChannelId)
const embedTokenRef = computed(() => props.embedToken)

const { float: citationFloat, rebind: rebindCitations } = useEmbedCitationPopover(
  parentMd,
  embedChannelIdRef,
  embedTokenRef,
  {
    getKnowledgeReferences: () => props.session?.knowledge_references,
  },
)

let citationCloseTimer: number | null = null
const cancelCitationClose = () => {
  if (citationCloseTimer) {
    window.clearTimeout(citationCloseTimer)
    citationCloseTimer = null
  }
}
const scheduleCitationClose = () => {
  cancelCitationClose()
  citationCloseTimer = window.setTimeout(() => {
    citationFloat.value.visible = false
  }, 120)
}

// Smooth the streamed answer into a steady typewriter cadence (shared with the
// Agent path). History reloads arrive complete and snap to full.
const answerText = computed(() => String(props.content || props.session?.content || ''))
const { displayed: typedAnswer } = useTypewriter(
  () => answerText.value,
  () => Boolean(props.session?.is_completed),
)

const renderedHTML = computed(() => {
  const text = typedAnswer.value
  if (!text.trim()) return ''
  return renderChatMarkdown(text, {
    renderer: markdownRenderer,
    escapeMarkdown: safeMarkdownToHTML,
    sanitizeHtml: sanitizeMarkdownHTML,
    streaming: !props.session?.is_completed,
  })
})

const hasActualContent = computed(() => {
  const text = String(props.content || props.session?.content || '')
  return text.trim().length > 0
})

const citationPreviewHTML = computed(() => renderCitationPreviewContent(citationFloat.value.content))
const citationFloatTop = ref(citationFloat.value.top)
const citationFloatLeft = ref(citationFloat.value.left)
const citationFloatStyle = computed(() => ({
  top: `${citationFloatTop.value}px`,
  left: `${citationFloatLeft.value}px`,
}))

const updateCitationFloatPosition = async () => {
  if (!citationFloat.value.visible) return
  await nextTick()
  const el = citationFloatRef.value
  const anchor = citationFloat.value.anchor
  if (!el || !anchor) {
    citationFloatTop.value = citationFloat.value.top
    citationFloatLeft.value = citationFloat.value.left
    return
  }

  const rect = el.getBoundingClientRect()
  const position = computeCitationFloatPosition({
    anchor,
    floatSize: {
      width: rect.width || 320,
      height: rect.height || 80,
    },
    viewport: currentCitationViewport(),
    offsetY: citationFloat.value.offsetY,
  })
  citationFloatTop.value = position.top
  citationFloatLeft.value = position.left
}

watch(citationPreviewHTML, async () => {
  await nextTick()
  const embedCtx =
    props.embedChannelId && props.embedToken
      ? { channelId: props.embedChannelId, token: props.embedToken }
      : undefined
  await hydrateProtectedFileImages(citationFloatBody.value, embedCtx)
  await updateCitationFloatPosition()
}, { flush: 'post' })

watch(() => [
  citationFloat.value.visible,
  citationFloat.value.top,
  citationFloat.value.left,
  citationFloat.value.anchor?.top,
  citationFloat.value.anchor?.bottom,
  citationFloat.value.anchor?.left,
  citationFloat.value.offsetY,
  citationFloat.value.loading,
  citationFloat.value.error,
  citationFloat.value.title,
  citationPreviewHTML.value,
], () => {
  void updateCitationFloatPosition()
}, { flush: 'post', immediate: true })

const hydrateImages = async () => {
  const embedCtx =
    props.embedChannelId && props.embedToken
      ? { channelId: props.embedChannelId, token: props.embedToken }
      : undefined
  await hydrateProtectedFileImages(parentMd.value, embedCtx)
}

const renderMermaidDiagrams = async () => {
  await enhanceMarkdownContainer(parentMd.value)
}

watch(renderedHTML, () => {
  nextTick(async () => {
    rebindCitations()
    await hydrateImages()
    if (props.session?.is_completed) {
      await renderMermaidDiagrams()
    }
  })
})

onUpdated(() => {
  nextTick(async () => {
    await hydrateImages()
    if (props.session?.is_completed) {
      await renderMermaidDiagrams()
    }
  })
})

onMounted(() => {
  nextTick(async () => {
    await hydrateImages()
    await renderMermaidDiagrams()
  })
})
</script>

<style scoped lang="less">
@import '../../components/css/chat-markdown.less';
@import '../../components/css/chat-citations.less';

.embed-bot-msg {
  border-radius: 4px;
  color: var(--td-text-color-primary);
  font-size: 16px;
  margin-right: auto;
  max-width: 100%;
  box-sizing: border-box;

  &.is-embedded {
    width: 100%;

    :deep(.agent-stream-display) {
      width: 100%;
    }
  }
}

.rag-answer-stack {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.content-wrapper {
  padding: 2px 0;
}

.markdown-content {
  // Chat Markdown visual styles are centralized in chat-markdown.less.
  // Do not add element-level Markdown rules here; update the shared mixin.
  .chat-markdown-typography();
  .chat-citation-pills();
}

.loading-indicator {
  padding: 8px 0;
}

.loading-typing {
  display: flex;
  align-items: center;
  gap: 4px;

  span {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--embed-primary, var(--td-brand-color));
    animation: typingBounce 1.4s ease-in-out infinite;

    &:nth-child(1) {
      animation-delay: 0s;
    }

    &:nth-child(2) {
      animation-delay: 0.2s;
    }

    &:nth-child(3) {
      animation-delay: 0.4s;
    }
  }
}

@keyframes typingBounce {

  0%,
  60%,
  100% {
    transform: translateY(0);
  }

  30% {
    transform: translateY(-8px);
  }
}

.embed-citation-float {
  position: absolute;
  z-index: 10000;
  width: max-content;
  max-width: min(520px, calc(100vw - 32px));
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--td-bg-color-container);
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.18);
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-primary);

  &__title {
    font-weight: 600;
    color: var(--td-brand-color);
    margin-bottom: 4px;
  }

  &__link {
    color: var(--td-brand-color);
    word-break: break-all;
  }

  &__body {
    max-height: 280px;
    overflow: auto;
    white-space: normal;
    overscroll-behavior: contain;
  }

  &__body p {
    margin: 0 0 8px;
  }

  &__body p:last-child {
    margin-bottom: 0;
  }

  &__body .chat-markdown-table {
    max-width: 100%;
    overflow-x: auto;
    margin: 4px 0;
  }

  &__body table {
    width: max-content;
    min-width: 100%;
    border-collapse: collapse;
    font-size: 11px;
    line-height: 1.35;
  }

  &__body th,
  &__body td {
    padding: 4px 6px;
    border: 1px solid var(--td-component-stroke);
    vertical-align: top;
    white-space: normal;
  }

  &__muted {
    color: var(--td-text-color-placeholder);
  }

  &__error {
    color: var(--td-error-color);
  }
}
</style>
