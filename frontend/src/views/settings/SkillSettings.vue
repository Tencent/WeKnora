<template>
  <div class="skill-settings">
    <div class="section-header">
      <div class="section-header__title-row">
        <h2>{{ $t('settings.skills.title') }}</h2>
        <t-tooltip
          :content="$t('settings.skills.helpTooltip')"
          placement="right"
          overlay-class-name="skill-settings__help-tooltip"
        >
          <t-icon
            name="help-circle"
            class="section-header__help"
            :aria-label="$t('settings.skills.helpTooltip')"
          />
        </t-tooltip>
      </div>
      <p class="section-description">{{ $t('settings.skills.description') }}</p>
    </div>

    <div v-if="loading" class="loading-container">
      <t-loading :text="$t('common.loading')" />
    </div>

    <template v-else>
      <div v-if="catalog.length === 0" class="empty-state">
        <t-empty :description="$t('settings.skills.emptyDesc')" />
        <p v-if="skillConfigs.length === 0" class="empty-hint">
          {{ $t('settings.skills.emptyNoSandboxHint') }}
        </p>
        <div class="empty-actions">
          <t-button theme="primary" @click="openAdd">
            {{ $t('settings.skills.addSkill') }}
          </t-button>
          <t-button
            v-if="skillConfigs.length === 0"
            theme="default"
            variant="outline"
            @click="uiStore.openSettings('sandbox')"
          >
            {{ $t('settings.skills.goSandboxSettings') }}
          </t-button>
        </div>
      </div>

      <div v-else class="skill-list">
        <article
          v-for="item in catalog"
          :key="item.id"
          class="skill-card"
          :class="{
            'skill-card--focused': focusedCatalogId === item.id,
            'skill-card--installed': liveInstalls(item).length > 0,
            'skill-card--idle': liveInstalls(item).length === 0,
          }"
        >
          <div class="skill-card__main">
            <div class="skill-card__badge" aria-hidden="true">
              <t-icon :name="SKILL_ICON" size="16px" />
            </div>
            <div class="skill-card__body">
              <div class="skill-card__header">
                <div class="skill-card__heading">
                  <h3 class="skill-card__title" :title="item.name">{{ item.name }}</h3>
                  <span v-if="item.version" class="skill-card__type">{{ item.version }}</span>
                </div>
                <div class="skill-card__actions">
                  <button
                    type="button"
                    class="skill-card__icon-btn"
                    :title="$t('settings.sandbox.skillFiles')"
                    :aria-label="$t('settings.sandbox.skillFiles')"
                    @click="openCatalogFiles(item)"
                  >
                    <folder-icon size="14px" />
                  </button>
                  <button
                    v-if="canDelete(item)"
                    type="button"
                    class="skill-card__icon-btn skill-card__icon-btn--danger"
                    :disabled="deletingId === item.id"
                    :title="$t('settings.skills.deleteCatalog')"
                    :aria-label="$t('settings.skills.deleteCatalog')"
                    @click="askDelete(item)"
                  >
                    <delete-icon size="14px" />
                  </button>
                </div>
              </div>
              <p v-if="item.description" class="skill-card__desc" :title="item.description">
                {{ compactText(item.description) }}
              </p>
              <div
                v-for="view in [installsView(item)]"
                :key="'installs'"
                class="skill-card__installs"
              >
                <span
                  v-if="view.installs.length === 0 && !view.canAdd"
                  class="skill-card__installs-label"
                >
                  {{ $t('settings.skills.noInstalls') }}
                </span>
                <button
                  v-else-if="!view.needsPanel"
                  type="button"
                  class="skill-card__chip"
                  :class="chipClass(item, view)"
                  :disabled="Boolean(view.installs[0] && !recordFor(view.installs[0].sandbox_config_id))"
                  :title="installSummaryTooltip(item, view)"
                  :aria-label="installSummary(item, view)"
                  @click="onInstallChipClick(item, view)"
                >
                  <span
                    v-if="view.installs.some(isInstallBusy)"
                    class="skill-card__entry-dot"
                    aria-hidden="true"
                  />
                  <span class="skill-card__chip-text">{{ installSummary(item, view) }}</span>
                  <t-icon name="chevron-right" size="14px" class="skill-card__chip-go" />
                </button>
                <t-popup
                  v-else
                  :visible="openPanelId === item.id"
                  trigger="click"
                  placement="bottom-left"
                  attach="body"
                  destroy-on-close
                  overlay-class-name="skill-install-panel-overlay"
                  :overlay-inner-style="{ padding: 0 }"
                  @visible-change="(visible: boolean) => setInstallPanel(item.id, visible)"
                >
                  <button
                    type="button"
                    class="skill-card__chip"
                    :class="chipClass(item, view)"
                    :title="installSummaryTooltip(item, view)"
                    :aria-label="installSummary(item, view)"
                    :aria-expanded="openPanelId === item.id"
                  >
                    <span
                      v-if="view.installs.some(isInstallBusy)"
                      class="skill-card__entry-dot"
                      aria-hidden="true"
                    />
                    <span class="skill-card__chip-text">{{ installSummary(item, view) }}</span>
                    <t-icon name="chevron-down" size="14px" class="skill-card__chip-go" />
                  </button>
                  <template #content>
                    <div class="skill-install-panel">
                      <p class="skill-install-panel__group">{{ $t('settings.skills.installPanelGroup') }}</p>
                      <button
                        v-for="inst in view.installs"
                        :key="inst.skill_id"
                        type="button"
                        class="skill-install-panel__item"
                        :class="installEntryClass(item, inst)"
                        :disabled="!recordFor(inst.sandbox_config_id)"
                        :title="installTooltip(item, inst)"
                        @click="openManageFromPanel(item, inst)"
                      >
                        <SandboxBackendBadge
                          v-if="inst.sandbox_type"
                          :type="inst.sandbox_type"
                          size="xs"
                        />
                        <span class="skill-install-panel__name">{{ installName(inst) }}</span>
                        <span
                          v-if="isInstallBusy(inst)"
                          class="skill-card__entry-dot"
                          aria-hidden="true"
                        />
                        <t-icon
                          v-else-if="installChipStatusIcon(item, inst)"
                          :name="installChipStatusIcon(item, inst)"
                          size="14px"
                          class="skill-card__entry-status"
                        />
                      </button>
                      <template v-if="view.canAdd">
                        <div class="skill-install-panel__split" role="separator" />
                        <button
                          type="button"
                          class="skill-install-panel__item"
                          @click="openInstallFromPanel(item)"
                        >
                          <t-icon name="add" size="16px" />
                          <span class="skill-install-panel__name">{{ $t('settings.skills.installToSandbox') }}</span>
                        </button>
                      </template>
                    </div>
                  </template>
                </t-popup>
              </div>
            </div>
          </div>
        </article>
        <button type="button" class="skill-card skill-card--add" @click="openAdd">
          <span class="skill-card--add__icon" aria-hidden="true">
            <add-icon />
          </span>
          <span class="skill-card--add__label">{{ $t('settings.skills.addSkill') }}</span>
        </button>
      </div>
    </template>

    <SettingDrawer
      v-model:visible="showAdd"
      :title="$t('settings.skills.addSkill')"
      :description="addStepDescription"
      :icon="SKILL_ICON"
      width="680px"
      :min-width="560"
      :max-width="920"
      storage-key="setting-drawer:width:skill-catalog-add"
      :confirm-loading="addPrimaryLoading"
      :confirm-disabled="addPrimaryDisabled"
      :confirm-text="addPrimaryText"
      @confirm="handleAddPrimary"
    >
      <template #header-extra>
        <nav class="skill-add-steps" :aria-label="$t('settings.skills.addProgress')">
          <component
            :is="canJumpAddStep(index) ? 'button' : 'div'"
            v-for="(item, index) in addSteps"
            :key="item.key"
            :type="canJumpAddStep(index) ? 'button' : undefined"
            :class="['skill-add-step', {
              'is-active': addStep === index,
              'is-done': addStep > index,
              'is-clickable': canJumpAddStep(index),
            }]"
            :aria-current="addStep === index ? 'step' : undefined"
            @click="goToAddStep(index)"
          >
            <span class="skill-add-step__marker">
              <t-icon v-if="addStep > index" name="check" />
              <template v-else>{{ index + 1 }}</template>
            </span>
            <span class="skill-add-step__title">{{ item.title }}</span>
            <span v-if="index < addSteps.length - 1" class="skill-add-step__line" aria-hidden="true" />
          </component>
        </nav>
      </template>
      <template #footer-left>
        <t-button v-if="addStep > 0" variant="outline" @click="addPreviousStep">
          {{ $t('settings.sandbox.back') }}
        </t-button>
      </template>

      <article v-if="addStep > 0 && registeredCatalog" class="skill-card parsed-skill">
        <div class="skill-card__main">
          <div class="skill-card__badge" aria-hidden="true">
            <t-icon :name="SKILL_ICON" size="16px" />
          </div>
          <div class="skill-card__body">
            <div class="skill-card__header">
              <h3 class="skill-card__title" :title="registeredCatalog.name">{{ registeredCatalog.name }}</h3>
              <span v-if="registeredCatalog.version" class="skill-card__type">{{ registeredCatalog.version }}</span>
            </div>
            <p v-if="registeredCatalog.description" class="skill-card__desc" :title="registeredCatalog.description">
              {{ compactText(registeredCatalog.description) }}
            </p>
          </div>
        </div>
      </article>

      <template v-if="addStep === 0">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillSourceSection') }}</h4>
          <p class="installer-model-hint">{{ $t('settings.sandbox.skillSourceSectionHint', { size: maxSkillBundleMB }) }}</p>
          <t-input
            v-model="sourceInput"
            :placeholder="$t('settings.sandbox.skillSourcePlaceholder')"
            :disabled="addBusy || !!registeredCatalog"
            @enter="handleAddPrimary"
          />
        </section>

        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillUploadSection') }}</h4>
          <p class="installer-model-hint">{{ $t('settings.sandbox.skillUploadSectionHint', { size: maxSkillBundleMB }) }}</p>
          <input
            ref="fileInputRef"
            type="file"
            accept=".zip,application/zip"
            class="file-input-hidden"
            @change="onFileInputChange"
          />
          <div
            class="file-upload-area file-upload-area--large"
            :class="{ 'has-file': !!pendingFile, 'is-disabled': addBusy || !!registeredCatalog }"
            @click="!addBusy && !registeredCatalog && fileInputRef?.click()"
            @dragover.prevent
            @dragenter.prevent
            @drop.prevent="onFileDrop"
          >
            <div class="file-upload-content">
              <div class="file-upload-icon-wrap" aria-hidden="true">
                <t-icon name="cloud-upload" size="32px" class="upload-icon" />
              </div>
              <div class="upload-text">
                <span v-if="pendingFile" class="upload-file-name">
                  {{ t('settings.skills.addFileSelected', { name: pendingFile.name }) }}
                </span>
                <template v-else>
                  <span class="upload-primary-text">{{ $t('settings.sandbox.skillUploadClick') }}</span>
                  <span class="upload-secondary-text">{{ $t('settings.sandbox.skillUploadDrag') }}</span>
                </template>
              </div>
              <t-progress v-if="uploading" :percentage="uploadPercent" size="small" />
            </div>
          </div>
          <t-button
            v-if="pendingFile && !registeredCatalog"
            variant="text"
            size="small"
            :disabled="addBusy"
            @click="pendingFile = null"
          >
            {{ $t('settings.skills.addClearFile') }}
          </t-button>
        </section>
      </template>

      <template v-else>
        <section v-if="skillConfigs.length > 0" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.skills.pickSandboxes') }}</h4>
          <p class="installer-model-hint">{{ $t('settings.skills.pickSandboxesHint') }}</p>
          <t-checkbox-group v-model="addTargetIds" class="sandbox-pick-list">
            <t-checkbox v-for="cfg in skillConfigs" :key="cfg.id" :value="cfg.id" class="sandbox-pick">
              <span class="sandbox-pick__main">
                <SandboxBackendBadge :type="cfg.sandbox_type" size="sm" />
                <span class="sandbox-pick__text">
                  <span class="sandbox-pick__name">{{ cfg.name }}</span>
                  <span class="sandbox-pick__meta">{{ sandboxMetaLine(cfg) }}</span>
                </span>
              </span>
            </t-checkbox>
          </t-checkbox-group>
        </section>
        <p v-else class="installer-model-hint">{{ $t('settings.skills.emptyNoSandboxHint') }}</p>

        <section v-if="addTargetIds.length > 0" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillInstallerModel') }}</h4>
          <p class="installer-model-hint">{{ $t('settings.sandbox.skillInstallerModelHint') }}</p>
          <ModelSelector
            model-type="KnowledgeQA"
            :selected-model-id="installerModelId"
            :disabled="savingInstallerModel || installing"
            @update:selected-model-id="onInstallerModelChange"
          />
        </section>
      </template>
    </SettingDrawer>

    <SettingDrawer
      v-model:visible="showInstall"
      :title="$t('settings.skills.installToSandbox')"
      :description="installDrawerDesc"
      :icon="SKILL_ICON"
      width="560px"
      :min-width="480"
      :max-width="760"
      storage-key="setting-drawer:width:skill-catalog-install"
      :confirm-loading="installing"
      :confirm-disabled="installTargetIds.length === 0"
      :confirm-text="$t('settings.skills.installToSandbox')"
      @confirm="confirmInstall"
    >
      <p class="installer-model-hint">{{ $t('settings.skills.installToSandboxDesc') }}</p>
      <section v-if="installTargets.length > 0" class="setting-drawer__section">
        <t-checkbox-group v-model="installTargetIds" class="sandbox-pick-list">
          <t-checkbox v-for="cfg in installTargets" :key="cfg.id" :value="cfg.id" class="sandbox-pick">
            <span class="sandbox-pick__main">
              <SandboxBackendBadge :type="cfg.sandbox_type" size="sm" />
              <span class="sandbox-pick__text">
                <span class="sandbox-pick__name">{{ cfg.name }}</span>
                <span class="sandbox-pick__meta">{{ sandboxMetaLine(cfg) }}</span>
              </span>
            </span>
          </t-checkbox>
        </t-checkbox-group>
      </section>
      <p v-else class="installer-model-hint">{{ $t('settings.skills.noSandboxToInstall') }}</p>
      <section v-if="installTargetIds.length > 0" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillInstallerModel') }}</h4>
        <p class="installer-model-hint">{{ $t('settings.sandbox.skillInstallerModelHint') }}</p>
        <ModelSelector
          model-type="KnowledgeQA"
          :selected-model-id="installerModelId"
          :disabled="savingInstallerModel || installing"
          @update:selected-model-id="onInstallerModelChange"
        />
      </section>
    </SettingDrawer>

    <SettingDrawer
      v-model:visible="showManage"
      :title="manageTitle"
      :description="manageDesc"
      :icon="SKILL_ICON"
      width="680px"
      :min-width="560"
      :max-width="920"
      storage-key="setting-drawer:width:skill-catalog-manage"
      :hide-footer="true"
    >
      <SandboxSkillsPanel
        v-if="showManage && manageRecord && manageSkillId"
        :record="manageRecord"
        mode="list"
        hide-add
        :focus-skill-id="manageSkillId"
        @updated="onPanelUpdated"
        @skills-changed="loadCatalog"
      />
    </SettingDrawer>

    <SkillFilesDrawer
      v-model:visible="filesDrawerVisible"
      :catalog-id="filesCatalogId"
      :skill-name="filesCatalogName"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { AddIcon, DeleteIcon, FolderIcon } from 'tdesign-icons-vue-next'
