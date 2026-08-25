<template>
  <section class="skill-timeline" :class="{ 'skill-timeline--compact': compact }" :aria-busy="loading">
    <t-loading v-if="loading && messages.length === 0" size="small" />
    <p v-else-if="messages.length === 0" class="skill-timeline__empty">
      {{ live
        ? $t('settings.sandbox.skillTranscriptWaiting')
        : $t('settings.sandbox.skillTranscriptEmpty') }}
    </p>
    <template v-else>
      <div v-for="(msg, index) in messages" :key="msg.id || index" class="skill-timeline__turn">
        <pre v-if="msg.role === 'user'" class="skill-timeline__prompt">{{ msg.content }}</pre>
        <AgentStreamDisplay
          v-else
          :session="msg"
          :session-id="sessionId"
          :user-query="''"
          embedded-mode
        />
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { onUnmounted, reactive, ref, watch } from 'vue'
import { fetchEventSource } from '@microsoft/fetch-event-source'
import { useChatStreamHandler } from '@/composables/useChatStreamHandler'
import { getMessageList } from '@/api/chat'
import { configSkillTranscriptUrl } from '@/api/system'
import { getApiBaseUrl } from '@/utils/api-base'
import { generateRandomString } from '@/utils/index'
import AgentStreamDisplay from '@/views/chat/components/AgentStreamDisplay.vue'
import i18n from '@/i18n'

const props = defineProps<{
  configId: string
  skillId: string
  // The durable rows behind the run, used when the event log has aged out.
  sessionId: string
  messageId: string
  // True while this skill is still installing. The locators are written only
  // after the installer sandbox is up, so a 404 here means "not yet" rather
  // than "gone" and the stream is retried.
  live?: boolean
  compact?: boolean
}>()

const messages = reactive<any[]>([])
const loading = ref(false)
const isReplying = ref(false)
const currentAssistantMessageId = ref('')
const fullContent = ref('')

let controller: AbortController | null = null
let closed = false

// The timeline grows inside the drawer's own scroll container, so following the
// tail would mean scrolling the whole drawer under a reader who is inspecting
// an earlier command. The stream handler requires the hook, so it is a no-op.
function scrollToBottom() {}

const { handleMsgList, processStreamChunk } = useChatStreamHandler({
  messagesList: messages,
  loading,
  isReplying,
  currentAssistantMessageId,
  fullContent,
  isAgentStreamSession: () => true,
  scrollToBottom,
})

// install_prompt is the installer's opening line. It is not an assistant
// event, so it becomes the user turn here rather than going through the chat
// stream handler.
function applyPrompt(content: string) {
  if (!content) return
  if (messages.some((msg) => msg.role === 'user')) return
  messages.unshift({ id: `${props.messageId}-prompt`, role: 'user', content })
}

async function loadPersisted() {
  const res: any = await getMessageList({
    session_id: props.sessionId,
    limit: 100,
    created_at: '',
  })
  handleMsgList(res?.data || [])
}

function stop() {
  closed = true
  if (controller) {
    controller.abort()
    controller = null
  }
}

// follow tails the transcript endpoint. It resolves when the stream ends, and
// reports whether it ever produced anything: a 404 means the event log has
// expired and the durable history is the only remaining source.
async function follow(): Promise<boolean> {
  const url = `${getApiBaseUrl()}${configSkillTranscriptUrl(props.configId, props.skillId)}`
  const token = localStorage.getItem('weknora_token')
  const tenantId = localStorage.getItem('weknora_selected_tenant_id')
  controller = new AbortController()
  let served = false

  await fetchEventSource(url, {
    method: 'GET',
    headers: {
      Authorization: token ? `Bearer ${token}` : '',
      'Accept-Language': i18n.global.locale?.value || localStorage.getItem('locale') || 'zh-CN',
      'X-Request-ID': generateRandomString(12),
      ...(tenantId ? { 'X-Tenant-ID': tenantId } : {}),
    },
    signal: controller.signal,
    openWhenHidden: true,
    onopen: async (response) => {
      if (response.ok) {
        served = true
        return
      }
      // Anything else is terminal for this attempt; fetchEventSource must not
      // retry, so the error is thrown for the caller to fall back on.
      throw new Error(`transcript stream refused: ${response.status}`)
    },
    onmessage(ev) {
      if (!ev.data) return
      let frame: any
      try {
        frame = JSON.parse(ev.data)
      } catch {
        return
      }
      if (frame.response_type === 'install_prompt') {
        applyPrompt(frame.content || '')
        return
      }
      processStreamChunk(frame)
    },
    onerror(err) {
      // Rethrowing stops fetchEventSource's own retry loop.
      throw err
    },
  })

  return served
}

