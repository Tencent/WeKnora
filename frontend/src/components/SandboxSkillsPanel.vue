<template>
  <div class="sandbox-skills-panel" :class="{
    'sandbox-skills-panel--list': mode === 'list',
    'sandbox-skills-panel--focused': mode === 'list' && !!focusSkillId,
  }">
    <t-loading :loading="mode === 'list' && loading" size="small">
      <section v-if="mode === 'install'" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillInstallerModel') }}</h4>
        <p class="installer-model-hint">{{ $t('settings.sandbox.skillInstallerModelHint') }}</p>
        <ModelSelector
          model-type="KnowledgeQA"
          :selected-model-id="installerModelId"
          :disabled="savingInstallerModel"
          @update:selected-model-id="onInstallerModelChange"
        />
      </section>

      <section v-if="mode === 'install'" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillSourceSection') }}</h4>
        <p class="installer-model-hint">{{ $t('settings.sandbox.skillSourceSectionHint', { size: MAX_SKILL_BUNDLE_SIZE_MB }) }}</p>
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
      </section>

      <section v-if="mode === 'install'" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ $t('settings.sandbox.skillUploadSection') }}</h4>
        <p class="installer-model-hint">{{ $t('settings.sandbox.skillUploadSectionHint', { size: MAX_SKILL_BUNDLE_SIZE_MB }) }}</p>
        <input
          ref="fileInputRef"
          type="file"
          accept=".zip,application/zip"
          class="file-input-hidden"
          @change="onFileInputChange"
        />
        <div
          class="file-upload-area file-upload-area--large"
          :class="{ 'has-file': uploading, 'is-disabled': installBusy }"
          @click="!installBusy && fileInputRef?.click()"
          @dragover.prevent
          @dragenter.prevent
          @drop.prevent="onFileDrop"
        >
          <div class="file-upload-content">
            <div class="file-upload-icon-wrap" aria-hidden="true">
              <t-icon name="cloud-upload" size="32px" class="upload-icon" />
            </div>
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

      <section v-if="mode === 'list' && focusSkillId" class="skill-manage">
        <div v-if="uninstallDone" class="skill-manage__done">
          <t-icon name="check-circle-filled" size="22px" />
          <p>{{ $t('settings.sandbox.skillRemoveDone', { name: uninstallingName }) }}</p>
        </div>
        <template v-else-if="managedSkill && isRemoving(managedSkill)">
          <section class="skill-manage__section skill-manage__section--remove">
            <div class="skill-manage__section-head">
              <h4>{{ $t('settings.sandbox.skillRemoveInProgress') }}</h4>
              <span class="skill-manage__progress-label">{{ progressOf(managedSkill) }}%</span>
            </div>
            <t-progress :percentage="progressOf(managedSkill)" :stroke-width="6" />
            <p class="skill-manage__remove-stage">{{ progressStageText(managedSkill) }}</p>
          </section>
        </template>
        <template v-else-if="managedSkill">
          <div class="skill-manage__row">
            <div class="skill-manage__info">
              <label>{{ $t('settings.skills.manageEnable') }}</label>
              <p>{{ $t('settings.sandbox.skillDisableHint') }}</p>
            </div>
            <div class="skill-manage__controls">
              <t-switch
                :value="managedSkill.enabled"
                :disabled="isBusy(managedSkill)"
                :loading="togglingId === managedSkill.id"
                @change="(v: any) => managedSkill && toggleEnabled(managedSkill, Boolean(v))"
              />
              <t-tooltip
                v-if="managedSkill.status === 'failed'"
                :content="$t('settings.sandbox.skillRetryHint')"
                placement="top"
              >
                <button
                  type="button"
                  class="skill-card__icon-btn"
                  :disabled="retryingId === managedSkill.id"
                  :title="$t('settings.sandbox.skillRetry')"
                  :aria-label="$t('settings.sandbox.skillRetry')"
                  @click="retrySkill(managedSkill)"
                >
                  <t-icon name="refresh" size="16px" />
                </button>
              </t-tooltip>
            </div>
          </div>
          <ul v-if="failedErrorLines(managedSkill).length" class="skill-card__error">
            <li v-for="(line, i) in failedErrorLines(managedSkill)" :key="i">{{ line }}</li>
          </ul>
          <section v-if="skillHasDeclaredEnvs(managedSkill)" class="skill-manage__section">
            <h4>{{ $t('settings.sandbox.skillEnv.toggle') }}</h4>
            <p class="skill-envs__hint">{{ $t('settings.sandbox.skillEnv.workspaceHint') }}</p>
            <div class="skill-envs__rows">
              <div v-for="(env, envIdx) in managedSkill.envs" :key="env.name" class="skill-envs__row">
                <div class="skill-envs__meta">
                  <code class="skill-envs__name">{{ env.name }}</code>
                  <span v-if="env.required" class="skill-envs__tag skill-envs__tag--required">
                    {{ $t('settings.sandbox.skillEnv.required') }}
                  </span>
                  <span
                    class="skill-envs__tag"
                    :class="env.is_set ? 'skill-envs__tag--set' : 'skill-envs__tag--unset'"
                  >
                    {{
                      env.is_set
                        ? $t('settings.sandbox.skillEnv.isSet')
                        : $t('settings.sandbox.skillEnv.notSet')
                    }}
                  </span>
                  <span v-if="env.description" class="skill-envs__desc">{{ env.description }}</span>
                </div>
                <div class="skill-envs__editor">
                  <t-input
                    :value="envDrafts[managedSkill.id]?.[env.name] ?? ''"
                    type="password"
                    autocomplete="new-password"
                    :name="`wk-se-focus-${envIdx}`"
                    :readonly="isBusy(managedSkill) || !isEnvInputUnlocked(managedSkill.id, env.name)"
                    spellcheck="false"
                    :placeholder="
                      env.is_set
                        ? $t('settings.sandbox.skillEnv.placeholderSet')
                        : $t('settings.sandbox.skillEnv.placeholderUnset')
                    "
                    @focus="unlockEnvInput(managedSkill.id, env.name)"
                    @update:value="(v: string) => managedSkill && setEnvDraft(managedSkill.id, env.name, v)"
                    @enter="saveEnvs(managedSkill, true)"
                    @blur="onEnvFieldBlur(managedSkill)"
                  />
                  <t-popconfirm
                    v-if="canClearAdminSkillEnv(env)"
                    theme="warning"
                    attach="body"
                    :content="$t('settings.sandbox.skillEnv.clearConfirm', { name: env.name })"
                    :confirm-btn="{ content: $t('settings.sandbox.skillEnv.clear'), theme: 'danger' }"
                    :cancel-btn="{ content: $t('common.cancel') }"
                    @confirm="clearEnv(managedSkill, env.name)"
                  >
                    <t-button
                      theme="danger"
                      variant="text"
                      size="small"
                      :disabled="envSaveInFlight(managedSkill)"
                      :loading="envSaveInFlight(managedSkill)"
                    >
                      {{ $t('settings.sandbox.skillEnv.clear') }}
                    </t-button>
                  </t-popconfirm>
                </div>
              </div>
            </div>
          </section>
          <section v-if="hasTranscript(managedSkill)" class="skill-manage__section">
            <div class="skill-manage__section-head">
              <h4>{{ $t('settings.sandbox.skillTranscriptTitle') }}</h4>
              <div v-if="managedSkill.status === 'installing'" class="skill-manage__progress">
                <t-progress
                  theme="circle"
                  :percentage="progressOf(managedSkill)"
                  :size="18"
                  :stroke-width="2"
                  :label="false"
                />
                <span>{{ progressOf(managedSkill) }}%</span>
              </div>
            </div>
            <SkillInstallTimeline
              :key="`${managedSkill.id}-${managedSkill.install_session_id || ''}-${transcriptEpoch}`"
              compact
              :config-id="record?.id || ''"
              :skill-id="managedSkill.id"
              :session-id="managedSkill.install_session_id || ''"
              :message-id="managedSkill.install_message_id || ''"
              :live="managedSkill.status === 'installing'"
            />
          </section>
          <section
            v-if="managedSkill.status !== 'installing'"
            class="skill-manage__section skill-manage__section--danger"
          >
            <h4>{{ $t('settings.skills.manageUninstall') }}</h4>
            <p class="skill-envs__hint">{{ deleteHint }}</p>
            <ul v-if="removalErrorLines(managedSkill).length" class="skill-card__error">
              <li v-for="(line, i) in removalErrorLines(managedSkill)" :key="i">{{ line }}</li>
            </ul>
            <t-button
              theme="danger"
              variant="outline"
              :disabled="isBusy(managedSkill)"
              :loading="deletingId === managedSkill.id"
              @click="askRemove(managedSkill)"
            >
              {{ $t('settings.skills.manageUninstall') }}
            </t-button>
          </section>
        </template>
      </section>

      <section v-else-if="mode === 'list'" class="skill-list-section">
        <div class="skill-list">
          <div
            v-for="skill in visibleSkills"
            :key="skill.id"
            :ref="(el) => bindSkillItem(skill.id, el)"
            class="skill-card"
            :class="{
              'skill-card--focused': focusedSkillId === skill.id && !focusSkillId,
              'skill-card--bare': !!focusSkillId,
            }"
          >
            <div v-if="!focusSkillId" class="skill-card__badge" aria-hidden="true">
              <t-progress
                v-if="isBusy(skill)"
                theme="circle"
                :percentage="progressOf(skill)"
                :size="18"
                :stroke-width="2"
                :label="false"
              />
              <t-icon v-else :name="SKILL_ICON" size="18px" />
            </div>
            <div class="skill-card__body">
              <div
                class="skill-card__header"
                :class="{ 'skill-card__header--toolbar': !!focusSkillId }"
              >
                <h3
                  v-if="!focusSkillId"
                  class="skill-card__title"
                  :title="skill.name || skill.id"
                >
                  {{ skill.name || skill.id }}
                </h3>
                <span
                  v-if="skill.status === 'failed' || isBusy(skill)"
                  class="skill-card__status"
                  :class="cardStatusClass(skill)"
                >
                  <span
                    class="skill-card__status-dot"
                    :class="{ 'is-live': isBusy(skill) }"
                  />
                  {{ cardStatusText(skill) }}
                </span>
                <div class="skill-card__actions">
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
                    v-if="skillHasDeclaredEnvs(skill)"
                    :visible="expandedEnvSkillId === skill.id"
                    trigger="click"
                    placement="bottom-right"
                    attach="body"
                    destroy-on-close
                    overlay-class-name="skill-env-popup"
                    :z-index="3200"
                    :overlay-inner-style="{ padding: '0' }"
                    @visible-change="(visible: boolean, context?: { e?: Event }) => onEnvVisible(skill, visible, context)"
                  >
                    <button
                      type="button"
                      class="skill-card__icon-btn"
                      :class="{ 'is-on': expandedEnvSkillId === skill.id }"
                      :title="$t('settings.sandbox.skillEnv.toggle')"
                      :aria-label="$t('settings.sandbox.skillEnv.toggle')"
                      @pointerdown="ensureEnvDrafts(skill.id)"
                    >
                      <t-icon name="key" size="14px" />
                    </button>
                    <template #content>
                        <div class="skill-env-popup__panel">
                          <header class="skill-env-popup__head">
                            <div class="skill-env-popup__head-text">
                              <div class="skill-env-popup__title">{{ skill.name || skill.id }}</div>
                              <div class="skill-env-popup__meta">
                                {{ $t('settings.sandbox.skillEnv.workspaceTitle') }}
                              </div>
                            </div>
                            <t-button
                              variant="text"
                              shape="square"
                              size="small"
                              class="skill-env-popup__close"
                              :title="$t('common.close')"
                              @click.stop="onEnvVisible(skill, false)"
                            >
                              <template #icon><t-icon name="close" size="16px" /></template>
                            </t-button>
                          </header>
                          <div class="skill-env-popup__body">
                            <p class="skill-envs__hint">{{ $t('settings.sandbox.skillEnv.workspaceHint') }}</p>
                            <div class="skill-envs__rows">
                              <div v-for="(env, envIdx) in skill.envs" :key="env.name" class="skill-envs__row">
                                <div class="skill-envs__meta">
                                  <code class="skill-envs__name">{{ env.name }}</code>
                                  <span v-if="env.required" class="skill-envs__tag skill-envs__tag--required">
                                    {{ $t('settings.sandbox.skillEnv.required') }}
                                  </span>
                                  <span
                                    class="skill-envs__tag"
                                    :class="env.is_set ? 'skill-envs__tag--set' : 'skill-envs__tag--unset'"
                                  >
                                    {{
                                      env.is_set
                                        ? $t('settings.sandbox.skillEnv.isSet')
                                        : $t('settings.sandbox.skillEnv.notSet')
                                    }}
                                  </span>
                                  <span v-if="env.description" class="skill-envs__desc">{{ env.description }}</span>
                                </div>
                                <div class="skill-envs__editor">
                                  <t-input
                                    :value="envDrafts[skill.id]?.[env.name] ?? ''"
                                    type="password"
                                    autocomplete="new-password"
                                    :name="`wk-se-${envIdx}`"
                                    :readonly="!isEnvInputUnlocked(skill.id, env.name)"
                                    spellcheck="false"
                                    data-lpignore="true"
                                    data-1p-ignore="true"
                                    data-bwignore="true"
                                    :aria-label="env.name"
                                    :placeholder="
                                      env.is_set
                                        ? $t('settings.sandbox.skillEnv.placeholderSet')
                                        : $t('settings.sandbox.skillEnv.placeholderUnset')
                                    "
                                    @focus="unlockEnvInput(skill.id, env.name)"
                                    @update:value="(v: string) => setEnvDraft(skill.id, env.name, v)"
                                  />
                                  <t-popconfirm
                                    v-if="canClearAdminSkillEnv(env)"
                                    theme="warning"
                                    attach="body"
                                    :z-index="3300"
                                    :popup-props="{ attach: 'body', zIndex: 3300 }"
                                    :content="$t('settings.sandbox.skillEnv.clearConfirm', { name: env.name })"
                                    :confirm-btn="{ content: $t('settings.sandbox.skillEnv.clear'), theme: 'danger' }"
                                    :cancel-btn="{ content: $t('common.cancel') }"
                                    @confirm="clearEnv(skill, env.name)"
                                  >
                                    <t-button
                                      theme="danger"
                                      variant="text"
                                      size="small"
                                      :disabled="envSaveInFlight(skill)"
                                      :loading="envSaveInFlight(skill)"
                                    >
                                      {{ $t('settings.sandbox.skillEnv.clear') }}
                                    </t-button>
                                  </t-popconfirm>
                                </div>
                              </div>
                            </div>
                          </div>
                          <div class="skill-env-popup__footer">
                            <t-button
                              theme="primary"
                              size="small"
                              :disabled="!hasEnvEdits(skill) || envSaveInFlight(skill)"
                              :loading="envSaveInFlight(skill)"
                              @click="saveEnvs(skill)"
                            >
                              {{ $t('settings.sandbox.skillEnv.save') }}
                            </t-button>
                          </div>
                        </div>
                      </template>
                    </t-popup>
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
                      class="skill-card__icon-btn"
                      :class="{
                        'is-on': expandedSkillId === skill.id,
                        'is-live': isBusy(skill),
                      }"
                      :aria-label="$t('settings.sandbox.skillTranscript')"
                    >
                      <span v-if="isBusy(skill)" class="skill-card__live-dot" aria-hidden="true" />
                      <t-icon name="chat-bubble-history" size="14px" />
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
                  <t-tooltip
                    v-else-if="isBusy(skill)"
                    :content="$t('settings.sandbox.skillTranscriptLiveHint')"
                    placement="top"
                  >
                    <button
                      type="button"
                      class="skill-card__icon-btn is-live"
                      :aria-label="$t('settings.sandbox.skillTranscript')"
                    >
                      <span class="skill-card__live-dot" aria-hidden="true" />
                      <t-icon name="chat-bubble-history" size="14px" />
                    </button>
                  </t-tooltip>
                  <t-tooltip
                    v-if="skill.status === 'failed'"
                    :content="$t('settings.sandbox.skillRetryHint')"
                    placement="top"
                  >
                    <button
                      type="button"
                      class="skill-card__icon-btn"
                      :disabled="retryingId === skill.id"
                      :aria-label="$t('settings.sandbox.skillRetry')"
                      @click="retrySkill(skill)"
                    >
                      <t-icon name="refresh" size="14px" />
                    </button>
                  </t-tooltip>
                  <button
                    v-if="!isBusy(skill)"
                    type="button"
                    class="skill-card__icon-btn skill-card__icon-btn--danger"
                    :disabled="deletingId === skill.id"
                    :aria-label="$t('settings.skills.manageUninstall')"
                    @click="askRemove(skill)"
                  >
                    <t-icon name="delete" size="14px" />
                  </button>
                </div>
              </div>
              <div v-if="skill.version && !focusSkillId" class="skill-card__type">{{ skill.version }}</div>
              <p
                v-if="skill.description && !focusSkillId"
                class="skill-card__desc"
                :title="skill.description"
              >{{ skill.description }}</p>
              <p v-if="isBusy(skill) && (progressOf(skill) || progressLog(skill))" class="skill-card__log">
                <template v-if="progressOf(skill)">{{ progressOf(skill) }}%</template>
                <template v-if="progressOf(skill) && progressLog(skill)"> · </template>
                {{ progressLog(skill) }}
              </p>
              <ul v-if="failedErrorLines(skill).length" class="skill-card__error">
                <li v-for="(line, i) in failedErrorLines(skill)" :key="i">{{ line }}</li>
              </ul>
            </div>
          </div>
          <button
            v-if="mode === 'list' && !hideAdd"
            type="button"
            class="skill-card skill-card--add"
            @click="emit('install')"
          >
            <span class="skill-card--add__icon" aria-hidden="true">
              <add-icon />
            </span>
            <span class="skill-card--add__label">{{ $t('settings.skills.installSkill') }}</span>
          </button>
        </div>
      </section>
    </t-loading>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, reactive, ref, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { AddIcon } from 'tdesign-icons-vue-next'
