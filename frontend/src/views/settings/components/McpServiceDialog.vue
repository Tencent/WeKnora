<template>
  <SettingDrawer
    :visible="dialogVisible"
    :title="mode === 'add' ? t('mcpServiceDialog.addTitle') : t('mcpServiceDialog.editTitle')"
    :class="`mcp-drawer mcp-drawer--${formData.transport_type}`"
    :confirm-loading="submitting"
    @update:visible="(v: boolean) => dialogVisible = v"
    @confirm="handleSubmit"
    @cancel="handleClose"
  >
    <!--
      Header icon — 与 McpSettings 列表 .service-card__badge 同款：
      transport_type 决定图标和容器配色。SSE 绿、HTTP-Streamable 蓝。
      非 scoped 块 .mcp-drawer--{transport} 注入背景与文字色，currentColor
      让 t-icon 跟着染色。
    -->
    <template #headerIcon>
      <t-icon :name="transportIcon" />
    </template>

    <!-- 副标题：transport 类型名 + 启用状态 mini chip -->
    <template #subtitle>
      <span>{{ transportLabel }}</span>
      <span
        class="subtitle-tag"
        :class="formData.enabled ? 'subtitle-tag--ok' : 'subtitle-tag--muted'"
      >
        {{ formData.enabled ? t('mcpSettings.enabled', '已启用') : t('mcpSettings.disabled', '已禁用') }}
      </span>
    </template>

    <!--
      测试连接按钮挪到 footer-left，与 ModelEditorDialog/Storage/Parser/
      WebSearch 抽屉同款。仅 edit 模式有效（需要服务 id 才能调 /test 端点）。
      create 模式下按钮 disabled 并提示"保存后可测试"。
    -->
    <template #footer-left>
      <t-button
        variant="outline"
        :loading="testing"
        :disabled="mode === 'add' || !props.service?.id"
        :title="mode === 'add' ? t('mcpServiceDialog.testAfterSaveHint', '保存后可测试连接') : ''"
        @click="handleTestConnection"
      >
        <template #icon>
          <t-icon
            v-if="!testing && lastTestOk === true"
            name="check-circle-filled"
            class="status-icon available"
          />
          <t-icon
            v-else-if="!testing && lastTestOk === false"
            name="close-circle-filled"
            class="status-icon unavailable"
          />
        </template>
        {{ testing ? t('webSearchSettings.testing', '测试中…') : t('mcpSettings.actions.test', '测试连接') }}
      </t-button>
    </template>

    <t-form ref="formRef" :data="formData" :rules="rules" label-align="top">
      <!-- Section 1 — 基本信息 -->
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('mcpServiceDialog.basicSection', '基本信息') }}</h4>

        <div class="form-item">
          <label class="form-label required">{{ t('mcpServiceDialog.name') }}</label>
          <t-input v-model="formData.name" :placeholder="t('mcpServiceDialog.namePlaceholder')" />
        </div>

        <div class="form-item">
          <label class="form-label">{{ t('mcpServiceDialog.description') }}</label>
          <t-textarea
            v-model="formData.description"
            :autosize="{ minRows: 2, maxRows: 5 }"
            :placeholder="t('mcpServiceDialog.descriptionPlaceholder')"
          />
        </div>

        <div class="form-item">
          <label class="form-label">{{ t('mcpServiceDialog.enableService') }}</label>
          <div class="vision-toggle">
            <t-switch v-model="formData.enabled" />
            <span class="form-desc form-desc--inline">
              {{ t('mcpServiceDialog.enableServiceDesc', '关闭后该服务不会被调用') }}
            </span>
          </div>
        </div>
      </section>

      <!-- Section 2 — 连接配置（transport + url） -->
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('mcpServiceDialog.connectionSection', '连接配置') }}</h4>

        <div class="form-item">
          <label class="form-label required">{{ t('mcpServiceDialog.transportType') }}</label>
          <!-- 紧凑 pill segmented，与 ModelEditorDialog 来源切换 / Storage MinIO 部署模式同款 -->
          <div class="source-options" role="radiogroup">
            <button
              type="button"
              class="source-option"
              :class="{ 'is-active': formData.transport_type === 'sse' }"
              @click="formData.transport_type = 'sse'"
            >
              <t-icon name="cast" class="source-option__icon" />
              <span class="source-option__label">SSE</span>
            </button>
            <button
              type="button"
              class="source-option"
              :class="{ 'is-active': formData.transport_type === 'http-streamable' }"
              @click="formData.transport_type = 'http-streamable'"
            >
              <t-icon name="link" class="source-option__icon" />
              <span class="source-option__label">HTTP Streamable</span>
            </button>
          </div>
        </div>

        <div class="form-item">
          <label class="form-label required">{{ t('mcpServiceDialog.serviceUrl') }}</label>
          <t-input v-model="formData.url" :placeholder="t('mcpServiceDialog.serviceUrlPlaceholder')" />
        </div>
      </section>

      <!-- Section 3 — 认证配置（API Key / Bearer Token） -->
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('mcpServiceDialog.authConfig') }}</h4>

        <!--
          Edit 模式下凭证由 CredentialResource 管理（独立的 /credentials
          子资源调用），不与本表单 submit 耦合；Create 模式下用 plain
          password input + lock prefix-icon。两个字段都是 optional —
          MCP 服务可能完全不需要鉴权（依赖 IP 白名单等）。
        -->
        <CredentialResource
          v-if="mode === 'edit' && props.service?.id"
          :api="credentialApi"
          :fields="credentialFields"
          :meta="credentialMeta"
        />
        <template v-else>
          <div class="form-item">
            <label class="form-label">{{ t('mcpServiceDialog.apiKey') }}</label>
            <t-input
              v-model="formData.auth_config.api_key"
              type="password"
              :placeholder="t('mcpServiceDialog.optional')"
            >
              <template #prefix-icon><t-icon name="lock-on" /></template>
            </t-input>
          </div>
          <div class="form-item">
            <label class="form-label">{{ t('mcpServiceDialog.bearerToken') }}</label>
            <t-input
              v-model="formData.auth_config.token"
              type="password"
              :placeholder="t('mcpServiceDialog.optional')"
            >
              <template #prefix-icon><t-icon name="lock-on" /></template>
            </t-input>
          </div>
        </template>
      </section>

      <!-- Section 4 — 高级配置（超时/重试），改用带后缀单位的轻量数字输入框，
           不再用 t-input-number 的加减器（步进按钮在这里没必要，用户更倾向直接键入）。 -->
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('mcpServiceDialog.advancedConfig') }}</h4>

        <div class="form-item">
          <label class="form-label">{{ t('mcpServiceDialog.timeoutSec') }}</label>
          <t-input
            v-model="advancedTimeoutText"
            type="number"
            :min="1"
            :max="300"
            placeholder="30"
            class="number-input"
            @blur="onAdvancedNumberBlur('timeout', 30, 1, 300)"
          >
            <template #suffix>
              <span class="number-input__unit">{{ t('mcpServiceDialog.unitSecond', '秒') }}</span>
            </template>
          </t-input>
        </div>
        <div class="form-item">
          <label class="form-label">{{ t('mcpServiceDialog.retryCount') }}</label>
          <t-input
            v-model="advancedRetryCountText"
            type="number"
            :min="0"
            :max="10"
            placeholder="3"
            class="number-input"
            @blur="onAdvancedNumberBlur('retry_count', 3, 0, 10)"
          >
            <template #suffix>
              <span class="number-input__unit">{{ t('mcpServiceDialog.unitTimes', '次') }}</span>
            </template>
          </t-input>
        </div>
        <div class="form-item">
          <label class="form-label">{{ t('mcpServiceDialog.retryDelaySec') }}</label>
          <t-input
            v-model="advancedRetryDelayText"
            type="number"
            :min="0"
            :max="60"
            placeholder="1"
            class="number-input"
            @blur="onAdvancedNumberBlur('retry_delay', 1, 0, 60)"
          >
            <template #suffix>
              <span class="number-input__unit">{{ t('mcpServiceDialog.unitSecond', '秒') }}</span>
            </template>
          </t-input>
        </div>
      </section>
    </t-form>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import type { FormInstanceFunctions, FormRule } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  createMCPService,
  updateMCPService,
  putMCPCredentials,
  deleteMCPCredentialField,
  testMCPService,
  type MCPService,
  type McpCredentialField,
  type MCPTestResult,
} from '@/api/mcp-service'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import CredentialResource, {
  type CredentialFieldDef,
  type CredentialResourceApi,
} from '@/components/credentials/CredentialResource.vue'

