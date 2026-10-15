<template>
  <!-- Hidden with a class, not v-if, so a reply keeps streaming while closed. -->
  <aside class="assistant-panel" :class="[`is-${effectiveLayout}`, { 'is-open': assistant.visible, 'is-resizing': resizing }]"
    :style="{ width: `${panelWidth}px` }" role="complementary" :aria-label="assistantName"
    :aria-hidden="!assistant.visible">
    <PanelResizeHandle edge="left" :label="t('assistant.resize')" :value="panelWidth"
      :min="ASSISTANT_PANEL_MIN_WIDTH" :max="ASSISTANT_PANEL_MAX_WIDTH"
      @start="startResize" @resize="resize" @end="resizing = false" />

    <header class="assistant-panel__header">
      <span class="assistant-panel__header-icon" aria-hidden="true">
        <t-icon name="compass" />
      </span>
      <span class="assistant-panel__title">{{ assistantName }}</span>
      <t-tooltip :content="t('assistant.newConversation')" placement="bottom">
        <button type="button" class="assistant-panel__action" :disabled="!assistant.sessionId || starting"
          :aria-label="t('assistant.newConversation')" @click="assistant.startNewConversation()">
          <t-icon name="chat-add" size="16px" />
        </button>
      </t-tooltip>
      <t-tooltip v-if="canDock" :content="layoutToggleLabel" placement="bottom">
        <button type="button" class="assistant-panel__action" :aria-label="layoutToggleLabel"
          :aria-pressed="assistant.layout === 'docked'" @click="toggleLayout">
          <t-icon :name="assistant.layout === 'docked' ? 'pin-filled' : 'pin'" size="16px" />
        </button>
      </t-tooltip>
      <t-tooltip :content="t('common.close')" placement="bottom">
        <button type="button" class="assistant-panel__action" :aria-label="t('common.close')"
          @click="assistant.close()">
          <t-icon name="close" size="16px" />
        </button>
      </t-tooltip>
    </header>

    <div class="assistant-panel__body">
      <ChatView v-if="assistant.sessionId" :key="assistant.sessionId" :session_id="assistant.sessionId"
        :agent-id="BUILTIN_WEKNORA_ASSISTANT_ID" :embedded-mode="true" :initial-query="initialQuery"
        :host-scope="hostScope" :model-id="assistant.modelId" :default-model-id="defaultModelId"
        @update:model-id="assistant.setModel">
        <template v-if="assistant.pageContext" #composer-chips>
          <AssistantContextChip />
        </template>
        <template #composer-controls>
          <AssistantAccessPicker />
        </template>
      </ChatView>

      <div v-else class="assistant-welcome">
        <div class="assistant-welcome-body">
          <div class="assistant-welcome-icon">
            <t-icon name="compass" size="22px" />
          </div>
          <h3 class="assistant-welcome-title">{{ t('assistant.welcomeTitle') }}</h3>
          <p class="assistant-welcome-desc">{{ t('assistant.welcomeDesc') }}</p>
          <div class="assistant-starters">
            <button v-for="starter in starters" :key="starter" type="button" class="assistant-starter"
              :disabled="starting" @click="startConversation(starter)">
              {{ starter }}
            </button>
          </div>
        </div>
        <div class="assistant-welcome-composer">
          <InputField :embedded-mode="true" compact :auto-focus="assistant.visible" :model-id="assistant.modelId"
            :default-model-id="defaultModelId" @update:model-id="assistant.setModel"
            @send-msg="(query) => startConversation(query)">
            <template v-if="assistant.pageContext" #chips>
              <AssistantContextChip />
            </template>
            <template #controls>
              <AssistantAccessPicker />
            </template>
          </InputField>
        </div>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import ChatView from '@/views/chat/index.vue'
import InputField from '@/components/Input-field.vue'
import PanelResizeHandle from '@/components/PanelResizeHandle.vue'
import AssistantContextChip from './AssistantContextChip.vue'
import AssistantAccessPicker from './AssistantAccessPicker.vue'
import { createSessions } from '@/api/chat'
import { BUILTIN_WEKNORA_ASSISTANT_ID } from '@/api/agent'
import { useMenuStore } from '@/stores/menu'
import { useAuthStore } from '@/stores/auth'
import {
  ASSISTANT_PANEL_MAX_WIDTH,
  ASSISTANT_PANEL_MIN_WIDTH,
  toPageContextPayload,
  useAssistantStore,
  type AssistantOpenRequest,
} from '@/stores/assistant'
import { SESSION_MUTATION_EVENT, type SessionMutationDetail } from '@/components/sessionMutations'

const { t } = useI18n()
const assistant = useAssistantStore()
const menuStore = useMenuStore()
const auth = useAuthStore()
const conversationOwner = computed(() => `${auth.user?.id}:${auth.effectiveTenantId}`)
const assistantName = computed(() => assistant.displayName || t('assistant.title'))

