<template>
  <section class="skill-timeline" :class="{ 'skill-timeline--compact': compact }" :aria-busy="loading">
    <div class="skill-timeline__content">
      <t-loading v-if="loading && messages.length === 0" size="small" />
      <p v-else-if="messages.length === 0" class="skill-timeline__empty">
        {{ live
          ? $t('settings.sandbox.skillTranscriptWaiting')
          : $t('settings.sandbox.skillTranscriptEmpty') }}
      </p>
      <template v-else>
        <div v-for="(msg, index) in messages" :key="msg.id || index" class="skill-timeline__turn"
          :class="{ 'skill-timeline__turn--command': index === messages.length - 1 && live && commandOutput && !commandOutput.done }">
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
      <SandboxCommandProgress
        v-if="live && commandOutput && !commandOutput.done"
        :progress="commandOutput"
        class="skill-timeline__command"
      />
      <div v-for="item in guidance.messages" :key="item.id" class="skill-timeline__guidance-message">
        <span>{{ $t(`settings.sandbox.skillGuidance.${item.status}`) }}</span>
        <p>{{ item.content }}</p>
      </div>
    </div>
    <div v-if="live || canRetry" class="skill-timeline__guidance">
      <t-textarea
        v-model="guidanceText"
        :placeholder="$t('settings.sandbox.skillGuidance.placeholder')"
        :maxlength="10000"
        :autosize="{ minRows: 2, maxRows: 6 }"
        :disabled="sendingGuidance"
      />
      <p v-if="guidanceError" role="alert" class="skill-timeline__guidance-error">{{ guidanceError }}</p>
      <div class="skill-timeline__guidance-actions">
        <span v-if="live && !guidance.accepting">{{ $t('settings.sandbox.skillGuidance.unavailable') }}</span>
        <t-button
          size="small"
          :loading="sendingGuidance"
          :disabled="!guidanceText.trim() || (live && !guidance.accepting)"
          @click="sendGuidance"
        >{{ $t(live ? 'settings.sandbox.skillGuidance.send' : 'settings.sandbox.skillGuidance.retry') }}</t-button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onUnmounted, reactive, ref, watch } from 'vue'
import { fetchEventSource } from '@microsoft/fetch-event-source'
import { useChatStreamHandler } from '@/composables/useChatStreamHandler'
import { getMessageList } from '@/api/chat'
import { configSkillTranscriptUrl, getConfigSkillGuidance, steerConfigSkill, reinstallConfigSkill, type SkillInstallGuidanceState } from '@/api/system'
import { getApiBaseUrl } from '@/utils/api-base'
import { generateRandomString } from '@/utils/index'
import { makeSteerClientId } from '@/utils/steerId'
import SandboxCommandProgress from './SandboxCommandProgress.vue'
import AgentStreamDisplay from '@/views/chat/components/AgentStreamDisplay.vue'
import i18n from '@/i18n'

const props = defineProps<{
  configId: string
  skillId: string
  // The durable rows behind the run, used when the event log has aged out.
  sessionId: string
  messageId: string
  // True while this skill is still installing. Locators are written after the
  // sandbox is up; until then this component shows the waiting copy and does
  // not hit /transcript.
  live?: boolean
  compact?: boolean
  canRetry?: boolean
}>()

const emit = defineEmits<{ restarted: [] }>()
const guidance = ref<SkillInstallGuidanceState>({ accepting: false, messages: [] })
const guidanceText = ref('')
const guidanceError = ref('')
const sendingGuidance = ref(false)
let guidanceEpoch = 0
let guidanceTimer: ReturnType<typeof setTimeout> | undefined
// Keep the same ID after an uncertain response so Retry cannot enqueue twice.
let pendingSend: { messageId: string; content: string; id: string } | undefined

async function refreshGuidance(epoch: number) {
  try {
    const res = await getConfigSkillGuidance(props.configId, props.skillId)
    if (epoch !== guidanceEpoch) return
    guidance.value = res.data
  } catch {
    if (epoch === guidanceEpoch) guidance.value.accepting = false
  }
  if (epoch === guidanceEpoch && props.live) {
    guidanceTimer = setTimeout(() => void refreshGuidance(epoch), 1500)
  }
}

