<template>
  <!--
    SystemSettings — platform-wide tunables (system_settings table) for
    SystemAdmin. Gated server-side by RequireSystemAdmin middleware;
    the route also has meta.requiresSystemAdmin so non-admins never
    reach this component (see frontend/src/router/index.ts).

    UI principle: each row is independently editable + savable. We don't
    show a global "Save all" button because backend Update is per-key
    (the audit log records each change individually) and the
    "save-and-see-effect" loop is friendlier per-field. Validation is
    client-side first (matching value_type), server-side strict.
  -->
  <div class="system-settings">
    <div class="page-header">
      <div>
        <h1 class="page-title">全局设置</h1>
        <p class="page-desc">
          平台级运行时配置。修改保存后立即生效（不需要重启服务）。
          所有变更会写入审计日志。
        </p>
      </div>
      <t-button variant="text" @click="loadSettings" :loading="loading">
        <template #icon><t-icon name="refresh" /></template>
        刷新
      </t-button>
    </div>

    <div v-if="loading && groupedSettings.length === 0" class="loading-state">
      <t-loading text="加载中..." />
    </div>

    <div v-else-if="groupedSettings.length === 0" class="empty-state">
      <t-icon name="info-circle" size="32px" />
      <div>暂无可配置的系统设置</div>
    </div>

    <t-card
      v-for="group in groupedSettings"
      :key="group.category"
      class="settings-group"
      :bordered="false"
      :header-bordered="true"
    >
      <template #title>
        <div class="group-title">
          <span class="group-title-text">{{ categoryLabel(group.category) }}</span>
          <span class="group-title-count">{{ group.items.length }}</span>
        </div>
      </template>

      <div
        v-for="item in group.items"
        :key="item.key"
        class="setting-row"
      >
        <div class="setting-info">
          <div class="setting-key">
            <span class="setting-key-text">{{ item.key }}</span>
            <t-tag
              v-if="item.requires_restart"
              theme="warning"
              variant="light"
              size="small"
            >需重启</t-tag>
            <t-tag
              v-if="item.is_secret"
              theme="primary"
              variant="light"
              size="small"
            >敏感</t-tag>
          </div>
          <div v-if="item.description" class="setting-desc">{{ item.description }}</div>
          <div class="setting-meta">
            <span>类型: {{ item.value_type }}</span>
            <span v-if="item.last_modified_by">· 最后修改: {{ item.last_modified_by.slice(0, 8) }}</span>
            <span v-if="item.updated_at">· {{ formatDate(item.updated_at) }}</span>
          </div>
        </div>

        <div class="setting-control">
          <!--
            Per-type input. value_type drives which control we render:
              int          → InputNumber
              bool         → Switch
              string_list  → TagInput (each entry as a tag)
              string       → InputNumber/Input/Select depending on enum
            When `enum` is non-empty (regardless of declared type, but
            only meaningful for string), we override to a Select.
          -->
          <t-select
            v-if="hasEnum(item)"
            v-model="editValues[item.key]"
            :options="enumOptions(item)"
            :disabled="savingKey === item.key"
            class="setting-input"
          />
          <t-input-number
            v-else-if="item.value_type === 'int'"
            v-model="editValues[item.key]"
            :placeholder="String(item.value)"
            :disabled="savingKey === item.key"
            theme="normal"
            :step="1"
            :min="0"
            class="setting-input"
          />
          <t-switch
            v-else-if="item.value_type === 'bool'"
            v-model="editValues[item.key]"
            :disabled="savingKey === item.key"
          />
          <t-tag-input
            v-else-if="item.value_type === 'string_list'"
            v-model="editValues[item.key]"
            :placeholder="emptyListPlaceholder"
            :disabled="savingKey === item.key"
            class="setting-input setting-input--wide"
            clearable
          />
          <t-input
            v-else
            v-model="editValues[item.key]"
            :placeholder="String(item.value)"
            :disabled="savingKey === item.key"
            class="setting-input"
            clearable
          />

          <t-button
            theme="primary"
            size="small"
            :loading="savingKey === item.key"
            :disabled="!isDirty(item)"
            @click="saveSetting(item)"
          >
            保存
          </t-button>
        </div>
      </div>
    </t-card>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import {
  listSystemSettings,
  updateSystemSetting,
  type SystemSettingItem,
} from '@/api/system'