import { useI18n } from 'vue-i18n'
import { fetchEventSource } from '@microsoft/fetch-event-source'
import ModelSelector from '@/components/ModelSelector.vue'
import SkillInstallTimeline from '@/components/SkillInstallTimeline.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { SKILL_ICON } from '@/types/mention'
import {
  getAgentById,
  updateAgent,
  type CustomAgent,
} from '@/api/agent'
import {
  configSkillInstallEventsUrl,
  deleteConfigSkill,
  getSandboxConfigById,
  listConfigSkills,
  patchConfigSkill,
  reinstallConfigSkill,
  uploadConfigSkill,
  installConfigSkillFromSource,
  type ConfigSkill,
  type ConfigSkillInstallEvent,
  type SandboxConfigRecord,
} from '@/api/system'
import { getApiBaseUrl } from '@/utils/api-base'
import { generateRandomString, MAX_SKILL_BUNDLE_SIZE_BYTES, MAX_SKILL_BUNDLE_SIZE_MB } from '@/utils/index'
import i18n from '@/i18n'
import {
  MAX_ENV_VALUE_BYTES,
  addSkillEnvSaveInFlight,
  adminSkillEnvClearPayload,
  canClearAdminSkillEnv,
  clearSkillEnvSaveInFlight,
  clearSubmittedSkillEnvDrafts,
  editedSkillEnvPayload,
  isSkillEnvSaveInFlight,
  isValidEnvValueLength,
  skillHasDeclaredEnvs,
  type SkillEnvSavesInFlight,
} from '@/views/settings/envVarState'