async function sendGuidance() {
  const content = guidanceText.value.trim()
  if (!content || sendingGuidance.value || (props.live ? !guidance.value.accepting : !props.canRetry)) return
  const epoch = guidanceEpoch
  const target = { configId: props.configId, skillId: props.skillId, messageId: props.messageId }
  sendingGuidance.value = true
  guidanceError.value = ''
  try {
    if (props.live) {
      if (!pendingSend || pendingSend.messageId !== target.messageId || pendingSend.content !== content) {
        pendingSend = { messageId: target.messageId, content, id: makeSteerClientId() }
      }
      await steerConfigSkill(target.configId, target.skillId, {
        expected_message_id: target.messageId, steer_id: pendingSend.id, content,
      })
      if (epoch !== guidanceEpoch) return
      // A failed refresh after a successful POST must not turn Retry into a duplicate send.
      if (!guidance.value.messages.some(item => item.id === pendingSend!.id)) {
        guidance.value.messages.push({ id: pendingSend.id, content, status: 'pending' })
      }
      pendingSend = undefined
    } else {
      await reinstallConfigSkill(target.configId, target.skillId, content)
      if (epoch !== guidanceEpoch) return
      emit('restarted')
    }
    guidanceText.value = ''
  } catch (err: any) {
    if (epoch === guidanceEpoch) {
      guidanceError.value = err?.response?.data?.error?.message || err?.message || i18n.global.t('settings.sandbox.skillGuidance.failed')
    }
  } finally {
    sendingGuidance.value = false
  }
}

watch(
  () => [props.configId, props.skillId, props.messageId, props.live] as const,
  () => {
    const epoch = ++guidanceEpoch
    clearTimeout(guidanceTimer)
    guidance.value = { accepting: false, messages: [] }
    if (props.configId && props.skillId) void refreshGuidance(epoch)
  },
  { immediate: true },
)
onUnmounted(() => { guidanceEpoch++; clearTimeout(guidanceTimer) })

const messages = reactive<any[]>([])
const commandOutput = ref<{ command: string; started_at: string; output: string; done: boolean } | null>(null)
const loading = ref(false)
const isReplying = ref(false)
const currentAssistantMessageId = ref('')
const fullContent = ref('')

let controller: AbortController | null = null
let closed = false
// stop() / a newer open() bump this so an in-flight live loop cannot keep
// calling follow() after closed is reset — that would replay a finished run.
let openRun = 0

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

async function loadPersisted(run: number) {
  const res: any = await getMessageList({
    session_id: props.sessionId,
    limit: 100,
    created_at: '',
  })
  if (run !== openRun) return
  handleMsgList(res?.data || [])
}

function stop() {
  openRun += 1
  closed = true
  if (controller) {
    controller.abort()
    controller = null
  }
}

