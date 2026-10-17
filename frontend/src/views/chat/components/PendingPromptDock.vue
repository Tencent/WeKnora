<template>
  <transition name="prompt-dock">
    <section v-if="dock && prompts.length" ref="root" class="prompt-dock" :class="{ 'is-collapsed': collapsed }"
      tabindex="-1" :aria-label="title" @keydown="onKeydown">
      <header class="prompt-dock__header" @click="collapsed && expand()">
        <t-icon class="prompt-dock__icon" :name="icon" />
        <span class="prompt-dock__title">{{ title }}</span>
        <div class="prompt-dock__tools" @click.stop>
          <div v-if="prompts.length > 1" class="prompt-dock__pager">
            <button type="button" class="prompt-dock__btn" :disabled="index === 0"
              :aria-label="$t('agentStream.promptDock.previous')" @click="step(-1)">
              <t-icon name="chevron-left" />
            </button>
            <span class="prompt-dock__count">
              {{ $t('agentStream.promptDock.pager', { current: index + 1, total: prompts.length }) }}
            </span>
            <button type="button" class="prompt-dock__btn" :disabled="index === prompts.length - 1"
              :aria-label="$t('agentStream.promptDock.next')" @click="step(1)">
              <t-icon name="chevron-right" />
            </button>
          </div>
          <button type="button" class="prompt-dock__btn"
            :aria-label="collapsed ? $t('agentStream.promptDock.expand') : $t('agentStream.promptDock.collapse')"
            @click="collapsed ? expand() : (dock.collapsed.value = true)">
            <t-icon :name="collapsed ? 'chevron-up' : 'remove'" />
          </button>
        </div>
      </header>

      <div v-show="!collapsed" class="prompt-dock__body">
        <div v-for="prompt in prompts" v-show="prompt.pending_id === activeId" :key="prompt.pending_id">
          <ToolApprovalCard v-if="prompt.type === 'tool_approval_required'" :ref="(el) => setCard(prompt, el)"
            variant="dock" v-bind="{ ...timing(prompt), ...authProps }" :service-name="prompt.service_name || ''"
            :mcp-tool-name="prompt.mcp_tool_name || ''" :description="prompt.description"
            :args-json="prompt.args_json" :grant-scope="prompt.grant_scope" />
          <AskUserCard v-else-if="prompt.type === 'agent_prompt_required'" :ref="(el) => setCard(prompt, el)"
            variant="dock" v-bind="{ ...timing(prompt), ...authProps }" :request="prompt.request" />
          <McpOAuthCard v-else :ref="(el) => setCard(prompt, el)" variant="dock" v-bind="authProps"
            :pending-id="prompt.pending_id" :service-id="prompt.service_id || ''"
            :service-name="prompt.service_name || ''" :mcp-tool-name="prompt.mcp_tool_name || ''"
            :timeout-seconds="prompt.timeout_seconds" :requested-at="prompt.requested_at" />
        </div>
      </div>
    </section>
  </transition>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePromptDock } from '@/composables/usePromptDock'
import { isTextEntry } from '@/utils/promptKeys'
import AskUserCard from './AskUserCard.vue'
import McpOAuthCard from './McpOAuthCard.vue'
import ToolApprovalCard from './ToolApprovalCard.vue'

const props = defineProps<{
  embeddedMode?: boolean
  embedChannelId?: string
  embedToken?: string
  embedSessionId?: string
  embedSessionSig?: string
  embedVisitorId?: string
}>()

type Prompt = Record<string, any>
type DockCard = { handleKey?: (e: KeyboardEvent) => boolean }

const { t } = useI18n()
const dock = usePromptDock()
const root = ref<HTMLElement | null>(null)
const cards = new Map<string, DockCard>()

const prompts = computed<Prompt[]>(() => dock?.prompts.value || [])
const collapsed = computed(() => Boolean(dock?.collapsed.value))
const activeId = computed(() => {
  const chosen = dock?.activeId.value
  return prompts.value.some((p) => p.pending_id === chosen) ? chosen : prompts.value[0]?.pending_id || ''
})
const index = computed(() => Math.max(0, prompts.value.findIndex((p) => p.pending_id === activeId.value)))
const active = computed(() => prompts.value[index.value])

const authProps = computed(() => ({
  embeddedMode: props.embeddedMode,
  embedChannelId: props.embedChannelId,
  embedToken: props.embedToken,
  embedSessionId: props.embedSessionId,
  embedSessionSig: props.embedSessionSig,
  embedVisitorId: props.embedVisitorId,
}))

