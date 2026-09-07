<template>
  <div class="event-webhooks">
    <div class="section-header">
      <h2>{{ t('settings.eventWebhooks.title') }}</h2>
      <p class="section-description">{{ t('settings.eventWebhooks.subtitle') }}</p>
    </div>

    <div v-if="loading" class="state-row">
      <t-loading size="small" />
      <span>{{ t('settings.eventWebhooks.loading') }}</span>
    </div>

    <t-alert v-else-if="error" theme="error" :message="error">
      <template #operation>
        <t-button size="small" @click="load">{{ t('settings.eventWebhooks.retry') }}</t-button>
      </template>
    </t-alert>

    <div v-else class="webhook-settings">
      <section class="settings-band">
        <div class="howto">
          <div class="howto__title">{{ t('settings.eventWebhooks.howItWorks') }}</div>
          <ol class="howto__list">
            <li>{{ t('settings.eventWebhooks.hintDeleteKb') }}</li>
            <li>{{ t('settings.eventWebhooks.hintBatch') }}</li>
            <li>{{ t('settings.eventWebhooks.hintNoRollback') }}</li>
          </ol>
          <p class="doc-link">{{ t('settings.eventWebhooks.docHint') }}</p>
        </div>

        <div class="api-key-section">
          <div class="api-key-section__header">
            <div class="api-key-section__title">
              <label>{{ t('settings.eventWebhooks.listLabel') }}</label>
              <p>{{ t('settings.eventWebhooks.listDesc') }}</p>
            </div>
            <t-button
              size="small"
              variant="outline"
              :disabled="endpoints.length >= 5"
              @click="openCreate"
            >
              <template #icon><t-icon name="add" /></template>
              {{ t('settings.eventWebhooks.add') }}
            </t-button>
          </div>
          <div class="api-key-section__body">
            <div class="api-key-list" :class="{ 'api-key-list--loading': listLoading }">
              <div v-if="listLoading" class="api-key-list__empty">
                <t-loading size="small" />
                <span>{{ t('settings.eventWebhooks.loading') }}</span>
              </div>
              <div v-else-if="endpoints.length === 0" class="api-key-list__empty">
                {{ t('settings.eventWebhooks.empty') }}
              </div>
              <div v-else class="api-key-table-wrap">
                <table class="api-key-table">
                  <thead>
                    <tr>
                      <th>{{ t('settings.eventWebhooks.colName') }}</th>
                      <th>{{ t('settings.eventWebhooks.colUrl') }}</th>
                      <th>{{ t('settings.eventWebhooks.colEvents') }}</th>
                      <th>{{ t('settings.eventWebhooks.colEnabled') }}</th>
                      <th class="api-key-table__actions-heading">{{ t('settings.eventWebhooks.colActions') }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="item in endpoints" :key="item.id">
                      <td>
                        <span class="api-key-name" :title="item.name">{{ item.name || '—' }}</span>
                      </td>
                      <td>
                        <code class="mono" :title="item.url">{{ item.url }}</code>
                      </td>
                      <td>
                        <t-tooltip placement="top" :show-arrow="false">
                          <template #content>
                            <ul class="event-tip-list">
                              <li v-for="ev in item.events" :key="ev">
                                <span>{{ eventTitle(ev) }}</span>
                                <code>{{ ev }}</code>
                              </li>
                            </ul>
                          </template>
                          <div class="event-chips">
                            <span v-if="isAllEvents(item.events)" class="event-chip event-chip--all">
                              {{ t('settings.eventWebhooks.eventsAll') }}
                            </span>
                            <template v-else>
                              <span
                                v-for="ev in visibleEventChips(item.events)"
                                :key="ev"
                                class="event-chip"
                              >
                                {{ eventTitle(ev) }}
                              </span>
                              <span v-if="hiddenEventCount(item.events)" class="event-chip event-chip--more">
                                {{ t('settings.eventWebhooks.eventsMore', { n: hiddenEventCount(item.events) }) }}
                              </span>
                            </template>
                          </div>
                        </t-tooltip>
                      </td>
                      <td>
                        <t-switch
                          size="small"
                          :model-value="item.enabled"
                          :disabled="togglingId === item.id"
                          @change="(val: boolean) => toggleEnabled(item, val)"
                        />
                      </td>
                      <td>
                        <div class="api-key-table__actions">
                          <t-button shape="square" variant="text" :title="t('settings.eventWebhooks.test')" @click="openDeliveries(item)">
                            <t-icon name="play-circle" />
                          </t-button>
                          <t-button shape="square" variant="text" :title="t('settings.eventWebhooks.edit')" @click="openEdit(item)">
                            <t-icon name="edit" />
                          </t-button>
                          <t-button shape="square" variant="text" theme="danger" :title="t('settings.eventWebhooks.delete')" @click="confirmDelete(item)">
                            <t-icon name="delete" />
                          </t-button>
                        </div>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>

        <t-collapse borderless class="example-collapse">
          <t-collapse-panel value="verify">
            <template #header>
              <div class="example-collapse__header">
                <span>{{ t('settings.eventWebhooks.exampleLabel') }}</span>
                <span class="example-collapse__desc">{{ t('settings.eventWebhooks.exampleDesc') }}</span>
              </div>
            </template>
            <t-tabs v-model="exampleLang" class="snippet-tabs" @click.stop>
              <t-tab-panel value="java" :label="t('settings.eventWebhooks.exampleTab.java')" />
              <t-tab-panel value="python" :label="t('settings.eventWebhooks.exampleTab.python')" />
              <t-tab-panel value="go" :label="t('settings.eventWebhooks.exampleTab.go')" />
              <t-tab-panel value="js" :label="t('settings.eventWebhooks.exampleTab.js')" />
              <t-tab-panel value="curl" :label="t('settings.eventWebhooks.exampleTab.curl')" />
            </t-tabs>
            <div class="code-panel">
              <div class="code-panel__toolbar">
                <span class="code-panel__label">{{ exampleTabLabel }}</span>
                <t-button size="small" variant="text" class="code-panel__copy" @click.stop="copyExample">
                  <t-icon name="file-copy" />
                  {{ t('settings.eventWebhooks.copy') }}
                </t-button>
              </div>
              <pre class="code-panel__pre">{{ activeExample }}</pre>
            </div>
          </t-collapse-panel>
          <t-collapse-panel value="payload">
            <template #header>
              <div class="example-collapse__header">
                <span>{{ t('settings.eventWebhooks.payloadLabel') }}</span>
                <span class="example-collapse__desc">{{ t('settings.eventWebhooks.payloadDesc') }}</span>
              </div>
            </template>
            <t-select
              v-model="payloadExampleType"
              class="payload-type-select"
              size="small"
              :popup-props="{ attach: 'body' }"
            >
              <t-option
                v-for="item in payloadExampleTypes"
                :key="item"
                :value="item"
                :label="item"
              />
            </t-select>
            <p class="payload-hint">{{ t(payloadHintKey) }}</p>
            <div class="code-panel">
              <div class="code-panel__toolbar">
                <span class="code-panel__label">{{ payloadExampleType }}</span>
                <t-button size="small" variant="text" class="code-panel__copy" @click.stop="copyPayloadExample">
                  <t-icon name="file-copy" />
                  {{ t('settings.eventWebhooks.copy') }}
                </t-button>
              </div>
              <pre class="code-panel__pre">{{ activePayloadExample }}</pre>
            </div>
          </t-collapse-panel>
        </t-collapse>
      </section>
    </div>

    <SettingDrawer
      v-model:visible="formVisible"
      class="webhook-form-drawer"
      :title="editing ? t('settings.eventWebhooks.editTitle') : t('settings.eventWebhooks.createTitle')"
      :description="editing ? t('settings.eventWebhooks.editHint') : t('settings.eventWebhooks.createHint')"
      icon="link"
      width="560px"
      :min-width="480"
      :max-width="920"
      storage-key="setting-drawer:width:event-webhook-form"
      :close-on-overlay-click="false"
      :confirm-loading="saving"
      @confirm="saveForm"
    >
      <div class="webhook-form">
        <div class="webhook-form-row">
          <div class="webhook-form-row__label">
            <label>{{ t('settings.eventWebhooks.fieldName') }}</label>
          </div>
          <t-input
            v-model="form.name"
            :placeholder="t('settings.eventWebhooks.fieldNamePh')"
            maxlength="80"
          />
        </div>

        <div class="webhook-form-row">
          <div class="webhook-form-row__label">
            <label>{{ t('settings.eventWebhooks.fieldUrl') }}</label>
            <p>{{ t('settings.eventWebhooks.fieldUrlHint') }}</p>
          </div>
          <t-input
            v-model="form.url"
            class="mono-input"
            :placeholder="t('settings.eventWebhooks.fieldUrlPh')"
            :status="formErrors.url ? 'error' : undefined"
          />
          <p v-if="formErrors.url" class="field-hint field-hint--error">{{ formErrors.url }}</p>
        </div>

        <div class="webhook-form-row">
          <div class="webhook-form-row__label">
            <label>
              {{ t('settings.eventWebhooks.fieldSecret') }}
              <t-tag v-if="editing" size="small" variant="light" theme="success">
                {{ t('settings.eventWebhooks.secretConfigured') }}
              </t-tag>
            </label>
            <p>{{ editing ? t('settings.eventWebhooks.fieldSecretEditHint') : t('settings.eventWebhooks.fieldSecretHint') }}</p>
          </div>
          <t-input
            v-model="form.secret"
            type="password"
            class="mono-input"
            autocomplete="new-password"
            :placeholder="editing ? t('settings.eventWebhooks.fieldSecretEditPh') : t('settings.eventWebhooks.fieldSecretPh')"
            :status="formErrors.secret ? 'error' : undefined"
          />
          <p v-if="formErrors.secret" class="field-hint field-hint--error">{{ formErrors.secret }}</p>
        </div>

        <div class="webhook-form-row">
          <div class="webhook-form-row__label">
            <label>{{ t('settings.eventWebhooks.fieldEvents') }}</label>
            <p>{{ t('settings.eventWebhooks.fieldEventsHint') }}</p>
          </div>
          <div class="event-group-list">
            <div v-for="group in eventGroups" :key="group.key" class="event-group">
              <div class="event-group__header">
                <div class="event-group__heading">
                  <span>{{ t(group.labelKey) }}</span>
                  <p v-if="group.hintKey" class="event-group__hint">{{ t(group.hintKey) }}</p>
                </div>
                <t-button
                  size="small"
                  variant="text"
                  @click="toggleEventGroup(group, !eventGroupAllSelected(group))"
                >
                  {{
                    eventGroupAllSelected(group)
                      ? t('settings.eventWebhooks.clearGroup')
                      : t('settings.eventWebhooks.selectGroup')
                  }}
                </t-button>
              </div>
              <div class="event-group__items">
                <div v-for="item in group.items" :key="item.type" class="event-item">
                  <t-checkbox
                    :model-value="form.events.includes(item.type)"
                    @change="(val: unknown) => toggleEvent(item.type, val)"
                  >
                    {{ item.labelKey ? t(item.labelKey) : item.type }}
                  </t-checkbox>
                  <p class="event-item__type">{{ item.type }}</p>
                </div>
              </div>
            </div>
          </div>
          <p v-if="formErrors.events" class="field-hint field-hint--error">{{ formErrors.events }}</p>
        </div>

        <div class="webhook-form-row">
          <div class="webhook-form-row__label">
            <label>{{ t('settings.eventWebhooks.fieldDesc') }}</label>
          </div>
          <t-input v-model="form.description" :placeholder="t('settings.eventWebhooks.fieldDescPh')" />
        </div>

        <div class="webhook-form-row webhook-form-row--switch">
          <div class="webhook-form-row__label">
            <label>{{ t('settings.eventWebhooks.fieldEnabled') }}</label>
            <p>{{ t('settings.eventWebhooks.fieldEnabledHint') }}</p>
          </div>
          <t-switch v-model="form.enabled" />
        </div>
      </div>
    </SettingDrawer>

    <SettingDrawer
      v-model:visible="deliveryVisible"
      class="webhook-delivery-drawer"
      :title="t('settings.eventWebhooks.deliveryTitle')"
      :description="active?.name || active?.url"
      icon="history"
      hide-footer
    >
      <div class="delivery-panel">
        <div class="delivery-toolbar">
          <t-button size="small" :loading="testing" @click="runTest">{{ t('settings.eventWebhooks.sendTest') }}</t-button>
          <t-tag v-if="testResult === 'queued'" theme="warning">{{ t('settings.eventWebhooks.testQueued') }}</t-tag>
          <t-tag v-else-if="testResult === 'success'" theme="success">{{ t('settings.eventWebhooks.testDelivered') }}</t-tag>
          <t-tag v-else-if="testResult === 'err'" theme="danger">{{ t('settings.eventWebhooks.testFailed') }}</t-tag>
        </div>
        <div v-if="deliveriesLoading && deliveries.length === 0" class="state-row state-row--compact">
          <t-loading size="small" />
        </div>
        <div v-else-if="deliveries.length === 0" class="api-key-list__empty">
          {{ t('settings.eventWebhooks.noDeliveries') }}
        </div>
        <div v-else class="delivery-table-wrap">
          <table class="delivery-table">
            <thead>
              <tr>
                <th>{{ t('settings.eventWebhooks.colTime') }}</th>
                <th>{{ t('settings.eventWebhooks.colType') }}</th>
                <th>{{ t('settings.eventWebhooks.colStatus') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in deliveries" :key="row.delivery_id">
                <td>{{ formatTime(row.created_at) }}</td>
                <td><code>{{ row.event_type }}</code></td>
                <td>
                  <t-tooltip :disabled="!row.error" placement="top-left" :show-arrow="false">
                    <template #content>{{ row.error }}</template>
                    <span class="delivery-status-tag">
                      <t-tag size="small" :theme="deliveryStatusTheme(row)">
                        {{ deliveryStatusText(row) }}
                      </t-tag>
                    </span>
                  </t-tooltip>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { useAuthStore } from '@/stores/auth'
import { getApiBaseUrl } from '@/utils/api-base'
import {
  createWorkspaceWebhook,
  deleteWorkspaceWebhook,
  listWorkspaceWebhookDeliveries,
  listWorkspaceWebhookEventTypes,
  listWorkspaceWebhooks,
  testWorkspaceWebhook,
  updateWorkspaceWebhook,
  type WorkspaceWebhookDelivery,
  type WorkspaceWebhookEndpoint,
} from '@/api/tenant/webhooks'
import {
  webhookVerifyExample,
  type WebhookVerifyLang,
} from './eventWebhookVerifyExamples'
import {
  WEBHOOK_PAYLOAD_EXAMPLE_TYPES,
  WEBHOOK_PAYLOAD_HINT_KEYS,
  webhookPayloadExample,
  type WebhookPayloadExampleType,
} from './eventWebhookPayloadExamples'

type WebhookEventGroupKey = 'knowledge' | 'kb' | 'rbac' | 'other'

interface WebhookEventItem {
  type: string
  labelKey?: string
}

interface WebhookEventGroup {
  key: WebhookEventGroupKey
  labelKey: string
  hintKey?: string
  items: WebhookEventItem[]
}

const EVENT_CATALOG: Array<WebhookEventItem & { group: Exclude<WebhookEventGroupKey, 'other'> }> = [
  { type: 'knowledge.created', group: 'knowledge', labelKey: 'settings.eventWebhooks.eventKnowledgeCreated' },
  { type: 'knowledge.parse_completed', group: 'knowledge', labelKey: 'settings.eventWebhooks.eventKnowledgeParsed' },
  { type: 'knowledge.parse_failed', group: 'knowledge', labelKey: 'settings.eventWebhooks.eventKnowledgeParseFailed' },
  { type: 'knowledge.deleted', group: 'knowledge', labelKey: 'settings.eventWebhooks.eventKnowledgeDeleted' },
  { type: 'knowledge.batch_deleted', group: 'knowledge', labelKey: 'settings.eventWebhooks.eventKnowledgeBatchDeleted' },
  { type: 'kb.created', group: 'kb', labelKey: 'settings.eventWebhooks.eventKbCreated' },
  { type: 'kb.deleted', group: 'kb', labelKey: 'settings.eventWebhooks.eventKbDeleted' },
  { type: 'rbac.member_added', group: 'rbac', labelKey: 'settings.eventWebhooks.eventMemberAdded' },
  { type: 'rbac.member_removed', group: 'rbac', labelKey: 'settings.eventWebhooks.eventMemberRemoved' },
]

const EVENT_GROUP_ORDER: Array<{ key: Exclude<WebhookEventGroupKey, 'other'>; labelKey: string; hintKey?: string }> = [
  { key: 'knowledge', labelKey: 'settings.eventWebhooks.eventGroupKnowledge' },
  { key: 'kb', labelKey: 'settings.eventWebhooks.eventGroupKb' },
  {
    key: 'rbac',
    labelKey: 'settings.eventWebhooks.eventGroupMembers',
    hintKey: 'settings.eventWebhooks.eventGroupMembersHint',
  },
]

const EVENT_LABEL_BY_TYPE = Object.fromEntries(
  EVENT_CATALOG.map((item) => [item.type, item.labelKey]),
) as Record<string, string>

const { t } = useI18n()
const authStore = useAuthStore()
const tenantId = computed(() => Number(authStore.currentTenantId ?? 0))

const loading = ref(true)
const listLoading = ref(false)
const error = ref('')
const endpoints = ref<WorkspaceWebhookEndpoint[]>([])
const eventTypes = ref<string[]>([])
const togglingId = ref('')

const formVisible = ref(false)
const saving = ref(false)
const editing = ref<WorkspaceWebhookEndpoint | null>(null)
const form = reactive({
  name: '',
  url: '',
  secret: '',
  events: [] as string[],
  description: '',
  enabled: true,
})
const formErrors = reactive({
  url: '',
  secret: '',
  events: '',
})

const availableEventTypes = computed(() => (eventTypes.value.length ? eventTypes.value : EVENT_CATALOG.map((item) => item.type)))

const eventGroups = computed<WebhookEventGroup[]>(() => {
  const available = new Set(availableEventTypes.value)
  const grouped = new Map<WebhookEventGroupKey, WebhookEventItem[]>()
  for (const item of EVENT_CATALOG) {
    if (!available.has(item.type)) continue
    const list = grouped.get(item.group) ?? []
    list.push({ type: item.type, labelKey: item.labelKey })
    grouped.set(item.group, list)
  }
  const known = new Set(EVENT_CATALOG.map((item) => item.type))
  const extras = availableEventTypes.value.filter((type) => !known.has(type))
  const groups: WebhookEventGroup[] = EVENT_GROUP_ORDER
    .map((meta) => ({
      key: meta.key,
      labelKey: meta.labelKey,
      hintKey: meta.hintKey,
      items: grouped.get(meta.key) ?? [],
    }))
    .filter((group) => group.items.length > 0)
  if (extras.length) {
    groups.push({
      key: 'other',
      labelKey: 'settings.eventWebhooks.eventGroupOther',
      items: extras.map((type) => ({ type })),
    })
  }
  return groups
})

const EVENT_CHIP_LIMIT = 2

function eventTitle(type: string) {
  const key = EVENT_LABEL_BY_TYPE[type]
  return key ? t(key) : type
}

function isAllEvents(events: string[]) {
  const all = availableEventTypes.value
  if (!all.length || events.length !== all.length) return false
  const selected = new Set(events)
  return all.every((type) => selected.has(type))
}

function visibleEventChips(events: string[]) {
  return events.slice(0, EVENT_CHIP_LIMIT)
}

function hiddenEventCount(events: string[]) {
  return Math.max(0, events.length - EVENT_CHIP_LIMIT)
}

function clearFormErrors() {
  formErrors.url = ''
  formErrors.secret = ''
  formErrors.events = ''
}

function isLoopbackHost(host: string) {
  const h = host.trim().toLowerCase().replace(/^\[|\]$/g, '')
  return h === 'localhost' || h === '127.0.0.1' || h === '::1'
}

function validateFormUrl(raw: string) {
  const trimmed = raw.trim()
  if (!trimmed) return t('settings.eventWebhooks.urlRequired')
  let parsed: URL
  try {
    parsed = new URL(trimmed)
  } catch {
    return t('settings.eventWebhooks.urlInvalid')
  }
  const loopback = isLoopbackHost(parsed.hostname)
  if (parsed.protocol === 'https:') return ''
  if (parsed.protocol === 'http:' && loopback) return ''
  if (parsed.protocol === 'http:') return t('settings.eventWebhooks.urlHttpsRequired')
  return t('settings.eventWebhooks.urlInvalid')
}

function eventGroupAllSelected(group: WebhookEventGroup) {
  return group.items.length > 0 && group.items.every((item) => form.events.includes(item.type))
}

function toggleEvent(type: string, checked: unknown) {
  const on = Boolean(checked)
  if (on) {
    if (!form.events.includes(type)) form.events = [...form.events, type]
    return
  }
  form.events = form.events.filter((item) => item !== type)
}

function toggleEventGroup(group: WebhookEventGroup, selected: boolean) {
  const types = new Set(group.items.map((item) => item.type))
  if (selected) {
    const next = [...form.events]
    for (const type of types) {
      if (!next.includes(type)) next.push(type)
    }
    form.events = next
    return
  }
  form.events = form.events.filter((type) => !types.has(type))
}

const deliveryVisible = ref(false)
const active = ref<WorkspaceWebhookEndpoint | null>(null)
const deliveries = ref<WorkspaceWebhookDelivery[]>([])
const deliveriesLoading = ref(false)
const testing = ref(false)
const testResult = ref<'queued' | 'success' | 'err' | ''>('')
let deliveryPollTimer: ReturnType<typeof setInterval> | null = null
let deliveryPollUntil = 0
let watchedTestId = ''
let testStartedAt = 0

const TEST_EVENT_TYPE = 'webhook.test'
const RECENT_PENDING_MS = 2 * 60 * 1000

function deliveryStatusTheme(row: WorkspaceWebhookDelivery) {
  if (row.status === 'success') return 'success'
  if (row.status === 'failed') return 'danger'
  return 'warning'
}

function deliveryStatusText(row: WorkspaceWebhookDelivery) {
  if (row.status === 'success') return String(row.http_status || 200)
  if (row.status === 'failed') return String(row.http_status || t('settings.eventWebhooks.statusFailed'))
  return t('settings.eventWebhooks.statusPending')
}

function deliveryInFlight(rows: WorkspaceWebhookDelivery[]) {
  const cutoff = Date.now() - RECENT_PENDING_MS
  return rows.some((row) => {
    if (row.status !== 'pending') return false
    const at = new Date(row.created_at).getTime()
    return Number.isFinite(at) && at > cutoff
  })
}

function syncTestResult(rows: WorkspaceWebhookDelivery[]) {
  if (testResult.value !== 'queued') return
  let row = watchedTestId
    ? rows.find((item) => item.delivery_id === watchedTestId)
    : undefined
  if (!row) {
    const cutoff = testStartedAt - 5000
    row = rows.find((item) => {
      if (item.event_type !== TEST_EVENT_TYPE) return false
      const at = new Date(item.created_at).getTime()
      return Number.isFinite(at) && at >= cutoff
    })
    if (row) watchedTestId = row.delivery_id
  }
  if (!row) return
  if (row.status === 'success') testResult.value = 'success'
  else if (row.status === 'failed') testResult.value = 'err'
}

function stopDeliveryPoll() {
  if (!deliveryPollTimer) return
  clearInterval(deliveryPollTimer)
  deliveryPollTimer = null
}

function startDeliveryPoll(holdMs = 0) {
  if (holdMs > 0) deliveryPollUntil = Date.now() + holdMs
  if (deliveryPollTimer || !deliveryVisible.value) return
  deliveryPollTimer = setInterval(() => {
    void loadDeliveries({ silent: true })
  }, 2000)
}

function shouldKeepDeliveryPoll(rows: WorkspaceWebhookDelivery[]) {
  return deliveryVisible.value && (deliveryInFlight(rows) || Date.now() < deliveryPollUntil)
}

const EXAMPLE_TAB_I18N: Record<WebhookVerifyLang, string> = {
  java: 'settings.eventWebhooks.exampleTab.java',
  python: 'settings.eventWebhooks.exampleTab.python',
  go: 'settings.eventWebhooks.exampleTab.go',
  js: 'settings.eventWebhooks.exampleTab.js',
  curl: 'settings.eventWebhooks.exampleTab.curl',
}

const exampleLang = ref<WebhookVerifyLang>('java')
const exampleTabLabel = computed(() => t(EXAMPLE_TAB_I18N[exampleLang.value]))
const activeExample = computed(() => webhookVerifyExample(exampleLang.value, getApiBaseUrl() || ''))

const payloadExampleTypes = WEBHOOK_PAYLOAD_EXAMPLE_TYPES
const payloadExampleType = ref<WebhookPayloadExampleType>('knowledge.created')
const payloadHintKey = computed(() => WEBHOOK_PAYLOAD_HINT_KEYS[payloadExampleType.value])
const activePayloadExample = computed(() => webhookPayloadExample(payloadExampleType.value))

async function load() {
  if (!tenantId.value) {
    error.value = t('settings.eventWebhooks.noTenant')
    loading.value = false
    return
  }
  loading.value = true
  error.value = ''
  try {
    const [rows, types] = await Promise.all([
      listWorkspaceWebhooks(tenantId.value),
      listWorkspaceWebhookEventTypes(tenantId.value),
    ])
    endpoints.value = rows
    eventTypes.value = types
  } catch (e: any) {
    error.value = e?.message || t('settings.eventWebhooks.loadFailed')
  } finally {
    loading.value = false
  }
}

async function reloadList() {
  if (!tenantId.value) return
  listLoading.value = true
  try {
    endpoints.value = await listWorkspaceWebhooks(tenantId.value)
  } finally {
    listLoading.value = false
  }
}

function openCreate() {
  editing.value = null
  form.name = ''
  form.url = ''
  form.secret = ''
  form.events = [...availableEventTypes.value]
  form.description = ''
  form.enabled = true
  clearFormErrors()
  formVisible.value = true
}

function openEdit(item: WorkspaceWebhookEndpoint) {
  editing.value = item
  form.name = item.name
  form.url = item.url
  form.secret = ''
  form.events = [...item.events]
  form.description = item.description
  form.enabled = item.enabled
  clearFormErrors()
  formVisible.value = true
}

function validateForm() {
  clearFormErrors()
  formErrors.url = validateFormUrl(form.url)
  if (!editing.value && form.secret.trim().length < 16) {
    formErrors.secret = t('settings.eventWebhooks.secretTooShort')
  } else if (editing.value && form.secret && form.secret.trim().length < 16) {
    formErrors.secret = t('settings.eventWebhooks.secretTooShort')
  }
  if (!form.events.length) {
    formErrors.events = t('settings.eventWebhooks.eventsRequired')
  }
  return !formErrors.url && !formErrors.secret && !formErrors.events
}

async function saveForm() {
  if (!tenantId.value) return
  if (!validateForm()) {
    const first = formErrors.url || formErrors.secret || formErrors.events
    if (first) MessagePlugin.warning(first)
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      const payload: Record<string, unknown> = {
        name: form.name,
        url: form.url.trim(),
        events: form.events,
        enabled: form.enabled,
        description: form.description,
      }
      if (form.secret.trim()) payload.secret = form.secret.trim()
      await updateWorkspaceWebhook(tenantId.value, editing.value.id, payload)
    } else {
      await createWorkspaceWebhook(tenantId.value, {
        name: form.name,
        url: form.url.trim(),
        secret: form.secret.trim(),
        events: form.events,
        enabled: form.enabled,
        description: form.description,
      })
    }
    formVisible.value = false
    MessagePlugin.success(t('settings.eventWebhooks.saveSuccess'))
    await reloadList()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.eventWebhooks.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(item: WorkspaceWebhookEndpoint, enabled: boolean) {
  if (!tenantId.value) return
  togglingId.value = item.id
  try {
    await updateWorkspaceWebhook(tenantId.value, item.id, { enabled })
    item.enabled = enabled
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.eventWebhooks.saveFailed'))
  } finally {
    togglingId.value = ''
  }
}

function confirmDelete(item: WorkspaceWebhookEndpoint) {
  const dialog = DialogPlugin.confirm({
    header: t('settings.eventWebhooks.deleteTitle'),
    body: t('settings.eventWebhooks.deleteBody', { name: item.name || item.url }),
    confirmBtn: { content: t('settings.eventWebhooks.delete'), theme: 'danger' },
    onConfirm: async () => {
      try {
        await deleteWorkspaceWebhook(tenantId.value, item.id)
        MessagePlugin.success(t('settings.eventWebhooks.deleteSuccess'))
        await reloadList()
        dialog.hide()
      } catch (e: any) {
        MessagePlugin.error(e?.message || t('settings.eventWebhooks.saveFailed'))
      }
    },
  })
}

async function openDeliveries(item: WorkspaceWebhookEndpoint) {
  active.value = item
  testResult.value = ''
  watchedTestId = ''
  testStartedAt = 0
  deliveryVisible.value = true
  await loadDeliveries()
}

async function loadDeliveries(opts: { silent?: boolean } = {}) {
  if (!tenantId.value || !active.value) return
  if (!opts.silent) deliveriesLoading.value = true
  try {
    deliveries.value = await listWorkspaceWebhookDeliveries(tenantId.value, active.value.id, 50)
    syncTestResult(deliveries.value)
    if (shouldKeepDeliveryPoll(deliveries.value)) startDeliveryPoll()
    else stopDeliveryPoll()
  } catch (e: any) {
    if (!opts.silent) MessagePlugin.error(e?.message || t('settings.eventWebhooks.loadFailed'))
  } finally {
    if (!opts.silent) deliveriesLoading.value = false
  }
}

async function runTest() {
  if (!tenantId.value || !active.value) return
  testing.value = true
  testResult.value = ''
  try {
    await testWorkspaceWebhook(tenantId.value, active.value.id)
    testResult.value = 'queued'
    testStartedAt = Date.now()
    watchedTestId = ''
    startDeliveryPoll(20000)
    await loadDeliveries({ silent: true })
  } catch (e: any) {
    testResult.value = 'err'
    MessagePlugin.error(e?.message || t('settings.eventWebhooks.testFailed'))
  } finally {
    testing.value = false
  }
}

function formatTime(raw: string) {
  if (!raw) return '—'
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  return d.toLocaleString()
}

async function copyExample() {
  try {
    await navigator.clipboard.writeText(activeExample.value)
    MessagePlugin.success(t('settings.eventWebhooks.copied'))
  } catch {
    MessagePlugin.error(t('settings.eventWebhooks.copyFailed'))
  }
}

async function copyPayloadExample() {
  try {
    await navigator.clipboard.writeText(activePayloadExample.value)
    MessagePlugin.success(t('settings.eventWebhooks.copied'))
  } catch {
    MessagePlugin.error(t('settings.eventWebhooks.copyFailed'))
  }
}

watch(tenantId, () => {
  load()
})

watch(deliveryVisible, (open) => {
  if (!open) {
    deliveryPollUntil = 0
    stopDeliveryPoll()
  }
})

onMounted(load)
onUnmounted(stopDeliveryPoll)
</script>

<style scoped lang="less">
.event-webhooks {
  width: 100%;
}

.state-row {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  min-height: 160px;
  color: var(--td-text-color-secondary);

  &--compact {
    min-height: 80px;
  }
}

.webhook-settings,
.settings-band {
  display: flex;
  flex-direction: column;
}

.settings-band {
  border-top: 1px solid var(--td-component-stroke);
}

.howto {
  padding: 16px 0 8px;
}

.howto__title {
  margin-bottom: 8px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.howto__list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
  counter-reset: howto;

  li {
    position: relative;
    margin: 0;
    padding-left: 28px;
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.6;
    counter-increment: howto;

    &::before {
      content: counter(howto);
      position: absolute;
      left: 0;
      top: 2px;
      width: 18px;
      height: 18px;
      border-radius: 50%;
      background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
      color: var(--td-brand-color);
      font-size: 11px;
      font-weight: 600;
      line-height: 18px;
      text-align: center;
    }
  }
}

.doc-link {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.api-key-section {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px 0 20px;
  border-top: 1px solid var(--td-component-stroke);
  border-bottom: 1px solid var(--td-component-stroke);

  &__header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
  }

  &__title {
    min-width: 0;

    label {
      display: block;
      margin-bottom: 6px;
      color: var(--td-text-color-primary);
      font-size: 15px;
      font-weight: 600;
      line-height: 1.4;
    }

    p {
      margin: 0;
      color: var(--td-text-color-secondary);
      font-size: 13px;
      line-height: 1.55;
    }
  }
}

.api-key-list {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  overflow: hidden;
}

.api-key-list__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 88px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.api-key-table-wrap {
  width: 100%;
  overflow-x: auto;
}

.api-key-table {
  width: 100%;
  min-width: 760px;
  border-collapse: collapse;
  table-layout: fixed;

  th,
  td {
    padding: 12px 14px;
    border-bottom: 1px solid var(--td-component-stroke);
    text-align: left;
    vertical-align: middle;
  }

  th {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-placeholder);
    font-size: 12px;
    font-weight: 500;
    line-height: 1.4;
  }

  td {
    color: var(--td-text-color-secondary);
    font-size: 13px;
    line-height: 1.45;
  }

  th:nth-child(1),
  td:nth-child(1) {
    width: 18%;
  }

  th:nth-child(2),
  td:nth-child(2) {
    width: 32%;
  }

  th:nth-child(3),
  td:nth-child(3) {
    width: 28%;
  }

  th:nth-child(4),
  td:nth-child(4) {
    width: 72px;
  }

  th:nth-child(5),
  td:nth-child(5) {
    width: 120px;
  }

  tbody tr:last-child td {
    border-bottom: none;
  }

  &__actions-heading {
    text-align: right !important;
  }

  &__actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 4px;
  }
}

.api-key-name {
  display: block;
  min-width: 0;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mono {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}

.event-chips {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  gap: 6px;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
}

.event-chip {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  max-width: 100%;
  height: 22px;
  padding: 0 8px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--td-success-color) 10%, var(--td-bg-color-container));
  color: var(--td-success-color);
  font-size: 12px;
  font-weight: 500;
  line-height: 20px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;

  &--all {
    background: color-mix(in srgb, var(--td-brand-color) 10%, var(--td-bg-color-container));
    color: var(--td-brand-color);
  }

  &--more {
    flex-shrink: 0;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
  }
}

.event-tip-list {
  margin: 0;
  padding: 0;
  list-style: none;
  max-width: 280px;

  li {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
    padding: 3px 0;
    font-size: 12px;
    line-height: 1.4;
  }

  code {
    color: var(--td-text-color-placeholder);
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 11px;
  }
}

.example-collapse {
  margin-top: 4px;

  :deep(.t-collapse-panel) {
    border: none;
  }

  :deep(.t-collapse-panel__header) {
    padding: 14px 0;
    background: transparent;
    font-size: 14px;
    font-weight: 600;
  }

  :deep(.t-collapse-panel__body) {
    padding-bottom: 8px;
    background: transparent;
  }

  :deep(.t-collapse-panel__wrapper) {
    background: transparent;
  }

  &__header {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
    padding-right: 8px;
  }

  &__desc {
    color: var(--td-text-color-placeholder);
    font-size: 12px;
    font-weight: 400;
    line-height: 1.5;
  }
}

.webhook-form {
  display: flex;
  flex-direction: column;
}

.webhook-form-row {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);

  &:first-child {
    padding-top: 0;
  }

  &:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }

  &--switch {
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
  }

  &__label {
    min-width: 0;

    label {
      display: flex;
      align-items: center;
      gap: 8px;
      color: var(--td-text-color-primary);
      font-size: 14px;
      font-weight: 600;
      line-height: 1.45;

      &::before {
        content: '';
        flex-shrink: 0;
        width: 3px;
        height: 14px;
        border-radius: 2px;
        background: var(--td-brand-color);
      }
    }

    p {
      margin: 2px 0 0;
      color: var(--td-text-color-placeholder);
      font-size: 12px;
      line-height: 1.5;
    }
  }

  :deep(.t-input) {
    background-color: var(--td-bg-color-secondarycontainer);
    border-color: transparent;
    box-shadow: none !important;
  }

  :deep(.t-input:hover),
  :deep(.t-input.t-is-focused) {
    border-color: var(--td-component-border);
    background-color: var(--td-bg-color-container);
  }
}

