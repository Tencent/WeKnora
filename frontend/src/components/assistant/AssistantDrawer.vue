<template>
  <t-drawer v-model:visible="assistant.visible" size="700px" :z-index="2100" :footer="false" :close-btn="true"
    class="assistant-drawer">
    <template #header>
      <div class="assistant-drawer-header">
        <div class="assistant-drawer-header-icon">
          <t-icon name="compass" />
        </div>
        <div class="assistant-drawer-header-title">{{ t('assistant.title') }}</div>
        <t-tooltip :content="t('assistant.newConversation')" placement="bottom">
          <t-button class="assistant-drawer-header-action" size="small" variant="text" shape="square"
            :disabled="!assistant.sessionId || starting" :aria-label="t('assistant.newConversation')"
            @click="assistant.startNewConversation()">
            <template #icon><t-icon name="chat-add" size="16px" /></template>
          </t-button>
        </t-tooltip>
      </div>
    </template>

    <ChatView v-if="assistant.sessionId" :key="assistant.sessionId" :session_id="assistant.sessionId"
      :agent-id="BUILTIN_WEKNORA_ASSISTANT_ID" :embedded-mode="true" :initial-query="initialQuery"
      :host-scope="hostScope">
      <template v-if="assistant.pageContext" #composer-chips>
        <AssistantContextChip />
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
        <InputField :embedded-mode="true" :auto-focus="assistant.visible"
          @send-msg="(query) => startConversation(query)">
          <template v-if="assistant.pageContext" #chips>
            <AssistantContextChip />
          </template>
        </InputField>
      </div>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import ChatView from '@/views/chat/index.vue'
import InputField from '@/components/Input-field.vue'
import AssistantContextChip from './AssistantContextChip.vue'
import { createSessions } from '@/api/chat'
import { BUILTIN_WEKNORA_ASSISTANT_ID } from '@/api/agent'
import { useMenuStore } from '@/stores/menu'
import { toPageContextPayload, useAssistantStore, type AssistantOpenRequest } from '@/stores/assistant'
import { SESSION_MUTATION_EVENT, type SessionMutationDetail } from '@/components/sessionMutations'

const { t } = useI18n()
const assistant = useAssistantStore()
const menuStore = useMenuStore()

const starting = ref(false)
const initialQuery = ref('')
// Applies to the first message of a conversation opened with a focus.
let pendingFocus: AssistantOpenRequest['focus'] | null = null

const starters = computed(() => {
  const c = assistant.contextAttached ? assistant.pageContext : null
  if (c?.wikiSlug) return [t('assistant.starters.wikiIssues'), t('assistant.starters.wikiSummary')]
  if (c?.knowledgeId) return [t('assistant.starters.documentStatus'), t('assistant.starters.documentSummary')]
  if (c) return [t('assistant.starters.failedDocuments'), t('assistant.starters.recentActivity')]
  return [t('assistant.starters.formats'), t('assistant.starters.createWiki')]
})

// The backend only honours a page context inside the run's search scope, so
// the page's knowledge base travels with it.
const hostScope = () => {
  const context = assistant.contextAttached ? assistant.pageContext : null
  const focus = pendingFocus
  pendingFocus = null
  if (!context) return { knowledgeBaseIds: [], pageContext: null }
  return {
    knowledgeBaseIds: [context.knowledgeBaseId],
    pageContext: toPageContextPayload(focus ? { ...context, ...focus } : context),
  }
}

async function startConversation(query: string, focus?: AssistantOpenRequest['focus']) {
  const text = query.trim()
  if (!text || starting.value) return
  starting.value = true
  try {
    const res: any = await createSessions({})
    const sessionId = res?.data?.id
    if (!sessionId) throw new Error('missing session id')
    const now = new Date().toISOString()
    menuStore.updataMenuChildren({
      title: t('assistant.title'),
      path: `chat/${sessionId}`,
      id: sessionId,
      isMore: false,
      isNoTitle: true,
      created_at: now,
      updated_at: now,
    })
    pendingFocus = focus || null
    initialQuery.value = text
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
  if (!id) initialQuery.value = ''
})

const handleSessionMutation = (event: Event) => {
  const detail = (event as CustomEvent<SessionMutationDetail>).detail
  if (detail?.removed && detail.sessionId === assistant.sessionId) assistant.startNewConversation()
}
onMounted(() => window.addEventListener(SESSION_MUTATION_EVENT, handleSessionMutation))
onUnmounted(() => window.removeEventListener(SESSION_MUTATION_EVENT, handleSessionMutation))
</script>

<style lang="less">
.assistant-drawer {
  .t-drawer__body {
    padding: 20px !important;
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }

  .chat {
    max-width: 100% !important;
    min-width: 100% !important;
    padding: 0 !important;
    height: 100% !important;
    flex: 1 !important;
    border-radius: 0 !important;
  }

  .chat_scroll_box {
    padding: 0 !important;
  }

  .chat>.input-container {
    padding: 16px 0 0 0 !important;
    box-sizing: border-box;
    width: 100% !important;
    max-width: 100% !important;
    margin: 0 !important;
    overflow-x: hidden;
  }

  .msg_list {
    max-width: 100% !important;
    padding-bottom: 0 !important;
    margin: 0 !important;
  }
}

.assistant-drawer-header {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  width: 100%;
  padding-right: 32px;
}

.assistant-drawer-header-icon {
  flex-shrink: 0;
  width: 24px;
  height: 24px;
  border-radius: var(--app-radius-sm);
  display: flex;
  align-items: center;
  justify-content: center;
  background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
  color: var(--td-brand-color);
  font-size: var(--app-text-lg);
}

.assistant-drawer-header-title {
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

.assistant-drawer-header-action {
  width: 28px;
  min-width: 28px;
  height: 28px;
  padding: 0;
  color: var(--td-text-color-secondary);
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

.assistant-welcome-composer {
  flex-shrink: 0;
  padding-top: 16px;
}
</style>
