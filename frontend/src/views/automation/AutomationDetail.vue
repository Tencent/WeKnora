<template>
  <t-drawer :visible="visible" size="640px" :footer="false" destroy-on-close @close="close">
    <template #header>
      <div v-if="automation" class="automation-detail__header">
        <span class="automation-detail__title" :title="automation.name">{{ automation.name }}</span>
        <AutomationStatusTag :status="automation.enabled ? '' : 'paused'" />
      </div>
    </template>

    <div v-if="automation" class="automation-detail">
      <t-alert v-if="!automation.enabled && automation.paused_reason" theme="warning"
        :message="t('automation.detail.pausedBecause', { reason: automation.paused_reason })" />

      <dl class="automation-detail__facts">
        <div>
          <dt>{{ t('automation.fields.schedule') }}</dt>
          <dd>{{ describeTrigger(automation.trigger, t, locale) }} · {{ automation.trigger.timezone || 'UTC' }}</dd>
        </div>
        <div>
          <dt>{{ t('automation.fields.agent') }}</dt>
          <dd>{{ agentName }}</dd>
        </div>
        <div>
          <dt>{{ t('automation.fields.nextRun') }}</dt>
          <dd>{{ automation.enabled && automation.next_run_at ? when(automation.next_run_at) : t('automation.notScheduled') }}</dd>
        </div>
        <div>
          <dt>{{ t('automation.fields.accessLevel') }}</dt>
          <dd>{{ t(`assistant.access.levels.${automation.access_level}.label`) }}</dd>
        </div>
      </dl>

      <div class="automation-detail__actions">
        <t-button size="small" theme="primary" :loading="busy === 'run'" @click="runNow">
          <template #icon><t-icon name="play-circle" /></template>
          {{ t('automation.actions.runNow') }}
        </t-button>
        <t-button size="small" variant="outline" :loading="busy === 'toggle'" @click="toggle">
          {{ automation.enabled ? t('automation.actions.pause') : t('automation.actions.resume') }}
        </t-button>
        <t-button size="small" variant="outline" @click="emit('edit', automation)">{{ t('common.edit') }}</t-button>
      </div>

      <t-tabs v-model="tab" class="automation-detail__tabs">
        <t-tab-panel value="runs" :label="t('automation.detail.runs')">
          <div v-if="runsLoading && !runs.length" class="automation-detail__empty"><t-loading size="small" /></div>
          <p v-else-if="!runs.length" class="automation-detail__empty">{{ t('automation.detail.noRuns') }}</p>
          <ul v-else class="automation-runs">
            <li v-for="run in runs" :key="run.id" class="automation-run" :class="{ 'is-focused': run.id === focusRunId }">
              <div class="automation-run__head">
                <AutomationStatusTag :status="run.status" />
                <span class="automation-run__time">{{ when(run.scheduled_at) }}</span>
                <span class="automation-run__kind">{{ t(`automation.runTrigger.${run.trigger_kind}`) }}</span>
                <span class="automation-run__spacer" />
                <t-button v-if="run.session_id" size="small" variant="text" theme="primary" @click="openRun(run)">
                  {{ run.status === 'awaiting_approval' ? t('automation.actions.review') : t('automation.actions.openConversation') }}
                </t-button>
                <t-button v-if="cancellable(run)" size="small" variant="text" theme="danger"
                  :disabled="run.cancel_requested" @click="cancel(run)">
                  {{ run.cancel_requested ? t('automation.detail.stopping') : t('automation.actions.stop') }}
                </t-button>
              </div>
              <p v-if="run.error" class="automation-run__error">{{ run.error }}</p>
              <p v-else-if="run.answer" class="automation-run__answer">{{ run.answer }}</p>
            </li>
          </ul>
          <div v-if="runs.length < runsTotal" class="automation-detail__more">
            <t-button size="small" variant="text" :loading="runsLoading" @click="loadRuns(false)">
              {{ t('common.loadMore') }}
            </t-button>
          </div>
        </t-tab-panel>

        <t-tab-panel value="grants" :label="t('automation.detail.grants')">
          <p class="automation-detail__intro">{{ t('automation.grants.intro') }}</p>
          <div v-if="grantsLoading" class="automation-detail__empty"><t-loading size="small" /></div>
          <template v-else>
            <h4 class="automation-detail__subhead">{{ t('automation.grants.active') }}</h4>
            <p v-if="!grants.length" class="automation-detail__empty">{{ t('automation.grants.none') }}</p>
            <ul v-else class="automation-grants">
              <li v-for="grant in grants" :key="grant.id" class="automation-grant">
                <div class="automation-grant__main">
                  <span class="automation-grant__label">{{ grant.label || grant.grant_key }}</span>
                  <t-tag size="small" variant="light" :theme="grant.risk === 'destructive' ? 'danger' : 'warning'">
                    {{ t(`automation.risk.${grant.risk}`, grant.risk) }}
                  </t-tag>
                </div>
                <p v-if="grant.lapse" class="automation-grant__lapse">
                  <t-icon name="error-circle" size="14px" /> {{ t('automation.grants.lapsed') }}
                </p>
                <t-button size="small" variant="text" theme="danger" @click="revoke(grant)">
                  {{ t('automation.grants.revoke') }}
                </t-button>
              </li>
            </ul>

            <h4 class="automation-detail__subhead">{{ t('automation.grants.candidates') }}</h4>
            <p v-if="!candidates.length" class="automation-detail__empty">{{ t('automation.grants.noCandidates') }}</p>
            <ul v-else class="automation-grants">
              <li v-for="candidate in candidates" :key="candidate.prompt_id" class="automation-grant">
                <div class="automation-grant__main">
                  <span class="automation-grant__label">{{ candidate.label || candidate.tool }}</span>
                  <t-tag size="small" variant="light" :theme="candidate.risk === 'destructive' ? 'danger' : 'warning'">
                    {{ t(`automation.risk.${candidate.risk}`, candidate.risk) }}
                  </t-tag>
                  <span v-if="candidate.service_name" class="automation-grant__meta">{{ candidate.service_name }}</span>
                </div>
                <t-button size="small" variant="outline" @click="allow(candidate)">
                  {{ t('automation.grants.allow') }}
                </t-button>
              </li>
            </ul>
          </template>
        </t-tab-panel>
      </t-tabs>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
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
const tab = ref<'runs' | 'grants'>('runs')
const busy = ref<'' | 'run' | 'toggle'>('')
const runs = ref<AutomationRun[]>([])
const runsTotal = ref(0)
const runsPage = ref(0)
const runsLoading = ref(false)
const grants = ref<AutomationGrant[]>([])
const candidates = ref<AutomationGrantCandidate[]>([])
const grantsLoading = ref(false)
let poll: ReturnType<typeof setInterval> | undefined

