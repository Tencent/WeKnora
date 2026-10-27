<template>
  <SettingDrawer :visible="visible" :title="automation?.name || ''" icon="time" width="720px" :min-width="480"
    :max-width="960" storage-key="setting-drawer:width:automation-detail" class="automation-detail-drawer" hide-footer
    :z-index="2300" @update:visible="(v: boolean) => !v && close()">
    <template #headerIcon><t-icon name="time" class="automation-detail__header-icon" /></template>
    <template v-if="automation" #subtitle>
      <span class="automation-detail__subtitle">
        <t-tag v-if="hasEndedOnceSchedule(automation)" size="small" variant="light" :title="t('automation.scheduleEndedHint')">{{ t('automation.scheduleEnded') }}</t-tag>
        <AutomationStatusTag v-else-if="!automation.enabled" status="paused" />
        <span>{{ describeTrigger(automation.trigger, t, locale) }} · {{ automation.trigger.timezone || 'UTC' }}</span>
      </span>
    </template>

    <template v-if="automation" #header-actions>
      <div class="automation-detail__actions">
        <t-popconfirm :content="t('automation.runConfirm', { name: automation.name })" theme="default" placement="bottom-right"
          :confirm-btn="{ content: t('automation.actions.runNow'), theme: 'primary' }" :cancel-btn="t('common.cancel')"
          :popup-props="{ zIndex: 2600, disabled: !!busy }" @confirm="runNow">
          <button type="button" class="automation-detail__icon-btn" :disabled="!!busy" :aria-busy="busy === 'run'"
            :title="t('automation.actions.runNow')" :aria-label="t('automation.actions.runNow')">
            <t-loading v-if="busy === 'run'" size="14px" />
            <ChatActionIcon v-else name="play" />
          </button>
        </t-popconfirm>
        <button v-if="!hasEndedOnceSchedule(automation)" type="button" class="automation-detail__icon-btn" :disabled="!!busy" :aria-busy="busy === 'toggle'"
          :title="automation.enabled ? t('automation.actions.pause') : t('automation.actions.resume')"
          :aria-label="automation.enabled ? t('automation.actions.pause') : t('automation.actions.resume')" @click="toggle">
          <t-loading v-if="busy === 'toggle'" size="14px" />
          <ChatActionIcon v-else :name="automation.enabled ? 'pause' : 'resume'" />
        </button>
        <button type="button" class="automation-detail__icon-btn" :title="t('common.edit')"
          :aria-label="t('common.edit')" @click="emit('edit', automation)">
          <ChatActionIcon name="edit" />
        </button>
      </div>
    </template>

    <template v-if="automation">
      <t-alert v-if="!automation.enabled && automation.paused_reason" theme="warning" class="automation-detail__alert"
        :message="t('automation.detail.pausedBecause', { reason: automation.paused_reason })" />
      <section class="automation-detail__brief" :aria-label="t('automation.detail.task')">
        <div class="automation-detail__brief-heading">
          <div class="automation-detail__brief-context">
            <h3>{{ t('automation.detail.task') }}</h3>
            <div class="automation-detail__execution">
              <span :title="t('automation.fields.accessLevel')">{{ t(`assistant.access.levels.${automation.access_level}.label`) }}</span>
              <span :title="t('automation.fields.sessionMode')">{{ t(`automation.sessionMode.${automation.session_mode}`) }}</span>
            </div>
          </div>
          <t-popup v-model:visible="grantsVisible" trigger="click" placement="bottom-right" :z-index="2600">
            <button type="button" class="automation-detail__grants-trigger" :class="{ 'is-attention': candidates.length }"
              :aria-expanded="grantsVisible">
              <t-icon name="secured" size="14px" />
              {{ t('automation.detail.grants') }}
              <span v-if="candidates.length || grants.length" class="automation-detail__count"
                :title="candidates.length ? t('automation.grants.candidates') : t('automation.grants.active')">
                {{ candidates.length || grants.length }}
              </span>
              <t-icon name="chevron-down" size="12px" />
            </button>
            <template #content>
              <div class="automation-detail__grants-panel" role="region" :aria-label="t('automation.detail.grants')">
                <h3>{{ t('automation.detail.grants') }}</h3>
                <p class="automation-detail__muted">{{ t('automation.grants.intro') }}</p>
                <div v-if="grantsLoading" class="automation-detail__loading"><t-loading size="small" /></div>
                <template v-else>
                  <ul v-if="grants.length" class="automation-detail__list">
                    <li v-for="grant in grants" :key="grant.id" class="automation-grant">
                      <span class="automation-grant__label">{{ grant.label || grant.grant_key }}</span>
                      <t-tag size="small" variant="light" :theme="grant.risk === 'destructive' ? 'danger' : 'warning'">
                        {{ t(`automation.risk.${grant.risk}`, grant.risk) }}
                      </t-tag>
                      <span v-if="grant.lapse" class="automation-grant__lapse">{{ t('automation.grants.lapsed') }}</span>
                      <t-link theme="danger" hover="color" class="automation-grant__action" @click="revoke(grant)">
                        {{ t('automation.grants.revoke') }}
                      </t-link>
                    </li>
                  </ul>
                  <template v-if="candidates.length">
                    <div class="automation-detail__subhead">{{ t('automation.grants.candidates') }}</div>
                    <ul class="automation-detail__list">
                      <li v-for="candidate in candidates" :key="candidate.prompt_id" class="automation-grant">
                        <span class="automation-grant__label">{{ candidate.label || candidate.tool }}</span>
                        <t-tag size="small" variant="light" :theme="candidate.risk === 'destructive' ? 'danger' : 'warning'">
                          {{ t(`automation.risk.${candidate.risk}`, candidate.risk) }}
                        </t-tag>
                        <span v-if="candidate.service_name" class="automation-grant__meta">{{ candidate.service_name }}</span>
                        <t-link theme="primary" hover="color" class="automation-grant__action" @click="allow(candidate)">
                          {{ t('automation.grants.allow') }}
                        </t-link>
                      </li>
                    </ul>
                  </template>
                  <p v-if="!grants.length && !candidates.length" class="automation-detail__muted">{{ t('automation.grants.none') }}</p>
                </template>
              </div>
            </template>
          </t-popup>
        </div>
        <div
          ref="instructionScroller"
          class="automation-detail__instruction-scroll"
          :class="{
            'automation-detail__instruction-scroll--more': instructionHasMore,
            'automation-detail__instruction-scroll--expanded': instructionExpanded,
          }"
          :style="{ maxHeight: instructionExpanded ? 'none' : `${INSTRUCTION_PREVIEW_HEIGHT}px` }"
          @scroll="updateInstructionOverflow"
        >
          <p ref="instructionContent" class="automation-detail__instruction">{{ automation.instruction }}</p>
        </div>
        <button
          v-if="instructionCanExpand"
          type="button"
          class="automation-detail__instruction-toggle"
          :aria-label="t(instructionExpanded ? 'common.collapse' : 'common.expand')"
          :title="t(instructionExpanded ? 'common.collapse' : 'common.expand')"
          :aria-expanded="instructionExpanded"
          @click="toggleInstruction"
        >
          <t-icon :name="instructionExpanded ? 'chevron-up' : 'chevron-down'" size="16px" />
        </button>
      </section>

      <section class="automation-detail__history" :aria-label="t('automation.detail.runs')">
        <div class="automation-detail__history-header">
          <h3 class="automation-detail__history-heading">
            {{ t('automation.detail.runs') }}<span class="automation-detail__count">{{ runsTotal }}</span>
          </h3>
          <span v-if="automation.enabled && automation.next_run_at" class="automation-detail__next-run">
            {{ t('automation.fields.nextRun') }}
            <time :datetime="automation.next_run_at">{{ when(automation.next_run_at) }}</time>
          </span>
        </div>
        <div v-if="runsLoading && !runs.length" class="automation-detail__loading"><t-loading size="small" /></div>
        <p v-else-if="!runs.length" class="automation-detail__muted">{{ t('automation.detail.noRuns') }}</p>
        <div v-else class="automation-detail__timeline">
          <section v-for="group in runGroups" :key="group.date" class="automation-detail__day">
            <h5 class="automation-detail__date">{{ group.date }}</h5>
            <ul class="automation-detail__runs">
              <li v-for="run in group.runs" :key="run.id" class="automation-run" :class="{ 'is-focused': run.id === focusRunId }">
                <span class="automation-run__marker"><AutomationStatusTag :status="run.status" icon-only /></span>
                <div class="automation-run__body">
                  <div class="automation-run__head">
                    <time class="automation-run__time" :datetime="run.scheduled_at">{{ runTime(run.scheduled_at) }}</time>
                    <span class="automation-run__meta">
                      {{ t(`automation.runTrigger.${run.trigger_kind}`) }}<template v-if="duration(run)"> · {{ duration(run) }}</template>
                    </span>
                    <t-link v-if="cancellable(run)" theme="danger" hover="color" class="automation-run__stop"
                      :disabled="run.cancel_requested" @click="cancel(run)">
                      {{ run.cancel_requested ? t('automation.detail.stopping') : t('automation.actions.stop') }}
                    </t-link>
                  </div>
                  <component :is="run.session_id ? 'button' : 'div'" class="automation-run__result"
                    :type="run.session_id ? 'button' : undefined"
                    :aria-label="run.session_id ? `${when(run.scheduled_at)} · ${t('automation.actions.openConversation')}` : undefined"
                    @click="run.session_id && openRun(run)">
                    <p v-if="run.error" class="automation-run__text is-error">{{ run.error }}</p>
                    <p v-else-if="run.answer" class="automation-run__text">{{ run.answer }}</p>
                    <span v-else class="automation-detail__muted">{{ t(`automation.status.${run.status}`) }}</span>
                    <t-icon v-if="run.session_id" name="arrow-right-up" size="16px" class="automation-run__chevron" />
                  </component>
                  <AssistantActionsButton v-if="run.status === 'succeeded' && run.answer" :label="t('assistant.actions.analyzeRun')"
                    :actions="runAssistantActions(run)"
                    :selection="{ field: t('automation.detail.runs'), text: run.answer.slice(0, 3000) }" />
                </div>
              </li>
            </ul>
          </section>
        </div>
        <div v-if="runs.length < runsTotal" class="automation-detail__more">
          <t-button size="small" variant="text" :loading="runsLoading" @click="loadRuns(false)">
            {{ t('common.loadMore') }}
          </t-button>
        </div>
      </section>
    </template>

  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import {
  addAutomationGrant,
  cancelAutomationRun,
  listAutomationGrantCandidates,
  listAutomationGrants,
  listAutomationRuns,
  pauseAutomation,
  resumeAutomation,
  revokeAutomationGrant,
  runAutomationNow,
  type Automation,
  type AutomationGrant,
  type AutomationGrantCandidate,
  type AutomationRun,
} from '@/api/automation'
import { describeTrigger, formatInZone, hasEndedOnceSchedule } from '@/utils/automationSchedule'
import { useAssistantDataChange } from '@/utils/assistantDataChange'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import AutomationStatusTag from './AutomationStatusTag.vue'
import ChatActionIcon from '@/components/ChatActionIcon.vue'
import AssistantActionsButton from '@/components/assistant/AssistantActionsButton.vue'