import { useI18n } from 'vue-i18n'
import SandboxSkillsPanel from '@/components/SandboxSkillsPanel.vue'
import SkillFilesDrawer from '@/components/SkillFilesDrawer.vue'
import SandboxBackendBadge from '@/components/settings/SandboxBackendBadge.vue'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import ModelSelector from '@/components/ModelSelector.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { SKILL_ICON } from '@/types/mention'
import { useUIStore } from '@/stores/ui'
import { MAX_FILE_SIZE_BYTES, MAX_FILE_SIZE_MB } from '@/utils'
import {
  deleteSkillCatalog,
  installSkillCatalog,
  listSkillCatalog,
  registerSkillCatalogFromFile,
  registerSkillCatalogFromSource,
  type SkillCatalogInstall,
  type SkillCatalogItem,
  type SkillCatalogRegisterResult,
} from '@/api/skill'
import {
  getAgentById,
  updateAgent,
  type CustomAgent,
} from '@/api/agent'
import {
  isNamedSandboxBackend,
  listSandboxConfigs,
  type SandboxConfigRecord,
} from '@/api/system'

const props = defineProps<{
  initialSandboxId?: string
}>()

const { t } = useI18n()
const uiStore = useUIStore()
const confirmDelete = useConfirmDelete()

const loading = ref(false)
const records = ref<SandboxConfigRecord[]>([])
const catalog = ref<SkillCatalogItem[]>([])
const focusedCatalogId = ref('')
const deletingId = ref('')
const showAdd = ref(false)
const showInstall = ref(false)
const showManage = ref(false)
const addStep = ref(0)
const registeredCatalog = ref<SkillCatalogRegisterResult | null>(null)
const pendingFile = ref<File | null>(null)
const addTargetIds = ref<string[]>([])
const installTargetIds = ref<string[]>([])
const installCatalog = ref<SkillCatalogItem | null>(null)
const manageRecord = ref<SandboxConfigRecord | null>(null)
const manageSkillId = ref('')
const manageTitle = ref('')
const filesDrawerVisible = ref(false)
const filesCatalogId = ref('')
const filesCatalogName = ref('')
const openPanelId = ref('')
const sourceInput = ref('')
const uploading = ref(false)
const addingFromSource = ref(false)
const installing = ref(false)
const uploadPercent = ref(0)
const fileInputRef = ref<HTMLInputElement | null>(null)
const installerAgent = ref<CustomAgent | null>(null)
const installerModelId = ref('')
const savingInstallerModel = ref(false)

