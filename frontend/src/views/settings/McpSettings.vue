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

    <div v-else class="services-container">
      <div class="services-header">
        <div class="header-info">
          <h3>{{ $t('mcpSettings.configuredServices') }}</h3>
          <p>{{ $t('mcpSettings.manageAndTest') }}</p>
        </div>
        <t-button size="small" theme="primary" @click="handleAdd">
          <template #icon><t-icon name="add" /></template>
          {{ $t('mcpSettings.addService') }}
        </t-button>
      </div>

      <div v-if="services.length === 0" class="empty-state">
        <t-empty :description="$t('mcpSettings.empty')" >
          <t-button theme="primary" @click="handleAdd">{{ $t('mcpSettings.addFirst') }}</t-button>
        </t-empty>
      </div>

      <div v-else class="services-list">
        <div v-for="service in services" :key="service.id" class="service-card">
          <div class="service-info">
            <div class="service-header">
              <div class="service-name">
                {{ service.name }}
                <t-tag 
                  v-if="service.is_builtin"
                  theme="warning"
                  size="small"
                  variant="light"
                >
                  {{ $t('mcpSettings.builtin') }}
                </t-tag>
                <t-tag 
                  :theme="getTransportTypeTheme(service.transport_type)" 
                  size="small"
                  variant="light"
                >
                  {{ getTransportTypeLabel(service.transport_type) }}
                </t-tag>
              </div>
              <div class="service-controls">
                <t-switch 
                  v-model="service.enabled" 
                  @change="() => handleToggleEnabled(service)"
                  size="medium"
                  :disabled="service.is_builtin"
                />
                <t-dropdown 
                  v-if="!service.is_builtin"
                  :options="getServiceOptions(service)" 
                  @click="(data: any) => handleMenuAction(data, service)"
                  placement="bottom-right"
                  :disabled="testing"
                >
                  <t-button variant="text" shape="square" size="small" class="more-btn" :disabled="testing">
                    <t-icon name="more" />
                  </t-button>
                </t-dropdown>
                <t-dropdown 
                  v-else
                  :options="getBuiltinServiceOptions(service)" 
                  @click="(data: any) => handleMenuAction(data, service)"
                  placement="bottom-right"
                  :disabled="testing"
                >
                  <t-button variant="text" shape="square" size="small" class="more-btn" :disabled="testing">
                    <t-icon name="more" />
                  </t-button>
                </t-dropdown>
              </div>
            </div>
            <div v-if="service.description" class="service-description">
              {{ service.description }}
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Add/Edit Dialog -->
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
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
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

const { t } = useI18n()

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
    // Revert on error
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
  
  // 显示测试开始提示
  MessagePlugin.info({
    content: t('mcpSettings.toasts.testing', { name: service.name }),
    duration: 0, // 不自动关闭
    closeBtn: false
  })
  
  try {
    const result = await testMCPService(service.id)
    
    console.log('Test result received:', result)
    
    // 关闭所有消息提示
    MessagePlugin.closeAll()
    
    // 检查结果是否存在
    if (!result) {
      // 即使没有结果，也显示错误对话框
      testResult.value = {
        success: false,
        message: t('mcpSettings.toasts.noResponse')
      }
      testDialogVisible.value = true
      return
    }
    
    // 设置测试结果
    testResult.value = result
    
    // 显示详细结果对话框
    console.log('Opening test dialog, result:', testResult.value)
    testDialogVisible.value = true
  } catch (error: any) {
    // 关闭所有消息提示
    MessagePlugin.closeAll()
    
    // 显示错误信息
    const errorMessage = error?.response?.data?.error?.message || error?.message || t('mcpSettings.toasts.testFailed')
    console.error('Failed to test MCP service:', error)
    
    // 即使出错也显示结果对话框，显示错误信息
    testResult.value = {
      success: false,
      message: errorMessage
    }
    testDialogVisible.value = true
  } finally {
    // 确保关闭 loading
    testing.value = false
  }
}

// Handle delete button click
const handleDelete = async (service: MCPService) => {
  if (!service || !service.id) return
  
  const confirmDialog = DialogPlugin.confirm({
    header: t('common.confirmDelete'),
    body: t('mcpSettings.deleteConfirmBody', { name: service.name || t('mcpSettings.unnamed') }),
    confirmBtn: t('common.delete'),
    cancelBtn: t('common.cancel'),
    theme: 'warning',
    onConfirm: async () => {
      try {
        await deleteMCPService(service.id)
        MessagePlugin.success(t('mcpSettings.toasts.deleted'))
        confirmDialog.hide()
        loadServices()
      } catch (error) {
        MessagePlugin.error(t('mcpSettings.toasts.deleteFailed'))
        console.error('Failed to delete MCP service:', error)
      }
    }
  })
}

// Get service options for dropdown menu
const getServiceOptions = (service: MCPService) => {
  return [
    {
      content: t('mcpSettings.actions.test'),
      value: `test-${service.id}`
    },
    {
      content: t('common.edit'),
      value: `edit-${service.id}`
    },
    {
      content: t('common.delete'),
      value: `delete-${service.id}`,
      theme: 'error'
    }
  ]
}

// Get service options for builtin services (test only)
const getBuiltinServiceOptions = (service: MCPService) => {
  return [
    {
      content: t('mcpSettings.actions.test'),
      value: `test-${service.id}`
    }
  ]
}

// Handle menu action
const handleMenuAction = (data: { value: string }, service: MCPService) => {
  const value = data.value
  
  if (value.startsWith('test-')) {
    handleTest(service)
  } else if (value.startsWith('edit-')) {
    handleEdit(service)
  } else if (value.startsWith('delete-')) {
    handleDelete(service)
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
  margin-bottom: 32px;

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
    line-height: 1.5;
  }
}

.loading-container {
  padding: 40px 0;
  text-align: center;
}

.services-container {
  margin-top: 16px;
}

.services-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--td-component-stroke);

  .header-info {
    flex: 1;

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

.services-list {
  display: flex;
  flex-direction: column;
  gap: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  padding: 16px;
  background: var(--td-bg-color-secondarycontainer);
}

.service-card {
  padding: 12px 0;
  border-bottom: 1px solid var(--td-component-stroke);
  transition: all 0.2s;

  &:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }

  &:first-child {
    padding-top: 0;
  }
}

.service-info {
  .service-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;

    .service-name {
      font-size: 15px;
      font-weight: 500;
      color: var(--td-text-color-primary);
      display: flex;
      align-items: center;
      gap: 8px;
      flex: 1;
    }

    .service-controls {
      display: flex;
      align-items: center;
      gap: 8px;
      flex-shrink: 0;

      .more-btn {
        color: var(--td-text-color-placeholder);
        padding: 4px;
        transition: all 0.2s;

        &:hover {
          background: var(--td-bg-color-secondarycontainer);
          color: var(--td-text-color-primary);
        }
      }
    }
  }

  .service-description {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin-bottom: 8px;
    line-height: 1.5;
  }

  .service-meta {
    display: flex;
    align-items: center;
    gap: 12px;
    font-size: 12px;
    color: var(--td-text-color-placeholder);

    .meta-item {
      display: flex;
      align-items: center;
      gap: 4px;

      .meta-icon {
        font-size: 12px;
      }
    }
  }
}
</style>

