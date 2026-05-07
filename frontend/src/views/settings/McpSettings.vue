<template>
  <div class="mcp-settings">
    <div class="section-header">
      <h2>{{ $t('mcpSettings.title') }}</h2>
      <p class="section-description">
        {{ $t('mcpSettings.description') }}
      </p>
    </div>

    <div v-if="loading" class="loading-container">
      <t-loading :text="$t('common.loading')" />
    </div>

    <template v-else>
      <div class="settings-toolbar">
        <div class="toolbar-info">
          <h3>{{ $t('mcpSettings.configuredServices') }}</h3>
          <p>{{ $t('mcpSettings.manageAndTest') }}</p>
        </div>
        <t-button size="small" theme="primary" variant="outline" @click="handleAdd">
          <template #icon><t-icon name="add" /></template>
          {{ $t('mcpSettings.addService') }}
        </t-button>
      </div>

      <div v-if="services.length === 0" class="empty-state">
        <t-empty :description="$t('mcpSettings.empty')">
          <t-button theme="primary" variant="outline" size="small" @click="handleAdd">
            <template #icon><t-icon name="add" /></template>
            {{ $t('mcpSettings.addFirst') }}
          </t-button>
        </t-empty>
      </div>

      <div v-else class="services-grid">
        <SettingCard
          v-for="service in services"
          :key="service.id"
          :title="service.name"
          :description="service.description || ''"
          :disabled="service.is_builtin"
          :actions="service.is_builtin ? getBuiltinServiceOptions() : getServiceOptions()"
          @action="(value: string) => handleMenuAction({ value }, service)"
        >
          <template #tags>
            <t-tag
              :theme="getTransportTypeTheme(service.transport_type)"
              size="small"
              variant="light"
            >
              {{ getTransportTypeLabel(service.transport_type) }}
            </t-tag>
            <t-tag
              v-if="service.is_builtin"
              theme="warning"
              size="small"
              variant="light"
            >
              {{ $t('mcpSettings.builtin') }}
            </t-tag>
            <t-tag
              :theme="service.enabled ? 'success' : 'default'"
              size="small"
              variant="light"
            >
              {{ service.enabled ? $t('common.on') : $t('common.off') }}
            </t-tag>
          </template>
          <template #controls>
            <t-switch
              v-model="service.enabled"
              size="medium"
              :disabled="service.is_builtin"
              @change="() => handleToggleEnabled(service)"
            />
          </template>
          <template #meta>
            <span v-if="service.url" class="service-meta-item" :title="service.url">
              <t-icon name="link" size="12px" />
              <span class="service-meta-text">{{ service.url }}</span>
            </span>
          </template>
        </SettingCard>
      </div>
    </template>

    <!-- Add/Edit Drawer -->
    <McpServiceDialog
      v-model:visible="dialogVisible"
      :service="currentService"
      :mode="dialogMode"
      @success="handleDialogSuccess"
    />

    <!-- Test Result Dialog -->
    <McpTestResult
      v-model:visible="testDialogVisible"
      :result="testResult"
      :service-name="testingServiceName"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  listMCPServices,
  updateMCPService,
  deleteMCPService,
  testMCPService,
  type MCPService,
  type MCPTestResult
} from '@/api/mcp-service'
import McpServiceDialog from './components/McpServiceDialog.vue'
import McpTestResult from './components/McpTestResult.vue'
import SettingCard from '@/components/settings/SettingCard.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'

const { t } = useI18n()
const confirmDelete = useConfirmDelete()

const services = ref<MCPService[]>([])
const loading = ref(false)
const dialogVisible = ref(false)
const dialogMode = ref<'add' | 'edit'>('add')
const currentService = ref<MCPService | null>(null)
const testDialogVisible = ref(false)
const testResult = ref<MCPTestResult | null>(null)
const testingServiceName = ref('')
const testing = ref(false)

// Load MCP services
const loadServices = async () => {
  loading.value = true
  try {
    services.value = await listMCPServices()
  } catch (error) {
    MessagePlugin.error(t('mcpSettings.toasts.loadFailed'))
    console.error('Failed to load MCP services:', error)
  } finally {
    loading.value = false
  }
}

// Handle add button click
const handleAdd = () => {
  currentService.value = null
  dialogMode.value = 'add'
  dialogVisible.value = true
}

// Handle edit button click
const handleEdit = (service: MCPService) => {
  currentService.value = { ...service }
  dialogMode.value = 'edit'
  dialogVisible.value = true
}

