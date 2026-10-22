<template>
  <SettingsModalShell :visible="visible"
    :title="automation ? t('automation.editor.editTitle') : t('automation.editor.createTitle')"
    v-model="section" :nav-groups="navGroups" :z-index="1000" @close="modalShell.requestClose">
    <div class="content-wrapper">
      <!-- 基本信息 -->
      <div v-show="section === 'basic'" class="section">
        <div class="section-header">
          <h2>{{ t('automation.editor.sections.basic.title') }}</h2>
          <p class="section-description">{{ t('automation.editor.sections.basic.desc') }}</p>
        </div>
        <div class="settings-group">
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.fields.name') }}<span class="required">*</span></label>
              <p class="desc">{{ t('automation.editor.nameDesc') }}</p>
            </div>
            <div class="setting-control">
              <t-input v-model="form.name" :maxlength="100" :placeholder="t('automation.editor.namePlaceholder')" />
            </div>
          </div>
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.editor.model') }}</label>
              <p class="desc">{{ t('automation.editor.modelHelp') }}</p>
            </div>
            <div class="setting-control">
              <ModelSelector v-model:selected-model-id="form.model_id" model-type="KnowledgeQA" clearable
                :placeholder="t('automation.editor.modelDefault')" @add-model="uiStore.openSettings('models', 'llm')" />
            </div>
          </div>
          <div class="setting-row setting-row-vertical">
            <div class="setting-info">
              <label>{{ t('automation.fields.instruction') }}<span class="required">*</span></label>
              <p class="desc">{{ t('automation.editor.unattendedHint') }}</p>
            </div>
            <div class="setting-control setting-control-full automation-editor__stack">
              <t-textarea v-model="form.instruction" :autosize="{ minRows: 6, maxRows: 16 }" :maxlength="20000"
                :placeholder="t('automation.editor.instructionPlaceholder')" />
              <p class="automation-editor__hint">
                {{ t('automation.editor.variablesHint') }}
                <code v-for="tag in VARIABLE_TAGS" :key="tag">{{ tag }}</code>
              </p>
            </div>
          </div>
        </div>
      </div>

      <!-- 运行时间 -->
      <div v-show="section === 'schedule'" class="section">
        <div class="section-header">
          <h2>{{ t('automation.editor.sections.schedule.title') }}</h2>
          <p class="section-description">{{ t('automation.editor.sections.schedule.desc') }}</p>
        </div>
        <div class="settings-group">
          <div class="setting-row setting-row-vertical">
            <div class="setting-info">
              <label>{{ t('automation.editor.frequency') }}<span class="required">*</span></label>
            </div>
            <div class="setting-control setting-control-full">
              <div class="automation-editor__frequency" role="group" :aria-label="t('automation.editor.frequency')">
                <label v-for="kind in KINDS" :key="kind" class="automation-editor__frequency-option">
                  <input v-model="schedule.kind" type="radio" name="automation-frequency" :value="kind" />
                  <span>{{ t(`automation.schedule.kinds.${kind}`) }}</span>
                </label>
              </div>
            </div>
          </div>

          <div v-if="schedule.kind === 'weekly'" class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.editor.weekdays') }}</label>
            </div>
            <div class="setting-control">
              <t-checkbox-group v-model="schedule.days" class="automation-editor__days">
                <t-checkbox v-for="day in WEEK" :key="day" :value="day">{{ weekdayName(day, locale) }}</t-checkbox>
              </t-checkbox-group>
            </div>
          </div>

          <div v-if="schedule.kind === 'monthly'" class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.schedule.dayOfMonth') }}</label>
              <p class="desc">{{ t('automation.editor.dayOfMonthDesc') }}</p>
            </div>
            <div class="setting-control">
              <t-input-number v-model="schedule.day" :min="1" :max="31" theme="column" />
            </div>
          </div>

          <div v-if="schedule.kind === 'hourly'" class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.schedule.atMinute') }}</label>
            </div>
            <div class="setting-control">
              <t-input-number v-model="schedule.minute" :min="0" :max="59" theme="column" />
            </div>
          </div>

          <div v-else-if="schedule.kind === 'once'" class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.editor.runAt') }}</label>
            </div>
            <div class="setting-control">
              <t-date-picker v-model="schedule.at" enable-time-picker format="YYYY-MM-DD HH:mm"
                value-type="YYYY-MM-DD HH:mm" :placeholder="t('automation.schedule.pickTime')" />
            </div>
          </div>

          <div v-else-if="schedule.kind === 'custom'" class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.editor.cron') }}</label>
              <p class="desc">{{ t('automation.schedule.customHint') }}</p>
            </div>
            <div class="setting-control">
              <t-input v-model="schedule.expr" class="automation-editor__mono" placeholder="0 9 * * 1-5" />
            </div>
          </div>

          <div v-else class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.editor.timeOfDay') }}</label>
            </div>
            <div class="setting-control">
              <t-time-picker v-model="clock" format="HH:mm" :clearable="false" />
            </div>
          </div>

          <div class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.fields.timezone') }}</label>
              <p class="desc">{{ t('automation.editor.timezoneDesc') }}</p>
            </div>
            <div class="setting-control">
              <t-select v-model="timezone" filterable>
                <t-option v-for="zone in zones" :key="zone" :value="zone" :label="zone" />
              </t-select>
            </div>
          </div>

          <div class="setting-row setting-row-vertical">
            <div class="setting-info">
              <label>{{ t('automation.schedule.nextRuns') }}</label>
            </div>
            <div class="setting-control setting-control-full">
              <div class="automation-editor__preview" :class="{ 'is-error': previewError }" aria-live="polite">
                <template v-if="previewError">
                  <t-icon name="error-circle" size="16px" /> {{ previewError }}
                </template>
                <ul v-else-if="previewRuns.length" class="automation-editor__runs">
                  <li v-for="run in previewRuns" :key="run">
                    <t-icon name="time" size="14px" /> {{ formatInZone(run, timezone, locale) }}
                  </li>
                </ul>
                <span v-else-if="previewLoading">{{ t('common.loading') }}</span>
                <span v-else class="automation-editor__muted">{{ t('automation.editor.previewEmpty') }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 执行方式 -->
      <div v-show="section === 'execution'" class="section">
        <div class="section-header">
          <h2>{{ t('automation.editor.sections.execution.title') }}</h2>
          <p class="section-description">{{ t('automation.editor.sections.execution.desc') }}</p>
        </div>
        <div class="settings-group">
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.fields.accessLevel') }}</label>
              <p class="desc">{{ t(`automation.editor.access.${form.access_level}`) }}</p>
            </div>
            <div class="setting-control">
              <t-select v-model="form.access_level">
                <t-option v-for="level in ASSISTANT_ACCESS_LEVELS" :key="level" :value="level"
                  :label="t(`assistant.access.levels.${level}.label`)" />
              </t-select>
            </div>
          </div>
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.fields.sessionMode') }}</label>
              <p class="desc">{{ t(`automation.editor.sessionModeDesc.${form.session_mode}`) }}</p>
            </div>
            <div class="setting-control">
              <t-select v-model="form.session_mode">
                <t-option value="per_run" :label="t('automation.sessionMode.per_run')" />
                <t-option value="persistent" :label="t('automation.sessionMode.persistent')" />
              </t-select>
            </div>
          </div>
          <div v-if="form.session_mode === 'per_run'" class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.fields.carryLastResult') }}</label>
              <p class="desc">{{ t('automation.editor.carryDesc') }}</p>
            </div>
            <div class="setting-control">
              <t-switch v-model="form.carry_last_result" />
            </div>
          </div>
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.fields.overlapPolicy') }}</label>
            </div>
            <div class="setting-control">
              <t-select v-model="form.overlap_policy">
                <t-option value="skip" :label="t('automation.overlap.skip')" />
                <t-option value="queue" :label="t('automation.overlap.queue')" />
              </t-select>
            </div>
          </div>
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.fields.misfirePolicy') }}</label>
              <p class="desc">{{ t('automation.editor.misfireDesc') }}</p>
            </div>
            <div class="setting-control">
              <t-select v-model="form.misfire_policy">
                <t-option value="skip" :label="t('automation.misfire.skip')" />
                <t-option value="run_once" :label="t('automation.misfire.run_once')" />
              </t-select>
            </div>
          </div>
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.fields.maxDuration') }}</label>
              <p class="desc">{{ t('automation.editor.maxDurationDesc') }}</p>
            </div>
            <div class="setting-control">
              <div class="automation-editor__number">
                <t-input-number v-model="maxMinutes" :min="1" :max="240" :step="1" :decimal-places="0"
                  theme="row" :allow-input-over-limit="false" :aria-label="t('automation.fields.maxDuration')" />
                <span>{{ t('automation.units.minutes') }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 审批与通知 -->
      <div v-show="section === 'notify'" class="section">
        <div class="section-header">
          <h2>{{ t('automation.editor.sections.notify.title') }}</h2>
          <p class="section-description">{{ t('automation.editor.sections.notify.desc') }}</p>
        </div>
        <div class="settings-group">
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.fields.approvalTimeout') }}</label>
              <p class="desc">{{ t('automation.editor.approvalTimeoutHelp') }}</p>
            </div>
            <div class="setting-control">
              <div class="automation-editor__number">
                <t-input-number v-model="approvalHours" :min="1" :max="168" :step="1" :decimal-places="0"
                  theme="row" :allow-input-over-limit="false" :aria-label="t('automation.fields.approvalTimeout')" />
                <span>{{ t('automation.units.hours') }}</span>
              </div>
            </div>
          </div>
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ t('automation.fields.notifyOnSuccess') }}</label>
              <p class="desc">{{ t('automation.editor.notifyHint') }}</p>
            </div>
            <div class="setting-control">
              <t-switch v-model="form.notify_on_success" />
            </div>
          </div>
        </div>
      </div>
    </div>

    <template #footer-note>
      <p v-if="previewRuns.length && !previewError" class="automation-editor__next-run">
        <t-icon name="time" size="14px" />
        <span>{{ t('automation.fields.nextRun') }}</span>
        <time :datetime="previewRuns[0]">{{ formatInZone(previewRuns[0], timezone, locale) }}</time>
      </p>
    </template>
    <template #footer>
      <t-button theme="default" variant="outline" @click="modalShell.requestClose">{{ t('common.cancel') }}</t-button>
      <t-button theme="primary" :loading="saving" @click="save">
        {{ automation ? t('common.save') : t('common.create') }}
      </t-button>
    </template>
  </SettingsModalShell>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import SettingsModalShell from '@/components/SettingsModalShell.vue'