interface Props {
  visible: boolean
  service: MCPService | null
  mode: 'add' | 'edit'
}

interface Emits {
  (e: 'update:visible', value: boolean): void
  (e: 'success'): void
  // Fired after a /test call inside the drawer so the parent can reuse its
  // existing McpTestResult dialog. We deliberately don't render that dialog
  // here — the parent owns the test-result dialog state across multiple
  // entry points (drawer test button, list 行操作菜单 used to call it too).
  (e: 'test', payload: { service: MCPService; result: MCPTestResult }): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const formRef = ref<FormInstanceFunctions>()
const submitting = ref(false)
const { t } = useI18n()

const formData = ref({
  name: '',
  description: '',
  enabled: true,
  transport_type: 'sse' as 'sse' | 'http-streamable',
  url: '',
  auth_config: {
    // Only used in add-mode; in edit-mode the CredentialResource owns these.
    api_key: '',
    token: '',
  },
  advanced_config: {
    timeout: 30,
    retry_count: 3,
    retry_delay: 1,
  },
})

// Header icon name + transport label, mirrored from McpSettings list cards
// so the list-card → drawer hand-off stays visually continuous.
const transportIcon = computed(() => {
  return formData.value.transport_type === 'http-streamable' ? 'link' : 'cast'
})

const transportLabel = computed(() => {
  return formData.value.transport_type === 'http-streamable' ? 'HTTP Streamable' : 'SSE'
})

// Field metadata for the credential subresource. Keep label keys local to
// MCP so other resources don't accidentally inherit "API Key" / "Bearer
// Token" labels via the shared component.
const credentialFields = computed<CredentialFieldDef<McpCredentialField>[]>(() => [
  { key: 'api_key', label: t('mcpServiceDialog.apiKey') },
  { key: 'token', label: t('mcpServiceDialog.bearerToken') },
])

// Adapter that binds the generic CredentialResource component to the MCP
// credential endpoints. Recomputed if the user opens a different service.
const credentialApi = computed<CredentialResourceApi<McpCredentialField>>(() => {
  const id = props.service?.id ?? ''
  return {
    save: async (patch) => {
      const meta = await putMCPCredentials(id, patch)
      return meta.fields
    },
    remove: async (field) => {
      await deleteMCPCredentialField(id, field)
    },
  }
})

// Initial "configured?" metadata read from the main service response. The
// component reads this on mount; subsequent state changes after save/remove
// are tracked locally by the component itself (and re-derived from this
// whenever the parent reloads the service).
const credentialMeta = computed(() => props.service?.credentials ?? {
  api_key: { configured: false },
  token: { configured: false },
})

const rules: Record<string, FormRule[]> = {
  name: [{ required: true, message: t('mcpServiceDialog.rules.nameRequired') as string, type: 'error' }],
  transport_type: [{ required: true, message: t('mcpServiceDialog.rules.transportRequired') as string, type: 'error' }],
  url: [
    {
      validator: (val: string) => {
        if (!val || val.trim() === '') {
          return { result: false, message: t('mcpServiceDialog.rules.urlRequired') as string, type: 'error' }
        }
        try {
          new URL(val)
          return { result: true, message: '', type: 'success' }
        } catch {
          return { result: false, message: t('mcpServiceDialog.rules.urlInvalid') as string, type: 'error' }
        }
      },
    },
  ],
}

const dialogVisible = computed({
  get: () => props.visible,
  set: (value) => emit('update:visible', value),
})

// ---- Test connection state (in-drawer) ----
const testing = ref(false)
// Tri-state icon hint on the test button: null=neutral, true=just succeeded,
// false=just failed. Cleared when transport/url change so a stale ✓/✗
// doesn't sit next to a config the user is now editing.
const lastTestOk = ref<boolean | null>(null)

watch(
  () => [formData.value.transport_type, formData.value.url],
  () => {
    lastTestOk.value = null
  },
)

async function handleTestConnection() {
  if (!props.service?.id) return
  testing.value = true
  MessagePlugin.info({
    content: t('mcpSettings.toasts.testing', { name: props.service.name || '' }),
    duration: 0,
    closeBtn: false,
  })
  try {
    const result = await testMCPService(props.service.id)
    MessagePlugin.closeAll()
    const safe: MCPTestResult = result ?? {
      success: false,
      message: t('mcpSettings.toasts.noResponse') as string,
    }
    lastTestOk.value = safe.success === true
    emit('test', { service: props.service, result: safe })
  } catch (error: any) {
    MessagePlugin.closeAll()
    const errorMessage =
      error?.response?.data?.error?.message ||
      error?.message ||
      (t('mcpSettings.toasts.testFailed') as string)
    console.error('Failed to test MCP service:', error)
    lastTestOk.value = false
    emit('test', {
      service: props.service,
      result: { success: false, message: errorMessage },
    })
  } finally {
    testing.value = false
  }
}

// ---- Advanced numeric inputs (text-bound proxies) ----
// We bind text instead of v-model directly to advanced_config.<n> so the
// user can clear the field and see the placeholder while typing. On blur
// we coerce, clamp, and write back; bad values fall back to the default.
const advancedTimeoutText = computed<string>({
  get: () => String(formData.value.advanced_config.timeout ?? ''),
  set: (v) => { formData.value.advanced_config.timeout = parseSloppyInt(v) ?? 30 },
})
const advancedRetryCountText = computed<string>({
  get: () => String(formData.value.advanced_config.retry_count ?? ''),
  set: (v) => { formData.value.advanced_config.retry_count = parseSloppyInt(v) ?? 3 },
})
const advancedRetryDelayText = computed<string>({
  get: () => String(formData.value.advanced_config.retry_delay ?? ''),
  set: (v) => { formData.value.advanced_config.retry_delay = parseSloppyInt(v) ?? 1 },
})

// Permissive int parser — keeps '' / NaN inputs as null instead of 0 so the
// field can stay visually empty while the user is still typing. Negative
// numbers and non-int chars are rejected (returns null).
function parseSloppyInt(raw: string): number | null {
  if (raw == null) return null
  const s = String(raw).trim()
  if (!s) return null
  const n = Number(s)
  if (!Number.isFinite(n)) return null
  return Math.trunc(n)
}

function onAdvancedNumberBlur(
  field: 'timeout' | 'retry_count' | 'retry_delay',
  fallback: number,
  min: number,
  max: number,
) {
  const cur = formData.value.advanced_config[field]
  if (cur == null || !Number.isFinite(cur)) {
    formData.value.advanced_config[field] = fallback
    return
  }
  // Clamp to [min, max] on blur — gives the input "settled" feedback even
  // though native type=number doesn't enforce its own min/max attribute
  // for typed values (only for stepper buttons).
  formData.value.advanced_config[field] = Math.min(max, Math.max(min, cur))
}

const resetForm = () => {
  formData.value = {
    name: '',
    description: '',
    enabled: true,
    transport_type: 'sse',
    url: '',
    auth_config: { api_key: '', token: '' },
    advanced_config: { timeout: 30, retry_count: 3, retry_delay: 1 },
  }
  formRef.value?.clearValidate()
}

watch(
  () => props.service,
  (service) => {
    // 切到不同服务（或新增）时清空上次测试反馈，避免旧的 ✓/✗ 漂在新表单上
    lastTestOk.value = null
    if (service) {
      const transportType = service.transport_type === 'stdio' ? 'sse' : (service.transport_type || 'sse')
      formData.value = {
        name: service.name || '',
        description: service.description || '',
        enabled: service.enabled ?? true,
        transport_type: transportType as 'sse' | 'http-streamable',
        url: service.url || '',
        // Credentials are owned by CredentialResource in edit mode, but reset
        // the local state too so a switch to add-mode starts clean.
        auth_config: { api_key: '', token: '' },
        advanced_config: {
          timeout: service.advanced_config?.timeout || 30,
          retry_count: service.advanced_config?.retry_count || 3,
          retry_delay: service.advanced_config?.retry_delay || 1,
        },
      }
    } else {
      resetForm()
    }
  },
  { immediate: true },
)

const handleSubmit = async () => {
  const valid = await formRef.value?.validate()
  if (!valid) return

  submitting.value = true
  try {
    const data: Partial<MCPService> = {
      name: formData.value.name,
      description: formData.value.description,
      enabled: formData.value.enabled,
      transport_type: formData.value.transport_type,
      advanced_config: formData.value.advanced_config,
      url: formData.value.url || undefined,
    }

    if (props.mode === 'add') {
      // Initial credentials go along with the first POST. Subsequent edits
      // route through the /credentials subresource.
      const initialAuth: NonNullable<MCPService['auth_config']> = {}
      if (formData.value.auth_config.api_key) initialAuth.api_key = formData.value.auth_config.api_key
      if (formData.value.auth_config.token) initialAuth.token = formData.value.auth_config.token
      if (Object.keys(initialAuth).length > 0) data.auth_config = initialAuth
      await createMCPService(data)
      MessagePlugin.success(t('mcpServiceDialog.toasts.created'))
    } else {
      // Edit-mode: never send credential fields here. CredentialResource
      // already committed any changes through the dedicated endpoint.
      await updateMCPService(props.service!.id, data)
      MessagePlugin.success(t('mcpServiceDialog.toasts.updated'))
    }

    emit('success')
  } catch (error) {
    MessagePlugin.error(
      props.mode === 'add'
        ? (t('mcpServiceDialog.toasts.createFailed') as string)
        : (t('mcpServiceDialog.toasts.updateFailed') as string),
    )
    console.error('Failed to save MCP service:', error)
  } finally {
    submitting.value = false
  }
}

const handleClose = () => {
  dialogVisible.value = false
}
</script>

<style scoped lang="less">
// ---- 抽屉内容 — 与 ModelEditorDialog 同款约定 ----
.form-item {
  margin-bottom: 0;
}

.form-label {
  display: block;
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  line-height: 1.4;

  &.required::before {
    content: '*';
    color: var(--td-error-color);
    margin-right: 4px;
    font-weight: 500;
    line-height: 1;
  }
}

.form-desc {
  margin: 4px 0 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);