const props = defineProps<{
  visible: boolean
  automation: Automation | null
  focusRunId?: string
}>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  changed: [value: Automation]
  edit: [value: Automation]
}>()

const { t, locale } = useI18n()
function runAssistantActions(run: AutomationRun) {
  const previous = runs.value.filter(item => item.id !== run.id && item.status === 'succeeded'
    && item.scheduled_at <= run.scheduled_at).slice(0, 2)
  const source = JSON.stringify({
    automation_id: props.automation?.id,
    run_id: run.id,
    session_id: run.session_id,
    message_id: run.assistant_message_id,
  })
  return [
    { key: 'summary', label: t('assistant.actions.summarizeRun'), query: t('assistant.prompts.summarizeRun', { source }) },
    { key: 'compare', label: t('assistant.actions.compareRuns'), disabled: previous.length === 0,
      query: t('assistant.prompts.compareRuns', { source, ids: JSON.stringify(previous.map(item => item.id)) }) },
  ]
}
const router = useRouter()

const PAGE = 20
const busy = ref<'' | 'run' | 'toggle'>('')
const grantsVisible = ref(false)
const runs = ref<AutomationRun[]>([])
const runsTotal = ref(0)
const runsPage = ref(0)
const runsLoading = ref(false)
const grants = ref<AutomationGrant[]>([])
const candidates = ref<AutomationGrantCandidate[]>([])
const grantsLoading = ref(false)
const instructionScroller = ref<HTMLElement | null>(null)
const instructionContent = ref<HTMLElement | null>(null)
const instructionHasMore = ref(false)
const instructionCanExpand = ref(false)
const instructionExpanded = ref(false)
const INSTRUCTION_PREVIEW_HEIGHT = 160
let poll: ReturnType<typeof setInterval> | undefined
let detailVersion = 0
let refreshRunsAfterLoad = false

