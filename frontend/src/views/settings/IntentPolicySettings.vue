<template>
  <div class="intent-policy-settings">
    <div class="section-header">
      <h2>{{ $t('settings.intentPolicy.title') }}</h2>
      <p class="section-description">{{ $t('settings.intentPolicy.description') }}</p>
    </div>

    <div class="toolbar">
      <button v-if="authStore.hasRole('admin')" type="button" class="toolbar__create" @click="openCreate">
        <t-icon name="add" size="14px" />
        {{ $t('settings.intentPolicy.actions.create') }}
      </button>
    </div>

    <div v-if="loading" class="loading-container">
      <t-loading :text="$t('common.loading')" />
    </div>

    <div v-else-if="lineages.length === 0" class="empty-state">
      <t-empty :description="$t('settings.intentPolicy.empty')" />
    </div>

    <div v-else class="lineage-list">
      <article v-for="lineage in lineages" :key="lineage.key" class="lineage-card">
        <div class="lineage-card__head">
          <div class="lineage-card__scope">
            <t-tag :theme="scopeTheme(lineage.current.scope_type)" variant="light" size="small">
              {{ $t(`settings.intentPolicy.scopes.${lineage.current.scope_type}`) }}
            </t-tag>
            <code v-if="lineage.current.scope_ref" class="lineage-card__ref">{{ lineage.current.scope_ref }}</code>
            <span v-else class="lineage-card__ref lineage-card__ref--all">
              {{ $t('settings.intentPolicy.wholeTenant') }}
            </span>
          </div>
          <div class="lineage-card__badges">
            <t-tag :theme="lineage.current.mode === 'enforce' ? 'warning' : 'primary'" variant="light" size="small">
              {{ $t(`settings.intentPolicy.modes.${lineage.current.mode}`) }}
            </t-tag>
            <t-tag v-if="lineage.current.risk_tier === 'high'" theme="danger" variant="light" size="small">
              {{ $t('settings.intentPolicy.riskHigh') }}
            </t-tag>
            <t-tag theme="default" variant="outline" size="small">
              v{{ lineage.current.version }}
            </t-tag>
            <t-switch
              :model-value="lineage.current.enabled"
              :disabled="!authStore.hasRole('admin') || togglingKey === lineage.key"
              size="small"
              @change="toggleLineage(lineage)"
            />
          </div>
        </div>

        <p class="lineage-card__constraint" :title="lineage.current.constraint_text">
          {{ lineage.current.constraint_text }}
        </p>
        <p v-if="lineage.current.rule_expr" class="lineage-card__rule">
          <code>rule_expr: {{ lineage.current.rule_expr }}</code>
          <code v-if="lineage.current.arg_path" class="lineage-card__argpath">arg_path: {{ lineage.current.arg_path }}</code>
        </p>

        <div class="lineage-card__footer">
          <span class="lineage-card__updated">{{ formatTime(lineage.current.updated_at) }}</span>
          <div class="lineage-card__actions">
            <button type="button" class="lineage-card__link" @click="toggleHistory(lineage.key)">
              <t-icon :name="expandedHistory[lineage.key] ? 'chevron-up' : 'chevron-down'" size="14px" />
              {{ $t('settings.intentPolicy.actions.history', { count: lineage.versions.length }) }}
            </button>
            <button
              v-if="authStore.hasRole('admin')"
              type="button"
              class="lineage-card__link"
              @click="openEdit(lineage.current)"
            >
              <t-icon name="edit" size="14px" />
              {{ $t('common.edit') }}
            </button>
          </div>
        </div>

        <div v-if="expandedHistory[lineage.key]" class="version-history">
          <div
            v-for="v in lineage.versions"
            :key="v.id"
            class="version-history__row"
            :class="{ 'version-history__row--current': v.id === lineage.current.id }"
          >
            <span class="version-history__ver">v{{ v.version }}</span>
            <t-tag :theme="v.mode === 'enforce' ? 'warning' : 'primary'" variant="light" size="small">
              {{ $t(`settings.intentPolicy.modes.${v.mode}`) }}
            </t-tag>
            <span class="version-history__text" :title="v.constraint_text">{{ v.constraint_text }}</span>
            <span class="version-history__time">{{ formatTime(v.updated_at) }}</span>
            <t-tag v-if="v.id === lineage.current.id" theme="success" variant="light" size="small">
              {{ $t('settings.intentPolicy.currentBadge') }}
            </t-tag>
            <t-tag v-else-if="!v.enabled" theme="default" variant="outline" size="small">
              {{ $t('settings.intentPolicy.supersededBadge') }}
            </t-tag>
          </div>
        </div>
      </article>
    </div>

    <t-dialog
      v-model:visible="showEditor"
      :header="editorTitle"
      :confirm-btn="$t('common.save')"
      :cancel-btn="$t('common.cancel')"
      :on-confirm="submitEditor"
      width="560px"
    >
      <t-form :data="form" label-align="top" @submit="submitEditor">
        <t-form-item :label="$t('settings.intentPolicy.form.scopeType')" required>
          <t-select
            v-model="form.scope_type"
            :disabled="!!editTarget"
            :options="scopeTypeOptions"
          />
        </t-form-item>
        <t-form-item
          v-if="form.scope_type !== 'tenant'"
          :label="$t('settings.intentPolicy.form.scopeRef')"
          required
        >
          <t-input
            v-model="form.scope_ref"
            :disabled="!!editTarget"
            :placeholder="$t(`settings.intentPolicy.scopeRefHints.${form.scope_type}`)"
          />
        </t-form-item>
        <t-form-item :label="$t('settings.intentPolicy.form.constraintText')" required>
          <t-textarea
            v-model="form.constraint_text"
            :placeholder="$t('settings.intentPolicy.form.constraintPlaceholder')"
            :autosize="{ minRows: 2, maxRows: 6 }"
          />
        </t-form-item>
        <t-form-item :label="$t('settings.intentPolicy.form.ruleExpr')">
          <t-input
            v-model="form.rule_expr"
            :placeholder="$t('settings.intentPolicy.form.ruleExprPlaceholder')"
          />
        </t-form-item>
        <t-form-item :label="$t('settings.intentPolicy.form.argPath')">
          <t-input v-model="form.arg_path" placeholder="$.amount" />
        </t-form-item>
        <t-form-item :label="$t('settings.intentPolicy.form.riskTier')">
          <t-radio-group v-model="form.risk_tier" :options="riskTierOptions" />
        </t-form-item>
        <t-form-item :label="$t('settings.intentPolicy.form.mode')">
          <t-radio-group v-model="form.mode" :options="modeOptions" />
        </t-form-item>
      </t-form>
    </t-dialog>

    <t-dialog
      v-model:visible="showEnforceConfirm"
      :header="$t('settings.intentPolicy.enforceConfirm.title')"
      :confirm-btn="$t('settings.intentPolicy.enforceConfirm.confirm')"
      :cancel-btn="$t('common.cancel')"
      theme="warning"
      :on-confirm="confirmEnforce"
    >
      <p>{{ $t('settings.intentPolicy.enforceConfirm.body') }}</p>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/stores/auth'
