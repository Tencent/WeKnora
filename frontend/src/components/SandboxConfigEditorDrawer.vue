<template>
  <SettingDrawer
    class="sandbox-config-drawer"
    :visible="visible"
    :title="record ? $t('settings.sandbox.editTitle') : $t('settings.sandbox.createTitle')"
    :description="stepDescription"
    icon="code"
    width="680px"
    :min-width="560"
    :max-width="920"
    storage-key="setting-drawer:width:sandbox-config-v2"
    :confirm-loading="saving || checking || templatesLoading"
    :confirm-disabled="primaryDisabled"
    :confirm-text="primaryText"
    @confirm="handlePrimaryAction"
    @cancel="close"
    @update:visible="(v: boolean) => emit('update:visible', v)"
  >
    <template #footer-left>
      <t-button v-if="wizardStep > 0" variant="outline" @click="previousStep">
        {{ $t('settings.sandbox.back') }}
      </t-button>
      <t-popconfirm
        v-if="canDeepCheck"
        :content="$t('settings.sandbox.deepCheckConfirm')"
        @confirm="runCheck(true)"
      >
        <t-button variant="outline" :loading="checking">
          {{ lastCheckWasDeep ? $t('settings.sandbox.recheck') : $t('settings.sandbox.deepCheck') }}
        </t-button>
      </t-popconfirm>
    </template>

    <template #header-extra>
      <nav class="sandbox-steps" :aria-label="$t('settings.sandbox.setupProgress')">
        <component
          :is="canJumpTo(index) ? 'button' : 'div'"
          v-for="(item, index) in wizardSteps"
          :key="item.key"
          :type="canJumpTo(index) ? 'button' : undefined"
          :class="['sandbox-step', {
            'is-active': wizardStep === index,
            'is-done': wizardStep > index,
            'is-clickable': canJumpTo(index),
          }]"
          :aria-current="wizardStep === index ? 'step' : undefined"
          @click="goToStep(index)"
        >
          <span class="sandbox-step__marker">
            <t-icon v-if="wizardStep > index" name="check" />
            <template v-else>{{ index + 1 }}</template>
          </span>
          <span class="sandbox-step__title">{{ item.title }}</span>
          <span v-if="index < wizardSteps.length - 1" class="sandbox-step__line" aria-hidden="true" />
        </component>
      </nav>
    </template>

    <!--
      Identity-change refusals must sit at the top: the form is long and the
      admin otherwise saves, sees nothing, and assumes the click did nothing.
    -->
    <div v-if="conflict && currentStepKey === 'runtime'" ref="conflictAlertRef" class="blocked blocked-top">
      <t-alert v-if="conflict.code === 'sandboxes_still_live'" theme="warning"
        :message="$t('settings.sandbox.sandboxesStillLive', { count: conflict.inventory?.sandbox_count ?? 0 })">
        <template #description>
          <p v-if="affectedSessionCount">{{ $t('settings.sandbox.affectedSessions', { count: affectedSessionCount }) }}</p>
          <p v-if="conflict.inventory?.agent_names?.length">
            {{ $t('settings.sandbox.affectedAgents', { names: conflict.inventory.agent_names.join('、') }) }}
          </p>
          <p>{{ $t('settings.sandbox.blockedHint') }}</p>
        </template>
      </t-alert>
      <t-alert v-else theme="warning" :message="$t('settings.sandbox.unverifiableBlocked')">
        <template #description>
          <p>{{ $t('settings.sandbox.unverifiableSaveHint') }}</p>
        </template>
      </t-alert>
    </div>

    <t-form label-align="top" class="sandbox-editor-form">
      <section v-if="currentStepKey === 'connection'" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionBasic') }}</h4>
        <t-form-item :label="$t('settings.sandbox.backendType')">
          <t-select :value="backend" :placeholder="$t('settings.sandbox.backendTypePlaceholder')"
            class="backend-select" :popup-props="{ overlayClassName: 'sandbox-backend-popup' }"
            @change="(v: any) => selectBackend(String(v))">
            <t-option v-for="opt in backendOptions" :key="opt" :value="opt" :label="backendLabel(opt)">
              <span class="backend-choice">
                <SandboxBackendBadge :type="opt" size="sm" />
                <span class="backend-choice__text">
                  <span class="backend-choice__name">{{ backendLabel(opt) }}</span>
                  <span class="backend-choice__desc">
                    {{ $t(`settings.sandbox.backendDescriptions.${opt}`) }}
                  </span>
                </span>
              </span>
            </t-option>
          </t-select>
          <p v-if="backend" class="section-help section-help--field">
            {{ $t(`settings.sandbox.backendDescriptions.${backend}`) }}
          </p>
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.configName')" :status="nameError ? 'error' : undefined"
          :tips="nameError || undefined">
          <t-input v-model="name" :placeholder="$t('settings.sandbox.configNamePlaceholder')" />
        </t-form-item>
        <t-form-item :label="$t('settings.sandbox.configDescription')">
          <t-input v-model="description" :placeholder="$t('settings.sandbox.configDescriptionPlaceholder')" />
        </t-form-item>
      </section>

      <section v-if="currentStepKey === 'connection' && isRemoteBackend" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionConnection') }}</h4>
        <t-alert v-if="record" theme="info" class="identity-hint compact-alert"
          :message="$t('settings.sandbox.identityFieldHint')" />

        <template v-if="backend === 'cube'">
          <t-form-item :label="requiredLabel('apiUrl')" :status="fieldStatus('api_url')" :tips="fieldTip('api_url')">
            <t-input v-model="cube.api_url" placeholder="http://cube.example.com:33000"
              @input="onConnectionInput('api_url')" />
          </t-form-item>
          <div class="form-grid form-grid--two">
            <t-form-item :label="requiredLabel('proxyUrl')" :status="fieldStatus('proxy_url')"
              :tips="fieldTip('proxy_url')">
              <t-input v-model="cube.proxy_url" placeholder="http://cube.example.com:80"
                @input="onConnectionInput('proxy_url')" />
            </t-form-item>
            <t-form-item :label="requiredLabel('sandboxDomain')" :status="fieldStatus('sandbox_domain')"
              :tips="fieldTip('sandbox_domain')">
              <t-input v-model="cube.sandbox_domain" placeholder="cube.app"
                @input="onConnectionInput('sandbox_domain')" />
            </t-form-item>
          </div>
          <t-form-item :label="$t('settings.sandbox.apiKey')">
            <t-input v-model="cube.api_key" type="password" :placeholder="secretInputPlaceholder('cube')"
              @input="invalidateConnection" />
            <div class="field-hints">
              <p class="section-help">
                {{ storedSecrets.cube
                  ? $t('settings.sandbox.secretConfigured')
                  : $t('settings.sandbox.cubeApiKeyOptional') }}
              </p>
              <a class="inline-guide-link" :href="clusterGuideUrl" target="_blank" rel="noopener noreferrer">
                <t-icon name="link" />
                {{ $t('settings.sandbox.cubeApiKeyWhere') }}
              </a>
            </div>
          </t-form-item>
        </template>

        <template v-else-if="backend === 'e2b'">
          <t-form-item :label="requiredLabel('apiKey')" :status="fieldStatus('api_key')"
            :tips="fieldTip('api_key')">
            <t-input v-model="e2b.api_key" type="password" :placeholder="secretInputPlaceholder('e2b')"
              @input="onConnectionInput('api_key')" />
            <div class="field-hints">
              <p class="section-help">
                {{ storedSecrets.e2b
                  ? $t('settings.sandbox.secretConfigured')
                  : $t('settings.sandbox.e2bApiKeyHelp') }}
              </p>
              <a class="inline-guide-link" :href="e2bApiKeysUrl" target="_blank" rel="noopener noreferrer">
                <t-icon name="link" />
                {{ $t('settings.sandbox.e2bApiKeyWhere') }}
              </a>
            </div>
          </t-form-item>
          <div class="form-grid form-grid--two">
            <t-form-item :label="$t('settings.sandbox.apiUrl')" :help="$t('settings.sandbox.e2bApiUrlOptional')">
              <t-input v-model="e2b.api_url" placeholder="https://api.e2b.app" @input="invalidateConnection" />
            </t-form-item>
            <t-form-item :label="$t('settings.sandbox.sandboxDomain')" :help="$t('settings.sandbox.e2bDomainOptional')">
              <t-input v-model="e2b.sandbox_domain" placeholder="e2b.app" @input="invalidateConnection" />
            </t-form-item>
          </div>
          <t-form-item :label="$t('settings.sandbox.proxyUrl')" :help="$t('settings.sandbox.e2bProxyUrlOptional')">
            <t-input v-model="e2b.proxy_url" placeholder="http://sandbox-gateway.example.com"
              @input="invalidateConnection" />
          </t-form-item>
        </template>
        <div class="private-endpoint-row">
          <div>
            <p class="private-endpoint-row__title">{{ $t('settings.sandbox.allowPrivateEndpoints') }}</p>
            <p class="section-help">{{ $t('settings.sandbox.allowPrivateEndpointsHint') }}</p>
          </div>
          <t-switch v-model="allowPrivateEndpoints" @change="invalidateConnection" />
        </div>
      </section>

      <section v-if="currentStepKey === 'connection' && !isRemoteBackend" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionRuntimeEnvironment') }}</h4>
        <template v-if="backend === 'docker'">
          <div class="weknora-template-card is-active">
            <SandboxBackendBadge type="docker" />
            <div class="weknora-template-card__content">
              <div class="weknora-template-card__title-row">
                <span class="weknora-template-card__title">{{ $t('settings.sandbox.weknoraDockerImage') }}</span>
                <t-tag theme="primary" variant="light" size="small">{{ $t('settings.sandbox.recommendedTag') }}</t-tag>
              </div>
              <p>{{ $t('settings.sandbox.weknoraDockerImageHint') }}</p>
            </div>
          </div>
          <t-form-item :label="requiredLabel('dockerImage')" :status="fieldStatus('image')"
            :tips="fieldTip('image')">
            <t-input v-model="docker.image" placeholder="wechatopenai/weknora-sandbox:latest"
              @input="onFieldInput('image')" />
          </t-form-item>
          <t-form-item :label="$t('settings.sandbox.dockerHost')" :help="$t('settings.sandbox.dockerHostHelp')">
            <t-input v-model="docker.host" placeholder="unix:///var/run/docker.sock"
              @input="onFieldInput('host')" />
          </t-form-item>
          <t-form-item :label="$t('settings.sandbox.dockerTlsCertPath')"
            :help="$t('settings.sandbox.dockerTlsCertPathHelp')">
            <t-input v-model="docker.tls_cert_path" placeholder="/etc/weknora/docker-certs"
              @input="onFieldInput('tls_cert_path')" />
          </t-form-item>
        </template>
        <t-alert v-else theme="warning" class="compact-alert" :message="$t('settings.sandbox.localRuntimeWarning')" />
      </section>

      <section v-if="currentStepKey === 'template'" class="setting-drawer__section">
        <div class="section-title-row">
          <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionTemplate') }}</h4>
          <t-button variant="text" size="small" :loading="templatesLoading" @click="loadTemplates(true)">
            <template #icon><t-icon name="refresh" /></template>
            {{ $t('settings.sandbox.refreshTemplates') }}
          </t-button>
        </div>
        <div class="connection-summary" role="status">
          <span class="connection-summary__icon"><t-icon name="check" /></span>
          <div>
            <p class="connection-summary__title">{{ $t('settings.sandbox.connectionPassedTitle') }}</p>
            <p class="connection-summary__text">{{ $t('settings.sandbox.connectionPassed') }}</p>
          </div>
        </div>
        <p class="section-help">{{ $t('settings.sandbox.templateStepHint') }}</p>
        <div v-if="templatesLoading && !templatesLoaded" class="template-loading">
          <t-loading size="small" />
          <span>{{ $t('settings.sandbox.loadingTemplates') }}</span>
        </div>
        <div v-else-if="templates.length" class="template-list" role="radiogroup"
          :aria-label="$t('settings.sandbox.sectionTemplate')">
          <button v-for="item in templates" :key="item.id" type="button" class="template-card"
            :class="{ 'is-active': currentTemplateId === item.id, 'is-pending': isTemplatePending(item) }"
            :disabled="!isTemplateSelectable(item)" role="radio" :aria-checked="currentTemplateId === item.id"
            @click="selectTemplate(item.id)">
            <span class="template-card__badge">
              <t-icon :name="isTemplatePending(item) ? 'time' : 'code'" />
            </span>
            <span class="template-card__body">
              <span class="template-card__title-row">
                <span class="template-card__title">{{ item.name }}</span>
                <t-tag v-if="item.standard" theme="primary" variant="light" size="small">
                  {{ $t('settings.sandbox.recommendedTag') }}
                </t-tag>
              </span>
              <span class="template-card__meta">{{ templateMeta(item) }}</span>
              <!--
                An untagged template is indistinguishable from a building one in
                the provider's list, so it needs its own line: waiting is futile
                and only a rebuild fixes it.
              -->
              <span v-if="isTemplateUntagged(item)" class="template-card__hint template-card__hint--error">
                {{ $t('settings.sandbox.templateUntaggedHint') }}
              </span>
              <span v-else-if="templateFailureReason(item)" class="template-card__hint template-card__hint--error">
                {{ templateFailureReason(item) }}
              </span>
              <span v-else-if="isTemplatePending(item) && item.standard" class="template-card__hint">
                {{ $t('settings.sandbox.templateBuildingHint') }}
              </span>
            </span>
            <span class="template-card__state">
              <t-tag :theme="templateStatusTheme(item)" variant="light" size="small">
                {{ templateStatusLabel(item) }}
              </t-tag>
              <t-icon v-if="currentTemplateId === item.id" name="check-circle-filled"
                class="template-card__selected" />
            </span>
          </button>
        </div>
        <div v-else-if="templatesLoaded && !templatesError" class="env-empty">
          {{ $t('settings.sandbox.noTemplates') }}
        </div>
        <div v-if="selectedTemplate" class="template-state-note template-state-note--ready" role="status">
          <t-icon name="check-circle-filled" />
          <span>{{ $t('settings.sandbox.templateReadyHint', { name: selectedTemplate.name }) }}</span>
        </div>
        <div v-else-if="hasPendingTemplates" class="template-state-note" role="status">
          <t-icon name="time" />
          <span>{{ $t('settings.sandbox.templateProvisioningHint') }}</span>
        </div>
        <t-alert v-if="templatesError" theme="warning" class="compact-alert" :message="templatesError" />
        <p v-else-if="!templatesLoaded" class="section-help">
          {{ $t('settings.sandbox.templateLoadHint') }}
        </p>
        <a class="inline-guide-link" :href="clusterGuideUrl" target="_blank" rel="noopener noreferrer">
          <t-icon name="link" />
          {{ $t('settings.sandbox.howToBuildTemplate') }}
        </a>
      </section>

      <section v-if="currentStepKey === 'runtime'" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionRuntime') }}</h4>
        <!--
          Three unlabelled numbers side by side shared one footnote, so nobody
          could tell which limit they were raising. Each gets its own row and
          its own sentence naming what it bounds and what happens on expiry.
        -->
        <div class="runtime-fields">
          <template v-if="isRemoteBackend">
            <t-form-item :label="$t('settings.sandbox.httpTimeout')">
              <t-input-number v-if="backend === 'cube'" v-model="cube.http_timeout_sec" :min="0" theme="column"
                placeholder="30" />
              <t-input-number v-else v-model="e2b.http_timeout_sec" :min="0" theme="column" placeholder="30" />
            </t-form-item>
            <p class="section-help section-help--field">{{ $t('settings.sandbox.httpTimeoutHelp') }}</p>
            <t-form-item :label="$t('settings.sandbox.sandboxTtl')">
              <t-input-number v-if="backend === 'cube'" v-model="cube.cube_sandbox_ttl_seconds" :min="0"
                theme="column" placeholder="1800" />
              <t-input-number v-else v-model="e2b.e2b_sandbox_ttl_seconds" :min="0" theme="column"
                placeholder="300" />
            </t-form-item>
            <p class="section-help section-help--field">{{ $t('settings.sandbox.sandboxTtlHelp') }}</p>
          </template>
          <!--
            Docker has no provider-side timeout at all: an abandoned container
            keeps its memory and CPU share on the daemon host until WeKnora
            reclaims it, so the idle TTL and the resource caps are the only
            things bounding what one workspace can hold.
          -->
          <template v-if="backend === 'docker'">
            <t-form-item :label="$t('settings.sandbox.dockerIdleTtl')">
              <t-input-number v-model="docker.idle_ttl_seconds" :min="0" theme="column" placeholder="1800" />
            </t-form-item>
            <p class="section-help section-help--field">{{ $t('settings.sandbox.dockerIdleTtlHelp') }}</p>
            <t-form-item :label="$t('settings.sandbox.dockerCpuLimit')">
              <t-input-number v-model="docker.cpu_limit" :min="0" :step="0.5" theme="column" placeholder="2" />
            </t-form-item>
            <t-form-item :label="$t('settings.sandbox.dockerMemoryLimit')">
              <t-input-number v-model="docker.memory_limit_mb" :min="0" theme="column" placeholder="2048" />
            </t-form-item>
            <t-form-item :label="$t('settings.sandbox.dockerPidsLimit')">
              <t-input-number v-model="docker.pids_limit" :min="0" theme="column" placeholder="512" />
            </t-form-item>
            <p class="section-help section-help--field">{{ $t('settings.sandbox.dockerResourceHelp') }}</p>
            <t-form-item :label="$t('settings.sandbox.dockerNetworkMode')">
              <t-select v-model="docker.network_mode" :placeholder="$t('settings.sandbox.dockerNetworkBridge')"
                clearable>
                <t-option value="bridge" :label="$t('settings.sandbox.dockerNetworkBridge')" />
                <t-option value="none" :label="$t('settings.sandbox.dockerNetworkNone')" />
              </t-select>
            </t-form-item>
            <p class="section-help section-help--field">{{ $t('settings.sandbox.dockerNetworkModeHelp') }}</p>
          </template>
          <t-form-item :label="$t('settings.sandbox.defaultTimeout')">
            <t-input-number v-model="defaultTimeoutSec" :min="0" theme="column" placeholder="60" />
          </t-form-item>
          <p class="section-help section-help--field">{{ $t('settings.sandbox.defaultTimeoutHelp') }}</p>
        </div>
      </section>

      <section v-if="currentStepKey === 'runtime'" class="setting-drawer__section">
        <div class="section-title-row">
          <div>
            <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.sectionEnvironment') }}</h4>
            <p class="section-help section-help--under-title">{{ $t('settings.sandbox.envVarsHint') }}</p>
          </div>
          <t-button variant="text" size="small" @click="envRows.push({ key: '', value: '' })">
            <template #icon><t-icon name="add" /></template>
            {{ $t('settings.sandbox.addRow') }}
          </t-button>
        </div>
        <div v-if="envRows.length" class="env-rows">
          <div v-for="(row, index) in envRows" :key="index" class="env-row">
            <t-input v-model="row.key" :placeholder="$t('settings.sandbox.envKey')" class="env-key" />
            <t-input v-model="row.value" type="password"
              :placeholder="row.stored ? $t('settings.sandbox.secretKeepHint') : $t('settings.sandbox.envValue')"
              class="env-value" />
            <t-button variant="text" shape="square" size="small" :aria-label="$t('common.delete')"
              @click="envRows.splice(index, 1)">
              <t-icon name="close" />
            </t-button>
          </div>
        </div>
        <div v-else class="env-empty">{{ $t('settings.sandbox.noEnvVars') }}</div>
      </section>
    </t-form>

    <!--
      Skills need an image to be installed into, so this step is only reachable
      once the config exists. During creation the wizard walks into it right
      after the first successful save; the hint covers the one case left, a
      config whose save was refused.
    -->
    <template v-if="currentStepKey === 'skills'">
      <SandboxSkillsPanel v-if="effectiveRecord" :record="effectiveRecord" @updated="onSkillsConfigUpdated" />
      <p v-else class="skills-locked">{{ $t('settings.sandbox.stepSkillsLocked') }}</p>
    </template>

    <div
      v-if="checkResult && showCheckResult"
      ref="checkResultRef"
      class="check-result"
    >
      <p :class="['check-result__title', checkResult.ok ? 'is-success' : 'is-error']">
        <t-icon :name="checkResult.ok ? 'check-circle-filled' : 'close-circle-filled'" />
        {{ checkResult.ok ? $t('settings.sandbox.checkPassed') : $t('settings.sandbox.checkFailed') }}
      </p>
      <p class="check-result__subtitle">{{ checkScopeHint }}</p>
      <ul class="check-list">
        <li v-for="item in reportedChecks" :key="item.name" class="check-item">
          <t-icon :name="item.ok === true ? 'check-circle-filled'
            : item.ok === false ? 'close-circle-filled' : 'minus-circle'"
            :class="item.ok === true ? 'ok' : item.ok === false ? 'err' : 'skip'" />
          <span class="check-name">{{ checkLabel(item.name) }}</span>
          <span v-if="item.latency_ms" class="check-latency">{{ item.latency_ms }} ms</span>
          <span v-if="checkDetail(item)" class="check-message">{{ checkDetail(item) }}</span>
        </li>
      </ul>
      <p v-if="pendingCheckNames.length" class="check-result__hint">
        {{ $t('settings.sandbox.checkPendingHint', { names: pendingCheckNames.join('、') }) }}
      </p>
      <t-alert v-if="checkResult.capabilities && checkResult.capabilities.supports_volumes === false" theme="warning"
        class="compact-alert"
        :message="$t('settings.sandbox.noVolumeSupport')" />
    </div>

  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import SandboxBackendBadge from '@/components/settings/SandboxBackendBadge.vue'
