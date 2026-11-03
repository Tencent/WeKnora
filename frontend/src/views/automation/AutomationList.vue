<template>
  <main class="automation-page">
    <header class="automation-page__header" style="--wails-draggable: drag">
      <div class="automation-page__title-row" style="--wails-draggable: drag">
        <h2 style="--wails-draggable: drag">
          <ResourceIcon type="automation" :size="24" />
          {{ t('automation.title') }}
        </h2>
        <div class="automation-page__tools" style="--wails-draggable: no-drag">
          <t-input v-if="automations.length > 0" v-model="query" class="automation-page__search"
            :placeholder="t('menu.search')" :aria-label="t('menu.search')" clearable>
            <template #prefix-icon><t-icon name="search" size="16px" /></template>
          </t-input>
          <t-button variant="text" theme="default" size="small" class="automation-page__add" @click="openEditor(null)">
            <template #icon><t-icon name="add" size="16px" /></template>
            {{ t('automation.add') }}
          </t-button>
        </div>
      </div>
      <p class="automation-page__subtitle" style="--wails-draggable: drag">{{ t('automation.description') }}</p>
    </header>

    <div v-if="loading" class="loading-container">
      <t-loading :text="t('common.loading')" />
    </div>

    <section v-else class="automation-page__main">
      <div v-if="automations.length > 0 && filtered.length === 0" class="empty-state">
        <t-empty :description="t('common.noResult')" />
        <t-button variant="outline" @click="query = ''">{{ t('common.clear') }}</t-button>
      </div>
      <div v-else class="automation-grid">
        <article v-for="item in filtered" :key="item.id" class="automation-card" :class="{ 'is-paused': !item.enabled }">
          <button type="button" class="automation-card__open" :aria-label="item.name" @click="openDetail(item)" />
          <div class="automation-card__header">
            <div class="automation-card__badge" aria-hidden="true"><t-icon name="time" size="14px" /></div>
            <h3 class="automation-card__title" :title="item.name">{{ item.name }}</h3>
            <div class="automation-card__actions">
              <t-popconfirm :content="t('automation.runConfirm', { name: item.name })"
                :confirm-btn="{ content: t('automation.actions.runNow'), theme: 'primary' }"
                :cancel-btn="t('common.cancel')" theme="default" placement="bottom"
                :popup-props="{ disabled: busy.has(item.id) }" @confirm="runNow(item)">
                <button type="button" class="automation-card__icon-btn" :title="t('automation.actions.runNow')"
                  :aria-label="`${item.name} · ${t('automation.actions.runNow')}`" :disabled="busy.has(item.id)">
                  <ChatActionIcon name="play" />
                </button>
              </t-popconfirm>
              <button type="button" class="automation-card__icon-btn" :title="t('common.edit')"
                :aria-label="`${item.name} · ${t('common.edit')}`" @click="openEditor(item)">
                <ChatActionIcon name="edit" />
              </button>
              <t-popconfirm :content="t('automation.deleteBody', { name: item.name })"
                :confirm-btn="{ content: t('common.delete'), theme: 'danger' }"
                :cancel-btn="t('common.cancel')" theme="danger" placement="bottom"
                :popup-props="{ disabled: busy.has(item.id) }" @confirm="remove(item)">
                <button type="button" class="automation-card__icon-btn" :title="t('common.delete')"
                  :aria-label="`${item.name} · ${t('common.delete')}`" :disabled="busy.has(item.id)">
                  <ChatActionIcon name="delete" />
                </button>
              </t-popconfirm>
            </div>
          </div>
          <p class="automation-card__schedule">{{ describeTrigger(item.trigger, t, locale) }}</p>
          <p v-if="!item.enabled && item.paused_reason" class="automation-card__paused" :title="item.paused_reason">
            {{ item.paused_reason }}
          </p>
          <div class="automation-card__footer">
            <div class="automation-card__meta">
              <span v-if="item.enabled && item.next_run_at">
                {{ t('automation.nextRunAt', { time: formatInZone(item.next_run_at, item.trigger.timezone, locale) }) }}
              </span>
              <AutomationStatusTag :status="item.active_run_status || item.last_run_status" />
            </div>
            <span v-if="hasEndedOnceSchedule(item)" class="automation-card__status is-ended" :title="t('automation.scheduleEndedHint')">
              {{ t('automation.scheduleEnded') }}
            </span>
            <button v-else type="button" role="switch" class="automation-card__status" :class="{ 'is-enabled': item.enabled }"
              :aria-checked="item.enabled" :aria-label="`${item.name} · ${t('automation.fields.enabled')}`"
              :disabled="busy.has(item.id)" @click="toggle(item)">
              <t-loading v-if="busy.has(item.id)" size="12px" />
              <span v-else class="automation-card__dot" aria-hidden="true" />
              {{ item.enabled ? t('common.on') : t('automation.paused') }}
            </button>
          </div>
        </article>
        <button type="button" class="automation-card automation-card--add" @click="openEditor(null)">
          <span class="automation-card--add__icon" aria-hidden="true"><t-icon name="add" /></span>
          <span class="automation-card--add__label">{{ t('automation.add') }}</span>
          <span class="automation-card--add__hint">{{ t('automation.addHint') }}</span>
        </button>
      </div>
    </section>

    <AutomationEditor v-model:visible="editorVisible" :automation="editing" @saved="onSaved" />
    <AutomationDetail v-model:visible="detailVisible" :automation="detail"
      :focus-run-id="focusRunId" @changed="onSaved" @edit="openEditor" />
  </main>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  deleteAutomation,
  getAutomation,
  listAutomations,
  pauseAutomation,
  resumeAutomation,
  runAutomationNow,
  type Automation,
} from '@/api/automation'
import { describeTrigger, formatInZone, hasEndedOnceSchedule } from '@/utils/automationSchedule'
import { useAssistantDataChange } from '@/utils/assistantDataChange'
import AutomationEditor from './AutomationEditor.vue'
import AutomationDetail from './AutomationDetail.vue'
import AutomationStatusTag from './AutomationStatusTag.vue'
import ChatActionIcon from '@/components/ChatActionIcon.vue'
import ResourceIcon from '@/components/icons/ResourceIcon.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()