// Skills are installed into the config's snapshot image, so the panel needs a
// config that already exists. The catalog page renders the list in place;
// the install drawer only mounts the source/zip form.
const props = withDefaults(defineProps<{
  record: SandboxConfigRecord | null
  mode?: 'install' | 'list'
  hideAdd?: boolean
  focusSkillId?: string
}>(), {
  mode: 'list',
  hideAdd: false,
  focusSkillId: '',
})

const emit = defineEmits<{
  updated: [record: SandboxConfigRecord]
  skillsChanged: []
  inFlightChange: [busy: boolean]
  install: []
  installed: [skillId: string]
}>()

const { t } = useI18n()
const confirmDelete = useConfirmDelete()

const loading = ref(false)
const uploading = ref(false)
const installingFromSource = ref(false)
const uploadPercent = ref(0)
const sourceInput = ref('')
const skills = ref<ConfigSkill[]>([])
const togglingId = ref('')
const deletingId = ref('')
const retryingId = ref('')
const uninstallingId = ref('')
const uninstallingName = ref('')
const uninstallDone = ref(false)
const sawRemoving = ref(false)
// Only one install timeline is open at a time: each one holds an SSE
// connection, and two runs' worth of agent steps in a drawer is unreadable.
const expandedSkillId = ref('')
const transcriptEpoch = ref(0)
const focusedSkillId = ref('')
const skillItemEls = new Map<string, HTMLElement>()
let focusTimer: number | null = null
// Workspace-wide env values. Only one skill's editor popup is open at a time,
// same as the install timeline.
const expandedEnvSkillId = ref('')
// Drafts keyed by skill then variable name. An absent key means "the admin did
// not touch this field", which is what keeps the PATCH partial: an empty string
// clears the stored value server-side, so submitting every input would wipe
// values nobody looked at.
const envDrafts = reactive<Record<string, Record<string, string>>>({})
const envInputUnlocked = reactive<Record<string, boolean>>({})
const envSavesInFlight = ref<SkillEnvSavesInFlight>({})
const fileInputRef = ref<HTMLInputElement | null>(null)
const progressById = ref<Record<string, ConfigSkillInstallEvent>>({})

