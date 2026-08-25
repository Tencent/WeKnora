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
        <input
          ref="fileInputRef"
          type="file"
          accept=".zip,application/zip"
          class="file-input-hidden"
          @change="onFileInputChange"
        />

        <div
          class="file-upload-area"
          :class="{ 'has-file': uploading }"
          @click="fileInputRef?.click()"
          @dragover.prevent
          @dragenter.prevent
          @drop.prevent="onFileDrop"
        >
          <div class="file-upload-content">
            <t-icon name="upload" size="28px" class="upload-icon" />
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
                  <t-tooltip
                    v-if="hasTranscript(skill)"
                    :content="
                      expandedSkillId === skill.id
                        ? $t('settings.sandbox.skillTranscriptHide')
                        : $t('settings.sandbox.skillTranscript')
                    "
                    placement="top"
                  >
                    <t-button
                      variant="text"
                      shape="square"
                      class="skill-item__icon-btn"
                      :class="{ 'skill-transcript-toggle--on': expandedSkillId === skill.id }"
                      @click="toggleTranscript(skill)"
                    >
                      <template #icon>
                        <t-icon
                          :name="expandedSkillId === skill.id ? 'chevron-up' : 'chat-bubble-history'"
                          size="16px"
                        />
                      </template>
                    </t-button>
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

            <SkillInstallTimeline
              v-if="expandedSkillId === skill.id"
              :key="`${skill.id}-${skill.install_session_id || ''}-${transcriptEpoch}`"
              class="skill-item__timeline"
              :config-id="record?.id || ''"
              :skill-id="skill.id"
              :session-id="skill.install_session_id || ''"
              :message-id="skill.install_message_id || ''"
              :live="skill.status === 'installing'"
            />
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
const uploadPercent = ref(0)
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

function toggleTranscript(skill: ConfigSkill) {
  if (expandedSkillId.value === skill.id) {
    expandedSkillId.value = ''
    return
  }
  expandedSkillId.value = skill.id
  // A run that finished while the timeline was open was tailed from the event
  // log; reopening it should read the run again from the top rather than show
  // whatever the closed stream left behind.
  transcriptEpoch.value += 1
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
  if (!props.record || uploading.value) return
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

function onFileInputChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (file) void uploadFile(file)
}

function onFileDrop(event: DragEvent) {
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
  min-height: 120px;
  border: 2px dashed var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  cursor: pointer;
  transition: all 0.3s ease;
  display: flex;
  align-items: center;
  justify-content: center;

  &:hover {
    border-color: var(--td-brand-color);
    background: var(--td-success-color-light);
  }

  &.has-file {
    border-color: var(--td-brand-color);
    background: var(--td-success-color-light);
    border-style: solid;
  }
}

.file-upload-content {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  text-align: center;
  padding: 16px;
  width: 100%;
}

.upload-icon {
  color: var(--td-brand-color);
  transition: transform 0.2s ease;
}

.file-upload-area:hover .upload-icon {
  transform: translateY(-2px);
}

.upload-text {
  display: flex;
  flex-direction: column;
  gap: 4px;
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

.upload-hint {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 1.5;
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

// A grid rather than a flex row so the expanded install timeline can span the
// full width underneath the status + body of the row it belongs to.
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

.skill-item__timeline {
  grid-column: 1 / -1;
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
