<template>
  <Teleport to="body" :disabled="!useOverlay">
    <Transition name="references-panel" @after-enter="handlePanelAfterEnter">
      <aside
        v-if="visible"
        class="chat-references-panel"
        :class="{ 'is-overlay': useOverlay, 'is-embedded': embeddedMode }"
        role="complementary"
        :aria-label="panelTitle"
      >
        <header class="chat-references-panel__header">
          <div class="chat-references-panel__heading">
            <h3 class="chat-references-panel__title">
              {{ panelTitle }}<span v-if="totalCount" class="chat-references-panel__count"> · {{ totalCount }}</span>
            </h3>
          </div>
          <button
            type="button"
            class="chat-references-panel__close"
            :aria-label="t('common.close')"
            @click="close"
          >
            <t-icon name="close" size="20px" />
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
              class="reference-item"
              :class="{
                'reference-item--web': item.kind === 'web',
                'reference-item--document': item.kind === 'document',
                'reference-item--tool': item.kind === 'tool',
                'is-highlighted': item.key === activeHighlightKey,
              }"
            >
              <component
                :is="item.kind === 'web' ? 'a' : 'div'"
                class="reference-item__body"
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
                <template v-if="item.kind === 'document'">
                  <div class="reference-item__document">
                    <t-icon name="file" class="reference-item__doc-icon" />
                    <div class="reference-item__document-main">
                      <div class="reference-item__title-row">
                        <h5 class="reference-item__title">{{ item.title }}</h5>
                        <a
                          v-if="item.knowledgeBaseId && !embeddedMode"
                          class="reference-item__open"
                          :href="getDocumentHref(item)"
                          target="_blank"
                          rel="noopener noreferrer"
                          :aria-label="t('chat.navigateToDocument')"
                          @click.stop
                        >
                          <t-icon name="jump" size="14px" />
                        </a>
                      </div>
                      <p v-if="item.snippet && !expandedKeys.has(item.key)" class="reference-item__snippet">
                        {{ formatReferenceSnippet(item.snippet) }}
                      </p>
                      <div v-if="expandedKeys.has(item.key)" class="reference-item__content">
                        {{ formatReferenceSnippet(item.content) }}
                      </div>
                    </div>
                  </div>
                </template>
                <template v-else>
                  <div v-if="item.kind === 'web' && item.domain" class="reference-item__source">
                    <img
                      v-if="item.faviconUrl"
                      class="reference-item__source-mark"
                      :src="item.faviconUrl"
                      alt=""
                      loading="lazy"
                      @error="onFaviconError"
                    />
                    <span class="reference-item__domain">{{ item.domain }}</span>
                  </div>
                  <div v-else-if="item.kind === 'tool' && item.domain" class="reference-item__source">
                    <t-icon name="tools" class="reference-item__source-mark" />
                    <span class="reference-item__domain">{{ item.domain }}</span>
                  </div>

                  <h5 v-if="shouldShowItemTitle(item)" class="reference-item__title">{{ item.title }}</h5>

                  <p v-if="item.snippet && !expandedKeys.has(item.key)" class="reference-item__snippet">
                    {{ formatReferenceSnippet(item.snippet) }}
                  </p>
                  <div v-if="item.kind === 'tool' && item.content" class="reference-item__content">
                    {{ formatReferenceSnippet(item.content) }}
                  </div>
                </template>
              </component>
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
  formatReferenceSnippet,
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
const panelEntered = ref(false)

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

function shouldShowItemTitle(item: ReferenceListItem) {
  if (item.kind !== 'web') return true
  const title = item.title?.trim()
  const domain = item.domain?.trim()
  return Boolean(title && title !== domain)
}

