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
        <div v-if="showRegistryToken" class="skill-token-row">
          <p class="skill-token-label">{{ $t('settings.sandbox.skillSourceTokenLabel') }}</p>
          <t-input
            v-model="sourceToken"
            type="password"
            size="small"
            autocomplete="off"
            :placeholder="$t('settings.sandbox.skillSourceTokenPlaceholder')"
            :disabled="installBusy"
          />
          <p class="skill-token-hint">{{ $t('settings.sandbox.skillSourceTokenHint') }}</p>
        </div>
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
          <li v-for="skill in skills" :key="skill.id" class="skill-item">
            <div class="skill-status-ring" :title="statusLabel(skill)">
              <t-progress
                v-if="isBusy(skill)"
                theme="circle"
                :percentage="progressOf(skill)"
                :size="16"
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
                    <t-button
                      variant="text"
                      shape="square"
                      class="skill-item__icon-btn"
                      :class="{ 'skill-transcript-toggle--on': expandedSkillId === skill.id }"
                      :title="$t('settings.sandbox.skillTranscript')"
                    >
                      <template #icon>
                        <t-icon name="chat-bubble-history" size="16px" />
                      </template>
                    </t-button>
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
                  <t-popconfirm
                    theme="warning"
                    :content="deleteHint"
                    :confirm-btn="{ content: $t('common.delete'), theme: 'danger' }"
                    :cancel-btn="{ content: $t('common.cancel') }"
                    placement="top-right"
                    @confirm="removeSkill(skill)"
                  >
                    <t-tooltip :content="$t('common.delete')" placement="top">
                      <t-button
                        theme="danger"
                        variant="text"
                        shape="square"
                        class="skill-item__icon-btn"
                        :disabled="isBusy(skill)"
                        :loading="deletingId === skill.id"
                      >
                        <template #icon><t-icon name="delete" size="16px" /></template>
                      </t-button>
                    </t-tooltip>
                  </t-popconfirm>
                </div>
              </div>
              <p
                v-if="skill.description"
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
              <p v-if="failedError(skill)" class="skill-item__error">{{ failedError(skill) }}</p>
            </div>
          </li>
        </ul>
      </section>
    </t-loading>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { fetchEventSource } from '@microsoft/fetch-event-source'
import ModelSelector from '@/components/ModelSelector.vue'
import SkillInstallTimeline from '@/components/SkillInstallTimeline.vue'
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
}>()

const { t, locale } = useI18n()

const loading = ref(false)
const uploading = ref(false)
const installingFromSource = ref(false)
const uploadPercent = ref(0)
const sourceInput = ref('')
const sourceToken = ref('')
const forceRegistryToken = ref(false)
const skills = ref<ConfigSkill[]>([])
const skillImage = ref<SandboxSkillImage | null>(null)
const togglingId = ref('')
const deletingId = ref('')
// Only one install timeline is open at a time: each one holds an SSE
// connection, and two runs' worth of agent steps in a drawer is unreadable.
const expandedSkillId = ref('')
const expandedCopyIds = ref<Set<string>>(new Set())
const transcriptEpoch = ref(0)
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
const sourceNeedsRegistryToken = computed(() => looksLikePrivateSkillHub(sourceInput.value))
const showRegistryToken = computed(() => sourceNeedsRegistryToken.value || forceRegistryToken.value)
const deleteHint = computed(() =>
  skillRollout.value === 'new_session'
    ? t('settings.sandbox.skillDeleteHintNewSession')
    : t('settings.sandbox.skillDeleteHint'),
)
const runtimeTemplateId = computed(() => {
  const cfg = props.record?.config
  return cfg?.cube?.template_id?.trim() || cfg?.e2b?.template_id?.trim() || ''
})
const hasSkillSnapshot = computed(() => Boolean(skillImage.value?.snapshot_id?.trim()))

const PUBLIC_SKILL_HOSTS = new Set([
  'github.com',
  'www.github.com',
  'codeload.github.com',
  'gitlab.com',
  'www.gitlab.com',
  'skills.sh',
  'www.skills.sh',
  'clawhub.ai',
  'www.clawhub.ai',
  'clawhub.com',
  'www.clawhub.com',
  'skillhub.cn',
  'www.skillhub.cn',
  'api.skillhub.cn',
])

function looksLikePrivateSkillHub(raw: string): boolean {
  const input = raw.trim()
  if (!input.includes('://')) return false
  try {
    const parsed = new URL(input)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return false
    return !PUBLIC_SKILL_HOSTS.has(parsed.hostname.toLowerCase())
  } catch {
    return false
  }
}

function isRegistryAuthError(err: unknown): boolean {
  const message = err && typeof err === 'object' && 'message' in err
    ? String((err as { message?: unknown }).message)
    : String(err ?? '')
  return /\bHTTP 401\b/.test(message) || /\bHTTP 403\b/.test(message)
}

watch(sourceInput, (value) => {
  if (!looksLikePrivateSkillHub(value)) {
    forceRegistryToken.value = false
  }
})

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

// The locators are written only after the installer sandbox is up and the
// agent has a message to stream into. The row itself is already "installing"
// the moment the upload is accepted, and that is when the button has to
// appear — waiting for the locators would hide it for the first minute.
function hasTranscript(skill: ConfigSkill): boolean {
  if (skill.status === 'installing') return true
  return Boolean(skill.install_session_id && skill.install_message_id)
}

function onTranscriptVisible(skill: ConfigSkill, visible: boolean) {
  if (visible) {
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

function failedError(skill: ConfigSkill): string {
  if (skill.status !== 'failed') return ''
  return skill.error || progressLog(skill)
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
  } catch {
    skillImage.value = props.record.config?.skill_image || null
  }
}

async function loadSkills(silent = false) {
  if (!props.record) return
  if (!silent) loading.value = true
  try {
    const res = await listConfigSkills(props.record.id)
    skills.value = res?.data || []
    followBusySkills()
    ensurePoll()
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
    await loadSkills()
    await refreshImage()
    if (skillId) followProgress(skillId)
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
    const token = showRegistryToken.value ? sourceToken.value.trim() : ''
    const res = await installConfigSkillFromSource(props.record.id, {
      source,
      ...(token ? { token } : {}),
    })
    MessagePlugin.success(t('settings.sandbox.skillUploadAccepted'))
    sourceInput.value = ''
    sourceToken.value = ''
    forceRegistryToken.value = false
    const skillId = res?.data?.skill_id
    await loadSkills()
    await refreshImage()
    if (skillId) followProgress(skillId)
  } catch (e: any) {
    if (isRegistryAuthError(e)) forceRegistryToken.value = true
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

async function removeSkill(skill: ConfigSkill) {
  if (!props.record) return
  deletingId.value = skill.id
  try {
    await deleteConfigSkill(props.record.id, skill.id)
    MessagePlugin.success(t('settings.sandbox.skillDeleteAccepted'))
    await loadSkills()
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

.skill-token-row {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 6px;
}

.skill-token-label {
  margin: 0;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
}

.skill-token-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
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
}

.skill-transcript-toggle--on {
  color: var(--td-brand-color);
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

  :deep(.t-progress),
  :deep(.t-icon) {
    width: 16px;
    height: 16px;
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
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.5;
  word-break: break-word;
}

.skill-item__desc {
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
  color: var(--td-error-color);
}

.skill-item__toggle {
  margin-top: 4px;
  padding: 0;
  border: 0;
  background: none;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-brand-color);
  cursor: pointer;
}

.skill-item__actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.skill-item__icon-btn {
  width: 24px;
  height: 24px;

  :deep(.t-button__icon) {
    margin: 0;
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