const when = (iso: string | null) => formatInZone(iso, props.automation?.trigger.timezone, locale.value)
const cancellable = (run: AutomationRun) =>
  run.status === 'queued' || run.status === 'running' || run.status === 'awaiting_approval'

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
  tab.value = 'runs'
  runs.value = []
  grants.value = []
  candidates.value = []
  void loadRuns(true)
  poll = setInterval(refreshActive, 5000)
}, { immediate: true })

watch(tab, (value) => {
  if (value === 'grants') void loadGrants()
})

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
.automation-detail {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-4);
}

.automation-detail__header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.automation-detail__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.automation-detail__facts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 16px;
  margin: 0;

  dt {
    color: var(--td-text-color-placeholder);
    font-size: var(--app-text-sm);
  }

  dd {
    margin: 2px 0 0;
    color: var(--td-text-color-primary);
    font-size: var(--app-text-base);
    overflow-wrap: anywhere;
  }
}

.automation-detail__actions {
  display: flex;
  gap: 8px;
}

.automation-detail__empty,
.automation-detail__intro {
  margin: 0;
  padding: 8px 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

.automation-detail__subhead {
  margin: 12px 0 4px;
  font-size: var(--app-text-base);
  font-weight: 600;
}

.automation-detail__more {
  display: flex;
  justify-content: center;
  padding: 8px 0;
}

.automation-runs,
.automation-grants {
  margin: 0;
  padding: 0;
  list-style: none;
}

.automation-run {
  padding: 10px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &.is-focused {
    margin: 0 -8px;
    padding: 10px 8px;
    border-radius: var(--app-radius-sm);
    background: var(--td-brand-color-light);
  }
}

.automation-run__head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 28px;
}

.automation-run__time {
  font-size: var(--app-text-sm);
  font-variant-numeric: tabular-nums;
}

.automation-run__kind {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
}

.automation-run__spacer {
  flex: 1;
}

.automation-run__error,
.automation-run__answer {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  margin: 4px 0 0;
  overflow: hidden;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.automation-run__error {
  color: var(--td-error-color);
}

.automation-run__answer {
  color: var(--td-text-color-secondary);
}

.automation-grant {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding: 8px 0;
  border-bottom: 1px solid var(--td-component-stroke);
}

.automation-grant__main {
  display: flex;
  flex: 1;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.automation-grant__label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.automation-grant__meta {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
}

.automation-grant__lapse {
  flex-basis: 100%;
  order: 3;
  margin: 0;
  color: var(--td-warning-color);
  font-size: var(--app-text-sm);
}
</style>
