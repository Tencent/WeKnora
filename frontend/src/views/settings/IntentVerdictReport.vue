<template>
  <div class="verdict-report">
    <div class="section-header">
      <h2>{{ $t('settings.intentVerdict.title') }}</h2>
      <p class="section-description">{{ $t('settings.intentVerdict.description') }}</p>
    </div>

    <div class="toolbar">
      <t-select
        v-model="selectedPolicyId"
        class="toolbar__select"
        :placeholder="$t('settings.intentVerdict.pickPolicy')"
        :options="policyOptions"
        clearable
        @change="reload"
      />
    </div>

    <div v-if="loading" class="loading-container">
      <t-loading :text="$t('common.loading')" />
    </div>

    <template v-else>
      <div class="stats">
        <div v-for="card in statCards" :key="card.key" class="stat-card" :data-verdict="card.key">
          <span class="stat-card__num">{{ card.count }}</span>
          <span class="stat-card__label">{{ card.label }}</span>
        </div>
        <div class="stat-card stat-card--total">
          <span class="stat-card__num">{{ summary.total }}</span>
          <span class="stat-card__label">{{ $t('settings.intentVerdict.total') }}</span>
        </div>
      </div>

      <div v-if="verdicts.length === 0" class="empty-state">
        <t-empty :description="$t('settings.intentVerdict.empty')" />
      </div>

      <div v-else class="verdict-table">
        <div
          v-for="v in verdicts"
          :key="v.id"
          class="verdict-row"
          :data-verdict="v.verdict"
        >
          <button type="button" class="verdict-row__head" @click="toggle(v.id)">
            <t-tag :theme="verdictTheme(v.verdict)" variant="light" size="small">
              {{ verdictLabel(v.verdict) }}
            </t-tag>
            <t-tag theme="default" variant="outline" size="small">{{ v.layer }}</t-tag>
            <code v-if="v.policy_id" class="verdict-row__policy">v{{ v.policy_version ?? '-' }}</code>
            <span class="verdict-row__tool">{{ v.tool_name }}</span>
            <span class="verdict-row__time">{{ formatTime(v.created_at) }}</span>
            <t-icon :name="expanded[v.id] ? 'chevron-up' : 'chevron-down'" size="14px" />
          </button>
          <div v-if="expanded[v.id]" class="verdict-row__detail">
            <p class="verdict-row__reason">{{ v.reason || $t('settings.intentVerdict.noReason') }}</p>
            <p class="verdict-row__meta">
              session={{ v.session_id }} · tool_call={{ v.tool_call_id }} ·
              mode={{ v.mode_at_decision }} · latency={{ v.latency_ms }}ms
              <template v-if="v.judge_tokens > 0"> · judge_tokens={{ v.judge_tokens }}</template>
            </p>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { listIntentPolicies, policyLineageKey, type IntentPolicy } from '@/api/intent-policy'
import { intentVerdictSummary, listIntentVerdicts, type IntentVerdict, type VerdictSummary } from '@/api/intent-verdict'

const { t } = useI18n()

const policies = ref<IntentPolicy[]>([])
const selectedPolicyId = ref<string>('')
const verdicts = ref<IntentVerdict[]>([])
const summary = ref<VerdictSummary>({ counts: {}, total: 0 })
const loading = ref(true)
const expanded = reactive<Record<string, boolean>>({})

// 谱系去重：同 (scope_type, scope_ref) 只留最新版本，与策略页同口径。
const policyOptions = computed(() => {
  const seen = new Map<string, IntentPolicy>()
  for (const p of policies.value) {
    const key = policyLineageKey(p)
    const cur = seen.get(key)
    if (!cur || cur.version < p.version) seen.set(key, p)
  }
  return [...seen.values()]
    .sort((a, b) => a.scope_type.localeCompare(b.scope_type))
    .map((p) => ({
      value: p.id,
      label: `v${p.version} · ${p.scope_type}${p.scope_ref ? `:${p.scope_ref}` : ''} · ${p.constraint_text.slice(0, 24)}`,
    }))
})

const statCards = computed(() =>
  (['allow', 'deny', 'require_approval', 'uncertain'] as const).map((key) => ({
    key,
    count: summary.value.counts[key] ?? 0,
    label: verdictLabel(key),
  })),
)

function verdictLabel(v: string): string {
  return t(`settings.intentVerdict.values.${v}`, v)
}

function verdictTheme(v: string): string {
  return ({ allow: 'success', deny: 'danger', require_approval: 'warning', uncertain: 'default' } as Record<string, string>)[v] ?? 'default'
}

function formatTime(iso: string) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

function toggle(id: string) {
  expanded[id] = !expanded[id]
}

async function reload() {
  loading.value = true
  try {
    const pid = selectedPolicyId.value
    ;[verdicts.value, summary.value] = await Promise.all([
      listIntentVerdicts(pid),
      intentVerdictSummary(pid),
    ])
  } catch {
    MessagePlugin.error(t('settings.intentVerdict.loadFailed'))
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  try {
    policies.value = await listIntentPolicies()
  } catch {
    MessagePlugin.error(t('settings.intentVerdict.loadFailed'))
  }
  // 默认选第一个谱系，报表直接有数。
  if (policyOptions.value.length > 0) {
    selectedPolicyId.value = policyOptions.value[0].value
  }
  await reload()
})
</script>

<style scoped lang="less">
.verdict-report {
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
  margin-bottom: 12px;

  &__select {
    width: 420px;
  }
}

.loading-container,
.empty-state {
  padding: 48px 0;
  text-align: center;
}

.stats {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 16px;
}

.stat-card {
  min-width: 120px;
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-md);
  padding: 12px 16px;
  background: var(--td-bg-color-container);
  display: flex;
  flex-direction: column;
  gap: 4px;

  &__num {
    font-size: var(--app-text-3xl);
    font-weight: 700;
  }

  &__label {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
  }

  &--total {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.verdict-table {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.verdict-row {
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);

  &__head {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 10px 14px;
    border: none;
    background: none;
    cursor: pointer;
    font-size: var(--app-text-base);
    text-align: left;
  }

  &__policy {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
  }

  &__tool {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__time {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
  }

  &__detail {
    border-top: 1px dashed var(--td-component-border);
    padding: 10px 14px;
  }

  &__reason {
    margin: 0 0 6px;
    font-size: var(--app-text-base);
    line-height: 1.6;
  }

  &__meta {
    margin: 0;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
    font-family: monospace;
  }
}
</style>
