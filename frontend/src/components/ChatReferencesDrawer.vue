<template>
  <Teleport to="body" :disabled="!useOverlay">
    <Transition name="references-panel">
      <aside
        v-if="visible"
        class="chat-references-panel"
        :class="{ 'is-overlay': useOverlay, 'is-embedded': embeddedMode }"
        role="complementary"
        :aria-label="panelTitle"
      >
        <header class="chat-references-panel__header">
          <div class="chat-references-panel__heading">
            <h3 class="chat-references-panel__title">{{ panelTitle }}</h3>
            <span v-if="totalCount" class="chat-references-panel__count">{{ totalCount }}</span>
          </div>
          <button
            type="button"
            class="chat-references-panel__close"
            :aria-label="t('common.close')"
            @click="close"
          >
            <t-icon name="close" />
          </button>
        </header>

        <div ref="listElement" class="chat-references-panel__body">
          <div v-if="sections.length === 0" class="chat-references-panel__empty">
            {{ t('chat.referencesDrawerEmpty') }}
          </div>

          <section
            v-for="section in sections"
            :key="section.id"
            class="chat-references-panel__section"
          >
            <h4 v-if="sections.length > 1" class="chat-references-panel__section-title">
              {{ sectionTitle(section.id) }}
            </h4>

            <article
              v-for="item in section.items"
              :key="item.key"
              :ref="(el) => setItemRef(item.key, el as HTMLElement | null)"
              class="reference-card"
              :class="{
                'reference-card--web': item.kind === 'web',
                'reference-card--document': item.kind === 'document',
                'reference-card--tool': item.kind === 'tool',
                'is-highlighted': item.key === activeHighlightKey,
              }"
            >
              <component
                :is="item.kind === 'web' ? 'a' : 'div'"
                class="reference-card__inner"
                :class="{ 'is-expandable': item.kind === 'document' && hasMoreContent(item) }"
                :href="item.kind === 'web' ? item.url : undefined"
                :target="item.kind === 'web' ? '_blank' : undefined"
                :rel="item.kind === 'web' ? 'noopener noreferrer' : undefined"
                :role="item.kind === 'document' && hasMoreContent(item) ? 'button' : undefined"
                :tabindex="item.kind === 'document' && hasMoreContent(item) ? 0 : undefined"
                @mousedown="trackContentPointerDown"
                @click="item.kind === 'document' && hasMoreContent(item) ? toggleDocumentSnippet(item, $event) : undefined"
                @keydown.enter="item.kind === 'document' && hasMoreContent(item) ? toggleDocumentSnippet(item) : undefined"
                @keydown.space.prevent="item.kind === 'document' && hasMoreContent(item) ? toggleDocumentSnippet(item) : undefined"
              >
                <div class="reference-card__meta">
                  <span class="reference-card__index">{{ item.index }}</span>
                  <img
                    v-if="item.kind === 'web' && item.faviconUrl"
                    class="reference-card__favicon"
                    :src="item.faviconUrl"
                    alt=""
                    loading="lazy"
                    @error="onFaviconError"
                  />
                  <t-icon
                    v-else-if="item.kind === 'document'"
                    name="file"
                    class="reference-card__doc-icon"
                  />
                  <t-icon
                    v-else-if="item.kind === 'tool'"
                    name="tools"
                    class="reference-card__doc-icon"
                  />
                  <span v-if="item.domain" class="reference-card__domain">{{ item.domain }}</span>
                  <span v-else-if="item.kind === 'document'" class="reference-card__domain">
                    {{ t('chat.referencesDrawerDocument') }}
                  </span>
                  <span v-else-if="item.kind === 'tool'" class="reference-card__domain">
                    {{ t('chat.referencesDrawerTool') }}
                  </span>
                </div>

                <h5 class="reference-card__title">{{ item.title }}</h5>
                <p v-if="item.snippet && !expandedKeys.has(item.key)" class="reference-card__snippet">
                  {{ item.snippet }}
                </p>
                <div v-if="item.kind === 'document' && expandedKeys.has(item.key)" class="reference-card__content">
                  {{ item.content }}
                </div>
                <div v-else-if="item.kind === 'tool' && item.content" class="reference-card__content">
                  {{ item.content }}
                </div>

              </component>

              <div v-if="item.kind === 'document'" class="reference-card__actions">
                <a
                  v-if="item.knowledgeBaseId && !embeddedMode"
                  class="reference-card__action"
                  :href="getDocumentHref(item)"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <t-icon name="jump" size="14px" />
                  <span>{{ t('chat.navigateToDocument') }}</span>
                </a>
              </div>
            </article>
          </section>
        </div>
      </aside>
    </Transition>
  </Teleport>

  <Transition name="references-backdrop">
    <div
      v-if="visible && useOverlay"
      class="chat-references-panel__backdrop"
      @click="close"
    />
  </Transition>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useChatReferencesDrawer } from '@/composables/useChatReferencesDrawer'