const INSTALLER_AGENT_ID = 'builtin-skill-installer'
const LAST_CHAT_MODEL_KEY = 'weknora_last_chat_model_id'

let pollTimer: number | null = null
let focusTimer: number | null = null

const skillConfigs = computed(() =>
  records.value.filter((record) => isNamedSandboxBackend(record.sandbox_type)),
)

const addBusy = computed(() => uploading.value || addingFromSource.value)
const maxSkillBundleMB = MAX_FILE_SIZE_MB

const addSteps = computed(() => [
  { key: 'register', title: t('settings.skills.addStepRegister') },
  { key: 'install', title: t('settings.skills.addStepInstall') },
])

const addStepDescription = computed(() =>
  addStep.value === 0
    ? t('settings.skills.addStepRegisterDesc')
    : t('settings.skills.addStepInstallDesc'),
)

const addPrimaryLoading = computed(() =>
  addStep.value === 0 ? addBusy.value : installing.value,
)

const addPrimaryDisabled = computed(() => {
  if (addBusy.value || installing.value) return true
  if (addStep.value === 0) {
    if (registeredCatalog.value) return false
    return !sourceInput.value.trim() && !pendingFile.value
  }
  return addTargetIds.value.length > 0 && !installerModelId.value
})

const addPrimaryText = computed(() => {
  if (addStep.value === 0) return t('common.next')
  if (addTargetIds.value.length > 0) return t('settings.skills.installToSandbox')
  return t('settings.skills.addFinish')
})