const abortBySkill = new Map<string, AbortController>()
let pollTimer: number | null = null

const INSTALLER_AGENT_ID = 'builtin-skill-installer'
const LAST_CHAT_MODEL_KEY = 'weknora_last_chat_model_id'

const installerAgent = ref<CustomAgent | null>(null)
const installerModelId = ref('')
const savingInstallerModel = ref(false)

function normalizeSkillRollout(value?: string): 'next_turn' | 'new_session' {
  return value === 'new_session' ? 'new_session' : 'next_turn'
}

const skillRollout = computed(() => normalizeSkillRollout(props.record?.config?.skill_rollout))

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

function readLastChatModelID(): string {
  try {
    return localStorage.getItem(LAST_CHAT_MODEL_KEY) || ''
  } catch {
    return ''
  }
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
  return skill.status === 'installing' || isRemoving(skill)
}

function isRemoving(skill: ConfigSkill): boolean {
  if (uninstallingId.value === skill.id && !uninstallDone.value) {
    if (progressById.value[skill.id]?.stage !== 'failed') return true
  }
  return skill.status === 'removing' || deletingId.value === skill.id
}

function cardStatusClass(skill: ConfigSkill): string {
  if (skill.status === 'failed') return 'skill-card__status--failed'
  if (skill.status === 'installing' || skill.status === 'removing') return 'skill-card__status--busy'
  return skill.enabled ? 'skill-card__status--on' : 'skill-card__status--off'
}

