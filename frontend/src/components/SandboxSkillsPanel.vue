<template>
  <div class="sandbox-skills-panel">
    <t-loading :loading="loading" size="small">
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.imageInfoTitle') }}</h4>
        <p v-if="!hasSkillSnapshot" class="image-info-note">
          {{ $t('settings.sandbox.imageInfoUsingBase') }}
        </p>
        <ul class="image-info">
          <template v-if="hasSkillSnapshot">
            <li>
              <span class="image-info__label">{{ $t('settings.sandbox.imageInfoBaseTemplate') }}</span>
              <span class="image-info__value image-info__value--id">
                {{ skillImage?.base_template_id || runtimeTemplateId || $t('settings.sandbox.imageInfoUnset') }}
              </span>
            </li>
            <li>
              <span class="image-info__label">{{ $t('settings.sandbox.imageInfoSnapshot') }}</span>
              <span class="image-info__value image-info__value--id">{{ skillImage?.snapshot_id }}</span>
            </li>
            <li>
              <span class="image-info__label">{{ $t('settings.sandbox.imageInfoGeneration') }}</span>
              <span class="image-info__value">
                {{ skillImage?.generation ? String(skillImage.generation) : $t('settings.sandbox.imageInfoUnset') }}
              </span>
            </li>
            <li>
              <span class="image-info__label">{{ $t('settings.sandbox.imageInfoBuiltAt') }}</span>
              <span class="image-info__value">{{ formatBuiltAt(skillImage?.built_at) }}</span>
            </li>
          </template>
          <li v-else>
            <span class="image-info__label">{{ $t('settings.sandbox.imageInfoRuntimeTemplate') }}</span>
            <span class="image-info__value image-info__value--id">
              {{ runtimeTemplateId || $t('settings.sandbox.imageInfoUnset') }}
            </span>
          </li>
        </ul>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillInstallerModel') }}</h4>
        <p class="installer-model-hint">{{ $t('settings.sandbox.skillInstallerModelHint') }}</p>
        <ModelSelector
          model-type="KnowledgeQA"
          :selected-model-id="installerModelId"
          :disabled="savingInstallerModel"
          @update:selected-model-id="onInstallerModelChange"
        />
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillRollout') }}</h4>
        <p class="installer-model-hint">{{ $t('settings.sandbox.skillRolloutHint') }}</p>
        <t-radio-group
          :value="skillRollout"
          :disabled="savingRollout"
          class="skill-rollout-group"
          @change="onSkillRolloutChange"
        >
          <t-radio value="next_turn">{{ $t('settings.sandbox.skillRolloutNextTurn') }}</t-radio>
          <t-radio value="new_session">{{ $t('settings.sandbox.skillRolloutNewSession') }}</t-radio>
        </t-radio-group>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillInstallGroup') }}</h4>
        <t-input-adornment class="skill-source-row">
          <t-input
            v-model="sourceInput"
            :placeholder="$t('settings.sandbox.skillSourcePlaceholder')"
            :disabled="installBusy"
            @enter="installFromSource"
          />
          <template #append>
            <t-button
              theme="primary"
              :loading="installingFromSource"
              :disabled="!sourceInput.trim() || uploading"
              @click="installFromSource"
            >
              {{ $t('settings.sandbox.skillSourceInstall') }}
            </t-button>
          </template>
        </t-input-adornment>
        <div class="skill-install-split">
          <span>{{ $t('settings.sandbox.skillInstallOr') }}</span>
        </div>
        <input
          ref="fileInputRef"
          type="file"
          accept=".zip,application/zip"
          class="file-input-hidden"
          @change="onFileInputChange"
        />
        <div
          class="file-upload-area"
          :class="{ 'has-file': uploading, 'is-disabled': installBusy }"
          @click="!installBusy && fileInputRef?.click()"
          @dragover.prevent
          @dragenter.prevent
          @drop.prevent="onFileDrop"
        >
          <div class="file-upload-content">
            <t-icon name="upload" size="18px" class="upload-icon" />
            <div class="upload-text">
              <span v-if="uploading" class="upload-file-name">
                {{ $t('settings.sandbox.skillUploading', { percent: uploadPercent }) }}
              </span>
              <template v-else>
                <span class="upload-primary-text">{{ $t('settings.sandbox.skillUploadClick') }}</span>
                <span class="upload-secondary-text">{{ $t('settings.sandbox.skillUploadDrag') }}</span>
              </template>
            </div>
            <t-progress v-if="uploading" :percentage="uploadPercent" size="small" />
          </div>
        </div>
        <p class="upload-hint">{{ uploadHint }}</p>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillInstalledGroup') }}</h4>
        <p v-if="!loading && skills.length === 0" class="skill-empty">
          {{ $t('settings.sandbox.skillEmpty') }}
        </p>

        <ul class="skill-list">
          <li
            v-for="skill in skills"
            :key="skill.id"
            :ref="(el) => bindSkillItem(skill.id, el)"
            class="skill-item"
            :class="{ 'skill-item--focused': focusedSkillId === skill.id }"
          >
            <div class="skill-status-ring" :title="statusLabel(skill)">
              <!-- The percentage is spelled out in the meta line below, so the
                   ring only has to show proportion: its own label is two digits
                   crammed into 16px. The default 6px stroke is most of the
                   radius at this size, which reads as a blob rather than a ring. -->
              <t-progress
                v-if="isBusy(skill)"
                theme="circle"
                :percentage="progressOf(skill)"
                :size="16"
                :stroke-width="2"
                :label="false"
              />
              <t-icon
                v-else-if="skill.status === 'failed'"
                name="close-circle-filled"
                size="16px"
                class="skill-status-ring__failed"
              />
              <t-icon
                v-else
                name="check-circle-filled"
                size="16px"
                class="skill-status-ring__ready"
              />
            </div>
            <div class="skill-item__body">
              <div class="skill-item__header">
                <div class="skill-item__heading">
                  <div class="skill-item__title">{{ skill.name || skill.id }}</div>
                  <p class="skill-item__meta">
                    <span v-if="skill.version">{{ skill.version }} · </span>
                    <span>{{ statusLabel(skill) }}</span>
                    <span v-if="isBusy(skill)"> · {{ progressOf(skill) }}%</span>
                    <span v-if="isBusy(skill) && progressLog(skill)"> · {{ progressLog(skill) }}</span>
                  </p>
                </div>
                <div class="skill-item__actions">
                  <t-tooltip :content="$t('settings.sandbox.skillDisableHint')" placement="top">
                    <t-switch
                      size="small"
                      :value="skill.enabled"
                      :disabled="isBusy(skill)"
                      :loading="togglingId === skill.id"
                      @change="(v: any) => toggleEnabled(skill, Boolean(v))"
                    />
                  </t-tooltip>
                  <span class="skill-item__actions-divider" />
                  <t-tooltip :content="$t('settings.sandbox.skillFiles')" placement="top">
                    <button
                      type="button"
                      class="skill-item__icon-btn"
                      :class="{ 'is-on': filesDrawerVisible && filesSkillId === skill.id }"
                      :aria-label="$t('settings.sandbox.skillFiles')"
                      @click="openSkillFiles(skill)"
                    >
                      <t-icon name="folder" size="16px" />
                    </button>
                  </t-tooltip>
                  <t-tooltip
                    v-if="hasTranscript(skill) || isBusy(skill)"
                    :content="isBusy(skill)
                      ? $t('settings.sandbox.skillTranscriptLiveHint')
                      : $t('settings.sandbox.skillTranscript')"
                    placement="top"
                  >
                    <t-popup
                      v-if="hasTranscript(skill)"
                      :visible="expandedSkillId === skill.id"
                      trigger="click"
                      placement="bottom-right"
                      attach="body"
                      destroy-on-close
                      overlay-class-name="skill-transcript-popup"
                      :z-index="3200"
                      :overlay-inner-style="{ padding: '0' }"
                      @visible-change="(visible: boolean) => onTranscriptVisible(skill, visible)"
                    >
                      <button
                        type="button"
                        class="skill-item__icon-btn"
                        :class="{
                          'is-on': expandedSkillId === skill.id,
                          'is-live': isBusy(skill),
                          'is-live-chip': isBusy(skill),
                        }"
                        :aria-label="$t('settings.sandbox.skillTranscript')"
                      >
                        <span v-if="isBusy(skill)" class="skill-item__live-dot" aria-hidden="true" />
                        <t-icon name="chat-bubble-history" size="16px" />
                        <span v-if="isBusy(skill)" class="skill-item__live-label">
                          {{ $t('settings.sandbox.skillTranscriptLive') }}
                        </span>
                      </button>
                      <template #content>
                        <div class="skill-transcript-popup__panel">
                          <header class="skill-transcript-popup__head">
                            <div class="skill-transcript-popup__head-text">
                              <div class="skill-transcript-popup__title">{{ skill.name || skill.id }}</div>
                              <div class="skill-transcript-popup__meta">
                                <span
                                  class="skill-transcript-popup__status"
                                  :data-status="skill.status"
                                >{{ statusLabel(skill) }}</span>
                                <span>{{ $t('settings.sandbox.skillTranscriptTitle') }}</span>
                              </div>
                            </div>
                            <t-button
                              variant="text"
                              shape="square"
                              size="small"
                              class="skill-transcript-popup__close"
                              :title="$t('common.close')"
                              @click.stop="onTranscriptVisible(skill, false)"
                            >
                              <template #icon><t-icon name="close" size="16px" /></template>
                            </t-button>
                          </header>
                          <div class="skill-transcript-popup__body">
                            <SkillInstallTimeline
                              :key="`${skill.id}-${skill.install_session_id || ''}-${transcriptEpoch}`"
                              compact
                              :config-id="record?.id || ''"
                              :skill-id="skill.id"
                              :session-id="skill.install_session_id || ''"
                              :message-id="skill.install_message_id || ''"
                              :live="skill.status === 'installing'"
                            />
                          </div>
                        </div>
                      </template>
                    </t-popup>
                    <button
                      v-else
                      type="button"
                      class="skill-item__icon-btn is-live is-live-chip"
                      :aria-label="$t('settings.sandbox.skillTranscript')"
                    >
                      <span class="skill-item__live-dot" aria-hidden="true" />
                      <t-icon name="chat-bubble-history" size="16px" />
                      <span class="skill-item__live-label">
                        {{ $t('settings.sandbox.skillTranscriptLive') }}
                      </span>
                    </button>
                  </t-tooltip>
                  <t-tooltip
                    v-if="skill.status === 'failed'"
                    :content="$t('settings.sandbox.skillRetryHint')"
                    placement="top"
                  >
                    <button
                      type="button"
                      class="skill-item__icon-btn"
                      :disabled="retryingId === skill.id"
                      :aria-label="$t('settings.sandbox.skillRetry')"
                      @click="retrySkill(skill)"
                    >
                      <t-icon name="refresh" size="16px" />
                    </button>
                  </t-tooltip>
                  <t-popconfirm
                    theme="warning"
                    :content="deleteHint"
                    :confirm-btn="{ content: $t('common.delete'), theme: 'danger' }"
                    :cancel-btn="{ content: $t('common.cancel') }"
                    placement="top-right"
                    @confirm="removeSkill(skill)"
                  >
                    <t-tooltip :content="$t('common.delete')" placement="top">
                      <button
                        type="button"
                        class="skill-item__icon-btn skill-item__icon-btn--danger"
                        :disabled="isBusy(skill) || deletingId === skill.id"
                        :aria-label="$t('common.delete')"
                      >
                        <t-icon name="delete" size="16px" />
                      </button>
                    </t-tooltip>
                  </t-popconfirm>
                </div>
              </div>
              <div v-if="skill.description" class="skill-item__copy">
                <p
                  class="skill-item__desc"
                  :class="{ 'skill-item__desc--expanded': isCopyExpanded(skill.id) }"
                >
                  {{ skill.description }}
                </p>
                <button
                  v-if="canToggleCopy(skill)"
                  type="button"
                  class="skill-item__toggle"
                  @click="toggleCopy(skill.id)"
                >
                  {{ isCopyExpanded(skill.id) ? $t('common.collapse') : $t('common.expand') }}
                </button>
              </div>
              <ul v-if="failedErrorLines(skill).length" class="skill-item__error">
                <li v-for="(line, i) in failedErrorLines(skill)" :key="i">{{ line }}</li>
              </ul>
            </div>
          </li>
        </ul>
      </section>
    </t-loading>

    <SkillFilesDrawer
      v-model:visible="filesDrawerVisible"
      :config-id="record?.id || ''"
      :skill-id="filesSkillId"
      :skill-name="filesSkillName"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { fetchEventSource } from '@microsoft/fetch-event-source'