const installTargets = computed(() => {
  const item = installCatalog.value
  if (!item) return skillConfigs.value
  return targetsFor(item)
})

const installDrawerDesc = computed(() => {
  const item = installCatalog.value
  if (!item) return t('settings.skills.installToSandboxDesc')
  return t('settings.skills.installDrawerDesc', { name: item.name })
})

const manageDesc = computed(() => {
  const record = manageRecord.value
  if (!record) return ''
  return t('settings.skills.manageDrawerDesc', { name: record.name })
})

function liveInstalls(item: SkillCatalogItem): SkillCatalogInstall[] {
  return (item.installations || []).filter((inst) => inst.status && inst.status !== 'removed')
}

function canDelete(item: SkillCatalogItem): boolean {
  return liveInstalls(item).length === 0
}

function targetsFor(item: SkillCatalogItem): SandboxConfigRecord[] {
  const taken = new Set(
    liveInstalls(item)
      .filter((inst) => inst.status === 'installing' || inst.status === 'ready' || inst.status === 'removing')
      .map((inst) => inst.sandbox_config_id),
  )
  return skillConfigs.value.filter((cfg) => !taken.has(cfg.id))
}

function recordFor(id: string): SandboxConfigRecord | undefined {
  return records.value.find((record) => record.id === id)
}

function backendLabel(type: string): string {
  return t(`settings.sandbox.backends.${type}`)
}

function sandboxTargetLine(record: SandboxConfigRecord): string {
  if (record.sandbox_type === 'docker') {
    return record.config?.docker?.image?.trim() || ''
  }
  const remote = record.config?.e2b || record.config?.cube
  const raw = remote?.api_url?.trim() || ''
  if (!raw) return ''
  try {
    return new URL(raw).host
  } catch {
    return raw
  }
}

function sandboxMetaLine(record: SandboxConfigRecord): string {
  const type = backendLabel(record.sandbox_type)
  const target = sandboxTargetLine(record)
  return target ? `${type} · ${target}` : type
}

function catalogFromRegister(data: SkillCatalogRegisterResult | undefined, fallbackName: string): SkillCatalogRegisterResult | null {
  const id = data?.id || ''
  if (!id) return null
  return {
    id,
    name: data?.name || fallbackName,
    version: data?.version,
    description: data?.description,
  }
}

function syncRegisteredFromCatalog() {
  const current = registeredCatalog.value
  if (!current?.id) return
  const item = catalog.value.find((row) => row.id === current.id)
  if (!item) return
  registeredCatalog.value = {
    id: item.id,
    name: item.name,
    version: item.version,
    description: item.description,
  }
}

function compactText(value: string): string {
  return value.replace(/\s+/g, ' ').trim()
}

function installOutdated(item: SkillCatalogItem, inst: SkillCatalogInstall): boolean {
  return Boolean(
    item.bundle_sha256
    && inst.bundle_sha256
    && item.bundle_sha256 !== inst.bundle_sha256,
  )
}

function installStatusText(inst: SkillCatalogInstall): string {
  if (inst.status === 'installing') return t('settings.sandbox.skillStatusInstalling')
  if (inst.status === 'removing') return t('settings.sandbox.skillStatusRemoving')
  if (inst.status === 'failed') return t('settings.sandbox.skillStatusFailed')
  if (inst.status === 'ready' && !inst.enabled) return t('common.off')
  if (inst.status === 'ready') return t('settings.sandbox.skillStatusReady')
  return inst.status
}

function installChipStatus(item: SkillCatalogItem, inst: SkillCatalogInstall): string {
  if (inst.status === 'ready' && inst.enabled && installOutdated(item, inst)) {
    return t('settings.skills.installOutdated')
  }
  if (inst.status === 'ready' && inst.enabled) return ''
  return installStatusText(inst)
}

