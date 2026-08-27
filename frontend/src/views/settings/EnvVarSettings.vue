<template>
  <div class="env-settings">
    <div class="section-header">
      <div class="section-header__titlewrap">
        <h2>{{ t('envVarSettings.title') }}</h2>
        <t-popup placement="bottom-start" trigger="hover" :overlay-inner-style="{ maxWidth: '380px' }">
          <button type="button" class="hint-trigger" :aria-label="t('envVarSettings.helpAria')">
            <t-icon name="help-circle" size="16px" />
          </button>
          <template #content>
            <div class="hint-popover">
              <div class="hint-popover__block">
                <p class="hint-popover__title">{{ t('envVarSettings.introPersonalTitle') }}</p>
                <p class="hint-popover__text">{{ t('envVarSettings.introPersonalBody') }}</p>
              </div>
              <div class="hint-popover__block">
                <p class="hint-popover__title">{{ t('envVarSettings.introRuntimeTitle') }}</p>
                <p class="hint-popover__text">{{ t('envVarSettings.introRuntimeBody') }}</p>
              </div>
            </div>
          </template>
        </t-popup>
      </div>
      <p class="section-description">{{ t('envVarSettings.description') }}</p>
    </div>

    <p v-if="loading" class="env-state">{{ t('envVarSettings.loading') }}</p>

    <div v-else-if="loadError" class="env-state env-state--error">
      <p class="env-state__text">{{ loadError }}</p>
      <t-button variant="outline" size="small" @click="load()">
        {{ t('envVarSettings.retry') }}
      </t-button>
    </div>

    <div v-else-if="groups.length === 0" class="env-empty">
      <p class="env-empty__title">{{ t('envVarSettings.noConfigTitle') }}</p>
      <p class="env-empty__desc">{{ t('envVarSettings.noConfigDescription') }}</p>
    </div>

    <template v-else>
      <section class="env-section">
        <header class="env-section__head">
          <h3>{{ t('envVarSettings.skillTitle') }}</h3>
          <p>{{ t('envVarSettings.skillHint') }}</p>
        </header>

        <div v-if="skillCards.length === 0" class="env-empty env-empty--soft">
          <p class="env-empty__title">{{ t('envVarSettings.skillEmptyTitle') }}</p>
          <p class="env-empty__desc">{{ t('envVarSettings.skillEmptyDesc') }}</p>
        </div>

        <div v-else class="env-skill-list">
          <article v-for="card in skillCards" :key="`${card.sandbox_config_id}:${card.skill.skill_id}`" class="env-skill-card">
            <header class="env-skill-card__head">
              <div class="env-skill-card__identity">
                <div class="env-skill-card__title">
                  <t-icon :name="SKILL_ICON" size="16px" class="env-group__icon" />
                  <h4>{{ card.skill.skill_name || card.skill.skill_id }}</h4>
                  <span
                    v-if="multipleSandboxes"
                    class="env-skill-card__sandbox"
                    :title="t('envVarSettings.skillOnSandbox', { name: card.sandbox_config_name })"
                  >
                    {{ card.sandbox_config_name }}
                  </span>
                </div>
                <p v-if="card.skill.description" class="env-group__desc" :title="card.skill.description">
                  {{ card.skill.description }}
                </p>
              </div>
              <span
                class="env-skill-card__status"
                :class="blockingVarCount(card.skill.vars) ? 'is-needs' : 'is-ready'"
              >
                {{
                  blockingVarCount(card.skill.vars)
                    ? t('envVarSettings.skillNeedsCount', { count: blockingVarCount(card.skill.vars) })
                    : t('envVarSettings.skillReady')
                }}
              </span>
            </header>

            <ul class="env-secret-rows">
              <li
                v-for="entry in card.skill.vars"
                :key="entry.name"
                class="env-kv"
                :class="{ 'is-editing': isEditing(skillKey(card.skill.skill_id, entry.name)) }"
              >
                <div class="env-kv__head">
                  <div class="env-kv__key">
                    <div class="env-secret__meta">
                      <code>{{ entry.name }}</code>
                      <span v-if="entry.required" class="env-tag env-tag--required">
                        {{ t('envVarSettings.requiredTag') }}
                      </span>
                      <span v-if="statusOf(entry).key === 'workspace'" class="env-tag env-tag--workspace">
                        {{ t(STATUS_LABEL.workspace) }}
                      </span>
                    </div>
                    <p v-if="entry.description" class="env-secret__desc" :title="entry.description">
                      {{ entry.description }}
                    </p>
                    <p v-if="entry.updated_at" class="env-secret__when">
                      {{ t('envVarSettings.updatedAt', { time: formatTime(entry.updated_at) }) }}
                    </p>
                  </div>
                  <div v-if="!isEditing(skillKey(card.skill.skill_id, entry.name))" class="env-kv__actions">
                    <t-button
                      variant="text"
                      theme="primary"
                      size="small"
                      shape="square"
                      :title="
                        statusOf(entry).key === 'unset'
                          ? t('envVarSettings.setValue')
                          : t('envVarSettings.replaceValue')
                      "
                      :aria-label="
                        statusOf(entry).key === 'unset'
                          ? t('envVarSettings.setValue')
                          : t('envVarSettings.replaceValue')
                      "
                      @click="startEdit(skillKey(card.skill.skill_id, entry.name))"
                    >
                      <template #icon><t-icon name="key" /></template>
                    </t-button>
                    <t-popconfirm
                      v-if="statusOf(entry).key === 'user'"
                      theme="warning"
                      :content="t('envVarSettings.clearConfirm', { name: entry.name })"
                      :confirm-btn="{ content: t('envVarSettings.clear'), theme: 'danger' }"
                      :cancel-btn="{ content: t('common.cancel') }"
                      @confirm="deleteSkill(card.skill.skill_id, entry.name)"
                    >
                      <t-button
                        theme="danger"
                        variant="text"
                        size="small"
                        shape="square"
                        :title="t('envVarSettings.clear')"
                        :aria-label="t('envVarSettings.clear')"
                        :loading="busyKey === skillKey(card.skill.skill_id, entry.name)"
                      >
                        <template #icon><t-icon name="delete" /></template>
                      </t-button>
                    </t-popconfirm>
                  </div>
                </div>
                <div v-if="isEditing(skillKey(card.skill.skill_id, entry.name))" class="env-kv__editor">
                  <t-input
                    ref="editorInput"
                    :value="drafts[skillKey(card.skill.skill_id, entry.name)] ?? ''"
                    type="password"
                    autocomplete="new-password"
                    :name="`wk-me-${card.skill.skill_id}-${entry.name.length}`"
                    spellcheck="false"
                    data-lpignore="true"
                    data-1p-ignore="true"
                    data-bwignore="true"
                    :aria-label="entry.name"
                    :placeholder="
                      statusOf(entry).key === 'unset'
                        ? t('envVarSettings.valuePlaceholder')
                        : t('envVarSettings.storedPlaceholder')
                    "
                    @update:value="(v: string) => setDraft(skillKey(card.skill.skill_id, entry.name), v)"
                    @enter="saveSkill(card.skill, entry)"
                  />
                  <t-button
                    variant="text"
                    theme="primary"
                    size="small"
                    shape="square"
                    :title="t('envVarSettings.save')"
                    :aria-label="t('envVarSettings.save')"
                    :loading="busyKey === skillKey(card.skill.skill_id, entry.name)"
                    @click="saveSkill(card.skill, entry)"
                  >
                    <template #icon><t-icon name="check" /></template>
                  </t-button>
                  <t-button
                    variant="text"
                    size="small"
                    shape="square"
                    :title="t('common.cancel')"
                    :aria-label="t('common.cancel')"
                    @click="cancelEdit(skillKey(card.skill.skill_id, entry.name))"
                  >
                    <template #icon><t-icon name="close" /></template>
                  </t-button>
                </div>
              </li>
            </ul>
          </article>
        </div>
      </section>

      <section class="env-section env-section--aside">
        <header class="env-section__head env-section__head--row">
          <div>
            <h3>{{ t('envVarSettings.sandboxTitle') }}</h3>
            <p>{{ t('envVarSettings.sandboxHint') }}</p>
          </div>
          <t-button
            v-if="!addingSandbox"
            variant="text"
            size="small"
            shape="square"
            :title="t('envVarSettings.addRow')"
            :aria-label="t('envVarSettings.addRow')"
            @click="startAddSandbox"
          >
            <template #icon><t-icon name="add" /></template>
          </t-button>
        </header>

        <div v-if="addingSandbox" class="env-composer">
          <t-select
            v-if="multipleSandboxes"
            v-model="sandboxDraft.configId"
            :options="sandboxOptions"
            :placeholder="t('envVarSettings.sandboxPick')"
            class="env-composer__sandbox"
          />
          <div class="env-new-row">
            <t-input
              v-model="sandboxDraft.name"
              autocomplete="off"
              :placeholder="t('envVarSettings.namePlaceholder')"
              :status="sandboxDraft.name && !isValidEnvName(sandboxDraft.name) ? 'error' : undefined"
              class="env-new-row__name"
            />
            <t-input
              :value="sandboxDraft.value"
              type="password"
              autocomplete="new-password"
              :placeholder="t('envVarSettings.valuePlaceholder')"
              class="env-new-row__value"
              @update:value="(v: string) => { sandboxDraft.value = v }"
            />
            <t-button
              variant="text"
              theme="primary"
              size="small"
              shape="square"
              :title="t('envVarSettings.save')"
              :aria-label="t('envVarSettings.save')"
              :loading="busyKey === 'sandbox-new'"
              @click="saveNewSandboxRow"
            >
              <template #icon><t-icon name="check" /></template>
            </t-button>
            <t-button
              variant="text"
              shape="square"
              size="small"
              :title="t('common.cancel')"
              :aria-label="t('common.cancel')"
              @click="cancelAddSandbox"
            >
              <template #icon><t-icon name="close" /></template>
            </t-button>
          </div>
          <p class="env-sandbox-card__empty">{{ t('envVarSettings.nameRule') }}</p>
        </div>

        <div v-if="sandboxCards.length" class="env-sandbox-list">
          <article v-for="group in sandboxCards" :key="group.sandbox_config_id" class="env-sandbox-card">
            <div class="env-skill-card__title">
              <svg class="env-group__icon" width="16" height="16" viewBox="0 0 18 18" fill="none" aria-hidden="true">
                <rect x="2.5" y="3" width="13" height="12" rx="2" stroke="currentColor" stroke-width="1.2" fill="none" />
                <path d="M2.5 6.5h13" stroke="currentColor" stroke-width="1.2" />
                <path d="M5.5 10h4M5.5 12.5h2.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
              </svg>
              <h4>{{ configLabel(group) }}</h4>
            </div>
            <p v-if="group.description" class="env-group__desc" :title="group.description">
              {{ group.description }}
            </p>
            <ul class="env-sandbox-rows">
              <li
                v-for="entry in group.vars"
                :key="entry.name"
                class="env-kv"
                :class="{ 'is-editing': isEditing(sandboxKey(group.sandbox_config_id, entry.name)) }"
              >
                <div class="env-kv__head">
                  <div class="env-kv__key">
                    <code>{{ entry.name }}</code>
                    <p v-if="entry.updated_at" class="env-secret__when">
                      {{ t('envVarSettings.updatedAt', { time: formatTime(entry.updated_at) }) }}
                    </p>
                  </div>
                  <div
                    v-if="!isEditing(sandboxKey(group.sandbox_config_id, entry.name))"
                    class="env-kv__actions"
                  >
                    <t-button
                      variant="text"
                      theme="primary"
                      size="small"
                      shape="square"
                      :title="t('envVarSettings.replaceValue')"
                      :aria-label="t('envVarSettings.replaceValue')"
                      @click="startEdit(sandboxKey(group.sandbox_config_id, entry.name))"
                    >
                      <template #icon><t-icon name="key" /></template>
                    </t-button>
                    <t-popconfirm
                      theme="warning"
                      :content="t('envVarSettings.deleteConfirm', { name: entry.name })"
                      :confirm-btn="{ content: t('envVarSettings.delete'), theme: 'danger' }"
                      :cancel-btn="{ content: t('common.cancel') }"
                      @confirm="deleteSandbox(group.sandbox_config_id, entry.name)"
                    >
                      <t-button
                        theme="danger"
                        variant="text"
                        size="small"
                        shape="square"
                        :title="t('envVarSettings.delete')"
                        :aria-label="t('envVarSettings.delete')"
                        :loading="busyKey === sandboxKey(group.sandbox_config_id, entry.name)"
                      >
                        <template #icon><t-icon name="delete" /></template>
                      </t-button>
                    </t-popconfirm>
                  </div>
                </div>
                <div
                  v-if="isEditing(sandboxKey(group.sandbox_config_id, entry.name))"
                  class="env-kv__editor"
                >
                  <t-input
                    ref="editorInput"
                    :value="drafts[sandboxKey(group.sandbox_config_id, entry.name)] ?? ''"
                    type="password"
                    autocomplete="new-password"
                    :name="`wk-sb-${group.sandbox_config_id.slice(0, 8)}`"
                    spellcheck="false"
                    data-lpignore="true"
                    data-1p-ignore="true"
                    data-bwignore="true"
                    :aria-label="entry.name"
                    :placeholder="t('envVarSettings.storedPlaceholder')"
                    @update:value="(v: string) => setDraft(sandboxKey(group.sandbox_config_id, entry.name), v)"
                    @enter="saveSandbox(group, entry.name)"
                  />
                  <t-button
                    variant="text"
                    theme="primary"
                    size="small"
                    shape="square"
                    :title="t('envVarSettings.save')"
                    :aria-label="t('envVarSettings.save')"
                    :loading="busyKey === sandboxKey(group.sandbox_config_id, entry.name)"
                    @click="saveSandbox(group, entry.name)"
                  >
                    <template #icon><t-icon name="check" /></template>
                  </t-button>
                  <t-button
                    variant="text"
                    size="small"
                    shape="square"
                    :title="t('common.cancel')"
                    :aria-label="t('common.cancel')"
                    @click="cancelEdit(sandboxKey(group.sandbox_config_id, entry.name))"
                  >
                    <template #icon><t-icon name="close" /></template>
                  </t-button>
                </div>
              </li>
            </ul>
          </article>
        </div>
        <p v-else-if="!addingSandbox" class="env-sandbox-card__empty">
          {{ t('envVarSettings.sandboxEmpty') }}
        </p>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  deleteMySandboxEnv,
  deleteMySkillEnv,
  listMyEnvVars,
  setMySandboxEnv,
  setMySkillEnv,
  type ConfigEnvGroup,
  type EnvVarSource,
  type EnvVarView,
  type SkillEnvGroup,
} from '@/api/env-vars'
import { SKILL_ICON } from '@/types/mention'
import {
  MAX_ENV_VALUE_BYTES,
  MAX_USER_ENV_VARS_PER_SCOPE,
  blockingVarCount,
  canAddEnvVar,
  configLabel,
  isValidEnvName,
  isValidEnvValueLength,
  sandboxGroupsWithVars,
  skillSecretCards,
  sortedConfigGroups,
  statusOf,
} from './envVarState'