// Friendly Chinese labels per category. Falls back to the raw category
// string when a new one shows up that we haven't translated yet, which
// is the right behaviour: a missing translation should be visible, not
// silently empty.
const CATEGORY_LABELS: Record<string, string> = {
  limits: '上限',
  agent: 'Agent',
  auth: '认证',
  security: '安全',
  storage: '存储',
  general: '通用',
}
function categoryLabel(c: string): string {
  return CATEGORY_LABELS[c] || c
}

// Keys that change platform-wide trust boundaries. Saving these triggers
// an extra "are you sure?" dialog so a careless click can't, say, flip
// auth.registration_mode=self_serve and let the public spam the system.
// New high-impact keys can be added here when they ship.
const HIGH_RISK_KEYS = new Set<string>([
  'ssrf.whitelist',
  'auth.registration_mode',
])

// Friendly labels for enum options (drives the t-select dropdown text).
// We keep them inline rather than fetching from i18n because (a) the
// list is small, (b) the options are tied to backend constants and
// shouldn't drift, (c) translators don't typically need to see them.
const ENUM_LABELS: Record<string, Record<string, string>> = {
  'auth.registration_mode': {
    self_serve: '自助注册（任何人可注册）',
    invite_only: '仅邀请（关闭公网注册）',
  },
}

const emptyListPlaceholder = '回车添加条目，例：example.com / *.foo.com / 10.0.0.0/8'

const settings = ref<SystemSettingItem[]>([])
const loading = ref(false)
const savingKey = ref<string | null>(null)

// Reactive map of in-progress edits, keyed by setting key. We don't
// mutate the canonical `settings` array directly so a failed save
// leaves the original value visible until the user retries or refreshes.
// Initialised lazily in loadSettings; setting.value is the JSON-decoded
// form (number / boolean / string / string[]).
const editValues = reactive<Record<string, unknown>>({})

const groupedSettings = computed(() => {
  const buckets: Record<string, SystemSettingItem[]> = {}
  for (const item of settings.value) {
    const c = item.category || 'general'
    if (!buckets[c]) buckets[c] = []
    buckets[c].push(item)
  }
  return Object.keys(buckets)
    .sort()
    .map((category) => ({ category, items: buckets[category] }))
})

function hasEnum(item: SystemSettingItem): boolean {
  return Array.isArray(item.enum) && item.enum.length > 0
}

function enumOptions(item: SystemSettingItem): { label: string; value: string }[] {
  const opts = item.enum ?? []
  const labelMap = ENUM_LABELS[item.key] ?? {}
  return opts.map((v) => ({ label: labelMap[v] ?? v, value: v }))
}

// isDirty drives the Save button's disabled state. Arrays need
// element-wise comparison; primitives use strict equality. We don't
// import a lodash isEqual to keep the bundle small — the comparison
// surface is intentionally narrow (4 value_types).
function isDirty(item: SystemSettingItem): boolean {
  const cur = editValues[item.key]
  const orig = item.value
  if (Array.isArray(cur) && Array.isArray(orig)) {
    if (cur.length !== orig.length) return true
    for (let i = 0; i < cur.length; i++) {
      if (cur[i] !== orig[i]) return true
    }
    return false
  }
  return cur !== orig
}

function formatDate(isoString: string): string {
  try {
    const d = new Date(isoString)
    return d.toLocaleString('zh-CN', { hour12: false })
  } catch {
    return isoString
  }
}

async function loadSettings() {
  loading.value = true
  try {
    const list = await listSystemSettings()
    settings.value = list
    // Reset edit values to the canonical state on every load — no
    // partial drafts survive a refresh, which avoids the "I came back
    // and my unsaved edits look saved" trap.
    for (const item of list) {
      // Defensive copy for arrays so the t-tag-input doesn't mutate
      // the canonical settings entry through the v-model binding.
      editValues[item.key] = Array.isArray(item.value)
        ? [...(item.value as unknown[])]
        : item.value
    }
  } catch (err: any) {
    const msg = err?.message || '加载系统设置失败'
    MessagePlugin.error(msg)
  } finally {
    loading.value = false
  }
}