function installChipStatusIcon(item: SkillCatalogItem, inst: SkillCatalogInstall): string {
  if (inst.status === 'failed') return 'close-circle'
  if (inst.status === 'ready' && inst.enabled && installOutdated(item, inst)) return 'error-circle'
  if (inst.status === 'ready' && inst.enabled) return 'check-circle-filled'
  return ''
}

function isInstallBusy(inst: SkillCatalogInstall): boolean {
  return inst.status === 'installing' || inst.status === 'removing'
}

function installPriority(item: SkillCatalogItem, inst: SkillCatalogInstall): number {
  if (inst.status === 'failed') return 0
  if (isInstallBusy(inst)) return 1
  if (installOutdated(item, inst)) return 2
  if (inst.status === 'ready' && !inst.enabled) return 3
  return 4
}

function installsView(item: SkillCatalogItem) {
  const installs = [...liveInstalls(item)].sort((a, b) => {
    const diff = installPriority(item, a) - installPriority(item, b)
    if (diff !== 0) return diff
    return installName(a).localeCompare(installName(b), undefined, { sensitivity: 'base' })
  })
  const canAdd = targetsFor(item).length > 0
  return {
    installs,
    canAdd,
    needsPanel: installs.length > 1 || (installs.length > 0 && canAdd),
  }
}

function installSummary(item: SkillCatalogItem, view: ReturnType<typeof installsView>): string {
  if (view.installs.length === 0) return t('settings.skills.installToSandbox')
  if (view.installs.length === 1) {
    return t('settings.skills.installedOnName', { name: installName(view.installs[0]) })
  }
  return t('settings.skills.installedCount', { count: view.installs.length })
}

function chipClass(item: SkillCatalogItem, view: ReturnType<typeof installsView>): (string | undefined)[] {
  return [
    view.installs.length === 0 ? 'skill-card__chip--idle' : 'skill-card__chip--installed',
    view.installs[0] ? installEntryClass(item, view.installs[0]) : undefined,
  ]
}

function installSummaryTooltip(item: SkillCatalogItem, view: ReturnType<typeof installsView>): string {
  if (view.installs.length === 0) return t('settings.skills.installToSandbox')
  return view.installs.map((inst) => installTooltip(item, inst)).join('\n')
}

function onInstallChipClick(item: SkillCatalogItem, view: ReturnType<typeof installsView>) {
  if (view.installs[0]) {
    openManage(item, view.installs[0])
    return
  }
  if (view.canAdd) openInstall(item)
}

function setInstallPanel(id: string, visible: boolean) {
  if (visible) {
    openPanelId.value = id
    return
  }
  if (openPanelId.value === id) openPanelId.value = ''
}

function openManageFromPanel(item: SkillCatalogItem, inst: SkillCatalogInstall) {
  openPanelId.value = ''
  openManage(item, inst)
}

function openInstallFromPanel(item: SkillCatalogItem) {
  openPanelId.value = ''
  openInstall(item)
}

function installEntryClass(item: SkillCatalogItem, inst: SkillCatalogInstall): string {
  if (inst.status === 'failed') return 'skill-card__entry--failed'
  if (isInstallBusy(inst)) return 'skill-card__entry--busy'
  if (inst.status === 'ready' && !inst.enabled) return 'skill-card__entry--off'
  if (installOutdated(item, inst)) return 'skill-card__entry--stale'
  return 'skill-card__entry--ready'
}

function installName(inst: SkillCatalogInstall): string {
  return inst.sandbox_config_name || inst.sandbox_config_id
}

function installTooltip(item: SkillCatalogItem, inst: SkillCatalogInstall): string {
  const parts = [
    inst.sandbox_config_name || inst.sandbox_config_id,
    inst.sandbox_type ? backendLabel(inst.sandbox_type) : '',
    installChipStatus(item, inst) || installStatusText(inst),
  ].filter(Boolean)
  return parts.join(' · ')
}

function openCatalogFiles(item: SkillCatalogItem) {
  filesCatalogId.value = item.id
  filesCatalogName.value = item.name
  filesDrawerVisible.value = true
}

function askDelete(item: SkillCatalogItem) {
  if (!canDelete(item)) {
    MessagePlugin.warning(t('settings.skills.deleteCatalogBlocked'))
    return
  }
  confirmDelete({
    body: t('settings.skills.deleteCatalogConfirm', { name: item.name }),
    onConfirm: () => removeCatalog(item),
  })
}

function defaultAddTargets(): string[] {
  const preferred = (props.initialSandboxId || '').trim()
  if (preferred && skillConfigs.value.some((cfg) => cfg.id === preferred)) return [preferred]
  if (skillConfigs.value.length === 1) return [skillConfigs.value[0].id]
  return []
}

function revealCatalog(id: string) {
  focusedCatalogId.value = id
  if (focusTimer != null) window.clearTimeout(focusTimer)
  focusTimer = window.setTimeout(() => {
    if (focusedCatalogId.value === id) focusedCatalogId.value = ''
    focusTimer = null
  }, 2400)
}

function resetAddWizard() {
  addStep.value = 0
  registeredCatalog.value = null
  pendingFile.value = null
  sourceInput.value = ''
  addTargetIds.value = []
  uploadPercent.value = 0
  if (fileInputRef.value) fileInputRef.value.value = ''
}

async function openAdd() {
  resetAddWizard()
  await loadInstallerModel()
  showAdd.value = true
}

function canJumpAddStep(index: number) {
  if (index === addStep.value) return false
  return Boolean(registeredCatalog.value) || index < addStep.value
}

function goToAddStep(index: number) {
  if (!canJumpAddStep(index) && index !== addStep.value) return
  addStep.value = index
}

function addPreviousStep() {
  if (addStep.value <= 0) return
  addStep.value -= 1
}

function openInstall(item: SkillCatalogItem) {
  installCatalog.value = item
  const remaining = targetsFor(item)
  installTargetIds.value = remaining.length === 1 ? [remaining[0].id] : []
  void loadInstallerModel()
  showInstall.value = true
}

