<template>
  <!-- Floating panels must escape the app root's stacking context to sit above body-attached drawers. -->
  <Teleport to="body" :disabled="effectiveLayout === 'docked'">
  <!-- Hidden with a class, not v-if, so a reply keeps streaming while closed. -->
  <aside ref="panelElement" class="assistant-panel"
    :class="[`is-${effectiveLayout}`, { 'is-open': assistant.visible, 'is-resizing': resizing, 'is-dragging': dragging }]"
    :style="[{ width: `${panelWidth}px` }, effectiveLayout === 'floating' ? positionStyle : {}]" role="complementary" :aria-label="assistantName"
    :aria-hidden="!assistant.visible">
    <PanelResizeHandle edge="left" :label="t('assistant.resize')" :value="panelWidth"
      :min="ASSISTANT_PANEL_MIN_WIDTH" :max="ASSISTANT_PANEL_MAX_WIDTH"
      @start="startResize" @resize="resize" @end="resizing = false" />

    <header class="assistant-panel__header" @pointerdown="startPanelDrag" @pointermove="moveDrag"
      @pointerup="stopDrag" @pointercancel="stopDrag" @lostpointercapture="stopDrag">
      <ChildAgentTabBar header-mode :tabs="childTabs.tabs.value"
        :active-id="childTabs.activeId.value" :attention="childTabs.attention.value"
        @select="selectConversationTab" @close="childTabs.close">
        <template #parent>
          <span class="assistant-panel__header-icon" aria-hidden="true"><ResourceIcon type="assistant" :size="16" /></span>
          <span class="assistant-panel__title">{{ assistantName }}</span>
        </template>
        <template #beside>
          <t-popup placement="bottom-left" trigger="hover" show-arrow
            :overlay-inner-style="{ maxWidth: '320px', padding: '12px 14px' }">
            <button type="button" class="assistant-panel__help" :aria-label="t('assistant.help.label')"
              @click.stop @pointerdown.stop>
              <t-icon name="help-circle" size="16px" />
            </button>
            <template #content>
              <div class="assistant-help">
                <p class="assistant-help__title">{{ t('assistant.help.title') }}</p>
                <p>{{ t('assistant.help.identity') }}</p>
                <p>{{ t('assistant.help.page') }}</p>
                <p>{{ t('assistant.help.access') }}</p>
              </div>
            </template>
          </t-popup>
        </template>
      </ChildAgentTabBar>
      <t-tooltip :content="t('assistant.newConversation')" placement="bottom">
        <button type="button" class="assistant-panel__action" :disabled="!assistant.sessionId || starting"
          :aria-label="t('assistant.newConversation')" @click="startNewConversation">
          <t-icon name="chat-add" size="16px" />
        </button>
      </t-tooltip>
      <t-tooltip :content="t('assistant.history')" placement="bottom">
        <button type="button" class="assistant-panel__action" :aria-label="t('assistant.history')"
          :aria-pressed="showHistory" @click="toggleHistory">
          <t-icon name="history" size="16px" />
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
      <div v-if="showHistory" class="assistant-history">
        <div v-for="session in history" :key="session.id" class="assistant-history__row"
          :class="{ 'is-current': session.id === assistant.sessionId }">
          <button type="button" class="assistant-history__select" @click="selectSession(session.id)">
            <span>{{ session.title || t('assistant.untitledConversation') }}</span>
            <small>{{ t(`time.${classifyDateBucket(session.updated_at || session.created_at)}`) }}</small>
          </button>
          <t-tooltip :content="t('common.delete')">
            <button type="button" class="assistant-panel__action" :aria-label="t('common.delete')"
              :disabled="deleting === session.id" @click="confirmDelete(session.id)">
              <t-icon name="delete" size="16px" />
            </button>
          </t-tooltip>
        </div>
        <p v-if="historyError" role="alert">{{ t('assistant.historyFailed') }}</p>
        <p v-else-if="!historyLoading && !history.length">{{ t('assistant.historyEmpty') }}</p>
        <t-button v-if="historyError || history.length < historyTotal" variant="text" block
          :loading="historyLoading" @click="loadHistory()">
          {{ historyError ? t('common.retry') : t('assistant.loadMore') }}
        </t-button>
        <t-loading v-else-if="historyLoading" size="small" />
      </div>
      <div v-show="!showHistory" class="assistant-panel__conversation">
        <section v-if="assistant.actionSuggestions && assistant.sessionId" class="assistant-suggestions"
          :aria-label="assistant.actionSuggestions.label">
          <div class="assistant-suggestions__header">
            <h3>{{ assistant.actionSuggestions.label }}</h3>
            <button type="button" class="assistant-panel__action" :aria-label="t('common.close')"
              @click="assistant.clearActionSuggestions()"><t-icon name="close" size="14px" /></button>
          </div>
          <p>{{ t('assistant.chooseAction') }}</p>
          <div class="assistant-starters">
            <button v-for="action in assistant.actionSuggestions.actions" :key="action.key" type="button"
              class="assistant-starter" :disabled="starting || action.disabled"
              @click="assistant.askSuggestedAction(action.key)">{{ action.label }}</button>
          </div>
        </section>
        <ChatView v-if="assistant.sessionId && readySessionId === assistant.sessionId"
          :key="assistant.sessionId" :session_id="assistant.sessionId"
          :agent-id="BUILTIN_WEKNORA_ASSISTANT_ID" :embedded-mode="true" :initial-query="initialQuery"
          :host-scope="hostScope" :model-id="assistant.modelId" :default-model-id="defaultModelId"
          @update:model-id="assistant.setModel">
          <template v-if="assistant.pageSelection" #composer-chips>
            <div class="assistant-composer-context">
              <AssistantSelectionChip />
            </div>
          </template>
          <template #composer-controls>
            <AssistantContextMenu />
            <AssistantAccessPicker />
          </template>
        </ChatView>

        <div v-else-if="assistant.sessionId" class="assistant-history">
          <template v-if="sessionLoadError">
            <p role="alert">{{ t('assistant.historyFailed') }}</p>
            <t-button variant="text" @click="validateSession(assistant.sessionId)">{{ t('common.retry') }}</t-button>
          </template>
          <t-loading v-else size="small" />
        </div>
        <div v-else class="assistant-welcome">
          <div class="assistant-welcome-body">
            <div class="assistant-welcome-icon">
              <ResourceIcon type="assistant" :size="28" />
            </div>
            <h3 class="assistant-welcome-title">{{ assistant.actionSuggestions?.label || t('assistant.welcomeTitle') }}</h3>
            <p class="assistant-welcome-desc">{{ assistant.actionSuggestions ? t('assistant.chooseAction') : welcomeContent.description }}</p>
          </div>
          <div v-if="assistant.actionSuggestions" class="assistant-starters">
            <button v-for="action in assistant.actionSuggestions.actions" :key="action.key" type="button"
              class="assistant-starter" :disabled="starting || action.disabled"
              @click="assistant.askSuggestedAction(action.key)">{{ action.label }}</button>
          </div>
          <div v-else class="assistant-starters">
            <button v-for="starter in welcomeContent.starters" :key="starter" type="button" class="assistant-starter"
              :disabled="starting" @click="startConversation(starter)">
              {{ starter }}
            </button>
          </div>
          <div class="assistant-welcome-composer">
            <InputField ref="welcomeInput" :embedded-mode="true" compact :auto-focus="assistant.visible"
              :model-id="assistant.modelId" :default-model-id="defaultModelId" @update:model-id="assistant.setModel"
              @send-msg="(query) => startConversation(query)">
              <template v-if="assistant.pageSelection" #chips>
                <div class="assistant-composer-context">
                  <AssistantSelectionChip />
                </div>
              </template>
              <template #controls>
                <AssistantContextMenu />
                <AssistantAccessPicker />
              </template>
            </InputField>
          </div>
        </div>
      </div>
    </div>
  </aside>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, provide, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import ChatView from '@/views/chat/index.vue'