const automations = ref<Automation[]>([])
const loading = ref(true)
const query = ref('')
const busy = ref(new Set<string>())
const editorVisible = ref(false)
const editing = ref<Automation | null>(null)
const detailVisible = ref(false)
const detail = ref<Automation | null>(null)
const focusRunId = ref('')
let loadVersion = 0
let poll: ReturnType<typeof setTimeout> | undefined
let disposed = false

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return automations.value
  return automations.value.filter((a) =>
    a.name.toLowerCase().includes(q) || a.instruction.toLowerCase().includes(q))
})

function schedulePoll() {
  clearTimeout(poll)
  if (!disposed) poll = setTimeout(() => { void load(true) }, 5000)
}

async function load(silent = false) {
  if (disposed) return
  clearTimeout(poll)
  const version = ++loadVersion
  try {
    const res = await listAutomations()
    if (version !== loadVersion) return
    automations.value = res.data ?? []
    if (detail.value) {
      const fresh = automations.value.find(item => item.id === detail.value?.id)
      if (fresh) detail.value = fresh
      else {
        detailVisible.value = false
        detail.value = null
      }
    }
  } catch (e: any) {
    if (version === loadVersion && !silent) MessagePlugin.error(e?.message || t('common.operationFailed'))
  } finally {
    if (version === loadVersion) {
      loading.value = false
      schedulePoll()
    }
  }
}

useAssistantDataChange(() => { void load() }, { areas: ['automation'] })
onBeforeUnmount(() => {
  disposed = true
  ++loadVersion
  clearTimeout(poll)
})

