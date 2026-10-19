<template>
  <t-drawer :visible="visible" :header="automation ? t('automation.editor.editTitle') : t('automation.editor.createTitle')"
    size="560px" :close-on-overlay-click="false" :footer="true" destroy-on-close @close="close">
    <t-form label-align="top" class="automation-editor" @submit.prevent>
      <t-form-item :label="t('automation.fields.name')" required-mark>
        <t-input v-model="form.name" :maxlength="100" :placeholder="t('automation.editor.namePlaceholder')" />
      </t-form-item>

      <t-form-item :label="t('automation.fields.agent')">
        <t-select v-model="form.agent_id" :loading="agentsLoading" filterable>
          <t-option v-for="agent in agentOptions" :key="agent.id" :value="agent.id" :label="agent.name" />
        </t-select>
      </t-form-item>

      <t-form-item :label="t('automation.fields.instruction')" required-mark>
        <div class="automation-editor__stack">
          <t-textarea v-model="form.instruction" :autosize="{ minRows: 4, maxRows: 12 }" :maxlength="20000"
            :placeholder="t('automation.editor.instructionPlaceholder')" />
          <p class="automation-editor__hint">
            {{ t('automation.editor.variablesHint') }}
            <code v-for="tag in VARIABLE_TAGS" :key="tag">{{ tag }}</code>
          </p>
          <p class="automation-editor__hint">{{ t('automation.editor.unattendedHint') }}</p>
        </div>
      </t-form-item>

      <t-form-item :label="t('automation.fields.schedule')" required-mark>
        <div class="automation-editor__stack">
          <t-radio-group v-model="schedule.kind" variant="default-filled" size="small" class="automation-editor__kinds">
            <t-radio-button v-for="kind in KINDS" :key="kind" :value="kind">
              {{ t(`automation.schedule.kinds.${kind}`) }}
            </t-radio-button>
          </t-radio-group>

          <div class="automation-editor__row">
            <template v-if="schedule.kind === 'hourly'">
              <span>{{ t('automation.schedule.atMinute') }}</span>
              <t-input-number v-model="schedule.minute" :min="0" :max="59" theme="normal" class="automation-editor__num" />
            </template>
            <template v-else-if="schedule.kind === 'custom'">
              <t-input v-model="schedule.expr" class="automation-editor__expr" placeholder="0 9 * * 1-5" />
            </template>
            <template v-else-if="schedule.kind === 'once'">
              <t-date-picker v-model="schedule.at" enable-time-picker format="YYYY-MM-DD HH:mm"
                value-type="YYYY-MM-DD HH:mm" :placeholder="t('automation.schedule.pickTime')" />
            </template>
            <template v-else>
              <template v-if="schedule.kind === 'monthly'">
                <span>{{ t('automation.schedule.dayOfMonth') }}</span>
                <t-input-number v-model="schedule.day" :min="1" :max="31" theme="normal" class="automation-editor__num" />
              </template>
              <t-time-picker v-model="clock" format="HH:mm" :clearable="false" class="automation-editor__clock" />
            </template>
          </div>

          <t-checkbox-group v-if="schedule.kind === 'weekly'" v-model="schedule.days">
            <t-checkbox v-for="day in WEEK" :key="day" :value="day">{{ weekdayName(day, locale) }}</t-checkbox>
          </t-checkbox-group>
          <p v-if="schedule.kind === 'custom'" class="automation-editor__hint">{{ t('automation.schedule.customHint') }}</p>

          <div class="automation-editor__row">
            <span>{{ t('automation.fields.timezone') }}</span>
            <t-select v-model="timezone" filterable class="automation-editor__tz">
              <t-option v-for="zone in zones" :key="zone" :value="zone" :label="zone" />
            </t-select>
          </div>

          <div class="automation-editor__preview" :class="{ 'is-error': previewError }" aria-live="polite">
            <template v-if="previewError">
              <t-icon name="error-circle" size="14px" /> {{ previewError }}
            </template>
            <template v-else-if="previewRuns.length">
              <span>{{ t('automation.schedule.nextRuns') }}</span>
              <span v-for="run in previewRuns" :key="run" class="automation-editor__chip">
                {{ formatInZone(run, timezone, locale) }}
              </span>
            </template>
            <template v-else-if="previewLoading">{{ t('common.loading') }}</template>
          </div>
        </div>
      </t-form-item>

      <t-form-item :label="t('automation.fields.accessLevel')">
        <div class="automation-editor__stack">
          <t-radio-group v-model="form.access_level">
            <t-radio v-for="level in ACCESS_LEVELS" :key="level" :value="level">
              {{ t(`assistant.access.levels.${level}.label`) }}
            </t-radio>
          </t-radio-group>
          <p class="automation-editor__hint">{{ t(`automation.editor.access.${form.access_level}`) }}</p>
        </div>
      </t-form-item>

      <t-collapse :borderless="true" class="automation-editor__advanced">
        <t-collapse-panel :header="t('automation.editor.advanced')">
          <t-form-item :label="t('automation.fields.sessionMode')">
            <div class="automation-editor__stack">
              <t-radio-group v-model="form.session_mode">
                <t-radio value="per_run">{{ t('automation.sessionMode.per_run') }}</t-radio>
                <t-radio value="persistent">{{ t('automation.sessionMode.persistent') }}</t-radio>
              </t-radio-group>
              <t-checkbox v-if="form.session_mode === 'per_run'" v-model="form.carry_last_result">
                {{ t('automation.fields.carryLastResult') }}
              </t-checkbox>
            </div>
          </t-form-item>
          <t-form-item :label="t('automation.fields.overlapPolicy')">
            <t-select v-model="form.overlap_policy">
              <t-option value="skip" :label="t('automation.overlap.skip')" />
              <t-option value="queue" :label="t('automation.overlap.queue')" />
            </t-select>
          </t-form-item>
          <t-form-item :label="t('automation.fields.misfirePolicy')">
            <t-select v-model="form.misfire_policy">
              <t-option value="skip" :label="t('automation.misfire.skip')" />
              <t-option value="run_once" :label="t('automation.misfire.run_once')" />
            </t-select>
          </t-form-item>
          <t-form-item :label="t('automation.fields.approvalTimeout')"
            :help="t('automation.editor.approvalTimeoutHelp')">
            <t-input-number v-model="approvalHours" :min="1" :max="168" theme="normal" :suffix="t('automation.units.hours')" />
          </t-form-item>
          <t-form-item :label="t('automation.fields.maxDuration')">
            <t-input-number v-model="maxMinutes" :min="1" :max="240" theme="normal" :suffix="t('automation.units.minutes')" />
          </t-form-item>
          <t-form-item :label="t('automation.fields.notifyOnSuccess')">
            <div class="automation-editor__stack">
              <t-switch v-model="form.notify_on_success" />
              <p class="automation-editor__hint">{{ t('automation.editor.notifyHint') }}</p>
            </div>
          </t-form-item>
        </t-collapse-panel>
      </t-collapse>
    </t-form>

    <template #footer>
      <div class="automation-editor__footer">
        <t-button variant="outline" @click="close">{{ t('common.cancel') }}</t-button>
        <t-button theme="primary" :loading="saving" :disabled="!canSave" @click="save">
          {{ automation ? t('common.save') : t('common.create') }}
        </t-button>
      </div>
    </template>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
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
import type { AutomationAgentOption } from './agents'