import ChildAgentTabBar from '@/views/chat/components/ChildAgentTabBar.vue'
import { createChildAgentTabs, childAgentTabsKey } from '@/composables/useChildAgentTabs'
import { useFloatingPreviewDrag } from '@/composables/useFloatingPreviewDrag'
import InputField from '@/components/Input-field.vue'
import PanelResizeHandle from '@/components/PanelResizeHandle.vue'
import ResourceIcon from '@/components/icons/ResourceIcon.vue'
import AssistantContextMenu from './AssistantContextMenu.vue'
import AssistantSelectionChip from './AssistantSelectionChip.vue'
import AssistantAccessPicker from './AssistantAccessPicker.vue'
import { createSessions, getSessionsList, getSession, type SessionSummary } from '@/api/chat'
import { classifyDateBucket } from '@/components/sessionGrouping'
import { BUILTIN_WEKNORA_ASSISTANT_ID } from '@/api/agent'
import { useAuthStore } from '@/stores/auth'
import { useAssistantDataChange } from '@/utils/assistantDataChange'
import {
  ASSISTANT_PANEL_MAX_WIDTH,
  ASSISTANT_PANEL_MIN_WIDTH,
  toPageContextPayload,
  toPageSelectionPayload,
  useAssistantStore,
  type AssistantOpenRequest,
} from '@/stores/assistant'
import { removeSession, SESSION_MUTATION_EVENT, type SessionMutationDetail } from '@/components/sessionMutations'
import type { PageContextPayload } from '@/api/chat/streame'

