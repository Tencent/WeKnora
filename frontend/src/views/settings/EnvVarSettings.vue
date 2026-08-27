<template>
  <div class="env-settings">
    <div class="section-header">
      <h2>{{ t('envVarSettings.title') }}</h2>
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

    <div v-else class="env-groups">
      <section v-for="group in groups" :key="group.sandbox_config_id" class="env-group">
        <h3 class="env-group__title">{{ configLabel(group) }}</h3>

        <!-- Config-wide variables: the member-side counterpart of the admin's
             sandbox env_vars, injected into every execution on this config. -->
        <div class="env-block">
          <div class="env-block__head">
            <div>
              <h4 class="env-block__title">{{ t('envVarSettings.sandboxTitle') }}</h4>
              <p class="env-block__hint">{{ t('envVarSettings.sandboxHint') }}</p>
            </div>
            <t-button variant="text" size="small" @click="addRow(group.sandbox_config_id)">
              <template #icon><t-icon name="add" /></template>
              {{ t('envVarSettings.addRow') }}
            </t-button>
          </div>

          <ul v-if="group.vars?.length" class="env-rows">
            <li v-for="entry in group.vars" :key="entry.name" class="env-row">
              <code class="env-row__name">{{ entry.name }}</code>
              <t-input
                v-model="drafts[sandboxKey(group.sandbox_config_id, entry.name)]"
                type="password"
                autocomplete="off"
                :aria-label="entry.name"
                :placeholder="t('envVarSettings.storedPlaceholder')"
                class="env-row__value"
              />
              <t-button
                theme="primary"
                size="small"
                :loading="busyKey === sandboxKey(group.sandbox_config_id, entry.name)"
                @click="saveSandbox(group, entry.name)"
              >
                {{ t('envVarSettings.save') }}
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
                  :loading="busyKey === sandboxKey(group.sandbox_config_id, entry.name)"
                >
                  {{ t('envVarSettings.delete') }}
                </t-button>
              </t-popconfirm>
              <p v-if="entry.updated_at" class="env-row__meta">
                {{ t('envVarSettings.updatedAt', { time: formatTime(entry.updated_at) }) }}
              </p>
            </li>
          </ul>
          <p v-else-if="!newRows[group.sandbox_config_id]?.length" class="env-block__empty">
            {{ t('envVarSettings.sandboxEmpty') }}
          </p>

          <div v-if="newRows[group.sandbox_config_id]?.length" class="env-new-rows">
            <div
              v-for="(row, index) in newRows[group.sandbox_config_id]"
              :key="index"
              class="env-new-row"
            >
              <t-input
                v-model="row.name"
                autocomplete="off"
                :placeholder="t('envVarSettings.namePlaceholder')"
                :status="row.name && !isValidEnvName(row.name) ? 'error' : undefined"
                class="env-new-row__name"
              />
              <t-input
                v-model="row.value"
                type="password"
                autocomplete="off"
                :placeholder="t('envVarSettings.valuePlaceholder')"
                class="env-new-row__value"
              />
              <t-button
                theme="primary"
                size="small"
                :loading="busyKey === sandboxKey(group.sandbox_config_id, row.name)"
                @click="saveNewRow(group, index)"
              >
                {{ t('envVarSettings.save') }}
              </t-button>
              <t-button
                variant="text"
                shape="square"
                size="small"
                :aria-label="t('common.delete')"
                @click="removeRow(group.sandbox_config_id, index)"
              >
                <t-icon name="close" />
              </t-button>
            </div>
            <p class="env-block__rule">{{ t('envVarSettings.nameRule') }}</p>
          </div>
        </div>

        <!-- Skill credentials: only what a skill declared, injected when a tool
             names that skill. -->
        <div v-if="group.skills?.length" class="env-block">
          <h4 class="env-block__title">{{ t('envVarSettings.skillTitle') }}</h4>
          <p class="env-block__hint">{{ t('envVarSettings.skillHint') }}</p>

          <div v-for="skill in group.skills" :key="skill.skill_id" class="env-skill">
            <h5 class="env-skill__title">{{ skill.skill_name || skill.skill_id }}</h5>
            <ul class="env-rows">
              <li
                v-for="entry in skill.vars"
                :key="entry.name"
                class="env-row env-row--declared"
                :class="{ 'env-row--blocking': statusOf(entry).blocking }"
              >
                <div class="env-row__head">
                  <code class="env-row__name">{{ entry.name }}</code>
                  <span v-if="entry.required" class="env-tag env-tag--required">
                    {{ t('envVarSettings.requiredTag') }}
                  </span>
                  <span class="env-tag" :class="`env-tag--${statusOf(entry).key}`">
                    {{ t(STATUS_LABEL[statusOf(entry).key]) }}
                  </span>
                </div>

                <p v-if="entry.description" class="env-row__desc">{{ entry.description }}</p>

                <p v-if="statusOf(entry).blocking" class="env-row__blocking">
                  <t-icon name="error-circle-filled" />
                  <span>{{ t('envVarSettings.blocking') }}</span>
                </p>

                <p v-if="entry.updated_at" class="env-row__meta">
                  {{ t('envVarSettings.updatedAt', { time: formatTime(entry.updated_at) }) }}
                </p>

                <div class="env-row__editor">
                  <t-input
                    v-model="drafts[skillKey(skill.skill_id, entry.name)]"
                    type="password"
                    autocomplete="off"
                    :aria-label="entry.name"
                    :placeholder="t('envVarSettings.valuePlaceholder')"
                  />
                  <t-button
                    theme="primary"
                    size="small"
                    :loading="busyKey === skillKey(skill.skill_id, entry.name)"
                    @click="saveSkill(skill, entry)"
                  >
                    {{ t('envVarSettings.save') }}
                  </t-button>
                  <t-popconfirm
                    v-if="statusOf(entry).key === 'user'"
                    theme="warning"
                    :content="t('envVarSettings.clearConfirm', { name: entry.name })"
                    :confirm-btn="{ content: t('envVarSettings.clear'), theme: 'danger' }"
                    :cancel-btn="{ content: t('common.cancel') }"
                    @confirm="deleteSkill(skill.skill_id, entry.name)"
                  >
                    <t-button
                      theme="danger"
                      variant="text"
                      size="small"
                      :loading="busyKey === skillKey(skill.skill_id, entry.name)"
                    >
                      {{ t('envVarSettings.clear') }}
                    </t-button>
                  </t-popconfirm>
                </div>
              </li>
            </ul>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
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
import {
  MAX_ENV_VALUE_BYTES,
  MAX_USER_ENV_VARS_PER_SCOPE,
  canAddEnvVar,
  configLabel,
  isValidEnvName,
  isValidEnvValueLength,
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

// Drafts are keyed by scope and name rather than held on the row, so a reload
// cannot silently discard what is half-typed elsewhere on the page.
const drafts = reactive<Record<string, string>>({})
const newRows = reactive<Record<string, Array<{ name: string; value: string }>>>({})
const busyKey = ref('')

function skillKey(skillId: string, name: string): string {
  return `skill:${skillId}:${name}`
}

function sandboxKey(configId: string, name: string): string {
  return `sandbox:${configId}:${name}`
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

function addRow(configId: string) {
  if (!newRows[configId]) newRows[configId] = []
  newRows[configId].push({ name: '', value: '' })
}

function removeRow(configId: string, index: number) {
  newRows[configId]?.splice(index, 1)
}

async function saveNewRow(group: ConfigEnvGroup, index: number) {
  const row = newRows[group.sandbox_config_id]?.[index]
  if (!row) return
  const name = row.name.trim()
  if (!isValidEnvName(name)) {
    MessagePlugin.error(t('envVarSettings.nameInvalid'))
    return
  }
  if (group.vars?.some((v) => v.name === name)) {
    MessagePlugin.error(t('envVarSettings.nameDuplicate'))
    return
  }
  if (rejectBadValue(row.value)) return
  if (!canAddEnvVar(group.vars || [], name)) {
    MessagePlugin.error(t('envVarSettings.tooManyValues', { max: MAX_USER_ENV_VARS_PER_SCOPE }))
    return
  }
  const key = sandboxKey(group.sandbox_config_id, name)
  busyKey.value = key
  try {
    await setMySandboxEnv(group.sandbox_config_id, name, row.value)
    removeRow(group.sandbox_config_id, index)
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
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.6;
  }
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
  border-radius: 8px;
  padding: 24px;
  text-align: center;
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

.env-groups {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.env-group {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  padding: 16px;
  background: var(--td-bg-color-container);
}

.env-group__title {
  font-size: 15px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 12px 0;
}

.env-block + .env-block {
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid var(--td-component-stroke);
}

.env-block__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.env-block__title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0;
}

.env-block__hint {
  margin: 2px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.env-block__empty,
.env-block__rule {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.env-rows {
  margin: 10px 0 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.env-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.env-row--declared {
  display: block;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  padding: 12px;
}

// A required variable nobody has filled in stops the skill from running, so it
// reads as an error rather than as one more grey "not set" row.
.env-row--blocking {
  border-color: var(--td-error-color);
  background: var(--td-error-color-light);
}

.env-row__head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.env-row__name {
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
  font-size: 13px;
  color: var(--td-text-color-primary);
  overflow-wrap: anywhere;
  flex: 0 0 30%;
}

.env-row--declared .env-row__name {
  flex: initial;
}

.env-row__value {
  flex: 1;
  min-width: 0;
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

.env-row__desc,
.env-row__meta {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
}

.env-row__meta {
  color: var(--td-text-color-placeholder);
  flex-basis: 100%;
}

.env-row__blocking {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-error-color);
}

.env-row__editor {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
}

.env-row__editor :deep(.t-input__wrap) {
  flex: 1;
  min-width: 0;
}

.env-skill {
  margin-top: 12px;
}

.env-skill__title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0;
}

.env-new-rows {
  margin-top: 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.env-new-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.env-new-row__name {
  flex: 0 0 30%;
}

.env-new-row__value {
  flex: 1;
  min-width: 0;
}
</style>
