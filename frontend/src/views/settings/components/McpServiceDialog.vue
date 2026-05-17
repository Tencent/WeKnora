<template>
  <SettingDrawer :visible="dialogVisible"
    :title="mode === 'add' ? t('mcpServiceDialog.addTitle') : t('mcpServiceDialog.editTitle')"
    :confirm-loading="submitting" @update:visible="(v: boolean) => dialogVisible = v" @confirm="handleSubmit"
    @cancel="handleClose">
    <t-form ref="formRef" :data="formData" :rules="rules" label-align="top">
      <t-form-item :label="t('mcpServiceDialog.name')" name="name">
        <t-input v-model="formData.name" :placeholder="t('mcpServiceDialog.namePlaceholder')" />
      </t-form-item>

      <t-form-item :label="t('mcpServiceDialog.description')" name="description">
        <t-textarea v-model="formData.description" :autosize="{ minRows: 3, maxRows: 5 }"
          :placeholder="t('mcpServiceDialog.descriptionPlaceholder')" />
      </t-form-item>

      <t-form-item :label="t('mcpServiceDialog.transportType')" name="transport_type">
        <t-radio-group v-model="formData.transport_type">
          <t-radio-button value="sse">{{ t('mcpServiceDialog.transport.sse') }}</t-radio-button>
          <t-radio-button value="http-streamable">{{ t('mcpServiceDialog.transport.httpStreamable') }}</t-radio-button>
          <!-- Stdio transport is disabled for security reasons -->
        </t-radio-group>
      </t-form-item>

      <!-- URL for SSE/HTTP Streamable -->
      <t-form-item :label="t('mcpServiceDialog.serviceUrl')" name="url">
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
            Edit mode: credentials live behind a dedicated subresource. The
            CredentialResource component drives configured/unconfigured/editing
            state per field and commits each change to /credentials directly,
            decoupling credential edits from the surrounding form.

            Add mode: the resource doesn't exist yet, so we accept the initial
            credentials as plain inputs and POST them together with the rest of
            the service in handleSubmit. From the second save onwards, the
            edit-mode path takes over.
          -->
          <CredentialResource v-if="mode === 'edit' && props.service?.id" :api="credentialApi"
            :fields="credentialFields" :meta="credentialMeta" />
          <template v-else>
            <t-form-item :label="t('mcpServiceDialog.apiKey')">
              <t-input v-model="formData.auth_config.api_key" type="password"
                :placeholder="t('mcpServiceDialog.optional')" />
            </t-form-item>
            <t-form-item :label="t('mcpServiceDialog.bearerToken')">
              <t-input v-model="formData.auth_config.token" type="password"
                :placeholder="t('mcpServiceDialog.optional')" />
            </t-form-item>
          </template>
        </t-collapse-panel>

        <!-- Advanced Config -->
        <t-collapse-panel :header="t('mcpServiceDialog.advancedConfig')" value="advanced">
          <t-form-item :label="t('mcpServiceDialog.timeoutSec')">
            <t-input-number v-model="formData.advanced_config.timeout" :min="1" :max="300" placeholder="30" />
          </t-form-item>
          <t-form-item :label="t('mcpServiceDialog.retryCount')">
            <t-input-number v-model="formData.advanced_config.retry_count" :min="0" :max="10" placeholder="3" />
          </t-form-item>
          <t-form-item :label="t('mcpServiceDialog.retryDelaySec')">
            <t-input-number v-model="formData.advanced_config.retry_delay" :min="0" :max="60" placeholder="1" />
          </t-form-item>
        </t-collapse-panel>
      </t-collapse>
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
  type MCPService,
  type McpCredentialField,
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
/* Stdio-related styles removed as stdio transport is disabled for security reasons */
</style>