import { useModalShell } from '@/composables/useModalShell'
import {
  createAutomation,
  updateAutomation,
  validateAutomationTrigger,
  type Automation,
  type AutomationInput,
} from '@/api/automation'
import { ASSISTANT_ACCESS_LEVELS } from '@/api/assistant'
import {
  browserTimezone,
  defaultScheduleForm,
  formatInZone,
  scheduleFormFromTrigger,
  timezoneOptions,
  triggerFromScheduleForm,
  weekdayName,
  type ScheduleKind,
} from '@/utils/automationSchedule'
import ModelSelector from '@/components/ModelSelector.vue'
import { useUIStore } from '@/stores/ui'

const props = defineProps<{
  visible: boolean
  automation: Automation | null
}>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; saved: [value: Automation] }>()

const { t, locale } = useI18n()
const uiStore = useUIStore()

type Section = 'basic' | 'schedule' | 'execution' | 'notify'

const VARIABLE_TAGS = ['now', 'last_run_at', 'last_success_at', 'run_id', 'automation_name']
  .map((name) => `{{${name}}}`)
const KINDS: ScheduleKind[] = ['daily', 'weekdays', 'weekly', 'monthly', 'hourly', 'once', 'custom']
const WEEK = [1, 2, 3, 4, 5, 6, 0]