// Below this the page would be too cramped beside a docked panel, so the
// panel floats instead whatever the preference.
const DOCK_MIN_VIEWPORT = 1100
// The page keeps at least this much room beside a docked panel.
const DOCK_MIN_PAGE_WIDTH = 720
const FLOATING_MARGIN = 12

const viewportWidth = ref(typeof window !== 'undefined' ? window.innerWidth || 0 : 0)
const onViewportResize = () => { viewportWidth.value = window.innerWidth }
const canDock = computed(() => viewportWidth.value >= DOCK_MIN_VIEWPORT)
const effectiveLayout = computed(() => assistant.layout === 'docked' && canDock.value ? 'docked' : 'floating')
const docked = computed(() => assistant.visible && effectiveLayout.value === 'docked')

const panelWidth = computed(() => {
  const room = effectiveLayout.value === 'docked'
    ? viewportWidth.value - DOCK_MIN_PAGE_WIDTH
    : viewportWidth.value - 2 * FLOATING_MARGIN
  return Math.max(ASSISTANT_PANEL_MIN_WIDTH, Math.min(assistant.width, room || assistant.width))
})

const layoutToggleLabel = computed(() => assistant.layout === 'docked'
  ? t('assistant.layout.float') : t('assistant.layout.dock'))
const toggleLayout = () => assistant.setLayout(assistant.layout === 'docked' ? 'floating' : 'docked')

const resizing = ref(false)
let resizeStartWidth = 0
const startResize = () => {
  resizeStartWidth = panelWidth.value
  resizing.value = true
}
const resize = (delta: number) => assistant.setWidth(resizeStartWidth - delta)

// Page overlays (drawers, dialogs, side panels) stop at a docked panel's edge
// instead of covering it: TDesign's in the unscoped styles below, ours by
// reading --assistant-dock-width in their own styles.
watch([docked, panelWidth], ([isDocked, width]) => {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  root.classList.toggle('has-assistant-dock', isDocked)
  root.style.setProperty('--assistant-dock-width', isDocked ? `${width}px` : '0px')
}, { immediate: true })

const defaultModelId = computed(() =>
  (assistant.contextAttached ? assistant.pageContext?.chatModelId : '') || '')

const starting = ref(false)
// A first message belongs to the session we created, never a restored one.
const initialMessage = ref<{
  sessionId: string
  query: string
  focus?: AssistantOpenRequest['focus']
} | null>(null)
const initialQuery = computed(() => initialMessage.value?.sessionId === assistant.sessionId
  ? initialMessage.value.query : '')

const starters = computed(() => {
  const c = assistant.contextAttached ? assistant.pageContext : null
  if (c?.wikiSlug) return [t('assistant.starters.wikiIssues'), t('assistant.starters.wikiSummary')]
  if (c?.knowledgeId) return [t('assistant.starters.documentStatus'), t('assistant.starters.documentSummary')]
  if (c) return [t('assistant.starters.failedDocuments'), t('assistant.starters.recentActivity')]
  return [t('assistant.starters.agents'), t('assistant.starters.formats'), t('assistant.starters.createWiki')]
})

// The backend only honours a page context inside the run's search scope, so
// the page's knowledge base travels with it.
const hostScope = () => {
  const context = assistant.contextAttached ? assistant.pageContext : null
  const focus = initialMessage.value?.sessionId === assistant.sessionId ? initialMessage.value.focus : undefined
  initialMessage.value = null
  const assistantAccess = assistant.effectiveAccessLevel
  if (!context) return { knowledgeBaseIds: [], pageContext: null, assistantAccess }
  return {
    knowledgeBaseIds: [context.knowledgeBaseId],
    pageContext: toPageContextPayload(focus ? { ...context, ...focus } : context),
    assistantAccess,
  }
}

async function startConversation(query: string, focus?: AssistantOpenRequest['focus']) {
  const text = query.trim()
  if (!text || starting.value) return
  const owner = conversationOwner.value
  starting.value = true
  try {
    const res: any = await createSessions({})
    // A workspace switch can complete while session creation is in flight.
    if (conversationOwner.value !== owner) return
    const sessionId = res?.data?.id
    if (!sessionId) throw new Error('missing session id')
    const now = new Date().toISOString()
    menuStore.updataMenuChildren({
      title: assistantName.value,
      path: `chat/${sessionId}`,
      id: sessionId,
      isMore: false,
      isNoTitle: true,
      created_at: now,
      updated_at: now,
    })
    initialMessage.value = { sessionId, query: text, focus }
    assistant.setSession(sessionId)
  } catch (e) {
    console.error('[Assistant] Failed to start a conversation', e)
    MessagePlugin.error(t('assistant.startFailed'))
  } finally {
    starting.value = false
  }
}