const { t, locale } = useI18n()

const STATUS_LABEL: Record<EnvVarSource, string> = {
  unset: 'envVarSettings.statusUnset',
  workspace: 'envVarSettings.statusWorkspace',
  user: 'envVarSettings.statusUser',
}

const loading = ref(true)
const loadError = ref('')
const groups = ref<ConfigEnvGroup[]>([])

const skillCards = computed(() => skillSecretCards(groups.value))
const sandboxCards = computed(() => sandboxGroupsWithVars(groups.value))
const multipleSandboxes = computed(() => groups.value.length > 1)
const sandboxOptions = computed(() =>
  groups.value.map((group) => ({
    label: configLabel(group),
    value: group.sandbox_config_id,
  })),
)

// Drafts are keyed by scope and name rather than held on the row, so a reload
// cannot silently discard what is half-typed elsewhere on the page.
const drafts = reactive<Record<string, string>>({})
const editingKey = ref('')
const editorInput = ref<{ focus?: () => void } | null>(null)
const addingSandbox = ref(false)
const sandboxDraft = reactive({ configId: '', name: '', value: '' })
const busyKey = ref('')

function skillKey(skillId: string, name: string): string {
  return `skill:${skillId}:${name}`
}

function sandboxKey(configId: string, name: string): string {
  return `sandbox:${configId}:${name}`
}