import {
  createIntentPolicy,
  listIntentPolicies,
  policyLineageKey,
  setIntentPolicyEnabled,
  updateIntentPolicy,
  type IntentPolicy,
  type IntentPolicyPayload,
  type PolicyMode,
  type PolicyRiskTier,
  type PolicyScopeType,
} from '@/api/intent-policy'

const { t } = useI18n()
const authStore = useAuthStore()

interface Lineage {
  key: string
  current: IntentPolicy
  versions: IntentPolicy[] // version 倒序
}

const loading = ref(true)
const policies = ref<IntentPolicy[]>([])
const togglingKey = ref<string | null>(null)
const expandedHistory = reactive<Record<string, boolean>>({})

const lineages = computed<Lineage[]>(() => {
  const groups = new Map<string, IntentPolicy[]>()
  for (const p of policies.value) {
    const key = policyLineageKey(p)
    const list = groups.get(key) ?? []
    list.push(p)
    groups.set(key, list)
  }
  const out: Lineage[] = []
  for (const [key, versions] of groups) {
    versions.sort((a, b) => b.version - a.version)
    out.push({ key, current: versions[0], versions })
  }
  // 谱系间按 scope 再按 ref 排序，列表稳定。
  out.sort((a, b) =>
    a.key.localeCompare(b.key))
  return out
})

const scopeTheme = (scope: PolicyScopeType) =>
  ({ tool: 'primary', service: 'success', agent: 'warning', workspace: 'default', tenant: 'danger' })[scope]

const scopeTypeOptions = computed(() =>
  (['tool', 'service', 'agent', 'workspace', 'tenant'] as PolicyScopeType[]).map((value) => ({
    value,
    label: t(`settings.intentPolicy.scopes.${value}`),
  })),
)

const modeOptions = computed(() =>
  (['observe', 'enforce'] as PolicyMode[]).map((value) => ({
    value,
    label: t(`settings.intentPolicy.modes.${value}`),
  })),
)

const riskTierOptions = computed(() =>
  (['low', 'high'] as PolicyRiskTier[]).map((value) => ({
    value,
    label: value === 'high' ? t('settings.intentPolicy.riskHigh') : t('settings.intentPolicy.riskLow'),
  })),
)

const formatTime = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

async function load() {
  loading.value = true
  try {
    policies.value = await listIntentPolicies()
  } catch {
    MessagePlugin.error(t('settings.intentPolicy.toasts.loadFailed'))
  } finally {
    loading.value = false
  }
}

function toggleHistory(key: string) {
  expandedHistory[key] = !expandedHistory[key]
}

async function toggleLineage(lineage: Lineage) {
  togglingKey.value = lineage.key
  try {
    await setIntentPolicyEnabled(lineage.current.id, !lineage.current.enabled)
    MessagePlugin.success(
      t(lineage.current.enabled ? 'settings.intentPolicy.toasts.disabled' : 'settings.intentPolicy.toasts.enabled'),
    )
    await load()
  } catch {
    MessagePlugin.error(t('settings.intentPolicy.toasts.toggleFailed'))
  } finally {
    togglingKey.value = null
  }
}

