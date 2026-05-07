<template>
  <SettingDrawer
    :visible="dialogVisible"
    :title="mode === 'add' ? t('mcpServiceDialog.addTitle') : t('mcpServiceDialog.editTitle')"
    :confirm-loading="submitting"
    @update:visible="(v: boolean) => dialogVisible = v"
    @confirm="handleSubmit"
    @cancel="handleClose"
  >
    <t-form
      ref="formRef"
      :data="formData"
      :rules="rules"
      label-align="top"
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
          <t-radio-button value="sse">{{ t('mcpServiceDialog.transport.sse') }}</t-radio-button>
          <t-radio-button value="http-streamable">{{ t('mcpServiceDialog.transport.httpStreamable') }}</t-radio-button>
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
          <t-form-item :label="t('mcpServiceDialog.apiKey')">
            <t-input
              v-model="formData.auth_config.api_key"
              type="password"
              :placeholder="t('mcpServiceDialog.optional')"
            />
          </t-form-item>
          <t-form-item :label="t('mcpServiceDialog.bearerToken')">
            <t-input
              v-model="formData.auth_config.token"
              type="password"
              :placeholder="t('mcpServiceDialog.optional')"
            />
          </t-form-item>
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
  type MCPService
} from '@/api/mcp-service'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'

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
    api_key: '',
    token: ''
  },
  advanced_config: {
    timeout: 30,
    retry_count: 3,
    retry_delay: 1
  }
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

// Reset form function - defined before watch to avoid hoisting issues
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
  formRef.value?.clearValidate()
}

// Watch service prop to initialize form
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
          api_key: service.auth_config?.api_key || '',
          token: service.auth_config?.token || ''
        },
        advanced_config: {
          timeout: service.advanced_config?.timeout || 30,
          retry_count: service.advanced_config?.retry_count || 3,
          retry_delay: service.advanced_config?.retry_delay || 1
        }
      }
    } else {
      resetForm()
    }
  },
  { immediate: true }
)

// Handle submit
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
      auth_config: {
        api_key: formData.value.auth_config.api_key || undefined,
        token: formData.value.auth_config.token || undefined
      },
      advanced_config: formData.value.advanced_config,
      url: formData.value.url || undefined
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
</style>