function setDraft(key: string, value: string) {
  drafts[key] = value ?? ''
}

function isEditing(key: string) {
  return editingKey.value === key
}

async function startEdit(key: string) {
  editingKey.value = key
  await nextTick()
  editorInput.value?.focus?.()
}

function cancelEdit(key: string) {
  if (editingKey.value === key) editingKey.value = ''
  delete drafts[key]
}

function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString(locale.value)
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await listMyEnvVars()
    groups.value = sortedConfigGroups(res?.data || [])
  } catch (e: any) {
    groups.value = []
    loadError.value = e?.message || t('envVarSettings.loadFailed')
  } finally {
    loading.value = false
  }
}

/** Shared guard for both scopes: a value is required and bounded. */
function rejectBadValue(value: string): boolean {
  if (!value) {
    MessagePlugin.warning(t('envVarSettings.valueRequired'))
    return true
  }
  if (!isValidEnvValueLength(value)) {
    MessagePlugin.error(t('envVarSettings.valueTooLong', { max: MAX_ENV_VALUE_BYTES }))
    return true
  }
  return false
}

async function submit(key: string, action: () => Promise<unknown>, successMessage: string) {
  busyKey.value = key
  try {
    await action()
    // Write-only by design: the value is stored and never echoed, so leaving it
    // in the input would keep a secret in the DOM for no benefit.
    delete drafts[key]
    if (editingKey.value === key) editingKey.value = ''
    MessagePlugin.success(successMessage)
    await load()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('envVarSettings.saveFailed'))
  } finally {
    busyKey.value = ''
  }
}