function updateInstructionOverflow() {
  const el = instructionScroller.value
  instructionCanExpand.value = !!el && el.scrollHeight > INSTRUCTION_PREVIEW_HEIGHT + 1
  instructionHasMore.value = !instructionExpanded.value && !!el && el.scrollHeight - el.clientHeight - el.scrollTop > 1
}

function toggleInstruction() {
  instructionExpanded.value = !instructionExpanded.value
  if (instructionScroller.value) instructionScroller.value.scrollTop = 0
}

watch(instructionExpanded, updateInstructionOverflow, { flush: 'post' })

watch(
  [instructionScroller, instructionContent, () => props.visible, () => props.automation?.instruction],
  (_, __, onCleanup) => {
    updateInstructionOverflow()
    if (!props.visible || !instructionScroller.value || !instructionContent.value) return
    const observer = new ResizeObserver(updateInstructionOverflow)
    observer.observe(instructionScroller.value)
    observer.observe(instructionContent.value)
    onCleanup(() => observer.disconnect())
  },
  { flush: 'post' },
)

const when = (iso: string | null) => formatInZone(iso, props.automation?.trigger.timezone, locale.value)
const runDateFormatter = computed(() => new Intl.DateTimeFormat(locale.value, {
  timeZone: props.automation?.trigger.timezone || 'UTC', year: 'numeric', month: 'long', day: 'numeric',
}))
const runTimeFormatter = computed(() => new Intl.DateTimeFormat(locale.value, {
  timeZone: props.automation?.trigger.timezone || 'UTC', hour: '2-digit', minute: '2-digit', hour12: false,
}))
const runTime = (iso: string) => runTimeFormatter.value.format(new Date(iso))
const runGroups = computed(() => {
  const groups = new Map<string, AutomationRun[]>()
  for (const run of runs.value) {
    const date = runDateFormatter.value.format(new Date(run.scheduled_at))
    const group = groups.get(date) ?? []
    group.push(run)
    groups.set(date, group)
  }
  return [...groups].map(([date, runs]) => ({ date, runs }))
})
const cancellable = (run: AutomationRun) =>
  run.status === 'queued' || run.status === 'running' || run.status === 'awaiting_approval'