import ModelSelector from '@/components/ModelSelector.vue'
import SkillInstallTimeline from '@/components/SkillInstallTimeline.vue'
import SkillFilesDrawer from '@/components/SkillFilesDrawer.vue'
import {
  getAgentById,
  updateAgent,
  type CustomAgent,
} from '@/api/agent'
import {
  configSkillInstallEventsUrl,
  deleteConfigSkill,
  getSandboxConfigById,
  updateSandboxConfigById,
  listConfigSkills,
  patchConfigSkill,
  reinstallConfigSkill,
  uploadConfigSkill,
  installConfigSkillFromSource,
  type ConfigSkill,
  type ConfigSkillInstallEvent,
  type SandboxConfigRecord,
  type SandboxSkillImage,
} from '@/api/system'
import { getApiBaseUrl } from '@/utils/api-base'
import { generateRandomString } from '@/utils/index'
import i18n from '@/i18n'

// Skills are installed into the config's snapshot image, so the panel needs a
// config that already exists. The editor only renders it on a saved config.
const props = defineProps<{
  record: SandboxConfigRecord | null
}>()

const emit = defineEmits<{
  updated: [record: SandboxConfigRecord]
  skillsChanged: []
  inFlightChange: [busy: boolean]
}>()

const { t, locale } = useI18n()