import {
  buildReferenceSections,
  resolveReferenceHighlightKey,
  type ReferenceListItem,
} from '@/utils/referenceSources'

const props = defineProps<{
  embeddedMode?: boolean
  overlayBreakpoint?: number
}>()

const { t } = useI18n()
const router = useRouter()
const drawer = useChatReferencesDrawer()

const listElement = ref<HTMLElement | null>(null)
const itemElements = new Map<string, HTMLElement>()
const expandedKeys = reactive(new Set<string>())
const pointerDownSelectionText = ref('')

const visible = computed(() => drawer?.visible.value ?? false)
const references = computed(() => drawer?.references.value ?? [])
const highlight = computed(() => drawer?.highlight.value ?? null)

const useOverlay = computed(() => {
  if (props.embeddedMode) return true
  if (typeof window === 'undefined') return false
  return window.innerWidth < (props.overlayBreakpoint ?? 960)
})

const sections = computed(() => buildReferenceSections(references.value))
const totalCount = computed(() => sections.value.reduce((sum, section) => sum + section.items.length, 0))

const activeHighlightKey = computed(() =>
  resolveReferenceHighlightKey(references.value, highlight.value),
)

const panelTitle = computed(() => {
  const webCount = sections.value.find((section) => section.id === 'web')?.items.length ?? 0
  const docCount = sections.value.find((section) => section.id === 'documents')?.items.length ?? 0
  const toolCount = sections.value.find((section) => section.id === 'tools')?.items.length ?? 0
  if (toolCount > 0 && webCount === 0 && docCount === 0) {
    return t('chat.referencesDrawerTitleTools')
  }
  if ([webCount, docCount, toolCount].filter((count) => count > 0).length > 1) {
    return t('chat.referencesDrawerTitleMixed')
  }
  if (webCount > 0) {
    return t('chat.referencesDrawerTitleWeb')
  }
  if (docCount > 0) {
    return t('chat.referencesDrawerTitleDocs')
  }
  return t('chat.referencesDrawerTitle')
})

function sectionTitle(id: 'web' | 'documents' | 'tools') {
  if (id === 'web') return t('chat.referencesDrawerWebSection')
  if (id === 'tools') return t('chat.referencesDrawerToolsSection')
  return t('chat.referencesDrawerDocsSection')
}

function close() {
  drawer?.close()
}

function setItemRef(key: string, el: HTMLElement | null) {
  if (!el) {
    itemElements.delete(key)
    return
  }
  itemElements.set(key, el)
}

function onFaviconError(event: Event) {
  const img = event.target as HTMLImageElement | null
  if (img) img.style.display = 'none'
}

function hasMoreContent(item: ReferenceListItem) {
  const content = String(item.content || '').trim()
  const snippet = String(item.snippet || '').replace(/…$/, '').trim()
  if (!content) return false
  if (!snippet) return true
  return content.length > snippet.length && !content.startsWith(snippet)
    ? true
    : content.length > snippet.length + 8
}

function getSelectedText() {
  if (typeof window === 'undefined') return ''
  return window.getSelection()?.toString().trim() || ''
}

function trackContentPointerDown() {
  pointerDownSelectionText.value = getSelectedText()
}

function shouldIgnoreContentToggle(event?: MouseEvent) {
  if (!event) return false
  const selectedText = getSelectedText()
  if (selectedText || pointerDownSelectionText.value) {
    pointerDownSelectionText.value = ''
    return true
  }
  pointerDownSelectionText.value = ''
  return false
}

function toggleDocumentSnippet(item: ReferenceListItem, event?: MouseEvent) {
  if (shouldIgnoreContentToggle(event)) return
  if (expandedKeys.has(item.key)) {
    expandedKeys.delete(item.key)
    return
  }
  expandedKeys.add(item.key)
}

function getDocumentHref(item: ReferenceListItem) {
  if (!item.knowledgeBaseId) return ''
  const query: Record<string, string> = {}
  if (item.knowledgeId) query.knowledge_id = item.knowledgeId
  return router.resolve({
    path: `/platform/knowledge-bases/${item.knowledgeBaseId}`,
    query,
  }).href
}

async function scrollToHighlight() {
  const key = activeHighlightKey.value
  if (!key) return
  await nextTick()
  const el = itemElements.get(key)
  if (!el) return
  el.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
}