  &--inline {
    margin: 0;
  }
}

:deep(.t-input),
:deep(.t-select),
:deep(.t-textarea),
:deep(.t-input-number) {
  width: 100%;
  font-size: 13px;
}

// 隐藏 t-form 默认 form-item 容器 — 走自定义 .form-item / .form-label
:deep(.t-form) .t-form-item {
  display: none;
}

// ---- 紧凑 pill segmented（transport 切换） ----
.source-options {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px;
  background: var(--td-bg-color-component);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
}

.source-option {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 12px;
  height: 28px;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 6px;
  cursor: pointer;
  font-family: inherit;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1;
  transition: all 0.15s ease;

  &:hover:not(.is-active) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &.is-active {
    background: var(--td-bg-color-container);
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
    font-weight: 500;
    box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  }
}

.source-option__icon {
  font-size: 14px;
  flex-shrink: 0;
}

.source-option__label {
  white-space: nowrap;
}

.vision-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
}

// ---- 高级配置数字输入：替代 t-input-number 的步进按钮，更轻量 ----
// 用普通 t-input + suffix 单位 + type=number。原生 number 输入会
// 在 Chrome 上显示一对 spin button，scoped 里把它们隐藏掉以保持视觉
// 干净。最大/最小值通过 onBlur clamp，而不是依赖原生 step 限制。
.number-input {
  :deep(input::-webkit-outer-spin-button),
  :deep(input::-webkit-inner-spin-button) {
    -webkit-appearance: none;
    appearance: none;
    margin: 0;
  }

  // Firefox 把 type=number 渲染成 textfield 风格更好看
  :deep(input[type="number"]) {
    -moz-appearance: textfield;
    appearance: textfield;
  }
}

.number-input__unit {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  user-select: none;
}

// ---- footer-left 测试按钮的状态 icon ----
.status-icon {
  font-size: 16px;
  flex-shrink: 0;

  &.available {
    color: var(--td-brand-color);
  }

  &.unavailable {
    color: var(--td-error-color);
  }
}

// ---- 副标题里的小标签 ----
.subtitle-tag {
  display: inline-flex;
  align-items: center;
  padding: 0 6px;
  margin-left: 6px;
  height: 16px;
  font-size: 10px;
  font-weight: 500;
  border-radius: 3px;

  &--ok {
    color: var(--td-success-color);
    background: var(--td-success-color-light);
  }

  &--muted {
    color: var(--td-text-color-placeholder);
    background: var(--td-bg-color-component);
  }
}
</style>

<!--
  Non-scoped block: per-transport header-icon coloring. Mirrors the matching
  .service-card--{transport} .service-card__badge in McpSettings so the
  list-card → drawer hand-off stays visually continuous.
-->
<style lang="less">
.mcp-drawer--sse .setting-drawer__header-icon {
  background: rgba(17, 128, 83, 0.12);
  color: #118053;
}

.mcp-drawer--http-streamable .setting-drawer__header-icon {
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}
</style>