async function scrollToHighlight() {
  if (!panelEntered.value) return
  const key = activeHighlightKey.value
  if (!key) return
  await nextTick()
  const el = itemElements.get(key)
  const container = listElement.value
  if (!el || !container) return

  // Keep citation positioning inside the drawer. Native element scrolling may
  // also adjust the outer chat viewport while the fixed panel is still
  // entering, which makes the conversation column visibly jump sideways.
  const itemRect = el.getBoundingClientRect()
  const containerRect = container.getBoundingClientRect()
  let nextTop: number | null = null
  if (itemRect.top < containerRect.top) {
    nextTop = container.scrollTop + itemRect.top - containerRect.top - 8
  } else if (itemRect.bottom > containerRect.bottom) {
    nextTop = container.scrollTop + itemRect.bottom - containerRect.bottom + 8
  }
  if (nextTop !== null) {
    container.scrollTo({ top: Math.max(0, nextTop), behavior: 'smooth' })
  }
}

function handlePanelAfterEnter() {
  panelEntered.value = true
  void scrollToHighlight()
}

watch(activeHighlightKey, () => {
  void scrollToHighlight()
})

// A user may click the same citation again after manually scrolling the drawer
// away from its card. The resolved key does not change in that case, but the
// highlight target object does, so replay the scroll for every activation.
watch(highlight, () => {
  void scrollToHighlight()
})

watch(visible, (open) => {
  if (!open) {
    panelEntered.value = false
    expandedKeys.clear()
    return
  }
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
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  line-height: 1.4;
}

.chat-references-panel__count {
  color: var(--td-text-color-placeholder);
  font-weight: 500;
}

.chat-references-panel__close {
  border: 0;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  width: 36px;
  height: 36px;
  border-radius: 10px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  transition: background 0.15s ease, color 0.15s ease;

  :deep(.t-icon) {
    font-size: 20px;
  }

  &:hover {
    background: color-mix(in srgb, var(--td-text-color-primary) 8%, var(--td-bg-color-secondarycontainer));
    color: var(--td-text-color-primary);
  }
}

.chat-references-panel__body {
  flex: 1;
  overflow-y: auto;
  padding: 4px 12px 24px;
}

.chat-references-panel__empty {
  padding: 24px 8px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.chat-references-panel__section {
  display: flex;
  flex-direction: column;
  gap: 6px;
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

.reference-item {
  border-radius: 12px;
  transition: background-color 0.15s ease;

  &:hover:not(.is-highlighted) {
    background: color-mix(in srgb, var(--td-text-color-primary) 4%, transparent);
  }

  &.is-highlighted {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.reference-item__body {
  display: block;
  padding: 10px 12px;
  color: inherit;
  text-decoration: none;

  &.is-expandable {
    cursor: pointer;
  }
}

.reference-item__document {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  min-width: 0;
}

.reference-item__doc-icon {
  flex-shrink: 0;
  width: 18px;
  margin-top: 3px;
  font-size: 16px;
  color: var(--td-text-color-primary);
}

.reference-item__document-main {
  flex: 1;
  min-width: 0;
}

.reference-item__source {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  margin-bottom: 6px;
}

.reference-item__source-mark {
  flex-shrink: 0;
  width: 16px;
  height: 16px;
  border-radius: 999px;
  object-fit: cover;
  font-size: 14px;
  color: var(--td-text-color-placeholder);
}

.reference-item__domain {
  font-size: 13px;
  line-height: 1.35;
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.reference-item__title-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  min-width: 0;
}

.reference-item__title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  word-break: break-word;
}

.reference-item__open {
  flex-shrink: 0;
  margin-top: 3px;
  color: var(--td-text-color-placeholder);
  line-height: 1;
  opacity: 0;
  transition: opacity 0.15s ease, color 0.15s ease;
}

.reference-item:hover .reference-item__open,
.reference-item.is-highlighted .reference-item__open {
  opacity: 1;
}

.reference-item__open:hover {
  color: var(--td-text-color-primary);
}

.reference-item__snippet {
  margin: 4px 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.reference-item__content {
  margin: 4px 0 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 360px;
  overflow-y: auto;
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