function openManage(item: SkillCatalogItem, inst: SkillCatalogInstall) {
  const record = recordFor(inst.sandbox_config_id)
  if (!record) return
  manageRecord.value = record
  manageSkillId.value = inst.skill_id
  manageTitle.value = item.name
  showManage.value = true
}

function onPanelUpdated(record: SandboxConfigRecord) {
  records.value = records.value.map((item) => (item.id === record.id ? { ...item, ...record } : item))
}

function readLastChatModelID(): string {
  try {
    return localStorage.getItem(LAST_CHAT_MODEL_KEY) || ''
  } catch {
    return ''
  }
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

async function ensureInstallerModelIfNeeded(configIds: string[]) {
  if (configIds.length === 0) return
  if (!installerModelId.value) {
    throw new Error(t('settings.sandbox.skillInstallerModelRequired'))
  }
  await persistInstallerModel(installerModelId.value)
}

function isZipFile(file: File): boolean {
  return file.name.toLowerCase().endsWith('.zip') || file.type === 'application/zip'
}

function acceptPendingFile(file: File) {
  if (addBusy.value || registeredCatalog.value) return
  if (!isZipFile(file)) {
    MessagePlugin.error(t('settings.sandbox.skillUploadFailed'))
    return
  }
  if (file.size > MAX_FILE_SIZE_BYTES) {
    MessagePlugin.error(t('settings.sandbox.skillBundleTooLarge', { size: maxSkillBundleMB }))
    return
  }
  pendingFile.value = file
}

function skillRegisterErrorMessage(err: any, fromFile: boolean): string {
  const raw = String(err?.message || '')
  if (/cannot exceed \d+\s*MB/i.test(raw)) {
    return t('settings.sandbox.skillBundleTooLarge', { size: maxSkillBundleMB })
  }
  const tooMany = raw.match(/archive holds more than (\d+) files/i)
  if (tooMany) {
    return t('settings.sandbox.skillBundleTooManyFiles', { count: tooMany[1] })
  }
  if (raw) return raw
  return fromFile
    ? t('settings.sandbox.skillUploadFailed')
    : t('settings.sandbox.skillSourceFailed')
}

async function registerThenAdvance() {
  if (registeredCatalog.value) {
    addTargetIds.value = defaultAddTargets()
    addStep.value = 1
    return
  }
  const source = sourceInput.value.trim()
  if (!pendingFile.value && !source) return

  try {
    let registered: SkillCatalogRegisterResult | null = null
    if (pendingFile.value) {
      uploading.value = true
      uploadPercent.value = 0
      const res = await registerSkillCatalogFromFile(pendingFile.value, (percent) => {
        uploadPercent.value = percent
      })
      registered = catalogFromRegister(res?.data, pendingFile.value.name)
    } else {
      addingFromSource.value = true
      const res = await registerSkillCatalogFromSource(source)
      registered = catalogFromRegister(res?.data, source)
    }
    if (!registered) {
      await loadCatalog()
      return
    }
    registeredCatalog.value = registered
    addTargetIds.value = defaultAddTargets()
    addStep.value = 1
    MessagePlugin.success(t('settings.skills.registerAccepted'))
    await loadCatalog()
    syncRegisteredFromCatalog()
  } catch (e: any) {
    MessagePlugin.error(skillRegisterErrorMessage(e, Boolean(pendingFile.value)))
  } finally {
    uploading.value = false
    addingFromSource.value = false
    uploadPercent.value = 0
    if (fileInputRef.value) fileInputRef.value.value = ''
  }
}

function catalogInstallFailedCount(res: { data?: { errors?: Record<string, string> } } | null | undefined): number {
  return Object.keys(res?.data?.errors || {}).length
}

async function handleAddPrimary() {
  if (addPrimaryLoading.value || addPrimaryDisabled.value) return
  if (addStep.value === 0) {
    await registerThenAdvance()
    return
  }
  const catalogId = registeredCatalog.value?.id
  if (!catalogId) return
  const targets = [...addTargetIds.value]
  if (targets.length === 0) {
    showAdd.value = false
    revealCatalog(catalogId)
    return
  }
  installing.value = true
  try {
    await ensureInstallerModelIfNeeded(targets)
    const res = await installSkillCatalog(catalogId, targets)
    const failed = catalogInstallFailedCount(res)
    if (failed > 0) {
      MessagePlugin.warning(t('settings.skills.installPartial', { failed }))
    } else {
      MessagePlugin.success(t('settings.skills.installAccepted'))
    }
    showAdd.value = false
    await loadCatalog()
    revealCatalog(catalogId)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.skillUploadFailed'))
  } finally {
    installing.value = false
  }
}

function onFileInputChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (file) acceptPendingFile(file)
}

function onFileDrop(event: DragEvent) {
  if (addBusy.value || registeredCatalog.value) return
  const file = event.dataTransfer?.files?.[0]
  if (file) acceptPendingFile(file)
}

async function confirmInstall() {
  const item = installCatalog.value
  if (!item || installTargetIds.value.length === 0) return
  installing.value = true
  try {
    await ensureInstallerModelIfNeeded(installTargetIds.value)
    const res = await installSkillCatalog(item.id, [...installTargetIds.value])
    const failed = catalogInstallFailedCount(res)
    if (failed > 0) {
      MessagePlugin.warning(t('settings.skills.installPartial', { failed }))
    } else {
      MessagePlugin.success(t('settings.skills.installAccepted'))
    }
    showInstall.value = false
    await loadCatalog()
    revealCatalog(item.id)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.skillUploadFailed'))
  } finally {
    installing.value = false
  }
}

async function removeCatalog(item: SkillCatalogItem) {
  if (!canDelete(item)) {
    MessagePlugin.warning(t('settings.skills.deleteCatalogBlocked'))
    return
  }
  deletingId.value = item.id
  try {
    await deleteSkillCatalog(item.id)
    MessagePlugin.success(t('settings.skills.deleteSuccess'))
    await loadCatalog()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('common.deleteFailed'))
  } finally {
    deletingId.value = ''
  }
}