import SandboxSkillsPanel from '@/components/SandboxSkillsPanel.vue'
import {
  checkSandboxConfig,
  createSandboxConfig,
  parseSandboxConflict,
  updateSandboxConfigById,
  querySandboxTemplates,
  type SandboxCheckItem,
  type SandboxCheckResult,
  type SandboxConfig,
  type SandboxConfigRecord,
  type SandboxConflict,
  type SandboxCubeConfig,
  type SandboxE2BConfig,
  type SandboxDockerConfig,
  type SandboxTemplate,
  isNamedSandboxBackend,
  NAMED_SANDBOX_BACKEND_TYPES,
} from '@/api/system'

type SandboxStepKey = 'connection' | 'template' | 'runtime' | 'skills'

const props = defineProps<{
  visible: boolean
  record: SandboxConfigRecord | null
  presetType?: string
  // Which page to land on when opening an existing config, e.g. 'skills' from
  // the card's 管理技能 entry. Ignored while creating, where order is enforced.
  initialStep?: SandboxStepKey
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'saved'): void
}>()

const { t } = useI18n()

// The backend echoes stored secrets as this placeholder. It never leaves the
// form as visible text: inputs stay empty and say "configured", and the
// placeholder is only re-attached on submit so the stored value survives.
const secretPlaceholder = '***'
const isMaskedSecret = (value?: string) => value === secretPlaceholder