.field-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);

  &--error {
    color: var(--td-error-color);
  }
}

.event-group-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.event-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 0 4px;

  & + & {
    border-top: 1px solid var(--td-component-stroke);
  }
}

.event-group__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  min-height: 24px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.event-group__heading {
  min-width: 0;
}

.event-group__hint {
  margin: 4px 0 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  font-weight: 400;
  line-height: 1.5;
}

.event-group__items {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px 16px;
}

.event-item {
  min-width: 0;

  :deep(.t-checkbox__label) {
    font-size: 13px;
    color: var(--td-text-color-primary);
  }
}

.event-item__type {
  margin: 2px 0 0 24px;
  color: var(--td-text-color-placeholder);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11px;
  line-height: 1.4;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mono-input :deep(.t-input__inner) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.code-panel {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  overflow: hidden;

  &__toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 10px;
    border-bottom: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-container);
  }

  &__label {
    font-size: 12px;
    font-weight: 500;
    color: var(--td-text-color-secondary);
  }

  &__pre {
    margin: 0;
    padding: 10px 12px;
    max-height: 360px;
    overflow: auto;
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 12px;
    line-height: 1.55;
    white-space: pre;
  }
}

.snippet-tabs {
  margin: 0 0 8px;

  :deep(.t-tabs__nav) {
    min-height: 36px;
  }

  :deep(.t-tabs__nav-item) {
    height: 36px;
    font-size: 13px;
    line-height: 36px;
  }

  :deep(.t-tabs__content) {
    display: none;
  }
}