function catalogBusy(): boolean {
  return catalog.value.some((item) =>
    liveInstalls(item).some((inst) => inst.status === 'installing' || inst.status === 'removing'),
  )
}

function stopPoll() {
  if (pollTimer != null) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
}

function ensurePoll() {
  if (!catalogBusy()) {
    stopPoll()
    return
  }
  if (pollTimer != null) return
  pollTimer = window.setInterval(() => {
    void loadCatalog(true)
  }, 2500)
}

async function loadCatalog(silent = false) {
  try {
    const res = await listSkillCatalog()
    catalog.value = res?.data || []
  } catch (e: any) {
    if (!silent) MessagePlugin.error(e?.message || t('settings.skills.loadFailed'))
  } finally {
    ensurePoll()
  }
}

async function load() {
  loading.value = true
  try {
    const [configRes] = await Promise.all([listSandboxConfigs(), loadCatalog()])
    records.value = configRes?.data || []
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.skills.loadFailed'))
  } finally {
    loading.value = false
  }
}

watch(showManage, (open) => {
  if (!open) {
    void loadCatalog()
    manageSkillId.value = ''
    manageRecord.value = null
  } else {
    openPanelId.value = ''
  }
})

watch(showInstall, (open) => {
  if (open) openPanelId.value = ''
})

watch(showAdd, (open) => {
  if (open) {
    openPanelId.value = ''
    return
  }
  const catalogId = registeredCatalog.value?.id
  resetAddWizard()
  void loadCatalog()
  if (catalogId) revealCatalog(catalogId)
})

onMounted(load)
onUnmounted(() => {
  stopPoll()
  if (focusTimer != null) window.clearTimeout(focusTimer)
})
</script>

<style lang="less" scoped>
.skill-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 28px;

  &__title-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
  }

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
  }

  &__help {
    color: var(--td-text-color-placeholder);
    font-size: 16px;
    cursor: help;
    transition: color 0.15s ease;

    &:hover {
      color: var(--td-text-color-secondary);
    }
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.6;
  }
}

:global(.skill-settings__help-tooltip .t-popup__content) {
  max-width: 340px;
  line-height: 1.55;
}

:global(.skill-install-panel-overlay) {
  z-index: 3050 !important;
}

:global(.skill-install-panel-overlay .t-popup__content) {
  padding: 0 !important;
  border-radius: 6px !important;
  border: 1px solid var(--td-component-stroke) !important;
  background: var(--td-bg-color-container) !important;
  box-shadow: var(--td-shadow-2, 0 3px 14px 2px rgba(0, 0, 0, 0.05)) !important;
}

.loading-container {
  padding: 40px 0;
  text-align: center;
}

.empty-state {
  padding: 80px 0;
  text-align: center;

  :deep(.t-empty__description) {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    margin-bottom: 16px;
  }
}

.empty-hint {
  margin: 0 0 16px;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.empty-actions {
  display: flex;
  justify-content: center;
  gap: 8px;
}

.skill-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 10px;
  align-items: stretch;
}

.skill-card {
  position: relative;
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
  min-width: 0;
  height: 100%;

  &--focused {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 2px var(--td-brand-color-focus, rgba(0, 168, 112, 0.18));
  }

  &--installed .skill-card__badge {
    background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
    color: var(--td-brand-color);
  }

  &--add {
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 6px;
    height: 100%;
    padding: 16px 12px;
    border-style: dashed;
    background: transparent;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    font: inherit;
    text-align: center;

    &:hover,
    &:focus-visible {
      color: var(--td-brand-color);
      border-color: var(--td-brand-color);
      background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
      box-shadow: none;

      .skill-card--add__icon {
        background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
        color: var(--td-brand-color);
      }
    }

    &__icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      border-radius: 8px;
      background: var(--td-bg-color-secondarycontainer);
      color: var(--td-text-color-secondary);
      font-size: 18px;
    }

    &__label {
      font-size: 13px;
      font-weight: 500;
      line-height: 1.4;
    }
  }
}

.skill-card__main {
  display: flex;
  align-items: stretch;
  gap: 10px;
  padding: 10px 12px;
  min-width: 0;
  flex: 1;
}

.skill-card__badge {
  flex-shrink: 0;
  align-self: flex-start;
  width: 28px;
  height: 28px;
  border-radius: 7px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
}

.skill-card__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.skill-card__header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  min-height: 28px;
}

.skill-card__heading {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 6px;
}

.skill-card__title {
  flex: 0 1 auto;
  min-width: 0;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-card__actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 2px;
}

.skill-card__icon-btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  margin: 0;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: none;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  :deep(svg) {
    display: block;
    overflow: visible;
  }

  &:hover:not(:disabled) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &--danger:hover:not(:disabled) {
    color: var(--td-error-color);
    background: color-mix(in srgb, var(--td-error-color) 8%, transparent);
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.4;
  }
}

.skill-card__type {
  flex-shrink: 0;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.35;
  color: var(--td-text-color-placeholder);
}

.skill-card__desc {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  margin: 0;
  overflow: hidden;
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-secondary);
  word-break: break-word;
}

.skill-card__installs {
  display: flex;
  align-items: center;
  min-width: 0;
  margin-top: auto;
}

.skill-card__installs-label {
  font-size: 12px;
  line-height: 20px;
  color: var(--td-text-color-placeholder);
}