async function saveSetting(item: SystemSettingItem) {
  if (!isDirty(item)) return // double-click guard

  // High-risk keys get an extra confirmation. The dialog text is
  // intentionally specific: a generic "are you sure?" trains people to
  // click through. Mention the actual setting + new value so the user
  // re-reads what they're about to do.
  if (HIGH_RISK_KEYS.has(item.key)) {
    const confirmed = await new Promise<boolean>((resolve) => {
      const dlg = DialogPlugin.confirm({
        header: '高危操作确认',
        body: highRiskConfirmBody(item),
        confirmBtn: { content: '确认保存', theme: 'danger' },
        cancelBtn: '取消',
        onConfirm: () => {
          resolve(true)
          dlg.destroy()
        },
        onClose: () => {
          resolve(false)
          dlg.destroy()
        },
      })
    })
    if (!confirmed) return
  }

  await persistSetting(item)
}

function highRiskConfirmBody(item: SystemSettingItem): string {
  const newValue = editValues[item.key]
  const renderedValue = Array.isArray(newValue)
    ? newValue.length === 0
      ? '（空）'
      : newValue.join(', ')
    : String(newValue)
  switch (item.key) {
    case 'auth.registration_mode':
      return `即将把「自助注册模式」改为：${renderedValue}\n\n` +
        `如果切到 self_serve，公网任何人都可以注册账号 — 务必确认是预期行为。`
    case 'ssrf.whitelist':
      return `即将把 SSRF 白名单改为：${renderedValue}\n\n` +
        `白名单中的主机/IP/网段会绕过 SSRF 防护。错误配置可能让 Agent 访问内网服务。`
    default:
      return `即将把「${item.key}」改为：${renderedValue}`
  }
}

async function persistSetting(item: SystemSettingItem) {
  const newValue = editValues[item.key]
  savingKey.value = item.key
  try {
    const updated = await updateSystemSetting(item.key, newValue)
    // Replace the row in-place so the table stays at scroll position
    // and other rows' edit state isn't disturbed.
    const idx = settings.value.findIndex((s) => s.key === item.key)
    if (idx >= 0) {
      settings.value[idx] = updated
    }
    editValues[item.key] = Array.isArray(updated.value)
      ? [...(updated.value as unknown[])]
      : updated.value
    MessagePlugin.success(`已保存 ${item.key}`)
  } catch (err: any) {
    const msg = err?.message || '保存失败'
    MessagePlugin.error(msg)
  } finally {
    savingKey.value = null
  }
}

onMounted(() => {
  loadSettings()
})
</script>

<style scoped>
.system-settings {
  max-width: 980px;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 20px;
}

.page-title {
  margin: 0 0 6px;
  font-size: 22px;
  font-weight: 600;
  color: var(--td-text-color-primary, #000);
}

.page-desc {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--td-text-color-secondary, #666);
  max-width: 720px;
}

.loading-state,
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 60px 0;
  color: var(--td-text-color-placeholder, #999);
}

.settings-group {
  margin-bottom: 16px;
}

.group-title {
  display: flex;
  align-items: center;
  gap: 8px;
}

.group-title-text {
  font-size: 15px;
  font-weight: 600;
}

.group-title-count {
  font-size: 12px;
  color: var(--td-text-color-placeholder, #999);
  background: var(--td-bg-color-component, #f5f5f5);
  padding: 1px 8px;
  border-radius: 10px;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  padding: 14px 0;
  border-bottom: 1px solid var(--td-border-level-1-color, #eee);
}

.setting-row:last-child {
  border-bottom: none;
  padding-bottom: 0;
}

.setting-info {
  flex: 1;
  min-width: 0;
}

.setting-key {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.setting-key-text {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary, #000);
  font-family: var(--td-font-family-mono, monospace);
}

.setting-desc {
  font-size: 13px;
  line-height: 1.6;
  color: var(--td-text-color-secondary, #666);
  margin-bottom: 4px;
}

.setting-meta {
  font-size: 11px;
  color: var(--td-text-color-placeholder, #999);
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}

.setting-control {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
}

.setting-input {
  width: 220px;
}

.setting-input--wide {
  width: 420px;
}
</style>