// Handle dialog success
const handleDialogSuccess = () => {
  dialogVisible.value = false
  loadServices()
}

// Handle toggle enabled/disabled
const handleToggleEnabled = async (service: MCPService) => {
  if (!service || !service.id) return

  const originalState = service.enabled
  try {
    await updateMCPService(service.id, { enabled: service.enabled })
    MessagePlugin.success(service.enabled ? t('mcpSettings.toasts.enabled') : t('mcpSettings.toasts.disabled'))
  } catch (error) {
    service.enabled = originalState
    MessagePlugin.error(t('mcpSettings.toasts.updateStateFailed'))
    console.error('Failed to update MCP service:', error)
  }
}

// Handle test button click
const handleTest = async (service: MCPService) => {
  if (!service || !service.id) return

  testingServiceName.value = service.name
  testing.value = true

  MessagePlugin.info({
    content: t('mcpSettings.toasts.testing', { name: service.name }),
    duration: 0,
    closeBtn: false
  })

  try {
    const result = await testMCPService(service.id)

    MessagePlugin.closeAll()

    if (!result) {
      testResult.value = {
        success: false,
        message: t('mcpSettings.toasts.noResponse')
      }
      testDialogVisible.value = true
      return
    }

    testResult.value = result
    testDialogVisible.value = true
  } catch (error: any) {
    MessagePlugin.closeAll()

    const errorMessage = error?.response?.data?.error?.message || error?.message || t('mcpSettings.toasts.testFailed')
    console.error('Failed to test MCP service:', error)

    testResult.value = {
      success: false,
      message: errorMessage
    }
    testDialogVisible.value = true
  } finally {
    testing.value = false
  }
}

// Handle delete button click
const handleDelete = (service: MCPService) => {
  if (!service || !service.id) return

  confirmDelete({
    body: t('mcpSettings.deleteConfirmBody', { name: service.name || t('mcpSettings.unnamed') }),
    onConfirm: async () => {
      try {
        await deleteMCPService(service.id)
        MessagePlugin.success(t('mcpSettings.toasts.deleted'))
        loadServices()
      } catch (error) {
        MessagePlugin.error(t('mcpSettings.toasts.deleteFailed'))
        console.error('Failed to delete MCP service:', error)
      }
    }
  })
}

// Get service options for dropdown menu
const getServiceOptions = () => {
  return [
    { content: t('mcpSettings.actions.test'), value: 'test' },
    { content: t('common.edit'), value: 'edit' },
    { content: t('common.delete'), value: 'delete', theme: 'error' as const }
  ]
}

// Builtin: 仅测试
const getBuiltinServiceOptions = () => {
  return [
    { content: t('mcpSettings.actions.test'), value: 'test' }
  ]
}

// Handle menu action
const handleMenuAction = (data: { value: string }, service: MCPService) => {
  if (testing.value) return
  switch (data.value) {
    case 'test':
      handleTest(service)
      break
    case 'edit':
      handleEdit(service)
      break
    case 'delete':
      handleDelete(service)
      break
  }
}

// Get transport type theme for tag
const getTransportTypeTheme = (transportType: string) => {
  switch (transportType) {
    case 'sse':
      return 'success'
    case 'http-streamable':
      return 'primary'
    case 'stdio':
      return 'warning'
    default:
      return 'default'
  }
}

// Get transport type label
const getTransportTypeLabel = (transportType: string) => {
  switch (transportType) {
    case 'sse':
      return 'SSE'
    case 'http-streamable':
      return 'HTTP Streamable'
    case 'stdio':
      return 'Stdio'
    default:
      return transportType
  }
}

onMounted(() => {
  loadServices()
})
</script>

<style scoped lang="less">
.mcp-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 28px;

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

.loading-container {
  padding: 40px 0;
  text-align: center;
}

.settings-toolbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;

  .toolbar-info {
    flex: 1;
    min-width: 0;

    h3 {
      font-size: 15px;
      font-weight: 500;
      color: var(--td-text-color-primary);
      margin: 0 0 4px 0;
    }

    p {
      font-size: 13px;
      color: var(--td-text-color-placeholder);
      margin: 0;
      line-height: 1.5;
    }
  }
}

.empty-state {
  padding: 80px 0;
  text-align: center;

  :deep(.t-empty__description) {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    margin-bottom: 16px;
  }
}

.services-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.service-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  overflow: hidden;

  .service-meta-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}
</style>