.skill-card__chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  max-width: 100%;
  margin: 0;
  padding: 3px 8px 3px 10px;
  border: 0;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  line-height: 20px;
  text-align: left;

  &:hover:not(:disabled) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &:disabled {
    cursor: default;
    opacity: 0.6;
  }

  &--idle {
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);

    &:hover:not(:disabled) {
      color: var(--td-brand-color);
      background: color-mix(in srgb, var(--td-brand-color) 16%, transparent);
    }

    .skill-card__chip-go {
      color: var(--td-brand-color);
    }
  }

  &.skill-card__entry--off {
    color: var(--td-text-color-placeholder);
  }

  &--installed .skill-card__entry-status {
    color: var(--td-success-color, var(--td-brand-color));
  }

  &.skill-card__entry--stale {
    background: color-mix(in srgb, var(--td-warning-color) 10%, transparent);

    .skill-card__entry-status {
      color: var(--td-warning-color);
    }
  }

  &.skill-card__entry--failed {
    background: color-mix(in srgb, var(--td-error-color) 10%, transparent);

    .skill-card__entry-status {
      color: var(--td-error-color);
    }
  }

  &.skill-card__entry--busy .skill-card__entry-dot {
    background: var(--td-warning-color);
  }
}

.skill-card__chip-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-card__chip-go,
.skill-card__entry-status {
  flex-shrink: 0;
}

.skill-card__entry--ready .skill-card__entry-status {
  color: var(--td-success-color, var(--td-brand-color));
}

.skill-card__chip-go {
  color: var(--td-text-color-placeholder);
}

.skill-card__chip:hover:not(:disabled) .skill-card__chip-go {
  color: currentColor;
}

.skill-card__entry-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  animation: skill-chip-dot 1.2s ease-in-out infinite;
}

.skill-install-panel {
  display: flex;
  flex-direction: column;
  width: 200px;
  max-width: calc(100vw - 32px);
  padding: 4px 0;
}

.skill-install-panel__group {
  margin: 0;
  padding: 6px 12px 4px;
  font-size: 12px;
  line-height: 20px;
  color: var(--td-text-color-placeholder);
}

.skill-install-panel__item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  height: 32px;
  margin: 0;
  padding: 0 12px;
  border: 0;
  border-radius: 0;
  background: none;
  color: var(--td-text-color-primary);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  line-height: 22px;
  text-align: left;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-container-hover);
  }

  &:disabled {
    cursor: default;
    opacity: 0.5;
  }

  &.skill-card__entry--stale .skill-card__entry-status {
    color: var(--td-warning-color);
  }

  &.skill-card__entry--failed .skill-card__entry-status {
    color: var(--td-error-color);
  }

  &.skill-card__entry--busy .skill-card__entry-dot {
    background: var(--td-warning-color);
  }
}

.skill-install-panel__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-install-panel__split {
  height: 1px;
  margin: 4px 0;
  background: var(--td-component-stroke);
}

@keyframes skill-chip-dot {
  0%,
  100% { opacity: 1; }
  50% { opacity: 0.35; }
}

.installer-model-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.parsed-skill {
  margin: 0 0 16px;
}

.skill-add-steps {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
}

.skill-add-step {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: var(--td-text-color-placeholder);
  transition: color 0.15s ease;

  &:not(:last-child) {
    flex: 1;
  }

  &.is-active {
    color: var(--td-brand-color);
  }

  &.is-done {
    color: var(--td-text-color-secondary);
  }

  &.is-clickable {
    padding: 0;
    font: inherit;
    text-align: left;
    background: none;
    border: 0;
    cursor: pointer;

    &:hover:not(.is-active) {
      color: var(--td-brand-color);
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
      border-radius: 4px;
    }
  }
}

.skill-add-step__marker {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  border: 1px solid currentColor;
  border-radius: 50%;
  font-size: 11px;
  font-weight: 600;
  line-height: 1;

  .is-active & {
    background: var(--td-brand-color);
    border-color: var(--td-brand-color);
    color: #fff;
  }

  .is-done & {
    background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
    border-color: color-mix(in srgb, var(--td-brand-color) 35%, transparent);
    color: var(--td-brand-color);
  }
}

.skill-add-step__title {
  overflow: hidden;
  font-size: 13px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-add-step__line {
  flex: 1;
  min-width: 16px;
  height: 1px;
  margin: 0 4px;
  background: var(--td-component-stroke);

  .is-done & {
    background: color-mix(in srgb, var(--td-brand-color) 35%, transparent);
  }
}

.sandbox-pick-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;

  :deep(.t-checkbox) {
    width: 100%;
    margin: 0;
    align-items: center;
    padding: 10px 12px;
    border: 1px solid var(--td-component-stroke);
    border-radius: 10px;
    background: var(--td-bg-color-container);
    cursor: pointer;

    &:hover:not(.t-is-disabled) {
      border-color: color-mix(in srgb, var(--td-brand-color) 40%, transparent);
      background: color-mix(in srgb, var(--td-brand-color) 4%, transparent);
    }

    &.t-is-checked {
      border-color: color-mix(in srgb, var(--td-brand-color) 40%, transparent);
      background: color-mix(in srgb, var(--td-brand-color) 5%, transparent);
    }
  }

  :deep(.t-checkbox__label) {
    width: 100%;
    margin: 0;
    padding-left: 8px;
    white-space: normal;
  }

  :deep(.t-checkbox__former),
  :deep(.t-checkbox__input) {
    flex-shrink: 0;
    margin-top: 0;
    align-self: center;
  }
}

.sandbox-pick__main {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.sandbox-pick__text {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.sandbox-pick__name {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  line-height: 1.3;
}

.sandbox-pick__meta {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.3;
}

.skill-source-row {
  width: 100%;
}

.file-input-hidden {
  display: none;
}

.file-upload-area {
  border: 1px dashed var(--td-component-stroke);
  border-radius: 10px;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease;

  &:hover:not(.is-disabled) {
    border-color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 4%, transparent);
  }

  &.is-disabled {
    cursor: not-allowed;
    opacity: 0.6;
  }
}

.file-upload-content {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 24px 20px;
}

.file-upload-icon-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 56px;
  height: 56px;
  border-radius: 50%;
  background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
}

.upload-icon {
  color: var(--td-brand-color);
}

.upload-text {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}

.upload-primary-text {
  font-size: 15px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.upload-secondary-text {
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.upload-file-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-brand-color);
}
</style>