// follow tails the transcript endpoint. It resolves when the stream ends, and
// reports whether it ever produced anything: a 404 means the event log has
// expired and the durable history is the only remaining source.
async function follow(run: number): Promise<boolean> {
  const url = `${getApiBaseUrl()}${configSkillTranscriptUrl(props.configId, props.skillId)}`
  const token = localStorage.getItem('weknora_token')
  const tenantId = localStorage.getItem('weknora_selected_tenant_id')
  const ac = new AbortController()
  controller = ac
  if (run !== openRun) {
    ac.abort()
    return false
  }
  let served = false

  await fetchEventSource(url, {
    method: 'GET',
    headers: {
      Authorization: token ? `Bearer ${token}` : '',
      'Accept-Language': i18n.global.locale?.value || localStorage.getItem('locale') || 'zh-CN',
      'X-Request-ID': generateRandomString(12),
      ...(tenantId ? { 'X-Tenant-ID': tenantId } : {}),
    },
    signal: ac.signal,
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
      // Live tail and post-refresh replay share this stream. The old guard
      // dropped every frame when the skill was already ready, which is why
      // reopening the popup after a refresh showed an empty transcript.
      if (run !== openRun) return
      if (!ev.data) return
      let frame: any
      try {
        frame = JSON.parse(ev.data)
      } catch {
        return
      }
      if (frame.response_type === 'install_output') {
        commandOutput.value = frame.data || null
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
  const run = ++openRun
  closed = false
  messages.splice(0, messages.length)
  commandOutput.value = null
  const stale = () => run !== openRun || closed
  try {
    // A finished install already has durable rows. Replaying the event log
    // through processStreamChunk would animate every tool call again, which
    // is what "view the run" must not do. If the durable history is empty
    // (which happens for maintenance sessions the chat message endpoint
    // filters out), fall back to a one-shot transcript replay so the popup
    // shows something on a refresh instead of the empty state.
    if (!props.live) {
      loading.value = true
      if (props.sessionId) {
        await loadPersisted(run)
        if (!stale() && messages.length === 0 && props.messageId) {
          await follow(run).catch(() => false)
        }
      }
      return
    }

    // Locators land after the installer sandbox is up. Hitting /transcript
    // before that 404s every second (WARNING in the access log) and leaves
    // the spinner up for the entire file seed, which can take minutes.
    // The parent already polls the skill list; this watch re-opens when
    // sessionId arrives.
    if (!props.sessionId || !props.messageId) {
      loading.value = false
      return
    }

    loading.value = true
    for (;;) {
      if (stale() || !props.live) return
      const served = await follow(run).catch(() => false)
      if (stale() || !props.live || served) return
      if (props.sessionId && props.messageId) {
        await loadPersisted(run)
        if (stale() || messages.length > 0) return
      }
      if (stale() || !props.live) return
      await wait(1000)
      if (stale() || !props.live) return
    }
  } catch {
    // Both sources are gone; the empty state says so.
  } finally {
    if (!stale()) loading.value = false
  }
}

watch(
  () => [props.configId, props.skillId, props.sessionId, props.messageId, props.live] as const,
  () => {
    stop()
    if (props.configId && props.skillId) void open()
  },
  { immediate: true },
)

onUnmounted(stop)
</script>

<style scoped lang="less">
.skill-timeline__content {
  flex: 1;
  min-width: 0;
}

.skill-timeline__guidance {
  position: sticky;
  bottom: 0;
  z-index: 1;
  flex-shrink: 0;
  margin-top: 12px;
  padding-top: 12px;
  background: var(--td-bg-color-container, #fff);
  border-top: 1px solid var(--td-component-stroke, #e7e7e7);

  // Cover the timeline's padding too, so scrolling text cannot peek around
  // the sticky composer. Its z-index keeps this backdrop above the transcript.
  &::before {
    content: '';
    position: absolute;
    inset: 0 calc(-1 * var(--skill-guidance-gutter, 12px)) calc(-1 * var(--skill-guidance-bottom-gap, 12px));
    z-index: -1;
    background: inherit;
    pointer-events: none;
  }
}
.skill-timeline__guidance-message {
  margin: 8px 0;
  padding: 8px;
  background: var(--td-bg-color-container);
  border-radius: 6px;
  span { font-size: 12px; color: var(--td-text-color-secondary); }
  p { margin: 4px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; }
}
.skill-timeline__guidance-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 8px;
  span { flex: 1; font-size: 12px; color: var(--td-text-color-secondary); }
}
.skill-timeline__guidance-error { color: var(--td-error-color); font-size: 12px; }

.skill-timeline {
  display: flex;
  flex-direction: column;
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
.skill-timeline__turn--command {
  // AgentStreamDisplay also renders an empty inline image-viewer trigger.
  // Stack its roots so that trigger cannot create a blank text line above the command.
  display: flex;
  flex-direction: column;
}
.skill-timeline__command { margin: 4px 0 8px 42px; }
</style>