const clusterGuideUrl = 'https://github.com/Tencent/WeKnora/blob/main/docs/sandbox-cluster.md'
const e2bApiKeysUrl = 'https://e2b.dev/dashboard?tab=keys'

const backendOptions = [...NAMED_SANDBOX_BACKEND_TYPES]

const saving = ref(false)
const checking = ref(false)
const checkResult = ref<SandboxCheckResult | null>(null)
// Distinguishes "run the deep probes" from "run them again" on the panel button.
const lastCheckWasDeep = ref(false)
const conflict = ref<SandboxConflict | null>(null)
const conflictAlertRef = ref<HTMLElement | null>(null)
const checkResultRef = ref<HTMLElement | null>(null)
const nameError = ref('')

const name = ref('')
const description = ref('')
const backend = ref('')
// undefined rather than 0 so the input renders empty and shows its placeholder,
// matching the HTTP timeout / TTL fields. A literal 0 would read as a real value.
const defaultTimeoutSec = ref<number | undefined>(undefined)
const allowPrivateEndpoints = ref(false)
const cube = reactive<SandboxCubeConfig>({})
const e2b = reactive<SandboxE2BConfig>({})
const docker = reactive<SandboxDockerConfig>({})
// Tracks which secrets the tenant already has stored, so an empty input can
// mean "keep the saved key" instead of "no key configured".
const storedSecrets = reactive({ cube: false, e2b: false })
const envRows = ref<{ key: string; value: string; stored?: boolean }[]>([])
const templates = ref<SandboxTemplate[]>([])
const templatesLoading = ref(false)
const templatesLoaded = ref(false)
const templatesError = ref('')
const wizardStep = ref(0)
let templatePollTimer: ReturnType<typeof setTimeout> | undefined