async function saveSkill(skill: SkillEnvGroup, entry: EnvVarView) {
  const key = skillKey(skill.skill_id, entry.name)
  const value = drafts[key] || ''
  if (rejectBadValue(value)) return
  await submit(
    key,
    () => setMySkillEnv(skill.skill_id, entry.name, value),
    t('envVarSettings.saveSuccess'),
  )
}

async function deleteSkill(skillId: string, name: string) {
  await submit(
    skillKey(skillId, name),
    () => deleteMySkillEnv(skillId, name),
    t('envVarSettings.clearSuccess'),
  )
}

async function saveSandbox(group: ConfigEnvGroup, name: string) {
  const key = sandboxKey(group.sandbox_config_id, name)
  const value = drafts[key] || ''
  if (rejectBadValue(value)) return
  await submit(
    key,
    () => setMySandboxEnv(group.sandbox_config_id, name, value),
    t('envVarSettings.saveSuccess'),
  )
}

async function deleteSandbox(configId: string, name: string) {
  await submit(
    sandboxKey(configId, name),
    () => deleteMySandboxEnv(configId, name),
    t('envVarSettings.deleteSuccess'),
  )
}

function startAddSandbox() {
  addingSandbox.value = true
  sandboxDraft.configId = groups.value[0]?.sandbox_config_id || ''
  sandboxDraft.name = ''
  sandboxDraft.value = ''
}