const loading = ref(false)
const uploading = ref(false)
const installingFromSource = ref(false)
const uploadPercent = ref(0)
const sourceInput = ref('')
const skills = ref<ConfigSkill[]>([])
const skillImage = ref<SandboxSkillImage | null>(null)
const togglingId = ref('')
const deletingId = ref('')
const retryingId = ref('')
// Only one install timeline is open at a time: each one holds an SSE
// connection, and two runs' worth of agent steps in a drawer is unreadable.
const expandedSkillId = ref('')
const filesSkillId = ref('')
const filesSkillName = ref('')
const filesDrawerVisible = ref(false)
const expandedCopyIds = ref<Set<string>>(new Set())
const transcriptEpoch = ref(0)
const focusedSkillId = ref('')
const skillItemEls = new Map<string, HTMLElement>()
let focusTimer: number | null = null
const fileInputRef = ref<HTMLInputElement | null>(null)
const progressById = ref<Record<string, ConfigSkillInstallEvent>>({})

const abortBySkill = new Map<string, AbortController>()
let pollTimer: number | null = null

const INSTALLER_AGENT_ID = 'builtin-skill-installer'
const LAST_CHAT_MODEL_KEY = 'weknora_last_chat_model_id'

const installerAgent = ref<CustomAgent | null>(null)
const installerModelId = ref('')
const savingInstallerModel = ref(false)
const skillRollout = ref<'next_turn' | 'new_session'>('next_turn')
const savingRollout = ref(false)