// Remote backends additionally expose a template catalog and control-plane
// settings. All four backends still share the same save/check API.
const isRemoteBackend = computed(() => backend.value === 'cube' || backend.value === 'e2b')
const hasImageCatalog = computed(() => isRemoteBackend.value || backend.value === 'docker')
const currentTemplateId = computed(() => (
  backend.value === 'cube' ? cube.template_id : backend.value === 'e2b' ? e2b.template_id : ''
)?.trim() || '')
const selectedTemplate = computed(() => templates.value.find((item) => item.id === currentTemplateId.value))
const wizardSteps = computed<Array<{ key: SandboxStepKey; title: string }>>(() => {
  const steps: Array<{ key: SandboxStepKey; title: string }> = [
    { key: 'connection', title: t('settings.sandbox.stepConnection') },
  ]
  if (isRemoteBackend.value) {
    steps.push({ key: 'template', title: t('settings.sandbox.stepTemplate') })
  }
  steps.push({ key: 'runtime', title: t('settings.sandbox.stepRuntime') })
  // Skills are baked into the config's snapshot image. Local configs end
  // at runtime rather than showing a step that could never do anything.
  if (isRemoteBackend.value || backend.value === 'docker') {
    steps.push({ key: 'skills', title: t('settings.sandbox.stepSkills') })
  }
  return steps
})
const currentStepKey = computed<SandboxStepKey>(() => wizardSteps.value[wizardStep.value]?.key || 'connection')
const stepDescription = computed(() => t(`settings.sandbox.stepDescriptions.${currentStepKey.value}`))
const primaryText = computed(() => {
  if (currentStepKey.value === 'connection') return t('settings.sandbox.connectAndContinue')
  if (currentStepKey.value === 'template') return t('common.next')
  // Nothing on the skills step is pending a save — each install, toggle and
  // removal already went to the server on its own.
  if (currentStepKey.value === 'skills') return t('common.finish')
  // Runtime is the last page of settings. If skills follow, keep the wizard
  // going after the save; otherwise this press closes the drawer.
  if (wizardSteps.value.some((step) => step.key === 'skills')) {
    return t('settings.sandbox.saveAndContinue')
  }
  return t('common.save')
})
// Deep check needs the fields that actually get probed. Docker/local collect
// the image on the connection step; Cube/E2B still have an empty template_id
// there, so the action waits until the template step.
const canDeepCheck = computed(() => {
  if (currentStepKey.value === 'template') return true
  return currentStepKey.value === 'connection' && !isRemoteBackend.value
})
const showCheckResult = computed(() => canDeepCheck.value)
const primaryDisabled = computed(() => (
  currentStepKey.value === 'template'
  && (!selectedTemplate.value || !isTemplateSelectable(selectedTemplate.value))
))