const section = ref<Section>('basic')
const navGroups = computed(() => [
  {
    key: 'task',
    label: t('automation.editor.navGroups.task'),
    items: [
      { key: 'basic', label: t('automation.editor.sections.basic.title'), icon: 'edit-1' },
      { key: 'schedule', label: t('automation.editor.sections.schedule.title'), icon: 'time' },
    ],
  },
  {
    key: 'run',
    label: t('automation.editor.navGroups.run'),
    items: [
      { key: 'execution', label: t('automation.editor.sections.execution.title'), icon: 'secured' },
      { key: 'notify', label: t('automation.editor.sections.notify.title'), icon: 'notification' },
    ],
  },
])

const blank = (): Required<Omit<AutomationInput, 'trigger' | 'limits' | 'description'>> => ({
  name: '',
  model_id: '',
  instruction: '',
  session_mode: 'per_run',
  carry_last_result: false,
  misfire_policy: 'skip',
  overlap_policy: 'skip',
  access_level: 'confirm',
  approval_timeout_minutes: 1440,
  notify_on_success: false,
})

const form = reactive(blank())
const schedule = reactive(defaultScheduleForm())
const timezone = ref(browserTimezone())
const maxMinutes = ref(30)
const saving = ref(false)

const zones = computed(() => timezoneOptions(timezone.value))