function normalizeSkillRollout(value?: string): 'next_turn' | 'new_session' {
  return value === 'new_session' ? 'new_session' : 'next_turn'
}

const uploadHint = computed(() =>
  skillRollout.value === 'new_session'
    ? t('settings.sandbox.skillUploadHintNewSession')
    : t('settings.sandbox.skillUploadHint'),
)
const installBusy = computed(() => uploading.value || installingFromSource.value)
const deleteHint = computed(() =>
  skillRollout.value === 'new_session'
    ? t('settings.sandbox.skillDeleteHintNewSession')
    : t('settings.sandbox.skillDeleteHint'),
)
const runtimeTemplateId = computed(() => {
  const cfg = props.record?.config
  return cfg?.cube?.template_id?.trim()
    || cfg?.e2b?.template_id?.trim()
    || cfg?.docker?.image?.trim()
    || ''
})
const hasSkillSnapshot = computed(() => Boolean(skillImage.value?.snapshot_id?.trim()))

function readLastChatModelID(): string {
  try {
    return localStorage.getItem(LAST_CHAT_MODEL_KEY) || ''
  } catch {
    return ''
  }
}

function formatBuiltAt(value?: string): string {
  if (!value) return t('settings.sandbox.imageInfoUnset')
  const date = new Date(value)
  if (Number.isNaN(date.getTime()) || date.getFullYear() <= 1) {
    return t('settings.sandbox.imageInfoUnset')
  }
  return date.toLocaleString(locale.value)
}

const STATUS_I18N: Record<string, string> = {
  installing: 'settings.sandbox.skillStatusInstalling',
  ready: 'settings.sandbox.skillStatusReady',
  failed: 'settings.sandbox.skillStatusFailed',
  removing: 'settings.sandbox.skillStatusRemoving',
}

function statusLabel(skill: ConfigSkill): string {
  const key = STATUS_I18N[skill.status]
  return key ? t(key) : skill.status
}

function isBusy(skill: ConfigSkill): boolean {
  return skill.status === 'installing' || skill.status === 'removing'
}

watch(
  () => skills.value.some(isBusy),
  (busy) => emit('inFlightChange', busy),
  { immediate: true },
)

// The locators are written only after the installer sandbox is up and the
// agent has a message to stream into. The row itself is already "installing"
// the moment the upload is accepted, and that is when the button has to
// appear — waiting for the locators would hide it for the first minute.
function hasTranscript(skill: ConfigSkill): boolean {
  if (skill.status === 'installing') return true
  return Boolean(skill.install_session_id && skill.install_message_id)
}

function bindSkillItem(id: string, el: unknown) {
  if (el instanceof HTMLElement) {
    skillItemEls.set(id, el)
    return
  }
  skillItemEls.delete(id)
}

function revealSkill(skillId: string) {
  focusedSkillId.value = skillId
  void nextTick(() => {
    skillItemEls.get(skillId)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  })
  if (focusTimer != null) window.clearTimeout(focusTimer)
  focusTimer = window.setTimeout(() => {
    if (focusedSkillId.value === skillId) focusedSkillId.value = ''
    focusTimer = null
  }, 2400)
}

function onTranscriptVisible(skill: ConfigSkill, visible: boolean) {
  if (visible) {
    filesDrawerVisible.value = false
    if (expandedSkillId.value !== skill.id) {
      expandedSkillId.value = skill.id
      // A run that finished while the popup was closed was tailed from the
      // event log; reopening it should read the run again from the top.
      transcriptEpoch.value += 1
    }
    return
  }
  if (expandedSkillId.value === skill.id) {
    expandedSkillId.value = ''
  }
}

function openSkillFiles(skill: ConfigSkill) {
  expandedSkillId.value = ''
  if (filesDrawerVisible.value && filesSkillId.value === skill.id) {
    filesDrawerVisible.value = false
    return
  }
  filesSkillId.value = skill.id
  filesSkillName.value = skill.name || skill.id
  filesDrawerVisible.value = true
}