function cancelAddSandbox() {
  addingSandbox.value = false
  sandboxDraft.configId = ''
  sandboxDraft.name = ''
  sandboxDraft.value = ''
}

async function saveNewSandboxRow() {
  const group = groups.value.find((item) => item.sandbox_config_id === sandboxDraft.configId)
  if (!group) {
    MessagePlugin.warning(t('envVarSettings.sandboxPick'))
    return
  }
  const name = sandboxDraft.name.trim()
  if (!isValidEnvName(name)) {
    MessagePlugin.error(t('envVarSettings.nameInvalid'))
    return
  }
  if (group.vars?.some((v) => v.name === name)) {
    MessagePlugin.error(t('envVarSettings.nameDuplicate'))
    return
  }
  if (rejectBadValue(sandboxDraft.value)) return
  if (!canAddEnvVar(group.vars || [], name)) {
    MessagePlugin.error(t('envVarSettings.tooManyValues', { max: MAX_USER_ENV_VARS_PER_SCOPE }))
    return
  }
  busyKey.value = 'sandbox-new'
  try {
    await setMySandboxEnv(group.sandbox_config_id, name, sandboxDraft.value)
    cancelAddSandbox()
    MessagePlugin.success(t('envVarSettings.saveSuccess'))
    await load()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('envVarSettings.saveFailed'))
  } finally {
    busyKey.value = ''
  }
}

