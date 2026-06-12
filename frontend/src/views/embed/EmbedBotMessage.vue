<template>
  <div class="embed-bot-msg" :class="{ 'is-embedded': embeddedMode }">
    <DocInfo
      v-if="session && !session.isAgentMode"
      :session="session"
      embedded-mode
    />
    <AgentStreamDisplay
      v-if="session?.isAgentMode"
      :session="session"
      :session-id="sessionId"
      :user-query="userQuery"
      :embedded-mode="embeddedMode"
      :embed-channel-id="embedChannelId"
      :embed-token="embedToken"
    />
    <template v-else-if="!session?.isAgentMode">
      <DeepThink v-if="session?.showThink" :deep-session="session" />
      <div v-if="!session?.hideContent" ref="parentMd">
        <div v-if="hasActualContent" class="content-wrapper">
          <div class="ai-markdown-template markdown-content" v-html="renderedHTML" />
        </div>
        <div v-if="hasActualContent && !session?.is_completed" class="loading-indicator">
          <div class="loading-typing">
            <span></span>
            <span></span>
            <span></span>
          </div>
        </div>
      </div>
    </template>
    <Teleport to="body">
      <div
        v-if="citationFloat.visible"
        class="embed-citation-float"
        :style="{ top: `${citationFloat.top}px`, left: `${citationFloat.left}px` }"
        @mouseenter="cancelCitationClose"
        @mouseleave="scheduleCitationClose"
      >
        <template v-if="citationFloat.type === 'web'">
          <div class="embed-citation-float__title">{{ citationFloat.title || citationFloat.url }}</div>
          <a
            v-if="citationFloat.url"
            class="embed-citation-float__link"
            :href="citationFloat.url"
            target="_blank"
            rel="noopener noreferrer"
          >{{ citationFloat.url }}</a>
        </template>
        <template v-else>
          <div class="embed-citation-float__title">{{ citationFloat.title }}</div>
          <div v-if="citationFloat.loading" class="embed-citation-float__muted">…</div>
          <div v-else-if="citationFloat.error" class="embed-citation-float__error">{{ citationFloat.error }}</div>
          <div v-else class="embed-citation-float__body">{{ citationFloat.content }}</div>
        </template>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, onMounted, onUpdated, ref, watch } from 'vue'
import { marked } from 'marked'
import markedKatex from 'marked-katex-extension'
import 'katex/dist/katex.min.css'
import {
  sanitizeHTML,
  safeMarkdownToHTML,
  createSafeImage,
  isValidImageURL,
  hydrateProtectedFileImages,
} from '@/utils/security'
import { replaceIncompleteImageWithPlaceholder } from '@/utils/chatMessageShared'
import {
  createMermaidCodeRenderer,
  ensureMermaidInitialized,
  renderMermaidInContainer,
} from '@/utils/mermaidShared'
import {
  extractCitationHtmlPlaceholders,
  preserveCitationTags,
  restoreCitationHtmlPlaceholders,
  restoreCitationTags,
} from '@/utils/citationMarkdown'
import { useEmbedCitationPopover } from '@/composables/useEmbedCitationPopover'

const AgentStreamDisplay = defineAsyncComponent(
  () => import('@/views/chat/components/AgentStreamDisplay.vue'),
)
const DocInfo = defineAsyncComponent(
  () => import('@/views/chat/components/docInfo.vue'),
)
const DeepThink = defineAsyncComponent(
  () => import('@/views/chat/components/deepThink.vue'),
)

marked.use({ breaks: true })
marked.use(markedKatex({ throwOnError: false, nonStandard: true }))
ensureMermaidInitialized()

const preprocessMathDelimiters = (rawText: string): string => {
  if (!rawText || typeof rawText !== 'string') return ''
  return rawText
    .replace(/\\\[([\s\S]*?)\\\]/g, '$$$$$1$$$$')
    .replace(/\\\(([\s\S]*?)\\\)/g, '$$$1$$')
}

const customRenderer = new marked.Renderer()
customRenderer.image = function ({ href, title, text }) {
  if (!isValidImageURL(href)) return ''
  return createSafeImage(href, text || '', title || '')
}
customRenderer.code = createMermaidCodeRenderer('mermaid-embed-botmsg')

const props = withDefaults(
  defineProps<{
    content?: string
    session?: Record<string, unknown>
    sessionId?: string
    userQuery?: string
    embeddedMode?: boolean
    embedChannelId?: string
    embedToken?: string
  }>(),
  {
    content: '',
    session: () => ({}),
    sessionId: '',
    userQuery: '',
    embeddedMode: true,
    embedChannelId: '',
    embedToken: '',
  },
)

const parentMd = ref<HTMLElement | null>(null)

const embedChannelIdRef = computed(() => props.embedChannelId)
const embedTokenRef = computed(() => props.embedToken)