function progressOf(skill: ConfigSkill): number {
  const percent = progressById.value[skill.id]?.percent
  if (typeof percent === 'number' && Number.isFinite(percent)) {
    return Math.max(0, Math.min(100, percent))
  }
  return skill.status === 'ready' || skill.status === 'failed' ? 100 : 0
}

function progressLog(skill: ConfigSkill): string {
  return progressById.value[skill.id]?.log || ''
}

// A re-run reuses the skill id, so the previous run's last event is still the
// one cached here. Left in place it renders as this run's state: a retry would
// open at 100% showing the failure it was started to fix, until the first new
// event lands.
function forgetProgress(skillId: string) {
  if (!(skillId in progressById.value)) return
  const next = { ...progressById.value }
  delete next[skillId]
  progressById.value = next
}

function failedError(skill: ConfigSkill): string {
  if (skill.status !== 'failed') return ''
  return skill.error || progressLog(skill)
}

// Script verification reports every problem it found rather than stopping at
// the first, so one failure is often several lines. Run together they are
// unreadable, and the list is what tells the operator whether this is one
// missing package or a bundle that needs rebuilding.
function failedErrorLines(skill: ConfigSkill): string[] {
  return failedError(skill)
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
}

function isCopyExpanded(skillId: string): boolean {
  return expandedCopyIds.value.has(skillId)
}

function descriptionNeedsToggle(skill: ConfigSkill): boolean {
  const desc = skill.description?.trim() || ''
  return desc.length > 80 || desc.includes('\n')
}

function canToggleCopy(skill: ConfigSkill): boolean {
  return descriptionNeedsToggle(skill) || isCopyExpanded(skill.id)
}

function toggleCopy(skillId: string) {
  const next = new Set(expandedCopyIds.value)
  if (next.has(skillId)) next.delete(skillId)
  else next.add(skillId)
  expandedCopyIds.value = next
}

function stopFollow(skillId: string) {
  const controller = abortBySkill.get(skillId)
  if (controller) {
    controller.abort()
    abortBySkill.delete(skillId)
  }
}

function stopAllFollows() {
  for (const skillId of [...abortBySkill.keys()]) {
    stopFollow(skillId)
  }
}

function stopPoll() {
  if (pollTimer != null) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
}

function ensurePoll() {
  const busy = skills.value.some(isBusy)
  if (busy && pollTimer == null) {
    pollTimer = window.setInterval(() => {
      void loadSkills(true)
    }, 2000)
  } else if (!busy) {
    stopPoll()
  }
}

function followBusySkills() {
  if (!props.record) return
  const busyIds = new Set(skills.value.filter(isBusy).map((skill) => skill.id))
  for (const skillId of [...abortBySkill.keys()]) {
    if (!busyIds.has(skillId)) stopFollow(skillId)
  }
  for (const skill of skills.value) {
    if (isBusy(skill)) followProgress(skill.id)
  }
}

function followProgress(skillId: string) {
  if (!props.record || abortBySkill.has(skillId)) return
  const configId = props.record.id
  const controller = new AbortController()
  abortBySkill.set(skillId, controller)

  const token = localStorage.getItem('weknora_token')
  const tenantId = localStorage.getItem('weknora_selected_tenant_id')
  const url = `${getApiBaseUrl()}${configSkillInstallEventsUrl(configId, skillId)}`

  void fetchEventSource(url, {
    method: 'GET',
    headers: {
      Authorization: token ? `Bearer ${token}` : '',
      'Accept-Language': i18n.global.locale?.value || localStorage.getItem('locale') || 'zh-CN',
      'X-Request-ID': generateRandomString(12),
      ...(tenantId ? { 'X-Tenant-ID': tenantId } : {}),
    },
    signal: controller.signal,
    openWhenHidden: true,
    onmessage(ev) {
      if (!ev.data) return
      let parsed: ConfigSkillInstallEvent
      try {
        parsed = JSON.parse(ev.data) as ConfigSkillInstallEvent
      } catch {
        return
      }
      progressById.value = { ...progressById.value, [skillId]: parsed }
      if (parsed.done) {
        stopFollow(skillId)
        void loadSkills()
        void refreshImage()
      }
    },
    onerror() {
      stopFollow(skillId)
      throw new Error('skill install stream closed')
    },
  }).catch(() => {
    stopFollow(skillId)
  })
}

async function refreshImage() {
  if (!props.record) return
  try {
    const res = await getSandboxConfigById(props.record.id)
    skillImage.value = res?.data?.config?.skill_image || null
    skillRollout.value = normalizeSkillRollout(res?.data?.config?.skill_rollout)
    if (res?.data) emit('updated', res.data)
  } catch {
    skillImage.value = skillImage.value || props.record.config?.skill_image || null
    emit('skillsChanged')
  }
}

function skillsSignature(list: ConfigSkill[]): string {
  return list.map((skill) => `${skill.id}:${skill.status}:${skill.enabled ? 1 : 0}`).join('|')
}