watch(activeHighlightKey, () => {
  void scrollToHighlight()
})

watch(visible, (open) => {
  if (!open) {
    expandedKeys.clear()
    return
  }
  void scrollToHighlight()
})
</script>

<style scoped lang="less">
.chat-references-panel__backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.28);
  z-index: 1200;
}

.chat-references-panel {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  width: min(420px, 100vw);
  z-index: 1201;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
  border-left: 1px solid var(--td-component-stroke);
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.06);

  &.is-overlay {
    box-shadow: -12px 0 32px rgba(0, 0, 0, 0.12);
  }
}

.chat-references-panel__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 16px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.chat-references-panel__heading {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.chat-references-panel__title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  line-height: 1.4;
}

.chat-references-panel__count {
  flex-shrink: 0;
  min-width: 22px;
  height: 22px;
  padding: 0 8px;
  border-radius: 999px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.chat-references-panel__close {
  border: 0;
  background: transparent;
  color: var(--td-text-color-placeholder);
  width: 32px;
  height: 32px;
  border-radius: 8px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;

  &:hover {
    background: var(--td-bg-color-component-hover);
    color: var(--td-text-color-secondary);
  }
}

.chat-references-panel__body {
  flex: 1;
  overflow-y: auto;
  padding: 12px 12px 20px;
}

.chat-references-panel__empty {
  padding: 24px 8px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.chat-references-panel__section + .chat-references-panel__section {
  margin-top: 16px;
}

.chat-references-panel__section-title {
  margin: 0 0 8px;
  padding: 0 4px;
  font-size: 12px;
  font-weight: 600;
  color: var(--td-text-color-placeholder);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.reference-card {
  border: 1px solid var(--td-component-stroke);
  border-radius: 12px;
  background: var(--td-bg-color-container);
  overflow: hidden;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background-color 0.2s ease;

  & + & {
    margin-top: 10px;
  }

  &:hover {
    border-color: color-mix(in srgb, var(--td-text-color-placeholder) 42%, var(--td-component-stroke));
    box-shadow: 0 4px 14px rgba(0, 0, 0, 0.04);
  }

  &.is-highlighted {
    border-color: var(--td-text-color-placeholder);
    background: var(--td-bg-color-secondarycontainer);
    box-shadow: none;
    animation: reference-card-highlight 1s ease;
  }
}

@keyframes reference-card-highlight {
  0% {
    background: var(--td-bg-color-component-hover);
  }
  100% {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.reference-card__inner {
  display: block;
  padding: 12px 14px;
  color: inherit;
  text-decoration: none;
  transition: background-color 0.16s ease;

  &.is-expandable {
    cursor: pointer;
  }
}

.reference-card--web .reference-card__inner:hover {
  background: color-mix(in srgb, var(--td-text-color-primary) 3%, var(--td-bg-color-container));
}

.reference-card--document .reference-card__inner:hover,
.reference-card--tool .reference-card__inner:hover {
  background: color-mix(in srgb, var(--td-text-color-primary) 3%, var(--td-bg-color-container));
}

.reference-card__meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  min-width: 0;
}

.reference-card__index {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  border-radius: 999px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 11px;
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.reference-card__favicon {
  width: 16px;
  height: 16px;
  border-radius: 4px;
  flex-shrink: 0;
}

.reference-card__doc-icon {
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

.reference-card__domain {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.reference-card__title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.45;
  color: var(--td-text-color-primary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.reference-card__snippet {
  margin: 8px 0 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--td-text-color-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 4;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.reference-card__content {
  margin-top: 8px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 360px;
  overflow-y: auto;
}

.reference-card__actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 0 14px 12px;
}

.reference-card__action {
  border: 0;
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  padding: 0;
  text-decoration: none;

  &:hover {
    color: var(--td-text-color-primary);
    text-decoration: underline;
  }
}

.references-panel-enter-active {
  transition:
    transform 0.24s cubic-bezier(0.22, 0.61, 0.36, 1),
    opacity 0.24s cubic-bezier(0.22, 0.61, 0.36, 1);
}

.references-panel-leave-active {
  transition:
    transform 0.3s cubic-bezier(0.22, 0.61, 0.36, 1),
    opacity 0.3s cubic-bezier(0.22, 0.61, 0.36, 1);
}

.references-panel-enter-from,
.references-panel-leave-to {
  transform: translateX(100%);
  opacity: 0.6;
}

.references-backdrop-enter-active {
  transition: opacity 0.24s ease;
}

.references-backdrop-leave-active {
  transition: opacity 0.3s ease;
}

.references-backdrop-enter-from,
.references-backdrop-leave-to {
  opacity: 0;
}
</style>
