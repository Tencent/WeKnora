<template>
  <t-dialog
    v-model:visible="dialogVisible"
    :header="mode === 'add' ? t('mcpServiceDialog.addTitle') : t('mcpServiceDialog.editTitle')"
    width="700px"
    :on-confirm="handleSubmit"
    :on-cancel="handleClose"
    :confirm-btn="{ content: t('common.save'), loading: submitting }"
  >
    <t-form
      ref="formRef"
      :data="formData"
      :rules="rules"
      label-width="120px"
    >
      <t-form-item :label="t('mcpServiceDialog.name')" name="name">
        <t-input v-model="formData.name" :placeholder="t('mcpServiceDialog.namePlaceholder')" />
      </t-form-item>

      <t-form-item :label="t('mcpServiceDialog.description')" name="description">
        <t-textarea
          v-model="formData.description"
          :autosize="{ minRows: 3, maxRows: 5 }"
          :placeholder="t('mcpServiceDialog.descriptionPlaceholder')"
        />
      </t-form-item>

      <t-form-item :label="t('mcpServiceDialog.transportType')" name="transport_type">
        <t-radio-group v-model="formData.transport_type">
          <t-radio value="sse">{{ t('mcpServiceDialog.transport.sse') }}</t-radio>
          <t-radio value="http-streamable">{{ t('mcpServiceDialog.transport.httpStreamable') }}</t-radio>
          <!-- Stdio transport is disabled for security reasons -->
        </t-radio-group>
      </t-form-item>

      <!-- URL for SSE/HTTP Streamable -->
      <t-form-item
        :label="t('mcpServiceDialog.serviceUrl')"
        name="url"
      >
        <t-input v-model="formData.url" :placeholder="t('mcpServiceDialog.serviceUrlPlaceholder')" />
      </t-form-item>

      <!-- Stdio Config removed for security reasons -->

      <t-form-item :label="t('mcpServiceDialog.enableService')" name="enabled">
        <t-switch v-model="formData.enabled" />
      </t-form-item>

      <!-- Authentication Config -->
      <t-collapse :default-value="[]">
        <t-collapse-panel :header="t('mcpServiceDialog.authConfig')" value="auth">
          <!--
            Credential fields follow the "write-only secrets" pattern.
            When a value is currently stored on the server the placeholder
            is swapped for a bullet run + "Enter new value to replace"
            hint, so the visual "something is there" signal is carried by
            the input itself rather than by a separate Set/Not-set badge.
            The Remove checkbox still offers an explicit clear path.
          -->
          <div class="credential-field">
            <label class="credential-label">{{ t('mcpServiceDialog.apiKey') }}</label>
            <t-input
              v-model="formData.auth_config.api_key"
              type="password"
              :disabled="clearApiKey"
              :placeholder="apiKeyPlaceholder"
            />
            <t-checkbox
              v-if="mode === 'edit' && hasExistingApiKey"
              v-model="clearApiKey"
              class="clear-credential"
            >
              {{ t('secret.clearHint') }}
            </t-checkbox>
          </div>

          <div class="credential-field">
            <label class="credential-label">{{ t('mcpServiceDialog.bearerToken') }}</label>
            <t-input
              v-model="formData.auth_config.token"
              type="password"
              :disabled="clearToken"
              :placeholder="tokenPlaceholder"
            />
            <t-checkbox
              v-if="mode === 'edit' && hasExistingToken"
              v-model="clearToken"
              class="clear-credential"
            >
              {{ t('secret.clearHint') }}
            </t-checkbox>
          </div>
        </t-collapse-panel>

        <!-- Advanced Config -->
        <t-collapse-panel :header="t('mcpServiceDialog.advancedConfig')" value="advanced">
          <t-form-item :label="t('mcpServiceDialog.timeoutSec')">
            <t-input-number
              v-model="formData.advanced_config.timeout"
              :min="1"
              :max="300"
              placeholder="30"
            />
          </t-form-item>
          <t-form-item :label="t('mcpServiceDialog.retryCount')">
            <t-input-number
              v-model="formData.advanced_config.retry_count"
              :min="0"
              :max="10"
              placeholder="3"
            />
          </t-form-item>
          <t-form-item :label="t('mcpServiceDialog.retryDelaySec')">
            <t-input-number
              v-model="formData.advanced_config.retry_delay"
              :min="0"
              :max="60"
              placeholder="1"
            />
          </t-form-item>
        </t-collapse-panel>
      </t-collapse>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import type { FormInstanceFunctions, FormRule } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  createMCPService,
  updateMCPService,
  type MCPService
} from '@/api/mcp-service'