function cardStatusText(skill: ConfigSkill): string {
  if (skill.status === 'installing') return t('settings.sandbox.skillStatusInstalling')
  if (skill.status === 'removing') return t('settings.sandbox.skillStatusRemoving')
  if (skill.status === 'failed') return t('settings.sandbox.skillStatusFailed')
  return skill.enabled ? t('common.on') : t('common.off')
}

const visibleSkills = computed(() => {
  if (props.mode === 'install') {
    return skills.value.filter(isBusy)
  }
  const rows = skills.value.filter((skill) => skill.status !== 'removed')
  if (!props.focusSkillId) return rows
  return rows.filter((skill) => skill.id === props.focusSkillId)
})

const managedSkill = computed(() =>
  props.focusSkillId ? (visibleSkills.value[0] || null) : null,
)

watch(managedSkill, (skill) => {
  if (skill && skillHasDeclaredEnvs(skill)) ensureEnvDrafts(skill.id)
}, { immediate: true })

watch(
  () => props.focusSkillId,
  () => {
    uninstallingId.value = ''
    uninstallingName.value = ''
    uninstallDone.value = false
    sawRemoving.value = false
  },
)

watch(skills, (list) => {
  const id = uninstallingId.value
  if (!id || uninstallDone.value) return
  const row = list.find((skill) => skill.id === id)
  if (row?.status === 'removing') {
    sawRemoving.value = true
    return
  }
  if (sawRemoving.value && (!row || row.status === 'removed')) {
    uninstallDone.value = true
  }
})

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
    expandedEnvSkillId.value = ''
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

function ensureEnvDrafts(skillId: string) {
  if (!envDrafts[skillId]) envDrafts[skillId] = {}
}

function setEnvDraft(skillId: string, name: string, value: string) {
  ensureEnvDrafts(skillId)
  envDrafts[skillId][name] = value ?? ''
}

function envInputLockKey(skillId: string, name: string) {
  return `${skillId}\n${name}`
}

function isEnvInputUnlocked(skillId: string, name: string) {
  return envInputUnlocked[envInputLockKey(skillId, name)] === true
}

function unlockEnvInput(skillId: string, name: string) {
  envInputUnlocked[envInputLockKey(skillId, name)] = true
}

function resetEnvInputLocks() {
  for (const key of Object.keys(envInputUnlocked)) delete envInputUnlocked[key]
}

function onEnvVisible(skill: ConfigSkill, visible: boolean, context?: { e?: Event }) {
  if (visible) {
    if (!skillHasDeclaredEnvs(skill)) return
    expandedSkillId.value = ''
    if (expandedEnvSkillId.value !== skill.id) {
      // Reopening starts from a clean slate rather than resurrecting a
      // half-typed secret from the last time the editor was open.
      envDrafts[skill.id] = {}
      resetEnvInputLocks()
      expandedEnvSkillId.value = skill.id
    } else {
      ensureEnvDrafts(skill.id)
    }
    return
  }
  // The clear confirm is another body-attached popup, so TDesign treats a
  // click on it as an outside click on this editor.
  const target = context?.e?.target
  if (target instanceof Element && target.closest('.t-popconfirm')) {
    return
  }
  if (expandedEnvSkillId.value === skill.id) {
    expandedEnvSkillId.value = ''
  }
}

function envPayload(skill: ConfigSkill): Record<string, string> {
  return editedSkillEnvPayload(
    (skill.envs || []).map((env) => env.name),
    envDrafts[skill.id],
  )
}

function hasEnvEdits(skill: ConfigSkill): boolean {
  return Object.keys(envPayload(skill)).length > 0
}

function envSaveInFlight(skill: ConfigSkill): boolean {
  const configId = props.record?.id
  return configId
    ? isSkillEnvSaveInFlight(envSavesInFlight.value, configId, skill.id)
    : false
}

async function saveEnvs(skill: ConfigSkill, silent = false) {
  if (isBusy(skill)) return
  const envs = envPayload(skill)
  if (Object.keys(envs).length === 0) return
  if (Object.values(envs).some((value) => !isValidEnvValueLength(value))) {
    MessagePlugin.error(
      t('settings.sandbox.skillEnv.valueTooLong', { max: MAX_ENV_VALUE_BYTES }),
    )
    return
  }
  await submitEnvs(skill, envs, silent ? '' : 'settings.sandbox.skillEnv.saveSuccess')
}

function onEnvFieldBlur(skill: ConfigSkill) {
  if (isBusy(skill) || !hasEnvEdits(skill) || envSaveInFlight(skill)) return
  void saveEnvs(skill, true)
}

async function clearEnv(skill: ConfigSkill, name: string) {
  await submitEnvs(skill, adminSkillEnvClearPayload(name), 'settings.sandbox.skillEnv.clearSuccess')
}