const approvalHours = computed({
  get: () => Math.max(1, Math.round(form.approval_timeout_minutes / 60)),
  set: (v: number | undefined) => {
    if (v === undefined || !Number.isFinite(v)) return
    form.approval_timeout_minutes = Math.min(168, Math.max(1, Math.round(v))) * 60
  },
})

const clock = computed({
  get: () => `${String(schedule.hour).padStart(2, '0')}:${String(schedule.minute).padStart(2, '0')}`,
  set: (v: string) => {
    const [h, m] = String(v || '').split(':').map(Number)
    if (Number.isFinite(h)) schedule.hour = h
    if (Number.isFinite(m)) schedule.minute = m
  },
})

const trigger = computed(() => triggerFromScheduleForm(schedule, timezone.value))

function close() {
  emit('update:visible', false)
}

const modalShell = useModalShell({
  visible: () => props.visible,
  close,
  snapshot: () => ({ form, schedule, timezone: timezone.value, maxMinutes: maxMinutes.value }),
})

watch(() => props.visible, (open) => {
  if (!open) return
  const a = props.automation
  section.value = 'basic'
  Object.assign(form, blank())
  Object.assign(schedule, defaultScheduleForm())
  timezone.value = browserTimezone()
  maxMinutes.value = 30
  if (a) {
    Object.assign(form, {
      name: a.name,
      model_id: a.model_id || '',
      instruction: a.instruction,
      session_mode: a.session_mode,
      carry_last_result: a.carry_last_result,
      misfire_policy: a.misfire_policy,
      overlap_policy: a.overlap_policy,
      access_level: a.access_level,
      approval_timeout_minutes: a.approval_timeout_minutes,
      notify_on_success: a.notify_on_success,
    })
    Object.assign(schedule, scheduleFormFromTrigger(a.trigger))
    timezone.value = a.trigger.timezone || 'UTC'
    maxMinutes.value = a.limits?.max_duration_minutes || 30
  }
  previewRuns.value = []
  previewError.value = ''
  modalShell.markClean()
}, { immediate: true })

const previewRuns = ref<string[]>([])
const previewError = ref('')
const previewLoading = ref(false)
let previewSeq = 0
let previewTimer: ReturnType<typeof setTimeout> | undefined

watch([trigger, () => props.visible], () => {
  clearTimeout(previewTimer)
  const current = trigger.value
  previewRuns.value = []
  previewError.value = ''
  if (!props.visible || !current) return
  previewLoading.value = true
  const seq = ++previewSeq
  previewTimer = setTimeout(async () => {
    try {
      const res = await validateAutomationTrigger(current)
      if (seq === previewSeq) previewRuns.value = res.data?.next_runs ?? []
    } catch (e: any) {
      if (seq === previewSeq) previewError.value = e?.message || t('automation.schedule.invalid')
    } finally {
      if (seq === previewSeq) previewLoading.value = false
    }
  }, 400)
}, { deep: true })

// The first section with a problem, with what to tell the user.
function firstProblem(): [Section, string] | null {
  if (!form.name.trim()) return ['basic', t('automation.editor.errors.name')]
  if (!form.instruction.trim()) return ['basic', t('automation.editor.errors.instruction')]
  if (!trigger.value || previewError.value) return ['schedule', previewError.value || t('automation.editor.errors.schedule')]
  return null
}