const { t, te } = useI18n()
const route = useRoute()
const childTabs = createChildAgentTabs()
provide(childAgentTabsKey, childTabs)
const assistant = useAssistantStore()
const auth = useAuthStore()
useAssistantDataChange(() => { void auth.refreshFromAuthMe() }, { areas: ['workspace'] })
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
const panelElement = ref<HTMLElement | null>(null)
const { positionStyle, dragging, startDrag, moveDrag, stopDrag } = useFloatingPreviewDrag(
  computed(() => assistant.visible && effectiveLayout.value === 'floating' ? panelElement.value : null), 'viewport',
)

function startPanelDrag(event: PointerEvent) {
  if (effectiveLayout.value !== 'floating' || (event.target as Element).closest('button, a, input, select, textarea, [role="button"], [role="tab"]')) return
  startDrag(event)
}

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

function settingsWelcome(section: string) {
  const key = te(`assistant.settingsPages.${section}.q1`) ? section : 'fallback'
  return {
    description: t(`assistant.settingsPages.${key}.description`),
    starters: [t(`assistant.settingsPages.${key}.q1`), t(`assistant.settingsPages.${key}.q2`), t(`assistant.settingsPages.${key}.q3`)],
  }
}

const welcomeContent = computed(() => {
  const general = {
    description: t('assistant.welcomeDesc'),
    starters: [t('assistant.starters.releaseHighlights'), t('assistant.starters.gettingStarted'), t('assistant.starters.workflow')],
  }
  if (!assistant.pageAccess || !assistant.contextAttached) return general
  const c = assistant.pageContext
  // An open resource or settings dialog takes precedence over the page behind it.
  if (c?.messageId) return {
    description: t('assistant.welcomeDescriptions.message'),
    starters: [t('assistant.starters.messageSources'), t('assistant.starters.messageCheck'), t('assistant.starters.messageReuse')],
  }
  if (c?.modelId) return {
    description: t('assistant.welcomeDescriptions.model'),
    starters: [t('assistant.starters.modelFit'), t('assistant.starters.modelCheck'), t('assistant.starters.modelUsage')],
  }
  if (c?.mcpServiceId) return {
    description: t('assistant.welcomeDescriptions.mcp'),
    starters: [t('assistant.starters.mcpTools'), t('assistant.starters.mcpUsage'), t('assistant.starters.mcpCheck')],
  }
  if (c?.page === 'settings_models') return {
    description: t('assistant.welcomeDescriptions.model'),
    starters: [t('assistant.starters.modelSetup'), t('assistant.starters.modelTypes'), t('assistant.starters.modelUsage')],
  }
  if (c?.page === 'mcp_services') return {
    description: t('assistant.welcomeDescriptions.mcp'),
    starters: [t('assistant.starters.mcpFind'), t('assistant.starters.mcpSetup'), t('assistant.starters.mcpExamples')],
  }
  if (c?.page === 'settings') return settingsWelcome(c.section || 'fallback')
  if (route.name === 'settings') {
    const section = typeof route.query.section === 'string' ? route.query.section : 'general'
    const panel = typeof route.query.panel === 'string' ? route.query.panel : ''
    return settingsWelcome(section === 'system-global' && panel ? `system-global-${panel}` : section)
  }
  if (c?.agentId) return {
    description: t('assistant.agentWelcomeDesc'),
    starters: [t('assistant.starters.agentConfig'), t('assistant.starters.agentModel'), t('assistant.starters.agentKnowledge')],
  }
  if (c?.wikiSlug) return {
    description: t('assistant.welcomeDescriptions.wiki'),
    starters: [t('assistant.starters.wikiSummary'), t('assistant.starters.wikiSources'), t('assistant.starters.wikiIssues')],
  }
  if (c?.knowledgeId) return {
    description: t('assistant.welcomeDescriptions.document'),
    starters: [t('assistant.starters.documentSummary'), t('assistant.starters.documentSteps'), t('assistant.starters.documentFaq')],
  }
  if (c?.section === 'faq' && c.knowledgeBaseId) return {
    description: t('assistant.welcomeDescriptions.faq'),
    starters: [t('assistant.starters.faqCoverage'), t('assistant.starters.faqDuplicates'), t('assistant.starters.faqImprove')],
  }
  if (c?.page === 'chat') return {
    description: t('assistant.welcomeDescriptions.chat'),
    starters: [t('assistant.starters.chatSummary'), t('assistant.starters.chatCheck'), t('assistant.starters.chatFollowup')],
  }
  if (c?.knowledgeBaseId || route.name === 'knowledgeBaseDetail') return {
    description: t('assistant.welcomeDescriptions.knowledgeBase'),
    starters: [t('assistant.starters.knowledgeTopics'), t('assistant.starters.recentActivity'), t('assistant.starters.knowledgeGaps')],
  }
  if (route.name === 'artifactLibrary') return {
    description: t('assistant.welcomeDescriptions.artifacts'),
    starters: [t('assistant.starters.artifactRecent'), t('assistant.starters.artifactSources'), t('assistant.starters.artifactCreate')],
  }
  if (route.name === 'knowledgeBaseList' || route.name === 'home') return {
    description: t('assistant.welcomeDescriptions.knowledgeBases'),
    starters: [t('assistant.starters.knowledgeOverview'), t('assistant.starters.knowledgeType'), t('assistant.starters.knowledgeOrganize')],
  }
  if (route.name === 'agentList') return {
    description: t('assistant.welcomeDescriptions.agents'),
    starters: [t('assistant.starters.agents'), t('assistant.starters.agentCreate'), t('assistant.starters.agentChoose')],
  }
  if (route.name === 'organizationList') return {
    description: t('assistant.welcomeDescriptions.organizations'),
    starters: [t('assistant.starters.organizationShared'), t('assistant.starters.organizationJoin'), t('assistant.starters.organizationCreate')],
  }
  if (route.name === 'automationList') return {
    description: t('assistant.welcomeDescriptions.automation'),
    starters: route.params.automationId ? [
      t('assistant.starters.automationSummary'), t('assistant.starters.automationSchedule'), t('assistant.starters.automationImprove'),
    ] : [
      t('assistant.starters.automationIdeas'), t('assistant.starters.automationCreate'), t('assistant.starters.automationOverview'),
    ],
  }
  if (route.name === 'toolbox' && route.params.section === 'mcp') return {
    description: t('assistant.welcomeDescriptions.mcp'),
    starters: [t('assistant.starters.mcpFind'), t('assistant.starters.mcpSetup'), t('assistant.starters.mcpExamples')],
  }
  if (route.name === 'toolbox' && route.params.section === 'browserconnection') return {
    description: t('assistant.welcomeDescriptions.browser'),
    starters: [t('assistant.starters.browserConnect'), t('assistant.starters.browserTasks'), t('assistant.starters.browserReconnect')],
  }
  if (route.name === 'toolbox') return {
    description: t('assistant.welcomeDescriptions.skills'),
    starters: [t('assistant.starters.skillFind'), t('assistant.starters.skillAdd'), t('assistant.starters.skillUse')],
  }
  if (route.name === 'chat') return {
    description: t('assistant.welcomeDescriptions.chat'),
    starters: [t('assistant.starters.chatSummary'), t('assistant.starters.chatCheck'), t('assistant.starters.chatFollowup')],
  }
  return general
})