async function submitEnvs(
  skill: ConfigSkill,
  envs: Record<string, string>,
  successKey: string,
) {
  if (!props.record) return
  const configId = props.record.id
  if (isSkillEnvSaveInFlight(envSavesInFlight.value, configId, skill.id)) return
  // A response that lands after the drawer moved to another config describes a
  // panel nobody is looking at any more.
  const isCurrent = () => props.record?.id === configId
  envSavesInFlight.value = addSkillEnvSaveInFlight(
    envSavesInFlight.value,
    configId,
    skill.id,
  )
  try {
    const res = await patchConfigSkill(configId, skill.id, { envs })
    if (!isCurrent()) return
    const updated = res?.data
    if (updated) {
      skills.value = skills.value.map((item) => (item.id === skill.id ? updated : item))
    }
    // Nothing reads a stored value back. Remove only values that are still the
    // submitted ones; newer typing during the request must survive cleanup.
    envDrafts[skill.id] = clearSubmittedSkillEnvDrafts(envDrafts[skill.id] || {}, envs)
    if (successKey) MessagePlugin.success(t(successKey))
  } catch (e: any) {
    if (!isCurrent()) return
    MessagePlugin.error(e?.message || t('settings.sandbox.skillEnv.saveFailed'))
  } finally {
    envSavesInFlight.value = clearSkillEnvSaveInFlight(
      envSavesInFlight.value,
      configId,
      skill.id,
    )
  }
}

function progressOf(skill: ConfigSkill): number {
  const percent = progressById.value[skill.id]?.percent
  if (typeof percent === 'number' && Number.isFinite(percent)) {
    return Math.max(0, Math.min(100, percent))
  }
  if (skill.status === 'removing' || deletingId.value === skill.id) return 5
  return skill.status === 'ready' || skill.status === 'failed' ? 100 : 0
}

const REMOVE_STAGE_I18N: Record<string, string> = {
  accepted: 'settings.sandbox.skillRemoveStage.accepted',
  sandbox_ready: 'settings.sandbox.skillRemoveStage.sandbox_ready',
  removed: 'settings.sandbox.skillRemoveStage.removed',
  done: 'settings.sandbox.skillRemoveStage.done',
  failed: 'settings.sandbox.skillRemoveStage.failed',
}

function progressStageText(skill: ConfigSkill): string {
  const ev = progressById.value[skill.id]
  const stageKey = ev?.stage ? REMOVE_STAGE_I18N[ev.stage] : ''
  if (stageKey) return t(stageKey)
  if (skill.status === 'removing' || deletingId.value === skill.id) {
    return t('settings.sandbox.skillRemoveWaiting')
  }
  return ev?.log || ''
}

function progressLog(skill: ConfigSkill): string {
  const ev = progressById.value[skill.id]
  if (ev?.log) return ev.log
  if (skill.status === 'removing') return progressStageText(skill)
  return ''
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

function removalErrorLines(skill: ConfigSkill): string[] {
  const ev = progressById.value[skill.id]
  const raw = ev?.stage === 'failed'
    ? (ev.log || skill.error || '')
    : (uninstallingId.value === skill.id && skill.status !== 'removing' ? (skill.error || '') : '')
  return raw
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
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
    if (res?.data) emit('updated', res.data)
  } catch {
    emit('skillsChanged')
  }
}

function skillsSignature(list: ConfigSkill[]): string {
  return list.map((skill) => `${skill.id}:${skill.status}:${skill.enabled ? 1 : 0}`).join('|')
}

function overlayUninstallStatus(list: ConfigSkill[]): ConfigSkill[] {
  const id = uninstallingId.value
  if (!id || uninstallDone.value) return list
  if (progressById.value[id]?.stage === 'failed') return list
  return list.map((item) => {
    if (item.id !== id) return item
    if (item.status === 'removed' || item.status === 'failed' || item.status === 'removing') {
      return item
    }
    return { ...item, status: 'removing' }
  })
}