onMounted(() => {
  void load()
})
</script>

<style lang="less" scoped>
.env-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 24px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 8px 0 0;
    line-height: 1.6;
  }
}

.section-header__titlewrap {
  display: flex;
  align-items: center;
  gap: 6px;
}

.hint-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 2px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: help;
  line-height: 1;

  &:hover,
  &:focus-visible {
    color: var(--td-brand-color);
  }
}

.hint-popover {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-width: 340px;
}

.hint-popover__title {
  margin: 0;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.hint-popover__text {
  margin: 4px 0 0;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.55;
}

.env-state {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  margin: 0;
}

.env-state--error .env-state__text {
  margin: 0;
  color: var(--td-error-color);
}

.env-empty {
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 10px;
  padding: 24px;
  text-align: center;
}

.env-empty--soft {
  padding: 16px;
}

.env-empty__title {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  margin: 0 0 4px 0;
}

.env-empty__desc {
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  margin: 0;
}

.env-section__head {
  margin-bottom: 12px;

  h3 {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  p {
    margin: 4px 0 0;
    font-size: 13px;
    line-height: 1.55;
    color: var(--td-text-color-secondary);
  }
}

.env-section__head--row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.env-composer {
  margin-bottom: 12px;
  padding: 12px 14px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-secondarycontainer);
}

.env-composer__sandbox {
  width: 100%;
  max-width: 280px;
  margin-bottom: 8px;
}

.env-section--aside {
  margin-top: 32px;
  padding-top: 24px;
  border-top: 1px solid var(--td-component-stroke);
}

.env-skill-list,
.env-sandbox-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.env-skill-card,
.env-sandbox-card {
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  padding: 14px 16px;
  background: var(--td-bg-color-container);
}

.env-skill-card__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.env-skill-card__identity h4,
.env-sandbox-card h4 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.env-skill-card__title {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.env-group__icon {
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.env-group__desc {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.55;
  color: var(--td-text-color-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.env-skill-card__sandbox {
  font-size: 12px;
  font-weight: 400;
  color: var(--td-text-color-placeholder);
}

.env-skill-card__status {
  flex-shrink: 0;
  font-size: 12px;
  line-height: 20px;
  padding: 0 8px;
  border-radius: 10px;
  background: var(--td-success-color-light);
  color: var(--td-success-color);

  &.is-needs {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
  }
}

.env-secret-rows,
.env-sandbox-rows {
  margin: 12px 0 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 0;
}

.env-kv {
  padding: 10px 0;
}

.env-kv + .env-kv {
  border-top: 1px solid var(--td-component-stroke);
}

.env-kv__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.env-kv__key {
  min-width: 0;
  flex: 1;

  code {
    font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
    font-size: 13px;
    color: var(--td-text-color-primary);
    overflow-wrap: anywhere;
  }
}

.env-kv__actions {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  margin: -4px -4px 0 0;
}

.env-kv__editor {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-top: 8px;
}

.env-kv__editor :deep(.t-input__wrap) {
  flex: 1;
  min-width: 0;
}

.env-secret__meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;

  code {
    font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
    font-size: 13px;
    color: var(--td-text-color-primary);
    overflow-wrap: anywhere;
  }
}

.env-tag {
  font-size: 12px;
  line-height: 18px;
  padding: 0 8px;
  border-radius: 10px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
}

.env-tag--required {
  background: var(--td-warning-color-light);
  color: var(--td-warning-color);
}

.env-tag--user {
  background: var(--td-success-color-light);
  color: var(--td-success-color);
}

.env-tag--workspace {
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
}

.env-secret__desc,
.env-secret__when {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.env-secret__desc {
  display: -webkit-box;
  -webkit-line-clamp: 1;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.env-secret__when {
  color: var(--td-text-color-placeholder);
}

.env-new-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.env-new-row__value {
  flex: 1;
  min-width: 0;
}

.env-sandbox-card__empty {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.env-new-row__name {
  flex: 0 0 34%;
}

@media (max-width: 720px) {
  .env-kv__head {
    flex-wrap: wrap;
  }

  .env-new-row,
  .env-new-row__name {
    flex-wrap: wrap;
    flex-basis: 100%;
  }
}
</style>