interface Props {
  visible: boolean
  service: MCPService | null
  mode: 'add' | 'edit'
}

interface Emits {
  (e: 'update:visible', value: boolean): void
  (e: 'success'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const formRef = ref<FormInstanceFunctions>()
const submitting = ref(false)
const { t } = useI18n()

// Fixed placeholder returned by the server for sensitive fields whose values
// are withheld. Must match internal/types/secret.go → RedactedSecretPlaceholder.
const REDACTED_PLACEHOLDER = '***'

const formData = ref({
  name: '',
  description: '',
  enabled: true,
  transport_type: 'sse' as 'sse' | 'http-streamable',
  url: '',
  auth_config: {
    api_key: '',
    token: ''
  },
  advanced_config: {
    timeout: 30,
    retry_count: 3,
    retry_delay: 1
  }
})

// Explicit "remove stored credential" flags. Reset to false on every open/reset.
const clearApiKey = ref(false)
const clearToken = ref(false)

// "Is a credential currently stored?" is signaled by the server returning the
// fixed REDACTED_PLACEHOLDER instead of an empty string. Drive the label
// badge and placeholder hint from this signal.
const hasExistingApiKey = computed(
  () => props.service?.auth_config?.api_key === REDACTED_PLACEHOLDER
)
const hasExistingToken = computed(
  () => props.service?.auth_config?.token === REDACTED_PLACEHOLDER
)

// Placeholders swap to the shared "stored credential" hint when the server
// signals a value is present (parameters come back as '***'). Otherwise
// the field is genuinely empty and we fall through to the normal
// "Optional" placeholder that appears in add mode too.
const apiKeyPlaceholder = computed(() =>
  hasExistingApiKey.value
    ? t('secret.storedPlaceholder')
    : t('mcpServiceDialog.optional')
)
const tokenPlaceholder = computed(() =>
  hasExistingToken.value
    ? t('secret.storedPlaceholder')
    : t('mcpServiceDialog.optional')
)

const rules: Record<string, FormRule[]> = {
  name: [{ required: true, message: t('mcpServiceDialog.rules.nameRequired') as string, type: 'error' }],
  transport_type: [{ required: true, message: t('mcpServiceDialog.rules.transportRequired') as string, type: 'error' }],
  url: [
    {
      validator: (val: string) => {
        if (!val || val.trim() === '') {
          return { result: false, message: t('mcpServiceDialog.rules.urlRequired') as string, type: 'error' }
        }
        // Basic URL validation
        try {
          new URL(val)
          return { result: true, message: '', type: 'success' }
        } catch {
          return { result: false, message: t('mcpServiceDialog.rules.urlInvalid') as string, type: 'error' }
        }
      }
    }
  ]
}

const dialogVisible = computed({
  get: () => props.visible,
  set: (value) => emit('update:visible', value)
})

// Reset form function — defined before watch to avoid hoisting issues.
// Sensitive fields are always blank: the watch below does not copy the
// server-provided redacted placeholder into formData, and clear flags are
// reset so a reopen does not carry over an aborted deletion intent.
const resetForm = () => {
  formData.value = {
    name: '',
    description: '',
    enabled: true,
    transport_type: 'sse',
    url: '',
    auth_config: {
      api_key: '',
      token: ''
    },
    advanced_config: {
      timeout: 30,
      retry_count: 3,
      retry_delay: 1
    }
  }
  clearApiKey.value = false
  clearToken.value = false
  formRef.value?.clearValidate()
}

// Watch service prop to initialize form.
// Sensitive fields (api_key, token) are NEVER pre-filled from the server
// response — even when the server returns '***' indicating a stored value.
// This keeps the "non-empty means user typed it" invariant that the submit
// logic relies on to decide between preserve and replace.
watch(
  () => props.service,
  (service) => {
    if (service) {
      // Note: stdio transport_type will fall back to 'sse' as stdio is disabled
      const transportType = service.transport_type === 'stdio' ? 'sse' : (service.transport_type || 'sse')
      formData.value = {
        name: service.name || '',
        description: service.description || '',
        enabled: service.enabled ?? true,
        transport_type: transportType as 'sse' | 'http-streamable',
        url: service.url || '',
        auth_config: {
          api_key: '',
          token: ''
        },
        advanced_config: {
          timeout: service.advanced_config?.timeout || 30,
          retry_count: service.advanced_config?.retry_count || 3,
          retry_delay: service.advanced_config?.retry_delay || 1
        }
      }
      clearApiKey.value = false
      clearToken.value = false
    } else {
      resetForm()
    }
  },
  { immediate: true }
)

// Prompt the user before irrevocable credential removal.
const confirmClearIfNeeded = (): Promise<boolean> => {
  if (!clearApiKey.value && !clearToken.value) return Promise.resolve(true)
  return new Promise((resolve) => {
    const d = DialogPlugin.confirm({
      header: t('secret.confirmClearTitle'),
      body: t('secret.confirmClearBody'),
      confirmBtn: { content: t('common.confirm'), theme: 'danger' },
      cancelBtn: t('common.cancel'),
      onConfirm: () => {
        d.hide()
        resolve(true)
      },
      onCancel: () => {
        d.hide()
        resolve(false)
      },
      onClose: () => {
        d.hide()
        resolve(false)
      }
    })
  })
}

// Handle submit — three-state auth_config payload:
//   - clear flag set → send { clear_api_key: true } / { clear_token: true }
//   - user typed a value → send the value
//   - otherwise → omit the field, server preserves the stored value
// If auth_config would be empty, omit the key entirely.
const handleSubmit = async () => {
  const valid = await formRef.value?.validate()
  if (!valid) return

  const proceed = await confirmClearIfNeeded()
  if (!proceed) return

  submitting.value = true
  try {
    const authPayload: Record<string, unknown> = {}
    if (clearApiKey.value) {
      authPayload.clear_api_key = true
    } else if (formData.value.auth_config.api_key) {
      authPayload.api_key = formData.value.auth_config.api_key
    }
    if (clearToken.value) {
      authPayload.clear_token = true
    } else if (formData.value.auth_config.token) {
      authPayload.token = formData.value.auth_config.token
    }

    const data: Partial<MCPService> = {
      name: formData.value.name,
      description: formData.value.description,
      enabled: formData.value.enabled,
      transport_type: formData.value.transport_type,
      advanced_config: formData.value.advanced_config,
      url: formData.value.url || undefined,
    }
    if (Object.keys(authPayload).length > 0) {
      data.auth_config = authPayload as MCPService['auth_config']
    }

    if (props.mode === 'add') {
      await createMCPService(data)
      MessagePlugin.success(t('mcpServiceDialog.toasts.created'))
    } else {
      await updateMCPService(props.service!.id, data)
      MessagePlugin.success(t('mcpServiceDialog.toasts.updated'))
    }

    emit('success')
  } catch (error) {
    MessagePlugin.error(
      props.mode === 'add' ? (t('mcpServiceDialog.toasts.createFailed') as string) : (t('mcpServiceDialog.toasts.updateFailed') as string)
    )
    console.error('Failed to save MCP service:', error)
  } finally {
    submitting.value = false
  }
}

// Handle close
const handleClose = () => {
  dialogVisible.value = false
}
</script>

<style scoped lang="less">
/* Stdio-related styles removed as stdio transport is disabled for security reasons */

/**
 * Credential field: stacks the label row, the password input, and the
 * optional "Remove this credential" checkbox vertically. Replaces the
 * t-form-item-based layout for the auth-config fields so the inline badge
 * doesn't get clipped by the form's 120px label-width and the clear
 * checkbox doesn't sit alongside the input.
 */
.credential-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 20px;

  &:last-child {
    margin-bottom: 0;
  }
}

.credential-label {
  display: block;
  font-size: 14px;
  color: var(--td-text-color-primary);
  line-height: 22px;
}

.clear-credential {
  :deep(.t-checkbox__label) {
    color: var(--td-error-color);
    font-size: 13px;
  }
}
</style>