// savedRecord is the config this drawer is editing, including one it just
// created: after the first save the wizard keeps going into the skills step,
// which needs an ID, and a second press of save must update that config rather
// than create another one.
const savedRecord = ref<SandboxConfigRecord | null>(null)
const effectiveRecord = computed(() => savedRecord.value || props.record)

function onSkillsConfigUpdated(record: SandboxConfigRecord) {
  savedRecord.value = record
}

// Jumping is what separates editing from creating. A config that does not exist
// yet has to be built in order — its connection has to check out before there
// are templates to choose from, and it has no image to install skills into. Once
// it exists, every step is just a page of its settings, so all of them open
// directly; steps already visited stay clickable during creation so the rail
// works as a way back.
function canJumpTo(index: number): boolean {
  if (index === wizardStep.value) return false
  return Boolean(effectiveRecord.value) || index < wizardStep.value
}

function goToStep(index: number) {
  if (!canJumpTo(index)) return
  wizardStep.value = index
  if (currentStepKey.value !== 'template') {
    stopTemplatePolling()
    return
  }
  // Landing on the template step without having passed through the connection
  // step still has to ask the cluster what it offers; a step already loaded
  // just resumes its poll, as walking back through it always has.
  if (templatesLoaded.value) scheduleTemplatePolling()
  else void loadTemplates(true)
}
const hasPendingTemplates = computed(() => templates.value.some(isTemplatePending))

const backendLabel = (value: string) => t(`settings.sandbox.backends.${value}`)
const checkLabel = (probe: string) => t(`settings.sandbox.checks.${probe}`, probe)

// Probes deferred to the opt-in deep check are expected, not a problem, so they
// are pulled out of the result list and explained once.
const PENDING_SKIP_REASON = 'needs_deep_check'

const reportedChecks = computed(() => (checkResult.value?.checks || []).filter(
  (item) => item.ok !== null || item.reason !== PENDING_SKIP_REASON,
))

const pendingCheckNames = computed(() => (checkResult.value?.checks || [])
  .filter((item) => item.ok === null && item.reason === PENDING_SKIP_REASON)
  .map((item) => checkLabel(item.name)))

// Says which layer the verdict covers, so "检测通过" is not read as "everything
// works" after a connection-only probe.
const checkScopeHint = computed(() => (pendingCheckNames.value.length
  ? t('settings.sandbox.checkScopeConnection')
  : t('settings.sandbox.checkScopeFull')))

function checkDetail(item: SandboxCheckItem): string {
  if (item.message) return item.message
  if (!item.reason) return ''
  return t(`settings.sandbox.skipReasons.${item.reason}`, item.reason)
}
const secretInputPlaceholder = (target: 'cube' | 'e2b') => (
  storedSecrets[target] ? t('settings.sandbox.secretKeepHint') : t('settings.sandbox.apiKeyPlaceholder')
)

// Mirrors sandbox.MissingRequiredFields on the server. Duplicated on purpose:
// the server stays the authority, this only spares the admin a round-trip and
// points at the offending input instead of showing one combined message.
const REQUIRED_FIELDS: Record<string, string[]> = {
  cube: ['api_url', 'proxy_url', 'sandbox_domain', 'template_id'],
  e2b: ['api_key', 'template_id'],
  docker: ['image'],
  local: [],
}

const fieldErrors = ref<Record<string, string>>({})

const fieldStatus = (field: string) => (fieldErrors.value[field] ? 'error' : undefined)
const fieldTip = (field: string) => fieldErrors.value[field] || undefined

const requiredLabel = (labelKey: string) => `${t(`settings.sandbox.${labelKey}`)} *`

// Clearing on input rather than re-validating keeps the error from flickering
// back while the admin is still halfway through typing a URL.
function onFieldInput(field: string) {
  delete fieldErrors.value[field]
  invalidateCheck()
}

function onConnectionInput(field: string) {
  delete fieldErrors.value[field]
  invalidateConnection()
}

// Snapshot of the active backend block as it will be submitted, so a secret the
// admin left blank on purpose still counts as filled in.
function submittedBackendValues(): Record<string, unknown> {
  if (backend.value === 'cube') return withStoredSecret({ ...cube }, storedSecrets.cube)
  if (backend.value === 'e2b') return withStoredSecret({ ...e2b }, storedSecrets.e2b)
  return { ...docker }
}

function validateRequiredFields(includeTemplate = true): boolean {
  const required = (REQUIRED_FIELDS[backend.value] || []).filter((field) => includeTemplate || field !== 'template_id')
  const values = submittedBackendValues()
  const errors: Record<string, string> = {}
  for (const field of required) {
    const value = values[field]
    if (typeof value !== 'string' || value.trim() === '') {
      errors[field] = t('settings.sandbox.fieldRequired')
    }
  }
  fieldErrors.value = errors
  return Object.keys(errors).length === 0
}

const affectedSessionCount = computed(() => conflict.value?.inventory?.session_ids?.length || 0)

function defaultBackendType(): string {
  const fromRecord = props.record?.config?.sandbox_type || props.presetType || ''
  if (isNamedSandboxBackend(fromRecord)) return fromRecord
  return 'cube'
}