function duration(run: AutomationRun): string {
  if (!run.started_at || !run.finished_at) return ''
  const seconds = Math.round((Date.parse(run.finished_at) - Date.parse(run.started_at)) / 1000)
  if (!Number.isFinite(seconds) || seconds < 0) return ''
  if (seconds < 60) return t('automation.detail.durationSeconds', { n: Math.max(1, seconds) })
  return t('automation.detail.durationMinutes', { n: Math.round(seconds / 60) })
}

function close() {
  emit('update:visible', false)
}

async function loadRuns(reset: boolean) {
  const a = props.automation
  if (!a) return
  const version = detailVersion
  runsLoading.value = true
  try {
    const page = reset ? 1 : runsPage.value + 1
    const res = await listAutomationRuns(a.id, page, PAGE)
    if (version !== detailVersion) return
    const items = res.data ?? []
    runs.value = reset ? items : [...runs.value, ...items]
    runsTotal.value = res.total ?? runs.value.length
    runsPage.value = page
  } catch (e: any) {
    if (version === detailVersion) MessagePlugin.error(e?.message || t('common.operationFailed'))
  } finally {
    if (version === detailVersion) {
      runsLoading.value = false
      if (refreshRunsAfterLoad) {
        refreshRunsAfterLoad = false
        void refreshActive(true)
      }
    }
  }
}

