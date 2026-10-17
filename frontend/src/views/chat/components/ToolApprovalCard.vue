<template>
  <div v-if="interactive" class="prompt-panel" @click.stop>
    <div class="prompt-question">{{ question }}</div>
    <div v-if="delegatedTask" class="prompt-task">{{ $t('agentStream.toolApproval.delegatedTask') }} · {{ delegatedTask }}</div>
    <div v-if="descriptionParts.head" class="prompt-desc">{{ compactDescription }}</div>
    <details v-if="hasLongDescription" class="prompt-tool-description">
      <summary>{{ $t('agentStream.toolApproval.toolDescription') }}</summary>
      <div class="prompt-desc">{{ descriptionParts.head }}</div>
    </details>
    <div v-if="delegatedTask && argumentSummary" class="prompt-argument-summary">{{ argumentSummary }}</div>
    <dl v-if="descriptionParts.fields.length" class="prompt-fields">
      <div v-for="(field, i) in descriptionParts.fields" :key="i" class="prompt-field">
        <dt>{{ approvalFieldLabel(field.label) }}</dt>
        <dd>
          {{ approvalFieldValue(field.label, field.value) }}<span v-if="field.count" class="prompt-field-count">{{
            $t('agentStream.toolApproval.characterCount', { count: field.count }) }}</span>
        </dd>
      </div>
    </dl>
    <ul v-if="descriptionParts.items.length" class="prompt-desc-items">
      <li v-for="(item, i) in descriptionParts.items" :key="i">{{ item }}</li>
    </ul>

    <div v-if="hasArgs" class="prompt-args">
      <button type="button" class="prompt-args-toggle" :aria-expanded="argsExpanded" @click="toggleArgs">
        <span>{{ $t('agentStream.toolApproval.argsLabel') }}</span>
        <t-icon class="args-toggle-icon" :name="argsExpanded ? 'chevron-down' : 'chevron-right'" />
        <span v-if="!isJsonValid" class="args-status args-invalid">
          <t-icon name="error-circle" />
          {{ $t('agentStream.toolApproval.invalidJson') }}
        </span>
        <span v-else-if="argsDirty" class="args-status args-dirty">
          {{ $t('agentStream.toolApproval.argsModified') }}
        </span>
      </button>
      <t-textarea v-if="argsExpanded" v-model="argsText" class="prompt-args-input"
        :autosize="{ minRows: 2, maxRows: 10 }" placeholder="{}" />
    </div>

    <t-checkbox v-if="grantScope" v-model="allowInSession" class="prompt-grant">
      {{ $t('agentStream.toolApproval.allowInSession') }}
      <span class="prompt-grant-scope">{{ grantScope }}</span>
    </t-checkbox>

    <div class="prompt-footer">
      <div class="prompt-footer-meta">
        <span v-if="parked" class="action-timer">{{ parkedText }}</span>
        <span v-else-if="secondsLeft >= 0" class="action-timer" :class="timerClass">{{ countdownText }}</span>
      </div>
      <div class="prompt-footer-actions">
        <button type="button" class="prompt-btn" :disabled="submitting" @click="submit('reject')">
          {{ $t('agentStream.toolApproval.reject') }}
        </button>
        <button type="button" class="prompt-btn is-primary" :disabled="submitting || !isJsonValid"
          @click="submit('approve')">
          {{ $t('agentStream.toolApproval.approve') }}<span class="prompt-btn-key" aria-hidden="true">↵</span>
        </button>
      </div>
    </div>
  </div>

  <div v-else class="action-card interaction-inline approval-inline" :class="{ 'action-pending': open }">
    <div class="action-header no-results">
      <div class="action-title">
        <t-icon class="action-title-icon" :name="headerIcon" />
        <component :is="hasArgs ? 'button' : 'span'" class="approval-heading"
          :type="hasArgs ? 'button' : undefined" :aria-expanded="hasArgs ? argsExpanded : undefined"
          @click.stop="hasArgs && toggleArgs()">
          <span v-if="delegatedTask" class="approval-task">{{ $t('agentStream.toolApproval.delegatedTask') }} · {{ delegatedTask }}</span>
          <span class="action-name">{{ inlineTitle }}</span>
          <span class="approval-status" role="status">
            {{ awaitingInDock ? $t('agentStream.promptDock.waiting') : statusLine }}
          </span>
          <t-icon v-if="hasArgs" class="args-toggle-icon" :name="argsExpanded ? 'chevron-down' : 'chevron-right'" />
        </component>
        <button v-if="awaitingInDock" type="button" class="inline-action is-primary" @click.stop="dock?.focus(pendingId)">
          {{ $t('agentStream.promptDock.handle') }}
        </button>
      </div>
    </div>

    <div v-if="hasArgs && (open || argsExpanded)" class="approval-args">
      <button v-if="open" type="button" class="approval-args-toggle" :aria-expanded="argsExpanded" @click.stop="toggleArgs">
        <span>{{ $t('agentStream.toolApproval.argsLabel') }}</span>
        <span v-if="!argsExpanded && argumentSummary" class="approval-args-summary">{{ argumentSummary }}</span>
        <t-icon class="args-toggle-icon" :name="argsExpanded ? 'chevron-down' : 'chevron-right'" />
      </button>
      <div v-else class="approval-args-label">{{ $t('agentStream.toolApproval.argsLabel') }}</div>
      <pre v-if="argsExpanded" class="approval-args-content">{{ argsText }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, inject, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { resolveToolApproval } from '@/api/mcp-service'
import { resolveEmbedToolApproval } from '@/api/embed'
import { resumeSuspendedRunKey } from '@/utils/suspendedRun'
import { usePromptCountdown } from '@/composables/usePromptCountdown'
import { markPromptSubmitted, promptSubmission, usePromptDock } from '@/composables/usePromptDock'
import { isSubmitKey } from '@/utils/promptKeys'

const props = defineProps<{
  pendingId: string
  delegatedTask?: string
  // dock: the approval form, shown above the composer. inline: the timeline row.
  variant?: 'dock' | 'inline'
  serviceName: string
  mcpToolName: string
  description?: string
  argsJson?: string
  // grantScope, when set, offers to approve calls like this one for the
  // rest of the session and says which calls that covers.
  grantScope?: string
  timeoutSeconds?: number
  requestedAt?: number
  resolved?: boolean
  approved?: boolean
  canceled?: boolean
  resolveReason?: string
  // suspendable: past hotWaitSeconds the run is suspended rather than failed,
  // and the prompt stays answerable until expiresAt.
  suspendable?: boolean
  hotWaitSeconds?: number
  suspended?: boolean
  expiresAt?: string
  abandoned?: boolean
  embeddedMode?: boolean
  embedChannelId?: string
  embedToken?: string
  embedSessionId?: string
  embedSessionSig?: string
  embedVisitorId?: string
}>()

const useEmbedApproval = () =>
  props.embeddedMode
  && props.embedChannelId
  && props.embedToken
  && props.embedSessionId
  && props.embedSessionSig
  && props.embedVisitorId

const { t } = useI18n()
const resumeSuspendedRun = inject(resumeSuspendedRunKey, undefined)
const dock = usePromptDock()

function formatJson(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

const initialArgs = formatJson(props.argsJson || '{}')
const argsText = ref(initialArgs)
const argsExpanded = ref(false)
const allowInSession = ref(false)
const submitting = ref(false)
const { parked, secondsLeft, timerClass, countdownText, parkedText } = usePromptCountdown(props)

const submission = computed(() => promptSubmission(props.pendingId))
const open = computed(() => !props.resolved && !submission.value)
// A handled approval returns to a compact timeline row, including local submits.
watch([open, () => props.pendingId], () => { argsExpanded.value = false })
// Without a dock (a view that does not provide one) the row stays answerable.
const interactive = computed(() => open.value && (props.variant === 'dock' || !dock))
const awaitingInDock = computed(() => open.value && props.variant !== 'dock' && Boolean(dock?.isDocked(props.pendingId)))

const hasArgs = computed(() => argsText.value.trim().length > 0)

// The service name the backend gives its own operations (run_operation).
const isOperation = computed(() => props.serviceName === 'WeKnora')

// A batch operation ends its description with one "- " line per item it acts
// on; those render as a list so every item stays visible. "Label: value"
// lines render as a definition list. A broken item list stays one block.
interface ApprovalField { label: string, value: string, count?: string }

const descriptionParts = computed(() => {
  const raw = (props.description || '').trim()
  const prefix = props.delegatedTask ? `${props.delegatedTask}: ` : ''
  const text = prefix && raw.startsWith(prefix) ? raw.slice(prefix.length) : raw
  const lines = text ? text.split('\n') : []
  const start = lines.findIndex((line) => line.startsWith('- '))
  const brokenList = start >= 0 && !lines.slice(start).every((line) => line.startsWith('- '))
  if (!text || brokenList) {
    return { head: text, fields: [], items: [], action: '', target: '' }
  }
  const body = start < 0 ? lines : lines.slice(0, start)
  const items = start < 0 ? [] : lines.slice(start).map((line) => line.slice(2))
  const fields = [] as ApprovalField[]
  const headLines = [] as string[]
  for (const line of body) {
    const match = /^([^:\n]{1,48}): (.+)$/.exec(line)
    if (!match) {
      headLines.push(line)
      continue
    }
    const counted = /^(.+) \((\d+) characters\)$/.exec(match[1])
    fields.push(counted
      ? { label: counted[1], value: match[2], count: counted[2] }
      : { label: match[1], value: match[2] })
  }
  // A WeKnora operation's description opens with its summary, then the label
  // of what it acts on; they make the question instead of the raw operation id.
  let action = ''
  let target = ''
  if (isOperation.value && headLines.length) {
    action = String(headLines.shift()).trim()
    if (headLines.length && !/[.。!！?？]$/.test(headLines[0].trim())) target = String(headLines.shift()).trim()
  }
  return { head: headLines.join('\n').trim(), fields, items, action, target }
})

// Long MCP schemas are reference material. Keep the actual task and arguments
// visible instead of letting parameter documentation occupy the approval dock.
const compactDescription = computed(() => {
  const description = descriptionParts.value.head
  if (!props.delegatedTask || isOperation.value) return description
  return description.split(/\n\s*\n/)[0].slice(0, 180)
})
const hasLongDescription = computed(() => compactDescription.value !== descriptionParts.value.head)
const argumentSummary = computed(() => {
  try {
    const args = JSON.parse(argsText.value)
    if (!args || Array.isArray(args) || typeof args !== 'object') return ''
    return Object.entries(args).map(([key, value]) => `${key}=${JSON.stringify(value)}`).join(' · ').slice(0, 240)
  } catch { return '' }
})

const question = computed(() => {
  const { action, target } = descriptionParts.value
  if (!action) return t('agentStream.toolApproval.dockQuestion', { target: targetLabel.value })
  return target
    ? t('agentStream.toolApproval.operationQuestion', { action, target })
    : t('agentStream.toolApproval.operationQuestionNoTarget', { action })
})

const isJsonValid = computed(() => {
  if (!argsText.value.trim()) return true
  try {
    JSON.parse(argsText.value)
    return true
  } catch {
    return false
  }
})

const approvalDetailKey: Record<string, string> = {
  Type: 'type',
  'Embedding model': 'embeddingModel',
  'Summary model': 'summaryModel',
  'Caption model': 'captionModel',
  'Speech model': 'speechModel',
  Storage: 'storage',
  Indexes: 'indexes',
  Name: 'name',
  Title: 'title',
  Status: 'status',
  Summary: 'summary',
  Model: 'model',
  Mode: 'mode',
  URL: 'url',
  From: 'from',
  To: 'to',
  Folder: 'folder',
  Page: 'page',
  Version: 'version',
  Documents: 'documents',
  'Knowledge bases': 'knowledgeBases',
  'MCP services': 'mcpServices',
  Skills: 'skills',
  'Web search': 'webSearch',
  'Multi-turn conversation': 'multiTurn',
  Transport: 'transport',
  Authentication: 'authentication',
  'Usage instructions': 'usageInstructions',
  'Display name': 'displayName',
  Enabled: 'enabled',
  Sandbox: 'sandbox',
  Sandboxes: 'sandboxes',
  'System prompt': 'systemPrompt',
  Content: 'content',
  'Knowledge base scope': 'kbScope',
  'MCP scope': 'mcpScope',
  'Skills scope': 'skillsScope',
}

const approvalValueKey: Record<string, string> = {
  document: 'document',
  faq: 'faq',
  wiki: 'wiki',
  on: 'on',
  off: 'off',
  vector: 'vector',
  keyword: 'keyword',
  graph: 'graph',
  'quick-answer': 'quickAnswer',
  'smart-reasoning': 'smartReasoning',
}

function approvalFieldLabel(label: string) {
  const key = approvalDetailKey[label]
  return key ? t(`agentStream.toolApproval.detail.${key}`) : label
}

function approvalToken(token: string) {
  const key = approvalValueKey[token]
  return key ? t(`agentStream.toolApproval.value.${key}`) : token
}

function approvalFieldValue(label: string, value: string) {
  if (label === 'Indexes') {
    const parts = value.split(', ').map((part) => part.split(' ').map(approvalToken).join(' '))
    return parts.join(t('agentStream.toolApproval.value.indexSep'))
  }
  if (label === 'Type' || label === 'Mode' || value === 'on' || value === 'off') {
    return approvalToken(value)
  }
  return value
}

const argsDirty = computed(() => argsText.value.trim() !== initialArgs.trim())

const headerIcon = computed(() => {
  if (!props.resolved) return 'secured'
  if (props.abandoned || props.canceled) return 'minus-circle'
  if (props.approved) return 'check-circle'
  return 'close-circle'
})

const targetLabel = computed(() => (
  t('agentStream.toolApproval.targetWithTool', {
    service: props.serviceName,
    tool: props.mcpToolName,
  })
))

const inlineTitle = computed(() => {
  const { action, target } = descriptionParts.value
  return action
    ? (target ? t('agentStream.toolApproval.operationTarget', { action, target }) : action)
    : targetLabel.value
})

const statusLine = computed(() => {
  if (!props.resolved) {
    return submission.value ? t('agentStream.toolApproval.submitted') : t('agentStream.toolApproval.waitingStatus')
  }
  if (props.canceled) return t('agentStream.toolApproval.canceledTag')
  if (props.abandoned) return t('agentStream.toolApproval.abandonedTag')
  if (props.approved) return t('agentStream.toolApproval.approvedTag')
  return t('agentStream.toolApproval.rejectedTag')
})

function toggleArgs() {
  argsExpanded.value = !argsExpanded.value
}

const allowForSession = (decision: 'approve' | 'reject') => (
  decision === 'approve' && Boolean(props.grantScope) && allowInSession.value ? true : undefined
)

const submit = async (decision: 'approve' | 'reject') => {
  if (!open.value || submitting.value) return
  submitting.value = true
  try {
    let modified: Record<string, unknown> | undefined
    if (decision === 'approve') {
      try {
        modified = JSON.parse(argsText.value || '{}') as Record<string, unknown>
      } catch {
        argsExpanded.value = true
        MessagePlugin.error(t('agentStream.toolApproval.invalidJson'))
        return
      }
    }
    const { resumed } = await (useEmbedApproval()
      ? resolveEmbedToolApproval(
        props.embedChannelId!,
        props.embedToken!,
        props.embedSessionId!,
        props.embedSessionSig!,
        props.embedVisitorId!,
        props.pendingId,
        {
          decision,
          modified_args: decision === 'approve' ? modified : undefined,
          reason: decision === 'reject' ? t('agentStream.toolApproval.userRejected') : undefined,
          allow_in_session: allowForSession(decision),
        },
      )
      : resolveToolApproval(props.pendingId, {
        decision,
        modified_args: decision === 'approve' ? modified : undefined,
        reason: decision === 'reject' ? t('agentStream.toolApproval.userRejected') : undefined,
        allow_in_session: allowForSession(decision),
      }))
    markPromptSubmitted(props.pendingId, { approved: decision === 'approve' })
    MessagePlugin.success(t('agentStream.toolApproval.submitted'))
    if (resumed) resumeSuspendedRun?.(props.pendingId)
  } catch (e: any) {
    const msg = e?.response?.data?.error?.message || e?.message || t('agentStream.toolApproval.submitFailed')
    MessagePlugin.error(msg)
  } finally {
    submitting.value = false
  }
}

/** Enter approves while docked; in the args editor it needs Cmd/Ctrl. */
const handleKey = (e: KeyboardEvent): boolean => {
  if (!interactive.value || !isSubmitKey(e)) return false
  const inArgs = (e.target as HTMLElement | null)?.tagName === 'TEXTAREA'
  if (inArgs && !e.metaKey && !e.ctrlKey) return false
  if (!isJsonValid.value) return false
  void submit('approve')
  return true
}

defineExpose({ handleKey })
</script>

<style scoped lang="less">
@import './agent-interaction-card.less';

.approval-inline {
  min-width: 0;
  .action-title { column-gap: 8px; row-gap: 4px; }
  .action-name { font-size: var(--agent-step-summary-size, 13px); white-space: normal; overflow-wrap: anywhere; flex-shrink: 1; }
}

.approval-status {
  padding: 1px 6px;
  border-radius: var(--app-radius-sm, 4px);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs, 12px);
  line-height: 1.5;
}

.approval-heading {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 8px;
  min-width: 0;
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  .approval-status, .args-toggle-icon { flex-shrink: 0; }
  .args-toggle-icon { color: var(--td-text-color-placeholder); }
}
button.approval-heading {
  cursor: pointer;
  &:hover { color: var(--td-text-color-primary); }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 3px; border-radius: var(--app-radius-xs); }
}
.approval-task {
  color: var(--td-text-color-placeholder);
  font-size: var(--agent-step-summary-size, 13px);
  overflow-wrap: anywhere;
}
.approval-args-label {
  padding: 10px 12px 6px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs, 12px);
}

.approval-args {
  margin-top: 10px;
  border-radius: var(--app-radius-md, 8px);
  background: var(--td-bg-color-secondarycontainer);
  overflow: hidden;
}

.approval-args-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 10px 12px;
  border: 0;
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-xs, 12px);
  line-height: 1.5;
  text-align: left;
  cursor: pointer;
  > span:first-child, .args-toggle-icon { flex-shrink: 0; }
  .args-toggle-icon { margin-left: auto; }
  &:hover { color: var(--td-text-color-primary); }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: -2px; }
}

.approval-args-summary {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.approval-args-content {
  margin: 0;
  padding: 0 12px 12px;
  max-height: 280px;
  overflow: auto;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-xs, 12px);
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.prompt-task { margin: 6px 0; color: var(--td-text-color-secondary); font-size: var(--app-text-sm); }
.prompt-argument-summary { margin: 8px 0; overflow-wrap: anywhere; font-family: monospace; font-size: var(--app-text-sm); }
.prompt-tool-description { margin-top: 8px; color: var(--td-text-color-secondary); font-size: var(--app-text-sm);
  summary { cursor: pointer; }
  .prompt-desc { max-height: 180px; overflow-y: auto; white-space: pre-wrap; }
}

</style>