function replace(item: Automation) {
  const index = automations.value.findIndex((a) => a.id === item.id)
  if (index >= 0) automations.value.splice(index, 1, item)
  else automations.value.unshift(item)
  if (detail.value?.id === item.id) detail.value = item
}

function onSaved(item: Automation) {
  replace(item)
  void load(true)
}

function openEditor(item: Automation | null) {
  editing.value = item
  detailVisible.value = false
  editorVisible.value = true
}

function openDetail(item: Automation, runId = '') {
  detail.value = item
  focusRunId.value = runId
  detailVisible.value = true
}

// Notifications link to /platform/automations/:id?run=…; the detail opens
// over the list and the URL drops back to the list.
async function openFromRoute() {
  const id = typeof route.params.automationId === 'string' ? route.params.automationId : ''
  if (!id) return
  const runId = typeof route.query.run === 'string' ? route.query.run : ''
  let item = automations.value.find((a) => a.id === id)
  if (!item) {
    try {
      item = (await getAutomation(id)).data
    } catch {
      MessagePlugin.warning(t('automation.messages.notFound'))
    }
  }
  if (item) openDetail(item, runId)
  const rest = { ...route.query }
  delete rest.run
  void router.replace({ name: 'automationList', query: rest })
}

watch(() => route.params.automationId, () => {
  if (!loading.value) void openFromRoute()
})

watch(detailVisible, (open) => {
  if (!open) focusRunId.value = ''
})

async function toggle(item: Automation) {
  if (busy.value.has(item.id)) return
  if (hasEndedOnceSchedule(item)) {
    MessagePlugin.warning(t('automation.scheduleEndedHint'))
    return
  }
  busy.value.add(item.id)
  try {
    const res = item.enabled ? await pauseAutomation(item.id) : await resumeAutomation(item.id)
    onSaved(res.data)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.operationFailed'))
  } finally {
    busy.value.delete(item.id)
  }
}

async function runNow(item: Automation) {
  if (busy.value.has(item.id)) return
  busy.value.add(item.id)
  try {
    const res = await runAutomationNow(item.id)
    MessagePlugin.success(t('automation.messages.queued'))
    openDetail(item, res.data?.id ?? '')
    void load(true)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.operationFailed'))
  } finally {
    busy.value.delete(item.id)
  }
}

async function remove(item: Automation) {
  if (busy.value.has(item.id)) return
  busy.value.add(item.id)
  try {
    await deleteAutomation(item.id)
    ++loadVersion
    schedulePoll()
    automations.value = automations.value.filter((a) => a.id !== item.id)
    if (detail.value?.id === item.id) detailVisible.value = false
    MessagePlugin.success(t('common.deleteSuccess'))
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.deleteFailed'))
  } finally {
    busy.value.delete(item.id)
  }
}

onMounted(async () => {
  await load()
  await openFromRoute()
})
</script>

<style scoped lang="less">
@import (reference) '@/components/css/provider-card.less';
@import (reference) '@/components/css/resource-card.less';

.automation-page {
  .resource-list-content();
  color: var(--td-text-color-primary);
}

.automation-page__header {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 16px;
  flex-shrink: 0;

  h2 {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0;
    font-family: var(--app-font-family);
    font-size: var(--app-text-4xl);
    font-weight: 600;
    line-height: 32px;
  }
}

.automation-page__title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--app-space-4);
}

.automation-page__tools {
  display: flex;
  align-items: center;
  gap: 8px;
}

.automation-page__search {
  width: 260px;
  max-width: 100%;

  :deep(.t-input) {
    border-radius: var(--app-radius-sm);
  }
}

.automation-page__add.t-button {
  flex-shrink: 0;
  min-height: 32px;
  padding: 0 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);

  :deep(.t-icon) {
    color: var(--td-text-color-secondary);
  }

  &:hover {
    background: var(--td-bg-color-container-hover);
    border-color: var(--td-component-stroke);
  }
}