function reset() {
  stopTemplatePolling()
  const cfg: SandboxConfig = props.record?.config || {}
  name.value = props.record?.name || ''
  description.value = props.record?.description || ''
  backend.value = isNamedSandboxBackend(cfg.sandbox_type || '')
    ? cfg.sandbox_type!
    : defaultBackendType()
  defaultTimeoutSec.value = cfg.default_timeout_sec || undefined
  allowPrivateEndpoints.value = cfg.allow_private_endpoints === true
  // Replace rather than merge: a reused reactive object would otherwise carry
  // the previously edited config's fields into the next one opened.
  Object.keys(cube).forEach((key) => delete (cube as Record<string, unknown>)[key])
  Object.keys(e2b).forEach((key) => delete (e2b as Record<string, unknown>)[key])
  Object.keys(docker).forEach((key) => delete (docker as Record<string, unknown>)[key])
  Object.assign(cube, cfg.cube || {})
  Object.assign(e2b, cfg.e2b || {})
  Object.assign(docker, cfg.docker || {})
  if (backend.value === 'docker' && !docker.image) {
    docker.image = 'wechatopenai/weknora-sandbox:latest'
  }
  storedSecrets.cube = isMaskedSecret(cube.api_key)
  storedSecrets.e2b = isMaskedSecret(e2b.api_key)
  if (storedSecrets.cube) cube.api_key = ''
  if (storedSecrets.e2b) e2b.api_key = ''
  envRows.value = Object.entries(cfg.env_vars || {}).map(([key, value]) => (
    isMaskedSecret(value) ? { key, value: '', stored: true } : { key, value }
  ))
  checkResult.value = null
  conflict.value = null
  nameError.value = ''
  fieldErrors.value = {}
  templates.value = []
  templatesLoaded.value = false
  templatesError.value = ''
  savedRecord.value = null
  wizardStep.value = 0
  // "管理技能" opens this drawer straight on the skills step. It is a jump like
  // any other, so it only holds for a config that already exists.
  if (props.initialStep && props.record) {
    const index = wizardSteps.value.findIndex((step) => step.key === props.initialStep)
    if (index >= 0) wizardStep.value = index
  }
}

function selectBackend(value: string) {
  if (backend.value === value) return
  backend.value = value
  if (value === 'docker' && !docker.image) {
    docker.image = 'wechatopenai/weknora-sandbox:latest'
  }
  onBackendChange()
}

watch(() => props.visible, (open) => {
  if (open) {
    reset()
  } else {
    stopTemplatePolling()
  }
})

function connectionReady(): boolean {
  if (!isRemoteBackend.value) return true
  const required = backend.value === 'cube'
    ? ['api_url', 'proxy_url', 'sandbox_domain']
    : ['api_key']
  const values = submittedBackendValues()
  const errors: Record<string, string> = {}
  for (const field of required) {
    if (typeof values[field] !== 'string' || String(values[field]).trim() === '') {
      errors[field] = t('settings.sandbox.fieldRequired')
    }
  }
  fieldErrors.value = { ...fieldErrors.value, ...errors }
  return Object.keys(errors).length === 0
}

function selectTemplate(value: string | number) {
  if (backend.value === 'cube') cube.template_id = String(value)
  else e2b.template_id = String(value)
  onFieldInput('template_id')
}

function clearTemplateSelection() {
  if (backend.value === 'cube') cube.template_id = ''
  if (backend.value === 'e2b') e2b.template_id = ''
  delete fieldErrors.value.template_id
}

function templateMeta(item: SandboxTemplate): string {
  return [item.status, item.version, item.id].filter(Boolean).join(' · ')
}

function isTemplateSelectable(item: SandboxTemplate): boolean {
  const status = item.status?.trim().toLowerCase()
  if (!status) return true
  return ['ready', 'available', 'complete', 'completed', 'success', 'succeeded'].includes(status)
}

function isTemplatePending(item: SandboxTemplate): boolean {
  const status = item.status?.trim().toLowerCase()
  return ['building', 'waiting', 'pending', 'queued', 'processing'].includes(status || '')
}

// The backend reports this when a template's builds finished without the tag
// sandbox creation resolves, so it can never be spawned as it stands.
function isTemplateUntagged(item: SandboxTemplate): boolean {
  return item.status?.trim().toLowerCase() === 'untagged'
}

function isTemplateFailed(item: SandboxTemplate): boolean {
  const status = item.status?.trim().toLowerCase()
  return ['failed', 'failure', 'error', 'cancelled', 'canceled'].includes(status || '')
}

// A red "failed" badge on its own leaves no way to tell a registry credential
// problem from a node that ran out of disk, so the provider's own message is
// shown verbatim when it sends one.
function templateFailureReason(item: SandboxTemplate): string {
  if (!isTemplateFailed(item)) return ''
  const reason = item.error?.trim()
  return reason ? t('settings.sandbox.templateFailedReason', { reason }) : ''
}

function templateStatusTheme(item: SandboxTemplate): 'success' | 'warning' | 'danger' | 'default' {
  if (isTemplateSelectable(item)) return 'success'
  if (isTemplateUntagged(item)) return 'danger'
  if (isTemplatePending(item)) return 'warning'
  if (isTemplateFailed(item)) return 'danger'
  return 'default'
}

function templateStatusLabel(item: SandboxTemplate): string {
  if (isTemplateSelectable(item)) return t('settings.sandbox.templateStatuses.ready')
  if (isTemplateUntagged(item)) return t('settings.sandbox.templateStatuses.untagged')
  if (isTemplatePending(item)) return t('settings.sandbox.templateStatuses.building')
  if (isTemplateFailed(item)) return t('settings.sandbox.templateStatuses.failed')
  return t('settings.sandbox.templateStatuses.unknown')
}

function stopTemplatePolling() {
  if (templatePollTimer) clearTimeout(templatePollTimer)
  templatePollTimer = undefined
}

function scheduleTemplatePolling() {
  stopTemplatePolling()
  if (!props.visible || currentStepKey.value !== 'template' || !hasPendingTemplates.value) return
  templatePollTimer = setTimeout(() => {
    void loadTemplates(false, true)
  }, 3000)
}

async function loadTemplates(ensureStandard: boolean, silent = false): Promise<boolean> {
  if (!hasImageCatalog.value) return true
  if (!connectionReady()) return false
  if (!silent) templatesLoading.value = true
  templatesError.value = ''
  try {
    const res = await querySandboxTemplates({
      config: collectPayload(),
      config_id: effectiveRecord.value?.id,
      ensure_standard: ensureStandard,
    })
    templates.value = res.data?.templates || []
    templatesLoaded.value = true
    const standardID = res.data?.standard_template_id
    const current = templates.value.find((item) => item.id === currentTemplateId.value)
    if (currentTemplateId.value && (!current || !isTemplateSelectable(current))) clearTemplateSelection()
    const readyStandard = templates.value.find((item) => item.id === standardID && isTemplateSelectable(item))
      || templates.value.find((item) => item.standard && isTemplateSelectable(item))
    if (!currentTemplateId.value && readyStandard) selectTemplate(readyStandard.id)
    if (res.data?.provisioned && !silent) {
      MessagePlugin.info(t('settings.sandbox.standardTemplateProvisioning'))
    }
    scheduleTemplatePolling()
    return true
  } catch (e: any) {
    templatesError.value = e?.message || t('settings.sandbox.templateLoadFailed')
    return false
  } finally {
    if (!silent) templatesLoading.value = false
  }
}

// Re-attaches the redaction placeholder to a secret the admin left untouched:
// the server reads it as "preserve the stored value".
function withStoredSecret<T extends { api_key?: string }>(block: T, stored: boolean): T {
  if (stored && !block.api_key?.trim()) block.api_key = secretPlaceholder
  return block
}