const timing = (p: Prompt) => ({
  pendingId: p.pending_id,
  timeoutSeconds: p.timeout_seconds,
  hotWaitSeconds: p.hot_wait_seconds,
  suspendable: p.suspendable,
  suspended: p.suspended,
  expiresAt: p.expires_at,
  requestedAt: p.requested_at,
})

const title = computed(() => {
  switch (active.value?.type) {
    case 'tool_approval_required': return t('agentStream.promptDock.approvalTitle')
    case 'mcp_oauth_required': return t('agentStream.promptDock.oauthTitle')
    default: return t('agentStream.askUser.title')
  }
})

const icon = computed(() => {
  switch (active.value?.type) {
    case 'tool_approval_required': return 'secured'
    case 'mcp_oauth_required': return 'lock-on'
    default: return 'help-circle'
  }
})

const setCard = (prompt: Prompt, el: unknown) => {
  if (el) cards.set(prompt.pending_id, el as DockCard)
  else cards.delete(prompt.pending_id)
}

const step = (delta: number) => {
  const next = prompts.value[index.value + delta]
  if (next && dock) dock.activeId.value = next.pending_id
}

const takeFocus = async () => {
  await nextTick()
  root.value?.focus({ preventScroll: true })
}

const expand = () => {
  if (!dock) return
  dock.collapsed.value = false
  void takeFocus()
}

const onKeydown = (e: KeyboardEvent) => {
  if (!dock) return
  if (e.key === 'Escape') {
    dock.collapsed.value = true
    e.preventDefault()
    return
  }
  if (collapsed.value) return
  if (e.altKey && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
    step(e.key === 'ArrowLeft' ? -1 : 1)
    e.preventDefault()
    return
  }
  if (cards.get(activeId.value)?.handleKey?.(e)) e.preventDefault()
}

watch(() => dock?.focusRequest.value, () => { void takeFocus() })

// A new prompt opens the dock; it takes focus unless the user is typing.
watch(() => prompts.value.map((p) => p.pending_id), (ids, previous = []) => {
  const arrived = ids.filter((id) => !previous.includes(id))
  if (!dock || !arrived.length) return
  if (!ids.includes(dock.activeId.value)) dock.activeId.value = arrived[0]
  dock.collapsed.value = false
  if (!isTextEntry(document.activeElement)) void takeFocus()
})
</script>

<style scoped lang="less">
.prompt-dock {
  width: 100%;
  max-width: 960px;
  margin: 0 auto 8px;
  box-sizing: border-box;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xl);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.04), 0 8px 16px -4px rgba(0, 0, 0, 0.06);
  outline: none;
  transition: border-color var(--app-motion-fast) ease;

  &:focus-within {
    border-color: color-mix(in srgb, var(--td-brand-color) 45%, var(--td-component-stroke));
  }

  &.is-collapsed .prompt-dock__header {
    padding-bottom: 6px;
    cursor: pointer;
  }
}

.prompt-dock__header {
  display: flex;
  align-items: center;
  gap: 6px;
  min-height: 34px;
  padding: 6px 8px 0 14px;
  box-sizing: border-box;
  user-select: none;
}

.prompt-dock__icon {
  flex-shrink: 0;
  font-size: var(--app-text-base, 14px);
  color: var(--td-text-color-placeholder);
}

.prompt-dock__title {
  flex: 1;
  min-width: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.prompt-dock__tools,
.prompt-dock__pager {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.prompt-dock__pager {
  margin-right: 4px;
}

.prompt-dock__count {
  min-width: 36px;
  font-size: var(--app-text-sm);
  font-variant-numeric: tabular-nums;
  text-align: center;
  color: var(--td-text-color-secondary);
}

.prompt-dock__btn {
  appearance: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  font-size: var(--app-text-base, 14px);
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: background var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &:disabled {
    opacity: 0.35;
    cursor: default;
  }
}

.prompt-dock__body {
  max-height: min(50vh, 440px);
  overflow-y: auto;
  padding: 6px 14px 12px;
  overscroll-behavior: contain;
}

.prompt-dock-enter-active,
.prompt-dock-leave-active {
  transition: opacity var(--app-motion-base) ease, transform var(--app-motion-base) ease;
}

.prompt-dock-enter-from,
.prompt-dock-leave-to {
  opacity: 0;
  transform: translateY(6px);
}

@media (prefers-reduced-motion: reduce) {
  .prompt-dock-enter-active,
  .prompt-dock-leave-active {
    transition: none;
  }
}
</style>