// Page context is a hint, not an explicit retrieval selection. The backend
// validates it against the agent's scope; only user selections narrow that scope.
const hostScope = () => {
  assistant.clearActionSuggestions()
  const context = assistant.contextAttached ? assistant.pageContext : null
  const title = assistant.contextAttached ? assistant.pageTitle : ''
  const view = assistant.contextAttached ? assistant.pageView : ''
  const selection = assistant.pageSelection
  // A selection goes with one message; the next is about what is selected then.
  if (selection) assistant.setPageSelection(null)
  const focus = initialMessage.value?.sessionId === assistant.sessionId ? initialMessage.value.focus : undefined
  initialMessage.value = null
  const assistantAccess = assistant.effectiveAccessLevel
  if (!context && !title && !view && !selection) return { knowledgeBaseIds: [], pageContext: null, assistantAccess }
  const pageContext: PageContextPayload = context ? toPageContextPayload(focus ? { ...context, ...focus } : context) : {}
  if (title) pageContext.title = title
  if (view) pageContext.view = view
  if (selection) pageContext.selection = toPageSelectionPayload(selection)
  return {
    knowledgeBaseIds: [],
    pageContext,
    assistantAccess,
  }
}

const welcomeInput = ref<{ focusInput?: () => void } | null>(null)
watch(() => assistant.focusRequest, () => {
  showHistory.value = false
  childTabs.select('')
  if (!assistant.sessionId) welcomeInput.value?.focusInput?.()
}, { flush: 'post' })