// Refresh the first page in place while a run is in flight, so its status
// moves without the user reopening the drawer.
async function refreshActive(force = false) {
  const a = props.automation
  if (!props.visible || !a) return
  if (runsLoading.value) {
    if (force) refreshRunsAfterLoad = true
    return
  }
  if (!force && !runs.value.some(cancellable)) return
  const version = detailVersion
  try {
    const res = await listAutomationRuns(a.id, 1, PAGE)
    if (version !== detailVersion) return
    const fresh = new Map((res.data ?? []).map((run) => [run.id, run]))
    const known = new Set(runs.value.map((run) => run.id))
    runs.value = [
      ...(res.data ?? []).filter((run) => !known.has(run.id)),
      ...runs.value.map((run) => fresh.get(run.id) ?? run),
    ]
    runsTotal.value = res.total ?? runsTotal.value
  } catch {
    // The next tick tries again.
  }
}

useAssistantDataChange(changes => {
  if (changes.some(change => change.operation === 'automation.run_now')) void refreshActive(true)
}, { areas: ['automation'] })

async function loadGrants() {
  const a = props.automation
  if (!a) return
  grantsLoading.value = true
  try {
    const [g, c] = await Promise.all([listAutomationGrants(a.id), listAutomationGrantCandidates(a.id)])
    grants.value = g.data ?? []
    candidates.value = c.data ?? []
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.operationFailed'))
  } finally {
    grantsLoading.value = false
  }
}

watch([() => props.visible, () => props.automation?.id], ([open, id]) => {
  ++detailVersion
  refreshRunsAfterLoad = false
  instructionExpanded.value = false
  clearInterval(poll)
  if (!open || !id) return
  grantsVisible.value = false
  runs.value = []
  runsTotal.value = 0
  grants.value = []
  candidates.value = []
  void loadRuns(true)
  void loadGrants()
  poll = setInterval(refreshActive, 5000)
}, { immediate: true })

onBeforeUnmount(() => {
  ++detailVersion
  clearInterval(poll)
})