async function loadSkills(silent = false) {
  if (!props.record) return
  if (!silent) loading.value = true
  const previous = skillsSignature(skills.value)
  const wasBusy = skills.value.some(isBusy)
  try {
    const res = await listConfigSkills(props.record.id)
    skills.value = res?.data || []
    followBusySkills()
    ensurePoll()
    if (skillsSignature(skills.value) !== previous) {
      emit('skillsChanged')
    }
    if (wasBusy && !skills.value.some(isBusy)) {
      void refreshImage()
    }
  } catch (e: any) {
    if (!silent) {
      MessagePlugin.error(e?.message || t('settings.sandbox.skillLoadFailed'))
    }
  } finally {
    if (!silent) loading.value = false
  }
}

async function loadAll() {
  skillImage.value = props.record?.config?.skill_image || null
  skillRollout.value = normalizeSkillRollout(props.record?.config?.skill_rollout)
  await Promise.all([loadSkills(), refreshImage(), loadInstallerModel()])
}

async function loadInstallerModel() {
  try {
    const res = await getAgentById(INSTALLER_AGENT_ID)
    installerAgent.value = res?.data || null
    const configured = installerAgent.value?.config?.model_id?.trim() || ''
    installerModelId.value = configured || readLastChatModelID()
  } catch {
    installerAgent.value = null
    installerModelId.value = readLastChatModelID()
  }
}

async function persistInstallerModel(modelId: string) {
  const id = modelId.trim()
  if (!id) {
    throw new Error(t('settings.sandbox.skillInstallerModelRequired'))
  }
  const current = installerAgent.value
  const config = { ...(current?.config || {}), model_id: id }
  const res = await updateAgent(INSTALLER_AGENT_ID, {
    name: current?.name || '',
    description: current?.description || '',
    avatar: current?.avatar || '',
    config,
  })
  installerAgent.value = res?.data || { ...(current as CustomAgent), config }
  installerModelId.value = id
}

async function onSkillRolloutChange(value: string) {
  const next = normalizeSkillRollout(value)
  if (!props.record || next === skillRollout.value) return
  const previous = skillRollout.value
  skillRollout.value = next
  savingRollout.value = true
  try {
    const res = await getSandboxConfigById(props.record.id)
    const current = res?.data
    const saved = await updateSandboxConfigById(props.record.id, {
      name: current?.name || props.record.name,
      description: current?.description || props.record.description,
      config: { ...(current?.config || props.record.config || {}), skill_rollout: next },
    })
    if (saved?.data) emit('updated', saved.data)
  } catch (e: any) {
    skillRollout.value = previous
    MessagePlugin.error(e?.message || t('settings.sandbox.skillRolloutSaveFailed'))
  } finally {
    savingRollout.value = false
  }
}

async function onInstallerModelChange(modelId: string) {
  if (!modelId || modelId === '__add_model__') return
  installerModelId.value = modelId
  savingInstallerModel.value = true
  try {
    await persistInstallerModel(modelId)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.skillInstallerModelSaveFailed'))
  } finally {
    savingInstallerModel.value = false
  }
}

function isZipFile(file: File): boolean {
  return file.name.toLowerCase().endsWith('.zip') || file.type === 'application/zip'
}