// ---- 新建 / 编辑 ----

const showEditor = ref(false)
const showEnforceConfirm = ref(false)
const editTarget = ref<IntentPolicy | null>(null)
const saving = ref(false)

const emptyForm = (): IntentPolicyPayload => ({
  scope_type: 'tenant',
  scope_ref: '',
  arg_path: '',
  constraint_text: '',
  rule_expr: '',
  risk_tier: 'low',
  mode: 'observe',
})

const form = reactive<IntentPolicyPayload>(emptyForm())

const editorTitle = computed(() =>
  editTarget.value
    ? t('settings.intentPolicy.editor.editTitle', { version: editTarget.value.version + 1 })
    : t('settings.intentPolicy.editor.createTitle'),
)

function openCreate() {
  editTarget.value = null
  Object.assign(form, emptyForm())
  showEditor.value = true
}

function openEdit(policy: IntentPolicy) {
  editTarget.value = policy
  Object.assign(form, {
    scope_type: policy.scope_type,
    scope_ref: policy.scope_ref,
    arg_path: policy.arg_path ?? '',
    constraint_text: policy.constraint_text,
    rule_expr: policy.rule_expr ?? '',
    risk_tier: policy.risk_tier,
    mode: policy.mode,
  })
  showEditor.value = true
}

// 表单提交：enforce 一律二次确认（验收硬要求）；确认后才真正保存。
function submitEditor() {
  if (saving.value) return
  if (!form.constraint_text.trim()) {
    MessagePlugin.warning(t('settings.intentPolicy.toasts.constraintRequired'))
    return
  }
  if (form.scope_type !== 'tenant' && !form.scope_ref.trim()) {
    MessagePlugin.warning(t('settings.intentPolicy.toasts.scopeRefRequired'))
    return
  }
  if (form.mode === 'enforce') {
    showEnforceConfirm.value = true
    return
  }
  void doSave()
}

async function confirmEnforce() {
  showEnforceConfirm.value = false
  await doSave()
}

async function doSave() {
  saving.value = true
  try {
    if (editTarget.value) {
      await updateIntentPolicy(editTarget.value.id, { ...form })
      MessagePlugin.success(t('settings.intentPolicy.toasts.updated'))
    } else {
      await createIntentPolicy({ ...form })
      MessagePlugin.success(t('settings.intentPolicy.toasts.created'))
    }
    showEditor.value = false
    await load()
  } catch (e: any) {
    // 409 谱系冲突（重复创建同 scope）后端有明确 message，透传给操作者。
    MessagePlugin.error(e?.message || t('settings.intentPolicy.toasts.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped lang="less">
.intent-policy-settings {
  padding: 4px 0 24px;
}

.section-header {
  margin-bottom: 16px;

  h2 {
    margin: 0 0 6px;
    font-size: var(--app-text-2xl);
    font-weight: 600;
  }

  .section-description {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);
  }
}

.toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 12px;

  &__create {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 6px 14px;
    border: none;
    border-radius: var(--app-radius-sm);
    background: var(--td-brand-color);
    color: #fff;
    font-size: var(--app-text-md);
    cursor: pointer;

    &:hover {
      filter: brightness(1.08);
    }
  }
}

.loading-container,
.empty-state {
  padding: 48px 0;
  text-align: center;
}

.lineage-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.lineage-card {
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-md);
  padding: 14px 16px;
  background: var(--td-bg-color-container);

  &__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }

  &__scope {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }

  &__ref {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
    border-radius: var(--app-radius-xs);
    padding: 2px 6px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 360px;

    &--all {
      background: transparent;
      padding: 0;
    }
  }

  &__badges {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  &__constraint {
    margin: 10px 0 4px;
    font-size: var(--app-text-base);
    line-height: 1.5;
  }

  &__rule {
    margin: 0 0 6px;
    display: flex;
    gap: 8px;
    flex-wrap: wrap;

    code {
      font-size: var(--app-text-sm);
      color: var(--td-text-color-secondary);
      background: var(--td-bg-color-secondarycontainer);
      border-radius: var(--app-radius-xs);
      padding: 2px 6px;
    }
  }

  &__footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 8px;
  }

  &__updated {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
  }

  &__actions {
    display: flex;
    gap: 12px;
  }

  &__link {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    border: none;
    background: none;
    color: var(--td-brand-color);
    font-size: var(--app-text-md);
    cursor: pointer;
    padding: 0;
  }
}

.version-history {
  margin-top: 10px;
  border-top: 1px dashed var(--td-component-border);
  padding-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;

  &__row {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: var(--app-text-sm);
    padding: 4px 6px;
    border-radius: var(--app-radius-xs);

    &--current {
      background: var(--td-bg-color-secondarycontainer);
    }
  }

  &__ver {
    font-weight: 600;
    min-width: 32px;
  }

  &__text {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--td-text-color-secondary);
  }

  &__time {
    color: var(--td-text-color-placeholder);
  }
}
</style>