async function startConversation(query: string, focus?: AssistantOpenRequest['focus']) {
  const text = query.trim()
  if (!text || starting.value) return
  const owner = conversationOwner.value
  starting.value = true
  try {
    const res: any = await createSessions({ kind: 'assistant' })
    // A workspace switch can complete while session creation is in flight.
    if (conversationOwner.value !== owner) return
    const sessionId = res?.data?.id
    if (!sessionId) throw new Error('missing session id')
    initialMessage.value = { sessionId, query: text, focus }
    showHistory.value = false
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

const showHistory = ref(false)
const selectConversationTab = (id: string) => { showHistory.value = false; childTabs.select(id) }
const history = ref<SessionSummary[]>([])
const historyPage = ref(0)
const historyTotal = ref(0)
const historyLoading = ref(false)
const historyError = ref(false)
const deleting = ref('')
const readySessionId = ref('')
const sessionLoadError = ref(false)
let historyGeneration = 0

async function loadHistory(reset = false) {
  if (historyLoading.value && !reset) return
  const generation = reset ? ++historyGeneration : historyGeneration
  const owner = conversationOwner.value
  if (reset) {
    history.value = []
    historyPage.value = 0
    historyTotal.value = 0
  }
  historyLoading.value = true
  historyError.value = false
  try {
    const res: any = await getSessionsList(historyPage.value + 1, 20, undefined, 'assistant')
    if (generation !== historyGeneration || owner !== conversationOwner.value) return
    if (!res?.success) throw new Error('session list failed')
    const seen = new Set(history.value.map(session => session.id))
    history.value.push(...(res.data as SessionSummary[]).filter(session => !seen.has(session.id)))
    historyTotal.value = res.total
    historyPage.value++
  } catch {
    if (generation === historyGeneration && owner === conversationOwner.value) historyError.value = true
  } finally {
    if (generation === historyGeneration && owner === conversationOwner.value) historyLoading.value = false
  }
}

function toggleHistory() {
  showHistory.value = !showHistory.value
  if (showHistory.value) void loadHistory(true)
}

function startNewConversation() {
  showHistory.value = false
  assistant.startNewConversation()
}

function selectSession(id: string) {
  assistant.setSession(id)
  showHistory.value = false
}

function confirmDelete(id: string) {
  const owner = conversationOwner.value
  const dialog = DialogPlugin.confirm({
    header: t('chatHeader.deleteConfirmTitle'),
    body: t('chatHeader.deleteConfirmBody'),
    confirmBtn: { content: t('common.delete'), theme: 'danger' },
    cancelBtn: t('common.cancel'),
    theme: 'warning',
    onConfirm: async () => {
      if (deleting.value || owner !== conversationOwner.value) return
      deleting.value = id
      try {
        await removeSession(id)
        if (owner !== conversationOwner.value) return
        if (id === assistant.sessionId) startNewConversation()
        if (showHistory.value) await loadHistory(true)
        dialog.destroy()
      } catch {
        if (owner === conversationOwner.value) MessagePlugin.error(t('chat.deleteSessionFailed'))
      } finally {
        deleting.value = ''
      }
    },
    onClose: () => dialog.destroy(),
  })
}

async function validateSession(id: string) {
  const owner = conversationOwner.value
  readySessionId.value = ''
  sessionLoadError.value = false
  if (!id) return
  if (id === initialMessage.value?.sessionId) {
    readySessionId.value = id
    return
  }
  try {
    const res: any = await getSession(id)
    if (owner !== conversationOwner.value || id !== assistant.sessionId) return
    if (res?.data?.kind !== 'assistant') {
      startNewConversation()
      return
    }
    readySessionId.value = id
  } catch (error: any) {
    if (owner !== conversationOwner.value || id !== assistant.sessionId) return
    const status = error?.$httpStatus ?? error?.response?.status ?? error?.status
    if (status === 403 || status === 404) startNewConversation()
    else sessionLoadError.value = true
  }
}

watch(() => assistant.sessionId, (id) => {
  childTabs.reset()
  if (id !== initialMessage.value?.sessionId) initialMessage.value = null
  void validateSession(id)
}, { immediate: true })

watch(conversationOwner, () => {
  childTabs.reset()
  ++historyGeneration
  history.value = []
  historyTotal.value = 0
  historyPage.value = 0
  historyLoading.value = false
  historyError.value = false
  showHistory.value = false
})

const handleSessionMutation = (event: Event) => {
  const detail = (event as CustomEvent<SessionMutationDetail>).detail
  if (detail?.removed && detail.sessionId === assistant.sessionId) startNewConversation()
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
@import (reference) '../css/suggested-questions.less';
@import (reference) '../css/settings-section.less';
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

  // Above document/model drawers (2100/2300), below confirmation dialogs (2500).
  &.is-floating {
    position: fixed;
    right: 12px;
    bottom: 12px;
    height: min(720px, calc(100% - 24px));
    max-width: calc(100% - 24px);
    z-index: 2400;
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-lg);
    box-shadow: var(--td-shadow-3);
    overflow: hidden;

    .assistant-panel__header {
      cursor: grab;
      touch-action: none;
      user-select: none;
    }

    &.is-dragging .assistant-panel__header {
      cursor: grabbing;
    }
  }

  &.is-resizing {
    user-select: none;

    .assistant-panel__body {
      pointer-events: none;
    }
  }
}

.assistant-composer-context {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-width: 0;
  height: 28px;
  flex: 0 0 100%;

  :deep(.assistant-selection-chip) {
    flex: 0 1 auto;
    max-width: 100%;
    height: 26px;
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

.assistant-panel__help {
  .settings-title-help();
  width: 22px;
  height: 22px;
  margin-right: 4px;
}

.assistant-help {
  display: flex;
  flex-direction: column;
  gap: 8px;

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-sm);
    line-height: 1.6;
  }

  p.assistant-help__title {
    color: var(--td-text-color-primary);
    font-size: var(--app-text-md);
    font-weight: 600;
  }
}

.assistant-panel__title {
  flex: 0 1 auto;
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

.assistant-panel__conversation {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;

  // Align the assistant composer with the main conversation composer.
  :deep(.input-container.is-embedded) {
    padding-bottom: max(8px, env(safe-area-inset-bottom));
  }
}

.assistant-history {
  overflow-y: auto;
  padding: 12px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
}

.assistant-history__row {
  display: flex;
  align-items: center;
  gap: 4px;
  border-radius: var(--app-radius-md);

  &:hover, &.is-current {
    background: var(--td-bg-color-container-hover);
  }

  &.is-current {
    color: var(--td-brand-color);
  }
}

.assistant-history__select {
  flex: 1;
  min-width: 0;
  padding: 10px 8px;
  border: 0;
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer;

  span, small {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  small {
    margin-top: 4px;
    color: var(--td-text-color-placeholder);
  }
}

.assistant-welcome {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  background: radial-gradient(ellipse 115% 65% at 50% 100%,
    color-mix(in srgb, var(--td-brand-color) 8%, transparent), transparent 75%);

  > .assistant-starters {
    flex: 0 0 auto;
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
    margin: 0;
    padding: 0 16px 4px;
  }

  .assistant-starter {
    display: inline-flex;
    align-items: center;
    max-width: 100%;
    padding: 8px 14px;
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-lg);
    background: var(--td-bg-color-container);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.04);
    font-size: var(--app-text-md);
    line-height: 1.5;
    text-align: left;
    white-space: normal;
    transition: border-color var(--app-motion-base) ease, box-shadow var(--app-motion-base) ease, background var(--app-motion-base) ease;

    &:hover:not(:disabled) {
      .suggestion-chip-hover();
    }

    &:active:not(:disabled) {
      .suggestion-chip-active();
    }
  }
}

.assistant-welcome-body {
  flex: 1;
  min-height: 120px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 24px 24px 20px;
  text-align: center;
}

.assistant-welcome-icon {
  width: 40px;
  height: 40px;
  border-radius: var(--app-radius-md, 8px);
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--td-brand-color);
}