async function save() {
  const problem = firstProblem()
  if (problem) {
    section.value = problem[0]
    MessagePlugin.warning(problem[1])
    return
  }
  saving.value = true
  const input: AutomationInput = {
    ...form,
    name: form.name.trim(),
    trigger: trigger.value!,
    limits: { max_duration_minutes: maxMinutes.value },
  }
  try {
    const res = props.automation
      ? await updateAutomation(props.automation.id, input)
      : await createAutomation(input)
    MessagePlugin.success(props.automation ? t('common.saveSuccess') : t('common.createSuccess'))
    modalShell.markClean()
    emit('saved', res.data)
    close()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.saveFailed'))
  } finally {
    saving.value = false
  }
}
</script>

<style scoped lang="less">
.automation-editor__next-run {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 6px;
  min-width: 0;
  margin: 0 auto 0 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  line-height: 1.6;

  > .t-icon { flex-shrink: 0; }
  > span { white-space: nowrap; }
  time { font-variant-numeric: tabular-nums; }
}

.content-wrapper {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
  padding: 28px 40px 48px;
  box-sizing: border-box;
}

.section {
  width: 100%;
}

.section-header {
  margin-bottom: 20px;

  h2 {
    margin: 0 0 6px;
    color: var(--td-text-color-primary);
    font-size: var(--app-text-3xl);
    font-weight: 600;
  }

  .section-description {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-base);
    line-height: 1.5;
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  min-width: 0;
  padding: 16px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }

  &.setting-row-vertical {
    flex-direction: column;
    gap: 12px;

    .setting-info {
      flex: none;
      max-width: 100%;
    }
  }
}

.setting-info {
  flex: 0 0 42%;
  max-width: 42%;
  min-width: 0;

  label {
    display: block;
    margin-bottom: 4px;
    color: var(--td-text-color-primary);
    font-size: var(--app-text-lg);
    font-weight: 500;

    .required {
      margin-left: 2px;
      color: var(--td-error-color);
    }
  }

  .desc {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);
    line-height: 1.5;
  }
}

.setting-control {
  display: flex;
  flex: 1 1 58%;
  justify-content: flex-end;
  align-items: flex-start;
  min-width: 0;
  max-width: 58%;

  > :deep(.t-select__wrap),
  > :deep(.t-input__wrap),
  > :deep(.t-date-picker) {
    width: 100%;
    max-width: 320px;
  }

  &.setting-control-full {
    width: 100%;
    max-width: 100%;
    justify-content: flex-start;
  }
}

.automation-editor__stack {
  flex-direction: column;
  gap: 8px;
}

.automation-editor__hint {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  line-height: 1.8;

  code {
    margin: 0 2px;
    padding: 1px 6px;
    border-radius: var(--app-radius-xs);
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-xs);
  }
}

.automation-editor__frequency {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.automation-editor__frequency-option {
  position: relative;
  cursor: pointer;

  input {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    margin: 0;
    opacity: 0;
    cursor: pointer;
  }

  span {
    display: block;
    padding: 8px 16px;
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-pill);
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-base);
    transition: background-color var(--app-motion-fast) ease, border-color var(--app-motion-fast) ease;
  }

  &:hover span { background: var(--td-bg-color-container-hover); }
  input:checked + span {
    color: var(--td-brand-color);
    border-color: color-mix(in srgb, var(--td-brand-color) 40%, var(--td-component-stroke));
    background: color-mix(in srgb, var(--td-brand-color) 7%, var(--td-bg-color-container));
    font-weight: 500;
  }
  input:focus-visible + span {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 3px;
  }
}

.automation-editor__number {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--td-text-color-secondary);
  white-space: nowrap;

  :deep(.t-input-number) {
    width: 160px;
    flex: none;
  }
}

.automation-editor__days {
  flex-wrap: wrap;
  justify-content: flex-end;
  row-gap: 4px;
}

.automation-editor__mono :deep(input) {
  font-family: var(--app-font-family-mono);
}

.automation-editor__preview {
  width: 100%;
  padding: 12px 16px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-base);

  &.is-error {
    display: flex;
    align-items: center;
    gap: 6px;
    background: var(--td-error-color-light);
    color: var(--td-error-color);
  }
}

.automation-editor__runs {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
  font-variant-numeric: tabular-nums;

  li {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  :deep(.t-icon) {
    color: var(--td-brand-color);
  }
}

.automation-editor__muted {
  color: var(--td-text-color-placeholder);
}
</style>