const { float: citationFloat, rebind: rebindCitations } = useEmbedCitationPopover(
  parentMd,
  embedChannelIdRef,
  embedTokenRef,
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

const renderedHTML = computed(() => {
  const text = String(props.content || props.session?.content || '')
  if (!text.trim()) return ''
  const { text: tagSafe, tags } = preserveCitationTags(text)
  const processed = replaceIncompleteImageWithPlaceholder(tagSafe)
  const mathSafe = preprocessMathDelimiters(processed)
  const restoredTags = restoreCitationTags(mathSafe, tags)
  const safeMarkdown = safeMarkdownToHTML(restoredTags)
  const { content: mdWithPlaceholders, htmlSnippets } = extractCitationHtmlPlaceholders(safeMarkdown)
  const html = marked.parse(mdWithPlaceholders, { renderer: customRenderer, breaks: true }) as string
  const withCitations = restoreCitationHtmlPlaceholders(html, htmlSnippets)
  return sanitizeHTML(withCitations)
})

const hasActualContent = computed(() => {
  const text = String(props.content || props.session?.content || '')
  return text.trim().length > 0
})

const hydrateImages = async () => {
  const embedCtx =
    props.embedChannelId && props.embedToken
      ? { channelId: props.embedChannelId, token: props.embedToken }
      : undefined
  await hydrateProtectedFileImages(parentMd.value, embedCtx)
}

const renderMermaidDiagrams = async () => {
  await renderMermaidInContainer(parentMd.value)
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
@import '../../components/css/markdown.less';

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

.content-wrapper {
  background: var(--td-bg-color-container);
  border-radius: 6px;
  padding: 8px 0;
}

.ai-markdown-template {
  font-size: 15px;
  color: var(--td-text-color-primary);
  line-height: 1.6;
}

.markdown-content {
  :deep(p) {
    margin: 6px 0;
    line-height: 1.6;
  }

  :deep(code) {
    background: var(--td-bg-color-secondarycontainer);
    padding: 2px 5px;
    border-radius: 3px;
    font-family: var(--app-font-family-mono);
    font-size: 11px;
  }

  :deep(pre) {
    background: var(--td-bg-color-secondarycontainer);
    padding: 10px;
    border-radius: 4px;
    overflow-x: auto;
    margin: 6px 0;

    code {
      background: none;
      padding: 0;
    }
  }

  :deep(ul),
  :deep(ol) {
    margin: 6px 0;
    padding-left: 20px;
  }

  :deep(li) {
    margin: 3px 0;
  }

  :deep(blockquote) {
    border-left: 2px solid var(--td-brand-color);
    padding-left: 10px;
    margin: 6px 0;
    color: var(--td-text-color-secondary);
  }

  :deep(h1),
  :deep(h2),
  :deep(h3),
  :deep(h4),
  :deep(h5),
  :deep(h6) {
    margin: 10px 0 6px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  :deep(a) {
    color: var(--td-brand-color);
    text-decoration: none;

    &:hover {
      text-decoration: underline;
    }
  }

  :deep(table) {
    border-collapse: collapse;
    margin: 6px 0;
    font-size: 11px;
    width: 100%;

    th,
    td {
      border: 1px solid var(--td-component-stroke);
      padding: 5px 8px;
      text-align: left;
    }

    th {
      background: var(--td-bg-color-secondarycontainer);
      font-weight: 600;
    }

    tbody tr:nth-child(even) {
      background: var(--td-bg-color-secondarycontainer);
    }
  }

  :deep(img) {
    max-width: 80%;
    max-height: 300px;
    width: auto;
    height: auto;
    border-radius: 8px;
    display: block;
    margin: 8px 0;
    border: 0.5px solid var(--td-component-stroke);
    object-fit: contain;
  }

  :deep(.mermaid) {
    margin: 16px 0;
    padding: 16px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 8px;
    overflow-x: auto;
    text-align: center;

    svg {
      max-width: 100%;
      height: auto;
    }
  }
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

.markdown-content {
  :deep(.citation) {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    border-radius: 10px;
    padding: 2px 4px;
    font-size: 11px;
    line-height: 1.4;
    margin: 0 4px;
  }

  :deep(.citation-web),
  :deep(.citation-kb) {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    color: var(--td-brand-color);
    border: 1px solid color-mix(in srgb, var(--td-brand-color) 20%, transparent);
    cursor: pointer;
    white-space: nowrap;
  }

  :deep(.citation-web:hover),
  :deep(.citation-kb:hover) {
    background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
    border-color: var(--td-brand-color);
  }

  :deep(.citation .citation-icon) {
    display: inline-block;
    width: 14px;
    height: 14px;
    background-color: var(--embed-primary, var(--td-brand-color));
    mask-size: contain;
    mask-repeat: no-repeat;
    mask-position: center;
    -webkit-mask-size: contain;
    -webkit-mask-repeat: no-repeat;
    -webkit-mask-position: center;
    flex-shrink: 0;
  }

  :deep(.citation .citation-icon.web) {
    mask-image: url('@/assets/img/websearch-globe.svg');
    -webkit-mask-image: url('@/assets/img/websearch-globe.svg');
  }

  :deep(.citation .citation-icon.kb) {
    mask-image: url('@/assets/img/zhishiku.svg');
    -webkit-mask-image: url('@/assets/img/zhishiku.svg');
  }

  :deep(.citation-tip) {
    display: none;
  }

  :deep(a.wiki-content-link) {
    color: var(--td-brand-color);
    border-bottom: 1px dashed var(--td-brand-color);
    text-decoration: none;
    font-weight: 500;
  }
}

.embed-citation-float {
  position: absolute;
  z-index: 10000;
  max-width: 320px;
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
    max-height: 200px;
    overflow-y: auto;
    white-space: pre-wrap;
  }

  &__muted {
    color: var(--td-text-color-placeholder);
  }

  &__error {
    color: var(--td-error-color);
  }
}
</style>