watch(() => [assistant.visible, assistant.pendingRequest] as const, ([visible, request]) => {
  if (!visible || !request) return
  const { query, focus } = assistant.takePendingRequest() || {}
  assistant.restorePageContext()
  if (query) void startConversation(query, focus)
}, { immediate: true })

watch(() => assistant.sessionId, (id) => {
  if (id !== initialMessage.value?.sessionId) initialMessage.value = null
})

const handleSessionMutation = (event: Event) => {
  const detail = (event as CustomEvent<SessionMutationDetail>).detail
  if (detail?.removed && detail.sessionId === assistant.sessionId) assistant.startNewConversation()
}
onMounted(() => {
  window.addEventListener(SESSION_MUTATION_EVENT, handleSessionMutation)
  window.addEventListener('resize', onViewportResize)
})
onUnmounted(() => {
  window.removeEventListener(SESSION_MUTATION_EVENT, handleSessionMutation)
  window.removeEventListener('resize', onViewportResize)
  if (typeof document !== 'undefined') {
    document.documentElement.classList.remove('has-assistant-dock')
    document.documentElement.style.removeProperty('--assistant-dock-width')
  }
})
</script>

<style lang="less" scoped>
.assistant-panel {
  display: none;
  position: relative;
  flex-direction: column;
  min-height: 0;
  box-sizing: border-box;
  background: var(--td-bg-color-container);

  &.is-open {
    display: flex;
  }

  // A column of the platform layout: the page to its left narrows.
  &.is-docked {
    flex-shrink: 0;
    height: 100%;
    border-left: 1px solid var(--td-component-stroke);
  }

  // Above page drawers (1500), below dialogs and the settings shell; no mask.
  &.is-floating {
    position: fixed;
    top: 12px;
    right: 12px;
    bottom: 12px;
    z-index: 1600;
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-lg);
    box-shadow: var(--td-shadow-3);
    overflow: hidden;

    &.is-open {
      animation: assistant-panel-in var(--app-motion-base) cubic-bezier(0.22, 0.61, 0.36, 1);
    }
  }

  &.is-resizing {
    user-select: none;

    .assistant-panel__body {
      pointer-events: none;
    }
  }
}

@keyframes assistant-panel-in {
  from {
    opacity: 0;
    transform: translateX(16px);
  }

  to {
    opacity: 1;
    transform: none;
  }
}

.assistant-panel__header {
  display: flex;
  align-items: center;
  gap: 4px;
  flex: 0 0 var(--app-chat-header-height);
  min-width: 0;
  padding: 0 12px 0 16px;
  box-sizing: border-box;
  border-bottom: 1px solid var(--td-component-stroke);
}

.assistant-panel__header-icon {
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  margin-right: 4px;
  border-radius: var(--app-radius-sm);
  display: flex;
  align-items: center;
  justify-content: center;
  background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
  color: var(--td-brand-color);
  font-size: var(--app-text-lg);
}

.assistant-panel__title {
  flex: 1 1 auto;
  min-width: 0;
  font-size: var(--app-text-base);
  font-weight: 500;
  line-height: 20px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.assistant-panel__action {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-secondary);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &:disabled {
    cursor: not-allowed;
    color: var(--td-text-color-disabled);
  }

  &[aria-pressed='true'] {
    color: var(--td-text-color-primary);
  }
}

.assistant-panel__body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.assistant-welcome {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.assistant-welcome-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 0 24px;
  text-align: center;
}

.assistant-welcome-icon {
  width: 40px;
  height: 40px;
  border-radius: var(--app-radius-md, 8px);
  display: flex;
  align-items: center;
  justify-content: center;
  background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
  color: var(--td-brand-color);
}

.assistant-welcome-title {
  margin: 4px 0 0;
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.assistant-welcome-desc {
  margin: 0;
  max-width: 420px;
  font-size: var(--app-text-md);
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.assistant-starters {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 8px;
  margin-top: 12px;
}

.assistant-starter {
  padding: 6px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: inherit;
  font-size: var(--app-text-sm);
  line-height: 18px;
  cursor: pointer;
  transition: background var(--app-motion-fast), border-color var(--app-motion-fast);

  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
    border-color: var(--td-component-border);
  }

  &:disabled {
    cursor: not-allowed;
    color: var(--td-text-color-disabled);
  }
}

// Same inset as the composer inside a conversation.
.assistant-welcome-composer {
  flex-shrink: 0;
  padding: 12px 16px 16px;
}
</style>

<style lang="less">
html.has-assistant-dock {
  .t-drawer:not(.t-drawer--attach),
  .t-dialog__ctx--fixed .t-dialog__mask,
  .t-dialog__ctx--fixed .t-dialog__wrap {
    width: calc(100% - var(--assistant-dock-width));
  }

  .t-drawer--right:not(.t-drawer--attach) {
    right: var(--assistant-dock-width);
  }
}
</style>