function collectPayload(): SandboxConfig {
  const envVars: Record<string, string> = {}
  for (const row of envRows.value) {
    const key = row.key.trim()
    if (!key) continue
    envVars[key] = row.stored && row.value === '' ? secretPlaceholder : row.value
  }
  const payload: SandboxConfig = {
    sandbox_type: backend.value,
    default_timeout_sec: defaultTimeoutSec.value || undefined,
    allow_private_endpoints: allowPrivateEndpoints.value || undefined,
    env_vars: envVars,
    skill_rollout: effectiveRecord.value?.config?.skill_rollout,
  }
  // Send only the selected backend's block so an unused one cannot fail
  // validation (e.g. a stale private URL left in the other tab).
  if (backend.value === 'cube') payload.cube = withStoredSecret({ ...cube }, storedSecrets.cube)
  if (backend.value === 'e2b') payload.e2b = withStoredSecret({ ...e2b }, storedSecrets.e2b)
  if (backend.value === 'docker') payload.docker = { ...docker }
  return payload
}

function close() {
  stopTemplatePolling()
  emit('update:visible', false)
}

function validateName(): boolean {
  if (!name.value.trim()) {
    nameError.value = t('settings.sandbox.configNameRequired')
    return false
  }
  nameError.value = ''
  return true
}

async function handlePrimaryAction() {
  if (currentStepKey.value === 'connection') {
    if (!validateName() || !validateRequiredFields(false)) return
    if (!(await runCheck(false))) return
    if (isRemoteBackend.value) {
      invalidateCheck()
      wizardStep.value += 1
      await loadTemplates(true)
      return
    }
    // Docker's template is the image typed on this step. Kick a background
    // pull so the first session does not block on a cold registry fetch.
    if (backend.value === 'docker') {
      void loadTemplates(true)
    }
    invalidateCheck()
    wizardStep.value += 1
    return
  }
  if (currentStepKey.value === 'template') {
    if (!selectedTemplate.value || !isTemplateSelectable(selectedTemplate.value)) {
      fieldErrors.value.template_id = t('settings.sandbox.templateNotReady')
      return
    }
    stopTemplatePolling()
    wizardStep.value += 1
    return
  }
  if (currentStepKey.value === 'skills') {
    close()
    return
  }
  await save()
}

function previousStep() {
  if (wizardStep.value <= 0) return
  wizardStep.value -= 1
  if (currentStepKey.value === 'template') scheduleTemplatePolling()
  else stopTemplatePolling()
}