.automation-page__subtitle {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-base);
  line-height: 20px;
}

.automation-page__main {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding-bottom: var(--app-space-8);
}

.loading-container {
  padding: 40px 0;
  text-align: center;
}

.empty-state {
  padding: 80px 0;
  text-align: center;
}

.automation-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 320px), 1fr));
  gap: 10px;
  align-items: stretch;
}

.automation-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  padding: 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  transition: background-color var(--app-motion-base) ease, box-shadow var(--app-motion-base) ease;

  &:hover:not(.automation-card--add) {
    box-shadow: 0 3px 12px color-mix(in srgb, var(--td-text-color-primary) 6%, transparent);
  }

  &.is-paused .automation-card__schedule {
    color: var(--td-text-color-placeholder);
  }

  &--add {
    align-items: center;
    justify-content: center;
    gap: 4px;
    min-height: 112px;
    border-style: dashed;
    background: transparent;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    font: inherit;
    text-align: center;

    &:hover,
    &:focus-visible {
      color: var(--td-text-color-primary);
      border-color: var(--td-component-border);
      background: color-mix(in srgb, var(--td-bg-color-secondarycontainer) 15%, var(--td-bg-color-container));
      box-shadow: 0 3px 12px color-mix(in srgb, var(--td-text-color-primary) 5%, transparent);
    }

    &__icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      border-radius: var(--app-radius-md);
      background: var(--td-bg-color-secondarycontainer);
      font-size: var(--app-text-2xl);
    }

    &__label {
      font-size: var(--app-text-md);
      font-weight: 500;
    }

    &__hint {
      font-size: var(--app-text-xs);
    }
  }
}

// The whole card opens the detail; the buttons on it sit above this layer.
.automation-card__open {
  position: absolute;
  inset: 0;
  border: 0;
  border-radius: inherit;
  background: transparent;
  cursor: pointer;
}

.automation-card__header,
.automation-card__footer,
.automation-card__schedule,
.automation-card__paused {
  position: relative;
  pointer-events: none;
}

.automation-card__actions,
.automation-card__status {
  pointer-events: auto;
}

.automation-card__header {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 28px;
}

.automation-card__badge {
  .provider-card-badge(28px, 8px);
}

.automation-card__title {
  flex: 1;
  min-width: 0;
  margin: 0;
  overflow: hidden;
  font-size: var(--app-text-base);
  font-weight: 600;
  line-height: 20px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.automation-card__actions {
  display: flex;
  gap: 2px;
}

.automation-card__icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: none;
  color: color-mix(in srgb, var(--td-text-color-primary) 55%, transparent);
  cursor: pointer;

  &:hover:not(:disabled) {
    color: var(--td-text-color-secondary);
    background: color-mix(in srgb, var(--td-text-color-primary) 5%, transparent);
  }

  &:active:not(:disabled) {
    background: color-mix(in srgb, var(--td-text-color-primary) 9%, transparent);
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.4;
  }
}

.automation-card__schedule,
.automation-card__paused {
  margin: 0;
  overflow: hidden;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-secondary);
}

.automation-card__paused {
  color: var(--td-warning-color);
}

.automation-card__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: auto;
}

.automation-card__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 8px;
  min-width: 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
}

.automation-card__status {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 4px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: none;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  font: inherit;
  font-size: var(--app-text-sm);

  &.is-enabled {
    color: var(--td-success-color);
  }

  &.is-ended { cursor: default; }

  &:hover:not(:disabled):not(.is-ended) {
    background: var(--td-bg-color-container-hover);
  }

  &:disabled {
    cursor: wait;
  }
}

.automation-card__dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
}

.automation-card button:focus-visible,
.automation-card--add:focus-visible {
  outline: 2px solid var(--td-text-color-secondary);
  outline-offset: -2px;
}
</style>
