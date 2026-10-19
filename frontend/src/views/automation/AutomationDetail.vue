<template>
  <SettingDrawer :visible="visible" :title="automation?.name || ''" icon="time" width="640px" :min-width="480"
    :max-width="960" storage-key="setting-drawer:width:automation-detail" class="automation-detail-drawer"
    @update:visible="(v: boolean) => !v && close()">
    <template v-if="automation" #subtitle>
      <span class="automation-detail__subtitle">
        <AutomationStatusTag v-if="!automation.enabled" status="paused" />
        <span>{{ describeTrigger(automation.trigger, t, locale) }} · {{ automation.trigger.timezone || 'UTC' }}</span>
      </span>
    </template>

    <template v-if="automation">
      <t-alert v-if="!automation.enabled && automation.paused_reason" theme="warning" class="automation-detail__alert"
        :message="t('automation.detail.pausedBecause', { reason: automation.paused_reason })" />

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('automation.detail.overview') }}</h4>
        <dl class="automation-detail__fields">
          <div class="automation-detail__field">
            <dt>{{ t('automation.fields.nextRun') }}</dt>
            <dd>{{ automation.enabled && automation.next_run_at ? when(automation.next_run_at) : t('automation.notScheduled') }}</dd>
          </div>
          <div class="automation-detail__field">
            <dt>{{ t('automation.detail.lastRun') }}</dt>
            <dd class="automation-detail__inline">
              <template v-if="automation.last_run_at">
                {{ when(automation.last_run_at) }}
                <AutomationStatusTag v-if="automation.last_run_status" :status="automation.last_run_status" />
              </template>
              <template v-else>{{ t('automation.detail.noRuns') }}</template>
            </dd>
          </div>
          <div class="automation-detail__field">
            <dt>{{ t('automation.fields.agent') }}</dt>
            <dd>{{ agentName }}</dd>
          </div>
          <div class="automation-detail__field">
            <dt>{{ t('automation.fields.accessLevel') }}</dt>
            <dd>{{ t(`assistant.access.levels.${automation.access_level}.label`) }}</dd>
          </div>
          <div class="automation-detail__field">
            <dt>{{ t('automation.fields.sessionMode') }}</dt>
            <dd>{{ t(`automation.sessionMode.${automation.session_mode}`) }}</dd>
          </div>
        </dl>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">
          {{ t('automation.detail.task') }}
          <t-button v-if="instructionLong" size="small" variant="text" theme="primary" class="automation-detail__toggle"
            @click="instructionOpen = !instructionOpen">
            {{ instructionOpen ? t('common.collapse') : t('common.expand') }}
          </t-button>
        </h4>
        <p class="automation-detail__instruction" :class="{ 'is-clamped': instructionLong && !instructionOpen }">
          {{ automation.instruction }}
        </p>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">
          {{ t('automation.detail.runs') }}
          <span v-if="runsTotal" class="automation-detail__count">{{ runsTotal }}</span>
        </h4>
        <div v-if="runsLoading && !runs.length" class="automation-detail__loading"><t-loading size="small" /></div>
        <p v-else-if="!runs.length" class="automation-detail__muted">{{ t('automation.detail.noRuns') }}</p>
        <ul v-else class="automation-detail__list">
          <li v-for="run in runs" :key="run.id" class="automation-run"
            :class="{ 'is-focused': run.id === focusRunId, 'is-link': !!run.session_id }"
            :role="run.session_id ? 'button' : undefined" :tabindex="run.session_id ? 0 : undefined"
            @click="run.session_id && openRun(run)"
            @keydown.enter="run.session_id && openRun(run)">
            <span class="automation-run__dot" :class="`is-${run.status}`" aria-hidden="true" />
            <div class="automation-run__body">
              <div class="automation-run__head">
                <span class="automation-run__time">{{ when(run.scheduled_at) }}</span>
                <span class="automation-run__status">{{ t(`automation.status.${run.status}`) }}</span>
                <span class="automation-run__meta">
                  {{ t(`automation.runTrigger.${run.trigger_kind}`) }}<template v-if="duration(run)"> · {{ duration(run) }}</template>
                </span>
                <t-link v-if="cancellable(run)" theme="danger" hover="color" class="automation-run__stop"
                  :disabled="run.cancel_requested" @click.stop="cancel(run)">
                  {{ run.cancel_requested ? t('automation.detail.stopping') : t('automation.actions.stop') }}
                </t-link>
              </div>
              <p v-if="run.error" class="automation-run__text is-error">{{ run.error }}</p>
              <p v-else-if="run.answer" class="automation-run__text">{{ run.answer }}</p>
            </div>
            <t-icon v-if="run.session_id" name="chevron-right" size="16px" class="automation-run__chevron" />
          </li>
        </ul>
        <div v-if="runs.length < runsTotal" class="automation-detail__more">
          <t-button size="small" variant="text" :loading="runsLoading" @click="loadRuns(false)">
            {{ t('common.loadMore') }}
          </t-button>
        </div>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('automation.detail.grants') }}</h4>
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
      </section>
    </template>

    <template v-if="automation" #footer-left>
      <t-button variant="outline" :loading="busy === 'toggle'" @click="toggle">
        {{ automation.enabled ? t('automation.actions.pause') : t('automation.actions.resume') }}
      </t-button>
      <t-button variant="outline" @click="emit('edit', automation)">{{ t('common.edit') }}</t-button>
    </template>
    <template v-if="automation" #footer-right>
      <t-button theme="primary" :loading="busy === 'run'" @click="runNow">{{ t('automation.actions.runNow') }}</t-button>
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
import { describeTrigger, formatInZone } from '@/utils/automationSchedule'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import AutomationStatusTag from './AutomationStatusTag.vue'