async function save() {
  const trimmed = name.value.trim()
  if (!validateName()) return
  if (!validateRequiredFields()) return
  if (isRemoteBackend.value && selectedTemplate.value && !isTemplateSelectable(selectedTemplate.value)) {
    fieldErrors.value.template_id = t('settings.sandbox.templateNotReady')
    return
  }
  saving.value = true
  conflict.value = null
  try {
    const payload = { name: trimmed, description: description.value, config: collectPayload() }
    const existing = effectiveRecord.value
    const res = existing
      ? await updateSandboxConfigById(existing.id, payload)
      : await createSandboxConfig(payload)
    MessagePlugin.success(t('common.saveSuccess'))
    // The list behind the drawer refreshes either way, so closing here is only
    // about whether the wizard has anything left to offer.
    emit('saved')
    const saved = res?.data
    if (saved) savedRecord.value = saved
    const skillsStep = wizardSteps.value.findIndex((step) => step.key === 'skills')
    if (skillsStep >= 0 && savedRecord.value) {
      wizardStep.value = skillsStep
      return
    }
    close()
  } catch (e: any) {
    const refusal = parseSandboxConflict(e)
    if (refusal) {
      // Keep the drawer open with the form intact: the admin has to act
      // elsewhere first, and retyping everything afterwards would be cruel.
      conflict.value = refusal
      await nextTick()
      conflictAlertRef.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      return
    }
    MessagePlugin.error(e?.message || t('settings.sandbox.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function runCheck(deep: boolean): Promise<boolean> {
  // The probe builds a real client, so an incomplete config would come back as
  // a generic client_build failure instead of naming the empty field.
  if (!validateRequiredFields(deep)) return false
  checking.value = true
  checkResult.value = null
  try {
    // config_id lets the backend resolve masked secrets against the stored row,
    // so an edited form can be probed without retyping the API key.
    const res = await checkSandboxConfig({
      config: collectPayload(),
      config_id: effectiveRecord.value?.id,
      deep,
    })
    checkResult.value = res?.data || null
    lastCheckWasDeep.value = deep
    if (checkResult.value) {
      await nextTick()
      checkResultRef.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
    return checkResult.value?.ok === true
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('settings.sandbox.checkFailed'))
    return false
  } finally {
    checking.value = false
  }
}

// A result that no longer matches the form is worse than none.
function invalidateCheck() {
  checkResult.value = null
}

function invalidateConnection() {
  stopTemplatePolling()
  invalidateCheck()
  templates.value = []
  templatesLoaded.value = false
  templatesError.value = ''
  clearTemplateSelection()
}

// The two backends require different fields, so carrying errors across a switch
// would flag inputs the admin can no longer even see.
function onBackendChange() {
  fieldErrors.value = {}
  invalidateConnection()
}

onUnmounted(stopTemplatePolling)
</script>

<style lang="less" scoped>
/*
  A step rail rather than segmented buttons: the connector line carries the
  "these run in order" meaning that boxed segments only implied, and the flat
  background keeps the drawer's first visual weight on the form itself.
*/
.sandbox-steps {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
}

.sandbox-step {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: var(--td-text-color-placeholder);
  transition: color 0.15s ease;

  /* Only the steps that draw a connector need to absorb the leftover width. */
  &:not(:last-child) {
    flex: 1;
  }

  &.is-active {
    color: var(--td-brand-color);
  }

  &.is-done {
    color: var(--td-text-color-secondary);
  }

  /*
    Reachable steps render as <button>, so the browser's own control styling has
    to be undone to keep the rail looking identical either way. Only the cursor
    and hover state give the affordance away.
  */
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

.sandbox-step__marker {
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

.sandbox-step__title {
  overflow: hidden;
  font-size: 13px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sandbox-step__line {
  flex: 1;
  min-width: 16px;
  height: 1px;
  margin: 0 4px;
  background: var(--td-component-stroke);

  .is-done & {
    background: color-mix(in srgb, var(--td-brand-color) 35%, transparent);
  }
}

.skills-locked {
  margin: 24px 0;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
  text-align: center;
}

.sandbox-editor-form {
  :deep(.t-form__item) {
    margin-bottom: 0;
  }

  /*
    Form items here stack a control with its own hint text and doc link, so the
    controls area has to be a block instead of TDesign's default single row.
  */
  :deep(.t-form__controls-content) {
    display: block;
  }

  :deep(.t-form__label) {
    padding-bottom: 6px;
    font-size: 13px;
    font-weight: 500;
    line-height: 1.4;
  }

  :deep(.t-input),
  :deep(.t-input-number),
  :deep(.t-select) {
    width: 100%;
    font-size: 13px;
  }
}

.identity-hint {
  margin: 0;
}

.compact-alert {
  padding: 9px 11px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 7px;
  background: var(--td-bg-color-secondarycontainer);

  :deep(.t-alert__icon) {
    font-size: 15px;
  }

  :deep(.t-alert__message) {
    color: var(--td-text-color-secondary);
    font-size: 12px;
    line-height: 1.5;
  }
}

.private-endpoint-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
}

.private-endpoint-row__title {
  margin: 0 0 3px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.form-grid {
  display: grid;
  gap: 12px;

  &--two {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

.backend-choice {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  padding: 2px 0;
}

.backend-choice__text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.backend-choice__name {
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 500;
  line-height: 1.4;
}

.backend-choice__desc {
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.45;
  white-space: normal;
}

.section-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;

  .setting-drawer__section-title {
    margin-bottom: 0;
  }
}

.connection-summary {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
}

.connection-summary__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  border-radius: 50%;
  background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
  color: var(--td-brand-color);
  font-size: 13px;
}

.connection-summary__title,
.connection-summary__text {
  margin: 0;
}

.connection-summary__title {
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
  line-height: 1.45;
}

.connection-summary__text {
  margin-top: 2px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.5;
}

.weknora-template-card {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);

  &.is-active {
    border-color: color-mix(in srgb, var(--td-brand-color) 45%, var(--td-component-stroke));
    background: color-mix(in srgb, var(--td-brand-color) 5%, var(--td-bg-color-container));
  }
}

.weknora-template-card__content {
  flex: 1;
  min-width: 0;

  p {
    margin: 4px 0 0;
    color: var(--td-text-color-secondary);
    font-size: 12px;
    line-height: 1.5;
  }
}

.weknora-template-card__title-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.weknora-template-card__title {
  font-size: 13px;
  font-weight: 600;
}

.template-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  min-height: 88px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 8px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
}

.template-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.template-card {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  min-height: 72px;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  cursor: pointer;
  text-align: left;
  transition: border-color 0.18s ease, background-color 0.18s ease, box-shadow 0.18s ease;

  &:hover:not(:disabled) {
    border-color: var(--td-brand-color-3, var(--td-brand-color));
    box-shadow: 0 3px 10px rgba(15, 23, 42, 0.05);
  }

  &.is-active {
    border-color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 4%, var(--td-bg-color-container));
  }

  &:disabled {
    cursor: not-allowed;
  }

  &.is-pending {
    background: color-mix(in srgb, var(--td-warning-color) 3%, var(--td-bg-color-container));
  }
}

.template-card__badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  flex-shrink: 0;
  border-radius: 9px;
  background: rgba(0, 82, 217, 0.1);
  color: #0052d9;
  font-size: 16px;

  .is-pending & {
    background: color-mix(in srgb, var(--td-warning-color) 12%, transparent);
    color: var(--td-warning-color);
  }
}

.template-card__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
}

.template-card__title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.template-card__title {
  overflow: hidden;
  font-size: 13px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.template-card__meta,
.template-card__hint {
  overflow-wrap: anywhere;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  line-height: 1.45;
}

.template-card__hint {
  color: var(--td-warning-color);

  &--error {
    color: var(--td-error-color);
  }
}

.template-card__state {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  align-self: flex-start;
  padding-top: 1px;
}

.template-card__selected {
  color: var(--td-brand-color);
  font-size: 17px;
}

.template-state-note {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 11px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 7px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.5;

  > .t-icon {
    flex-shrink: 0;
    color: var(--td-warning-color);
    font-size: 15px;
  }

  &--ready > .t-icon {
    color: var(--td-brand-color);
  }
}

.field-hints {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  margin-top: 6px;

  .inline-guide-link {
    margin-top: 0;
  }
}

.inline-guide-link {
  display: inline-flex;
  align-items: center;
  align-self: flex-start;
  gap: 5px;
  margin-top: -4px;
  color: var(--td-brand-color);
  font-size: 12px;
  text-decoration: none;

  &:hover {
    color: var(--td-brand-color-hover);
  }
}

.runtime-fields {
  display: flex;
  flex-direction: column;
}

.runtime-fields :deep(.t-form__item) {
  margin-bottom: 0;
}

.runtime-fields :deep(.t-input-number) {
  width: 100%;
  max-width: 240px;
}

.runtime-fields .section-help--field {
  margin-bottom: 16px;

  &:last-child {
    margin-bottom: 0;
  }
}

.env-rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.env-row {
  display: grid;
  grid-template-columns: minmax(140px, 0.7fr) minmax(200px, 1.3fr) 32px;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.env-empty {
  padding: 18px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 8px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  text-align: center;
}

.section-help {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1.5;

  &--under-title {
    margin-top: 5px;
    max-width: 470px;
  }

  /* Sits under an input inside the same form item. */
  &--field {
    margin-top: 6px;
  }
}

.check-result {
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.check-result__title {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
  line-height: 1.45;

  &.is-success {
    color: var(--td-success-color);
  }

  &.is-error {
    color: var(--td-error-color);
  }
}

.check-result__subtitle,
.check-result__hint {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.55;
}

.check-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.check-item {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 2px 0;
  font-size: 12px;
}

.check-item .ok {
  color: var(--td-success-color);
}

.check-item .err {
  color: var(--td-error-color);
}

.check-item .skip {
  color: var(--td-text-color-placeholder);
}

.check-name {
  min-width: 140px;
}

.check-latency,
.check-message {
  color: var(--td-text-color-secondary);
}

.footer-check-ok {
  color: var(--td-success-color);
}

.footer-check-error {
  color: var(--td-error-color);
}

.blocked {
  margin-top: 16px;
}

.blocked-top {
  margin-top: 0;
  margin-bottom: 16px;
}

.blocked p {
  margin: 4px 0 0;
}

@media (max-width: 720px) {
  .form-grid--two,
  .weknora-template-card {
    align-items: flex-start;
    flex-wrap: wrap;
  }

  .sandbox-step__title {
    font-size: 12px;
  }
}
</style>

<!--
  The select popup is attached to body, outside the scoped style boundary, so
  the two-line backend options need their row height relaxed globally. The
  overlay class keeps it from touching any other select in the app.
-->
<style lang="less">
.sandbox-backend-popup {
  .t-select-option {
    height: auto;
    min-height: 44px;
    padding: 6px 8px;
    line-height: 1.4;
  }
}
</style>