async function runNow() {
  if (busy.value) return
  const a = props.automation
  if (!a) return
  busy.value = 'run'
  try {
    const res = await runAutomationNow(a.id)
    MessagePlugin.success(t('automation.messages.queued'))
    if (res.data) {
      grantsVisible.value = false
      runs.value = [res.data, ...runs.value]
      runsTotal.value += 1
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.operationFailed'))
  } finally {
    busy.value = ''
  }
}

async function toggle() {
  const a = props.automation
  if (!a || busy.value) return
  if (hasEndedOnceSchedule(a)) {
    MessagePlugin.warning(t('automation.scheduleEndedHint'))
    return
  }
  busy.value = 'toggle'
  try {
    const res = a.enabled ? await pauseAutomation(a.id) : await resumeAutomation(a.id)
    emit('changed', res.data)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.operationFailed'))
  } finally {
    busy.value = ''
  }
}

function openRun(run: AutomationRun) {
  close()
  void router.push(`/platform/chat/${encodeURIComponent(run.session_id)}`)
}

async function cancel(run: AutomationRun) {
  try {
    const res = await cancelAutomationRun(run.id)
    runs.value = runs.value.map((r) => (r.id === run.id ? res.data : r))
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.operationFailed'))
  }
}

function allow(candidate: AutomationGrantCandidate) {
  const a = props.automation
  if (!a) return
  grantsVisible.value = false
  const destructive = candidate.risk === 'destructive'
  const dialog = DialogPlugin.confirm({
    header: t('automation.grants.allowTitle'),
    body: t(destructive ? 'automation.grants.allowDestructiveBody' : 'automation.grants.allowBody', {
      label: candidate.label || candidate.tool,
    }),
    theme: destructive ? 'danger' : 'warning',
    confirmBtn: { content: t('automation.grants.allow'), theme: destructive ? 'danger' : 'primary' },
    onConfirm: async () => {
      dialog.hide()
      try {
        await addAutomationGrant(a.id, candidate.prompt_id)
        await loadGrants()
      } catch (e: any) {
        MessagePlugin.error(e?.message || t('common.operationFailed'))
      }
    },
  })
}

async function revoke(grant: AutomationGrant) {
  try {
    await revokeAutomationGrant(grant.id)
    await loadGrants()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.operationFailed'))
  }
}
</script>

<style scoped lang="less">
.automation-detail__actions {
  display: flex;
  align-items: center;
  gap: 2px;
}

.automation-detail__icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: color-mix(in srgb, var(--td-text-color-primary) 55%, transparent);
  cursor: pointer;

  &:hover:not(:disabled) {
    color: var(--td-text-color-secondary);
    background: color-mix(in srgb, var(--td-text-color-primary) 5%, transparent);
  }
  &:active:not(:disabled) {
    background: color-mix(in srgb, var(--td-text-color-primary) 9%, transparent);
  }
  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 2px;
  }
  &:disabled {
    cursor: not-allowed;
    opacity: 0.4;
  }
}

.automation-detail__subtitle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.automation-detail__alert {
  margin-bottom: 12px;
}

.automation-detail__header-icon {
  color: var(--td-text-color-secondary);
}

:global(.automation-detail-drawer .setting-drawer__header-icon) {
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
}

.automation-detail__next-run {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  line-height: 1.6;

  time { font-variant-numeric: tabular-nums; }
}

.automation-detail__brief {
  padding: 14px 16px 16px;
  border-radius: var(--app-radius-lg);
  background: color-mix(in srgb, var(--td-bg-color-secondarycontainer) 40%, var(--td-bg-color-container));
}

.automation-detail__brief-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 14px;

  h3 { margin: 0; font-size: var(--app-text-sm); font-weight: 600; color: var(--td-text-color-primary); }
}

.automation-detail__brief-context { min-width: 0; }

.automation-detail__grants-trigger {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 6px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-xs);
  cursor: pointer;

  &:hover, &[aria-expanded="true"] { color: var(--td-text-color-primary); background: var(--td-bg-color-container-hover); }
  &:focus-visible { outline: 1px solid var(--td-brand-color-focus); outline-offset: 2px; }
  &.is-attention { color: var(--td-warning-color); }
}

.automation-detail__count {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
  font-weight: 400;
  font-variant-numeric: tabular-nums;
}

.is-attention .automation-detail__count { color: inherit; }

.automation-detail__grants-panel {
  display: flex;
  flex-direction: column;
  gap: 14px;
  box-sizing: border-box;
  width: min(380px, calc(100vw - 48px));
  max-height: min(480px, 65vh);
  overflow-y: auto;
  padding: 12px;

  h3 { margin: 0; font-size: var(--app-text-md); font-weight: 600; color: var(--td-text-color-primary); }
}