const props = defineProps<{
  visible: boolean
  automation: Automation | null
  agents: AutomationAgentOption[]
  agentsLoading?: boolean
  defaultAgentId: string
}>()
const emit = defineEmits<{ 'update:visible': [value: boolean]; saved: [value: Automation] }>()

const { t, locale } = useI18n()

const VARIABLE_TAGS = ['now', 'last_run_at', 'last_success_at', 'run_id', 'automation_name']
  .map((name) => `{{${name}}}`)
const KINDS: ScheduleKind[] = ['daily', 'weekdays', 'weekly', 'monthly', 'hourly', 'once', 'custom']
const WEEK = [1, 2, 3, 4, 5, 6, 0]
const ACCESS_LEVELS = ASSISTANT_ACCESS_LEVELS

const blank = (): Required<Omit<AutomationInput, 'trigger' | 'limits' | 'description'>> => ({
  name: '',
  agent_id: props.defaultAgentId,
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

const agentOptions = computed(() => {
  const list = [...props.agents]
  if (form.agent_id && !list.some((a) => a.id === form.agent_id)) {
    list.push({ id: form.agent_id, name: t('automation.agentUnavailable') })
  }
  return list
})

const approvalHours = computed({
  get: () => Math.max(1, Math.round(form.approval_timeout_minutes / 60)),
  set: (v: number) => { form.approval_timeout_minutes = Math.max(1, Number(v) || 1) * 60 },
})

const clock = computed({
  get: () => `${String(schedule.hour).padStart(2, '0')}:${String(schedule.minute).padStart(2, '0')}`,
  set: (v: string) => {
    const [h, m] = String(v || '').split(':').map(Number)
    if (Number.isFinite(h)) schedule.hour = h
    if (Number.isFinite(m)) schedule.minute = m
  },
})

watch(() => props.visible, (open) => {
  if (!open) return
  const a = props.automation
  Object.assign(form, blank())
  Object.assign(schedule, defaultScheduleForm())
  timezone.value = browserTimezone()
  maxMinutes.value = 30
  if (a) {
    Object.assign(form, {
      name: a.name,
      agent_id: a.agent_id,
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
}, { immediate: true })

const trigger = computed(() => triggerFromScheduleForm(schedule, timezone.value))

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

const canSave = computed(() => !!form.name.trim() && !!form.instruction.trim() && !!trigger.value && !previewError.value)

function close() {
  emit('update:visible', false)
}

async function save() {
  if (!canSave.value || !trigger.value) return
  saving.value = true
  const input: AutomationInput = {
    ...form,
    name: form.name.trim(),
    trigger: trigger.value,
    limits: { max_duration_minutes: maxMinutes.value },
  }
  try {
    const res = props.automation
      ? await updateAutomation(props.automation.id, input)
      : await createAutomation(input)
    MessagePlugin.success(props.automation ? t('common.saveSuccess') : t('common.createSuccess'))
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
.automation-editor {
  :deep(.t-form__item) {
    margin-bottom: var(--app-space-4);
  }
}

.automation-editor__stack {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.automation-editor__row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
}

.automation-editor__kinds {
  flex-wrap: wrap;
}

.automation-editor__num {
  width: 96px;
}

.automation-editor__clock {
  width: 120px;
}

.automation-editor__expr {
  max-width: 280px;
  font-family: var(--app-font-mono, monospace);
}

.automation-editor__tz {
  flex: 1;
  min-width: 200px;
}

.automation-editor__hint {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  line-height: 1.5;

  code {
    margin: 0 2px;
    padding: 0 4px;
    border-radius: var(--app-radius-xs);
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
  }
}

.automation-editor__preview {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  min-height: 22px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);

  &.is-error {
    color: var(--td-error-color);
  }
}

.automation-editor__chip {
  padding: 1px 8px;
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-secondarycontainer);
  font-variant-numeric: tabular-nums;
}

.automation-editor__advanced {
  :deep(.t-collapse-panel__wrapper .t-collapse-panel__content) {
    padding: var(--app-space-3) 0 0;
  }
}

.automation-editor__footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