function wait(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms)
  })
}

async function open() {
  closed = false
  messages.splice(0, messages.length)
  loading.value = true
  try {
    // A finished install already has durable rows. Replaying the event log
    // through processStreamChunk would animate every tool call again, which
    // is what "view the run" must not do.
    if (!props.live) {
      if (props.sessionId) {
        await loadPersisted()
      }
      return
    }

    // Locators land after the installer sandbox is up. Keep asking until the
    // stream answers; falling through to the empty state on the first 404
    // would flash "no record" during setup.
    for (;;) {
      const served = await follow().catch(() => false)
      if (closed || served) return
      if (props.sessionId && props.messageId) {
        await loadPersisted()
        if (messages.length > 0 || closed) return
      }
      if (closed) return
      await wait(1000)
      if (closed) return
    }
  } catch {
    // Both sources are gone; the empty state says so.
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.configId, props.skillId, props.sessionId, props.live] as const,
  () => {
    stop()
    if (props.configId && props.skillId) void open()
  },
  { immediate: true },
)

onUnmounted(stop)
</script>

<style scoped lang="less">
.skill-timeline {
  padding: 12px;
  background: var(--td-bg-color-secondarycontainer, #f7f7f7);
  border: 1px solid var(--td-component-stroke, #e7e7e7);
  border-radius: 8px;
}

.skill-timeline__turn + .skill-timeline__turn {
  margin-top: 12px;
}

.skill-timeline__prompt {
  max-height: 140px;
  margin: 0;
  padding: 8px 10px;
  overflow-y: auto;
  color: var(--td-text-color-secondary, #666);
  font-size: 12px;
  line-height: 1.6;
  background: var(--td-bg-color-container, #fff);
  border-radius: 6px;
  white-space: pre-wrap;
  word-break: break-word;
}

.skill-timeline__empty {
  margin: 8px 0;
  color: var(--td-text-color-placeholder, #999);
  font-size: 13px;
}

.skill-timeline--compact {
  padding: 10px 12px 12px;
  background: transparent;
  border: 0;
  border-radius: 0;
}

.skill-timeline--compact .skill-timeline__prompt {
  max-height: 72px;
  padding: 6px 8px;
  font-size: 11px;
  line-height: 1.5;
}

.skill-timeline--compact .skill-timeline__empty {
  margin: 4px 0;
  font-size: 12px;
}

.skill-timeline--compact :deep(.agent-stream-display.is-embedded) {
  font-size: 12px;
  --agent-step-text-size: 12px;
  --agent-step-summary-size: 12px;
}

.skill-timeline--compact :deep(.agent-stream-display.is-embedded .tree-root .action-name) {
  font-size: 12px;
}

// Chat answer Markdown is 16px. That is the right size in a conversation and
// too loud in this 420px popup, where the prompt and step summary are 11–13px.
.skill-timeline--compact :deep(.agent-stream-display .answer-content.markdown-content) {
  font-size: 12px;
  line-height: 1.55;
}

.skill-timeline--compact :deep(.agent-stream-display .answer-content.markdown-content h1) {
  font-size: 13px;
  margin-bottom: 0.35em;
}

.skill-timeline--compact :deep(.agent-stream-display .answer-content.markdown-content h2),
.skill-timeline--compact :deep(.agent-stream-display .answer-content.markdown-content h3) {
  font-size: 12px;
}
</style>