const props = defineProps<{
  visible: boolean
  automation: Automation | null
  agentName: string
  focusRunId?: string
}>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  changed: [value: Automation]
  edit: [value: Automation]
}>()

const { t, locale } = useI18n()
const router = useRouter()

const PAGE = 20
const busy = ref<'' | 'run' | 'toggle'>('')
const instructionOpen = ref(false)
const runs = ref<AutomationRun[]>([])
const runsTotal = ref(0)
const runsPage = ref(0)
const runsLoading = ref(false)
const grants = ref<AutomationGrant[]>([])
const candidates = ref<AutomationGrantCandidate[]>([])
const grantsLoading = ref(false)
let poll: ReturnType<typeof setInterval> | undefined

const instructionLong = computed(() => {
  const text = props.automation?.instruction ?? ''
  return text.length > 160 || text.split('\n').length > 4
})

const when = (iso: string | null) => formatInZone(iso, props.automation?.trigger.timezone, locale.value)
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
  runsLoading.value = true
  try {
    const page = reset ? 1 : runsPage.value + 1
    const res = await listAutomationRuns(a.id, page, PAGE)
    const items = res.data ?? []
    runs.value = reset ? items : [...runs.value, ...items]
    runsTotal.value = res.total ?? runs.value.length
    runsPage.value = page
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.operationFailed'))
  } finally {
    runsLoading.value = false
  }
}

// Refresh the first page in place while a run is in flight, so its status
// moves without the user reopening the drawer.
async function refreshActive() {
  const a = props.automation
  if (!a || runsLoading.value || !runs.value.some(cancellable)) return
  try {
    const res = await listAutomationRuns(a.id, 1, PAGE)
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

watch(() => [props.visible, props.automation?.id] as const, ([open, id]) => {
  clearInterval(poll)
  if (!open || !id) return
  instructionOpen.value = false
  runs.value = []
  runsTotal.value = 0
  grants.value = []
  candidates.value = []
  void loadRuns(true)
  void loadGrants()
  poll = setInterval(refreshActive, 5000)
}, { immediate: true })

onBeforeUnmount(() => clearInterval(poll))

async function runNow() {
  const a = props.automation
  if (!a) return
  busy.value = 'run'
  try {
    const res = await runAutomationNow(a.id)
    MessagePlugin.success(t('automation.messages.queued'))
    if (res.data) {
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
  if (!a) return
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
.automation-detail__subtitle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.automation-detail__alert {
  margin-bottom: 12px;
}

.automation-detail__fields {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 0;
}

.automation-detail__field {
  display: grid;
  grid-template-columns: 96px minmax(0, 1fr);
  gap: 12px;
  align-items: baseline;

  dt {
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-sm);
    line-height: 1.45;
    white-space: nowrap;
  }

  dd {
    margin: 0;
    color: var(--td-text-color-primary);
    font-size: var(--app-text-md);
    line-height: 1.55;
    overflow-wrap: anywhere;
  }
}

.automation-detail__inline {
  display: flex;
  align-items: center;
  gap: 8px;
}

.automation-detail__toggle {
  margin-left: auto;
}

.automation-detail__count {
  color: var(--td-text-color-placeholder);
  font-weight: 400;
}

.automation-detail__instruction {
  margin: 0;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;

  &.is-clamped {
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 4;
    line-clamp: 4;
    overflow: hidden;
  }
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

.automation-run {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 14px;

  &.is-link {
    cursor: pointer;

    &:hover {
      background: var(--td-bg-color-container-hover);
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color-focus);
      outline-offset: -2px;
    }
  }

  &.is-focused {
    background: var(--td-bg-color-container-hover);
  }
}

.automation-run__dot {
  flex-shrink: 0;
  width: 6px;
  height: 6px;
  margin-top: 7px;
  border-radius: 50%;
  background: var(--td-text-color-placeholder);

  &.is-succeeded { background: var(--td-success-color); }
  &.is-failed { background: var(--td-error-color); }
  &.is-running { background: var(--td-brand-color); }
  &.is-awaiting_approval,
  &.is-needs_attention { background: var(--td-warning-color); }
}

.automation-run__body {
  flex: 1;
  min-width: 0;
}

.automation-run__head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.automation-run__time {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-variant-numeric: tabular-nums;
}

.automation-run__status {
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  white-space: nowrap;
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
  align-self: center;
  color: var(--td-text-color-placeholder);
}

.automation-run__text {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  margin: 6px 0 0;
  overflow: hidden;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 1.6;
  white-space: pre-wrap;
  overflow-wrap: anywhere;

  &.is-error {
    color: var(--td-error-color);
  }
}

.automation-grant {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 10px 14px;
}

.automation-grant__label {
  min-width: 0;
  overflow: hidden;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  text-overflow: ellipsis;
  white-space: nowrap;
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