async function uploadFile(file: File) {
  if (!props.record || installBusy.value) return
  if (!installerModelId.value) {
    MessagePlugin.warning(t('settings.sandbox.skillInstallerModelRequired'))
    return
  }
  if (!isZipFile(file)) {
    MessagePlugin.error(t('settings.sandbox.skillUploadFailed'))
    return
  }
  uploading.value = true
  uploadPercent.value = 0
  try {
    await persistInstallerModel(installerModelId.value)
    const res = await uploadConfigSkill(props.record.id, file, (percent) => {
      uploadPercent.value = percent
    })
    MessagePlugin.success(t('settings.sandbox.skillUploadAccepted'))
    const skillId = res?.data?.skill_id
    // Re-uploading a skill by the same name reuses its row, so this may be a
    // second run of a skill already on screen.
    if (skillId) forgetProgress(skillId)
    await loadSkills()
    await refreshImage()
    if (skillId) {
      followProgress(skillId)
      revealSkill(skillId)
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.skillUploadFailed'))
  } finally {
    uploading.value = false
    uploadPercent.value = 0
    if (fileInputRef.value) fileInputRef.value.value = ''
  }
}

async function installFromSource() {
  if (!props.record || installBusy.value) return
  const source = sourceInput.value.trim()
  if (!source) return
  if (!installerModelId.value) {
    MessagePlugin.warning(t('settings.sandbox.skillInstallerModelRequired'))
    return
  }
  installingFromSource.value = true
  try {
    await persistInstallerModel(installerModelId.value)
    const res = await installConfigSkillFromSource(props.record.id, { source })
    MessagePlugin.success(t('settings.sandbox.skillUploadAccepted'))
    sourceInput.value = ''
    const skillId = res?.data?.skill_id
    if (skillId) forgetProgress(skillId)
    await loadSkills()
    await refreshImage()
    if (skillId) {
      followProgress(skillId)
      revealSkill(skillId)
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.skillSourceFailed'))
  } finally {
    installingFromSource.value = false
  }
}

function onFileInputChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (file) void uploadFile(file)
}

function onFileDrop(event: DragEvent) {
  if (installBusy.value) return
  const file = event.dataTransfer?.files?.[0]
  if (file) void uploadFile(file)
}

async function toggleEnabled(skill: ConfigSkill, enabled: boolean) {
  if (!props.record) return
  togglingId.value = skill.id
  try {
    const res = await patchConfigSkill(props.record.id, skill.id, { enabled })
    const updated = res?.data
    skills.value = skills.value.map((item) => (item.id === skill.id ? (updated || { ...item, enabled }) : item))
    MessagePlugin.success(
      enabled ? t('settings.sandbox.skillEnabled') : t('settings.sandbox.skillDisabled'),
    )
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.skillToggleFailed'))
  } finally {
    togglingId.value = ''
  }
}

// The server still holds the archive, so a retry needs nothing from the
// operator. It reuses the same row, which is why the progress follow can be
// re-attached under the id already on screen.
async function retrySkill(skill: ConfigSkill) {
  if (!props.record) return
  retryingId.value = skill.id
  forgetProgress(skill.id)
  try {
    await reinstallConfigSkill(props.record.id, skill.id)
    MessagePlugin.success(t('settings.sandbox.skillRetryAccepted'))
    await loadSkills()
    followProgress(skill.id)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.skillRetryFailed'))
  } finally {
    retryingId.value = ''
  }
}

async function removeSkill(skill: ConfigSkill) {
  if (!props.record) return
  deletingId.value = skill.id
  try {
    await deleteConfigSkill(props.record.id, skill.id)
    MessagePlugin.success(t('settings.sandbox.skillDeleteAccepted'))
    await loadSkills()
    await refreshImage()
    followProgress(skill.id)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.deleteFailed'))
  } finally {
    deletingId.value = ''
  }
}

// The panel is mounted only while its wizard step is showing, so switching
// steps tears the follows down and coming back re-reads the list.
watch(
  () => props.record?.id,
  (configID) => {
    if (configID) {
      void loadAll()
      return
    }
    stopAllFollows()
    stopPoll()
    skills.value = []
    progressById.value = {}
    expandedCopyIds.value = new Set()
    installerAgent.value = null
    installerModelId.value = ''
  },
  { immediate: true },
)

onUnmounted(() => {
  stopAllFollows()
  stopPoll()
  if (focusTimer != null) window.clearTimeout(focusTimer)
})
</script>

<style lang="less" scoped>
.image-info-note {
  margin: 0 0 10px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.image-info {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.image-info li {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr);
  gap: 12px;
  align-items: start;
}

.installer-model-hint {
  margin: 0 0 10px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.skill-rollout-group {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
}

.image-info__label {
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.45;
  padding-top: 1px;
}

.image-info__value {
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 500;
  line-height: 1.45;
  min-width: 0;
}

.image-info__value--id {
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
  font-weight: 400;
  overflow-wrap: anywhere;
  word-break: break-all;
  user-select: all;
}

.file-input-hidden {
  display: none;
}

.file-upload-area {
  position: relative;
  width: 100%;
  min-height: 44px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  cursor: pointer;
  transition: border-color 0.2s ease, background 0.2s ease;
  display: flex;
  align-items: center;
  justify-content: center;

  &:hover:not(.is-disabled) {
    border-color: var(--td-brand-color);
    background: var(--td-success-color-light);
  }

  &.has-file {
    border-color: var(--td-brand-color);
    background: var(--td-success-color-light);
    border-style: solid;
  }

  &.is-disabled {
    cursor: not-allowed;
    opacity: 0.6;
  }
}

.file-upload-content {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 8px;
  text-align: center;
  padding: 8px 12px;
  width: 100%;
}

.upload-icon {
  color: var(--td-brand-color);
  flex-shrink: 0;
}

.upload-text {
  display: flex;
  flex-direction: row;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: center;
  gap: 4px 8px;
}

.upload-primary-text {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.upload-secondary-text {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.upload-file-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-brand-color);
}

.file-upload-content :deep(.t-progress) {
  flex: 1 1 100%;
}

.upload-hint {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 1.5;
}

.skill-source-row {
  width: 100%;

  :deep(.t-input-adornment__append .t-button) {
    border-top-left-radius: 0;
    border-bottom-left-radius: 0;
  }
}

.skill-install-split {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 10px 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);

  &::before,
  &::after {
    content: '';
    flex: 1;
    height: 1px;
    background: var(--td-component-stroke);
  }
}

.skill-empty {
  margin: 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.skill-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

// The install timeline opens in a popup, so the card stays a two-column row.
.skill-item {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: flex-start;
  gap: 12px;
  padding: 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.skill-item--focused {
  border-color: var(--td-brand-color);
  box-shadow: 0 0 0 2px var(--td-brand-color-focus, rgba(0, 168, 112, 0.18));
}

.skill-status-ring {
  width: 16px;
  height: 16px;
  margin-top: 3px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: var(--td-text-color-secondary);

  :deep(.t-icon) {
    width: 16px;
    height: 16px;
  }

  /* The ring is already sized to 16px by the component. The svg is inline by
     default, so without this it sits on a text baseline and pushes the ring
     a few pixels below the icon the other two states draw. */
  :deep(.t-progress--circle svg) {
    display: block;
  }

  &__ready {
    color: var(--td-success-color);
  }

  &__failed {
    color: var(--td-error-color);
  }
}

.skill-item__body {
  min-width: 0;
}

.skill-item__header {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.skill-item__heading {
  min-width: 0;
  flex: 1;
}

.skill-item__title {
  font-size: 14px;
  font-weight: 600;
  line-height: 22px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-item__meta {
  margin: 2px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.skill-item__desc,
.skill-item__error {
  margin: 0;
  padding: 0;
  font-size: 12px;
  line-height: 1.5;
  word-break: break-word;
  list-style: none;
}

/* Verification reports every problem it found, so a failure is often several
   lines. They are bulleted only when there is more than one: a lone problem
   reads as a sentence, not as a one-item list. */
.skill-item__error li:not(:only-child) {
  padding-left: 10px;
  text-indent: -10px;
}

.skill-item__error li:not(:only-child)::before {
  content: '· ';
}

.skill-item__error li + li {
  margin-top: 2px;
}

.skill-item__copy {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  margin-top: 6px;
}

.skill-item__desc {
  min-width: 0;
  flex: 1;
  color: var(--td-text-color-secondary);
}

.skill-item__desc:not(.skill-item__desc--expanded) {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.skill-item__error {
  margin-top: 6px;
  color: var(--td-error-color);
}

.skill-item__toggle {
  flex-shrink: 0;
  margin: 0;
  padding: 0;
  border: 0;
  background: none;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
  cursor: pointer;

  &:hover {
    color: var(--td-brand-color);
  }
}

.skill-item__actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.skill-item__actions-divider {
  width: 1px;
  height: 12px;
  margin: 0 4px;
  background: var(--td-component-stroke);
}

.skill-item__icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.4;
  }

  &.is-on {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &.is-live {
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
  }

  &.is-live-chip {
    width: auto;
    min-width: 26px;
    padding: 0 8px;
    gap: 5px;
  }

  &--danger:hover:not(:disabled) {
    background: var(--td-error-color-1, var(--td-bg-color-secondarycontainer));
    color: var(--td-error-color);
  }
}

.skill-item__live-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--td-brand-color);
  animation: skill-transcript-dot 2.4s ease-in-out infinite;
}

.skill-item__live-label {
  font-size: 12px;
  font-weight: 500;
  line-height: 1;
  white-space: nowrap;
}

@keyframes skill-transcript-dot {
  0%,
  100% {
    opacity: 1;
  }

  50% {
    opacity: 0.45;
  }
}
</style>

<style lang="less">
.skill-transcript-popup {
  z-index: 3200 !important;

  .t-popup__content {
    padding: 0 !important;
    width: 420px;
    max-width: min(420px, calc(100vw - 32px));
    border-radius: 10px !important;
    background: var(--td-bg-color-container) !important;
    border: 1px solid var(--td-component-stroke) !important;
    box-shadow:
      0 0 0 0.5px rgba(0, 0, 0, 0.04),
      0 8px 24px rgba(0, 0, 0, 0.12) !important;
    overflow: hidden;
  }

  &__panel {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  &__head {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 10px 8px 10px 14px;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  &__head-text {
    min-width: 0;
    flex: 1;
  }

  &__title {
    font-size: 13px;
    font-weight: 600;
    line-height: 1.35;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__meta {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 2px;
    font-size: 11px;
    line-height: 1.4;
    color: var(--td-text-color-placeholder);
  }

  &__status {
    color: var(--td-text-color-secondary);

    &[data-status='installing'],
    &[data-status='removing'] {
      color: var(--td-brand-color);
    }

    &[data-status='failed'] {
      color: var(--td-error-color);
    }

    &[data-status='ready'] {
      color: var(--td-success-color);
    }
  }

  &__close {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);
  }

  &__body {
    max-height: min(360px, 52vh);
    overflow: auto;
    background: var(--td-bg-color-secondarycontainer);

    &::-webkit-scrollbar {
      width: 6px;
    }

    &::-webkit-scrollbar-thumb {
      background: var(--td-bg-color-component-disabled);
      border-radius: 3px;
    }
  }
}
</style>