.automation-detail__execution {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 0;
  margin-top: 4px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  line-height: 1.5;

  > span + span::before { content: '·'; margin: 0 8px; color: var(--td-text-color-placeholder); }
}

.automation-detail__history { margin-top: 24px; }

.automation-detail__history-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 16px;
  margin-bottom: 18px;
}

.automation-detail__history-heading {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-weight: 600;
}

.automation-detail__instruction-scroll {
  box-sizing: border-box;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;

  &--expanded {
    overflow: visible;
    overscroll-behavior: auto;
  }
}

.automation-detail__instruction-scroll--more {
  mask-image: linear-gradient(to bottom, #000 calc(100% - 28px), transparent);
}

.automation-detail__instruction-toggle {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 24px;
  margin: 6px auto -6px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container);
  }

  &:focus-visible {
    outline: 2px solid var(--td-text-color-secondary);
    outline-offset: 2px;
  }
}

.automation-detail__instruction {
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.automation-detail__muted {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  line-height: 1.5;
}

.automation-detail__loading,
.automation-detail__more {
  display: flex;
  justify-content: center;
}

.automation-detail__subhead {
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
}

.automation-detail__list {
  margin: 0;
  padding: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  list-style: none;

  > li + li {
    border-top: 1px solid var(--td-component-stroke);
  }
}

.automation-detail__day + .automation-detail__day { margin-top: 20px; }

.automation-detail__date {
  margin: 0 0 12px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-weight: 500;
}

.automation-detail__runs {
  margin: 0;
  padding: 0;
  list-style: none;
}

.automation-run {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding-bottom: 20px;

  &::before {
    content: '';
    position: absolute;
    left: 9px;
    top: 24px;
    bottom: 0;
    width: 1px;
    background: var(--td-component-stroke);
  }
  &:last-child { padding-bottom: 0; }
  &:last-child::before { display: none; }
  &.is-focused .automation-run__result {
    background: color-mix(in srgb, var(--td-brand-color) 5%, var(--td-bg-color-container));
  }
}

.automation-run__marker {
  display: flex;
  justify-content: center;
  min-width: 20px;
  padding-top: 2px;
}

.automation-run__result {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  width: 100%;
  box-sizing: border-box;
  margin-top: 8px;
  padding: 12px 14px;
  border: 0;
  border-radius: var(--app-radius-lg);
  background: color-mix(in srgb, var(--td-bg-color-secondarycontainer) 40%, var(--td-bg-color-container));
  text-align: left;
  font: inherit;
  transition: background-color var(--app-motion-fast) ease;
}

button.automation-run__result {
  cursor: pointer;
  &:hover { background: var(--td-bg-color-container-hover); }
  &:focus-visible {
    outline: 1px solid var(--td-brand-color-focus);
    outline-offset: 2px;
  }
}

.automation-run__body {
  flex: 1;
  min-width: 0;
}

.automation-run__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.automation-run__time {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-variant-numeric: tabular-nums;
}

.automation-run__meta {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  white-space: nowrap;
}

.automation-run__stop {
  margin-left: auto;
  font-size: var(--app-text-sm);
}

.automation-run__chevron {
  flex-shrink: 0;
  margin-left: auto;
  margin-top: 3px;
  color: var(--td-text-color-placeholder);
}

.automation-run__text {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  flex: 1;
  min-width: 0;
  margin: 0;
  overflow: hidden;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;

  &.is-error {
    color: var(--td-error-color);
  }
}

.automation-grant {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 10px 14px;
}

.automation-grant__label {
  min-width: 0;
  flex-basis: 100%;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-sm);
  overflow-wrap: anywhere;
}

.automation-grant__meta,
.automation-grant__lapse {
  font-size: var(--app-text-sm);
  white-space: nowrap;
}

.automation-grant__meta {
  color: var(--td-text-color-placeholder);
}

.automation-grant__lapse {
  color: var(--td-warning-color);
}

.automation-grant__action {
  flex-shrink: 0;
  margin-left: auto;
  font-size: var(--app-text-sm);
}
</style>