.payload-type-select {
  width: 280px;
  margin: 0 0 8px;
}

.payload-hint {
  margin: 0 0 8px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.55;
}

.delivery-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.delivery-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
  width: 100%;
  min-height: 0;
}

.delivery-table-wrap {
  width: 100%;
  flex: 0 0 auto;
  overflow: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
}

.delivery-table {
  width: 100%;
  height: auto !important;
  border-collapse: collapse;
  table-layout: fixed;

  thead,
  tbody,
  tr {
    height: auto;
  }

  th,
  td {
    padding: 8px 12px;
    border-bottom: 1px solid var(--td-component-stroke);
    text-align: left;
    vertical-align: middle;
    height: 40px;
    max-height: 40px;
    line-height: 1.4;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  th {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-placeholder);
    font-size: 12px;
    font-weight: 500;
  }

  td {
    color: var(--td-text-color-secondary);
    font-size: 13px;
  }

  th:nth-child(1),
  td:nth-child(1) {
    width: 168px;
  }

  th:nth-child(2),
  td:nth-child(2) {
    width: 46%;
  }

  th:nth-child(3),
  td:nth-child(3) {
    width: 96px;
  }

  tbody tr:last-child td {
    border-bottom: none;
  }

  code {
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 12px;
  }
}

.state-row--compact {
  min-height: 48px;
}

.delivery-status-tag {
  display: inline-flex;
  vertical-align: middle;
}
</style>

<style lang="less">
.webhook-delivery-drawer.setting-drawer {
  .t-drawer__body,
  .setting-drawer__body {
    display: block;
    height: auto;
  }
}
</style>