async function loadSkills(silent = false) {
  if (!props.record) return
  if (!silent) loading.value = true
  const previous = skillsSignature(skills.value)
  const wasBusy = skills.value.some(isBusy)
  try {
    const res = await listConfigSkills(props.record.id)
    skills.value = overlayUninstallStatus(res?.data || [])
    followBusySkills()
    ensurePoll()
    if (skillsSignature(skills.value) !== previous) {
      emit('skillsChanged')
    }
    if (props.focusSkillId) revealSkill(props.focusSkillId)
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
  const tasks: Array<Promise<unknown>> = [loadSkills(), refreshImage()]
  if (props.mode === 'install') tasks.push(loadInstallerModel())
  await Promise.all(tasks)
}

defineExpose({
  reload: loadAll,
  revealSkill,
})

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

function isZipFile(file: File): boolean {
  return file.name.toLowerCase().endsWith('.zip') || file.type === 'application/zip'
}

function skillBundleErrorMessage(err: any, fallbackKey: string): string {
  const raw = String(err?.message || '')
  if (/cannot exceed \d+\s*MB/i.test(raw)) {
    return t('settings.sandbox.skillBundleTooLarge', { size: MAX_SKILL_BUNDLE_SIZE_MB })
  }
  const tooManyFiles = raw.match(/skill directory holds more than (\d+) files/i)
  if (tooManyFiles) {
    return t('settings.sandbox.skillBundleTooManyFiles', { count: tooManyFiles[1] })
  }
  const tooManyEntries = raw.match(/archive has more than (\d+) zip entries/i)
  if (tooManyEntries) {
    return t('settings.sandbox.skillBundleTooManyZipEntries', { count: tooManyEntries[1] })
  }
  const legacyTooMany = raw.match(/archive holds more than (\d+) files/i)
  if (legacyTooMany) {
    return t('settings.sandbox.skillBundleTooManyFiles', { count: legacyTooMany[1] })
  }
  return raw || t(fallbackKey)
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
  if (file.size > MAX_SKILL_BUNDLE_SIZE_BYTES) {
    MessagePlugin.error(t('settings.sandbox.skillBundleTooLarge', { size: MAX_SKILL_BUNDLE_SIZE_MB }))
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
    const skillId = res?.data?.skill_id || ''
    emit('installed', skillId)
  } catch (e: any) {
    MessagePlugin.error(skillBundleErrorMessage(e, 'settings.sandbox.skillUploadFailed'))
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
    const skillId = res?.data?.skill_id || ''
    emit('installed', skillId)
  } catch (e: any) {
    MessagePlugin.error(skillBundleErrorMessage(e, 'settings.sandbox.skillSourceFailed'))
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

function askRemove(skill: ConfigSkill) {
  if (!props.record || isBusy(skill) || deletingId.value) return
  confirmDelete({
    title: t('settings.skills.manageUninstall'),
    body: deleteHint.value,
    confirmText: t('settings.skills.manageUninstall'),
    onConfirm: () => removeSkill(skill),
  })
}

async function removeSkill(skill: ConfigSkill) {
  if (!props.record || isBusy(skill) || deletingId.value) return
  deletingId.value = skill.id
  uninstallingId.value = skill.id
  uninstallingName.value = skill.name
  uninstallDone.value = false
  forgetProgress(skill.id)
  try {
    await deleteConfigSkill(props.record.id, skill.id)
    MessagePlugin.success(t('settings.sandbox.skillDeleteAccepted'))
    skills.value = skills.value.map((item) => (
      item.id === skill.id ? { ...item, status: 'removing' } : item
    ))
    sawRemoving.value = true
    progressById.value = {
      ...progressById.value,
      [skill.id]: { percent: 5, stage: 'accepted', done: false },
    }
    await loadSkills()
    await refreshImage()
    followProgress(skill.id)
  } catch (e: any) {
    uninstallingId.value = ''
    uninstallingName.value = ''
    sawRemoving.value = false
    MessagePlugin.error(e?.message || t('common.deleteFailed'))
  } finally {
    deletingId.value = ''
  }
}

// The panel is mounted while its catalog drawer is open. Switching sandbox
// configs or closing the drawer tears the follows down.
watch(
  () => props.record?.id,
  (configID, previousConfigID) => {
    if (configID !== previousConfigID) {
      expandedEnvSkillId.value = ''
      for (const skillId of Object.keys(envDrafts)) delete envDrafts[skillId]
    }
    if (configID) {
      void loadAll()
      return
    }
    stopAllFollows()
    stopPoll()
    skills.value = []
    progressById.value = {}
    installerAgent.value = null
    installerModelId.value = ''
  },
  { immediate: true },
)

watch(
  () => props.focusSkillId,
  (skillId) => {
    if (skillId) revealSkill(skillId)
  },
)

onUnmounted(() => {
  stopAllFollows()
  stopPoll()
  if (focusTimer != null) window.clearTimeout(focusTimer)
})
</script>

<style lang="less" scoped>
.sandbox-skills-panel--list {
  min-height: 36px;
}

.sandbox-skills-panel--focused .skill-list {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.installer-model-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
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
    border-color: var(--td-text-color-placeholder);
    background: var(--td-bg-color-container-hover);
  }

  &.has-file {
    border-color: var(--td-brand-color);
    background: var(--td-bg-color-container);
    border-style: solid;
  }

  &.is-disabled {
    cursor: not-allowed;
    opacity: 0.6;
  }

  &--large {
    min-height: 180px;
    border-radius: 12px;
    border-width: 2px;
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

.file-upload-area--large .file-upload-content {
  flex-direction: column;
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

.file-upload-area--large .upload-text {
  flex-direction: column;
  gap: 4px;
}

.file-upload-area--large .upload-primary-text {
  font-size: 15px;
}

.file-upload-area--large .upload-secondary-text {
  font-size: 13px;
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

.sandbox-skills-panel--focused {
  min-height: 0;
}

.skill-manage {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.skill-manage__row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.skill-manage__controls {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.skill-manage__info {
  min-width: 0;

  label {
    display: block;
    margin-bottom: 4px;
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  p {
    margin: 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }
}

.skill-manage__section {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 10px;
  padding-top: 12px;
  border-top: 1px solid var(--td-component-stroke);

  > .t-button {
    align-self: flex-start;
  }

  h4 {
    margin: 0;
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .skill-manage__section-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }

  .skill-manage__progress {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    font-weight: 500;
    line-height: 1;
    color: var(--td-brand-color);

    :deep(.t-progress--circle svg) {
      display: block;
    }
  }

  &--danger h4 {
    color: var(--td-error-color);
  }

  &--remove {
    border-top: 0;
    padding-top: 0;
  }
}

.skill-manage__progress-label {
  font-size: 12px;
  font-weight: 600;
  color: var(--td-brand-color);
}

.skill-manage__remove-stage {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.skill-manage__done {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 10px;
  padding: 8px 0 4px;
  color: var(--td-success-color, var(--td-brand-color));

  p {
    margin: 0;
    font-size: 14px;
    line-height: 1.55;
    color: var(--td-text-color-primary);
  }
}

.skill-envs__hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.skill-envs__rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.skill-envs__row {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.skill-envs__meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.skill-envs__name {
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
  font-size: 12px;
  color: var(--td-text-color-primary);
  overflow-wrap: anywhere;
}

.skill-envs__tag {
  font-size: 12px;
  line-height: 18px;
  padding: 0 8px;
  border-radius: 10px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
}

.skill-envs__tag--required {
  background: var(--td-warning-color-light);
  color: var(--td-warning-color);
}

.skill-envs__tag--set {
  background: var(--td-success-color-light);
  color: var(--td-success-color);
}

.skill-envs__desc {
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.skill-envs__editor {
  display: flex;
  align-items: center;
  gap: 8px;
}

.skill-list {
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.sandbox-skills-panel--list .skill-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
}

.skill-card {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 14px 14px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
  min-width: 0;

  &--focused {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 2px var(--td-brand-color-focus, rgba(0, 168, 112, 0.18));
  }

  &--bare {
    padding: 0;
    border: none;
    border-radius: 0;
    background: transparent;
    box-shadow: none;
  }

  &--add {
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 68px;
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
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }

    &__icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      border-radius: 8px;
      background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
      color: var(--td-brand-color);
      font-size: 18px;
    }

    &__label {
      font-size: 13px;
      font-weight: 500;
      line-height: 1.4;
    }
  }
}

.skill-card__badge {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: 1px;
  background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
  color: var(--td-brand-color);
  overflow: hidden;

  :deep(.t-progress--circle svg) {
    display: block;
  }
}

.skill-card__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.skill-card__header {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;

  &--toolbar {
    justify-content: flex-end;
    width: 100%;
  }
}

.skill-card__title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.skill-card__status {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px 8px 1px 6px;
  font-size: 11px;
  font-weight: 500;
  line-height: 16px;
  border-radius: 10px;
  background: var(--td-bg-color-secondarycontainer);

  &--on {
    color: var(--td-success-color-7, #118053);

    .skill-card__status-dot {
      background: var(--td-success-color, #118053);
    }
  }

  &--off {
    color: var(--td-text-color-placeholder);

    .skill-card__status-dot {
      background: var(--td-gray-color-5);
    }
  }

  &--busy {
    color: var(--td-brand-color);

    .skill-card__status-dot {
      background: var(--td-brand-color);
    }
  }

  &--failed {
    color: var(--td-warning-color-7, #b85c00);

    .skill-card__status-dot {
      background: var(--td-warning-color, #e37318);
    }
  }
}

.skill-card__status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;

  &.is-live {
    animation: skill-status-dot 2.4s ease-in-out infinite;
  }
}

@keyframes skill-status-dot {
  0%,
  100% { opacity: 1; }
  50% { opacity: 0.45; }
}

.skill-card__actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0;

  :deep(.t-switch) {
    transform: scale(0.84);
    transform-origin: center right;
    margin-right: 2px;
  }

  :deep(.t-popup),
  :deep(.t-popup__reference),
  :deep(.t-popconfirm) {
    display: inline-flex;
  }
}

.skill-card__icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  line-height: 0;
  transition: background 0.15s ease, color 0.15s ease;

  :deep(.t-icon) {
    display: block;
  }

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
  }

  &--danger:hover:not(:disabled) {
    background: var(--td-error-color-1, var(--td-bg-color-secondarycontainer));
    color: var(--td-error-color);
  }
}

.skill-card__live-dot {
  width: 5px;
  height: 5px;
  margin-right: 1px;
  border-radius: 50%;
  background: var(--td-brand-color);
  animation: skill-status-dot 2.4s ease-in-out infinite;
}

.skill-card__type {
  font-size: 11px;
  font-weight: 500;
  line-height: 1.3;
  color: var(--td-text-color-placeholder);
}

.skill-card__desc {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  min-width: 0;
  margin: 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-secondary);
}

.skill-card__log {
  margin: 0;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
}

.skill-card__error {
  margin: 2px 0 0;
  padding: 0;
  font-size: 12px;
  line-height: 1.5;
  word-break: break-word;
  list-style: none;
  color: var(--td-error-color);
}

.skill-card__error li:not(:only-child) {
  padding-left: 10px;
  text-indent: -10px;
}

.skill-card__error li:not(:only-child)::before {
  content: '· ';
}

.skill-card__error li + li {
  margin-top: 2px;
}

</style>

<style lang="less">
.skill-transcript-popup,
.skill-env-popup {
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

  &__close {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);
  }

  &__body {
    max-height: min(360px, 52vh);
    overflow: auto;

    &::-webkit-scrollbar {
      width: 6px;
    }

    &::-webkit-scrollbar-thumb {
      background: var(--td-bg-color-component-disabled);
      border-radius: 3px;
    }
  }
}

.skill-transcript-popup {
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

  &__body {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.skill-env-popup {
  .t-popup__content {
    width: 440px;
    max-width: min(440px, calc(100vw - 32px));
  }

  &__body {
    padding: 12px 14px 14px;
    background: var(--td-bg-color-container);
  }

  &__footer {
    display: flex;
    justify-content: flex-end;
    padding: 10px 14px;
    border-top: 1px solid var(--td-component-stroke);
  }

  .skill-envs__hint {
    margin: 0;
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }

  .skill-envs__rows {
    margin-top: 10px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .skill-envs__row {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .skill-envs__meta {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
  }

  .skill-envs__name {
    font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
    font-size: 12px;
    color: var(--td-text-color-primary);
    overflow-wrap: anywhere;
  }

  .skill-envs__tag {
    font-size: 12px;
    line-height: 18px;
    padding: 0 8px;
    border-radius: 10px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
  }

  .skill-envs__tag--required {
    background: var(--td-warning-color-light);
    color: var(--td-warning-color);
  }

  .skill-envs__tag--set {
    background: var(--td-success-color-light);
    color: var(--td-success-color);
  }

  .skill-envs__desc {
    font-size: 12px;
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }

  .skill-envs__editor {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .skill-envs__editor .t-input__wrap {
    flex: 1;
    min-width: 0;
  }

  /* readonly is only there to block password-manager fill; keep the field looking editable. */
  .skill-envs__editor .t-input.t-is-readonly {
    cursor: text;
    background-color: var(--td-bg-color-specialcomponent);
  }
}
</style>