.assistant-welcome-title {
  margin: 8px 0 0;
  font-size: var(--app-text-2xl);
  font-weight: 500;
  line-height: 1.5;
  color: var(--td-text-color-primary);
}

.assistant-welcome-desc {
  margin: 0;
  max-width: 360px;
  font-size: var(--app-text-sm);
  line-height: 1.7;
  color: var(--td-text-color-secondary);
  text-wrap: balance;
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

.assistant-suggestions {
  flex-shrink: 0;
  padding: 16px;
  border-bottom: 1px solid var(--td-component-stroke);

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;

    h3 {
      margin: 0;
      font-size: var(--app-text-base);
      font-weight: 500;
      color: var(--td-text-color-primary);
    }
  }

  p {
    margin: 4px 0 0;
    font-size: var(--app-text-sm);
    line-height: 1.6;
    color: var(--td-text-color-secondary);
  }

  .assistant-starters {
    justify-content: flex-start;
  }
}

// Same inset as the composer inside a conversation.
.assistant-welcome-composer {
  margin-top: auto;
  flex-shrink: 0;
  padding: 12px 16px max(8px, env(safe-area-inset-bottom));
}

// Opening only changes presentation; the mounted conversation keeps streaming.
@media (prefers-reduced-motion: no-preference) {
  .assistant-panel.is-open {
    animation: assistant-panel-in var(--app-motion-slow) cubic-bezier(0.16, 1, 0.3, 1);

    .assistant-welcome-title,
    .assistant-welcome-desc,
    .assistant-welcome > .assistant-starters,
    .assistant-welcome-composer,
    .assistant-suggestions {
      animation: assistant-content-in var(--app-motion-slow) cubic-bezier(0.16, 1, 0.3, 1) backwards;
      animation-delay: var(--app-motion-instant);
    }

    .assistant-welcome-desc {
      animation-delay: var(--app-motion-fast);
    }

    .assistant-welcome > .assistant-starters {
      animation-delay: var(--app-motion-base);
    }
  }
}

@keyframes assistant-panel-in {
  from {
    opacity: 0;
    transform: translateX(24px);
  }

  to {
    opacity: 1;
    transform: none;
  }
}

@keyframes assistant-content-in {
  from {
    opacity: 0;
    transform: translateY(8px);
  }

  to {
    opacity: 1;
    transform: none;
  }
}
</style>

<style lang="less">
html.has-assistant-dock {
  .t-drawer:not(.t-drawer--attach):not(.chat-artifacts-drawer),
  .t-dialog__ctx--fixed .t-dialog__mask,
  .t-dialog__ctx--fixed .t-dialog__wrap {
    width: calc(100% - var(--assistant-dock-width));
  }

  .t-drawer--right:not(.t-drawer--attach):not(.chat-artifacts-drawer) {
    right: var(--assistant-dock-width);
  }
}
</style>
