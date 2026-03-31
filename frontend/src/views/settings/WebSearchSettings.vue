<template>
  <div class="websearch-settings">
    <div class="section-header">
      <h2>{{ t('webSearchSettings.title') }}</h2>
      <p class="section-description">{{ t('webSearchSettings.description') }}</p>
    </div>

    <div class="settings-group">
      <div class="section-subheader">
        <h3>{{ t('webSearchSettings.providersTitle') }}</h3>
        <t-button theme="primary" size="small" @click="openAddDialog">
          <template #icon><add-icon /></template>
          {{ t('webSearchSettings.addProvider') }}
        </t-button>
      </div>

      <!-- Provider List -->
      <div v-if="providerEntities.length > 0" class="provider-list">
        <div v-for="entity in providerEntities" :key="entity.id" class="provider-item">
          <div class="item-info">
            <div class="item-header">
              <span class="item-name">{{ entity.name }}</span>
              <t-tag v-if="entity.is_default" theme="primary" size="small" variant="light">
                {{ t('webSearchSettings.default') }}
              </t-tag>
              <t-tag size="small" variant="outline">{{ entity.provider }}</t-tag>
            </div>
            <div class="item-desc">{{ entity.description || t('webSearchSettings.noDescription') }}</div>
          </div>
          <div class="item-actions">
            <t-button theme="default" variant="text" size="small" @click="testExistingConnection(entity)" :loading="testingId === entity.id">
              {{ t('webSearchSettings.testConnection') }}
            </t-button>
            <t-button theme="primary" variant="text" size="small" @click="editProvider(entity)">
              {{ t('common.edit') }}
            </t-button>
            <t-popconfirm :content="t('webSearchSettings.deleteConfirm')" @confirm="deleteProvider(entity.id!)">
              <t-button theme="danger" variant="text" size="small">
                {{ t('common.delete') }}
              </t-button>
            </t-popconfirm>
          </div>
        </div>
      </div>

      <!-- Empty State -->
      <div v-else class="empty-providers">
        <p>{{ t('webSearchSettings.noProvidersDesc') }}</p>
      </div>
    </div>

    <!-- Add/Edit Dialog -->
    <t-dialog
      v-model:visible="showAddProviderDialog"
      :header="editingProvider ? t('webSearchSettings.editProvider') : t('webSearchSettings.addProvider')"
      width="520px"
      :footer="false"
      destroy-on-close
    >
      <div class="dialog-form-container">
        <t-form :data="providerForm" label-align="top" @submit="saveProvider" class="provider-form">
          <t-form-item :label="t('webSearchSettings.providerTypeLabel')" name="provider">
            <t-select v-model="providerForm.provider" :disabled="!!editingProvider" @change="onProviderTypeChange">
              <t-option v-for="pt in providerTypes" :key="pt.id" :value="pt.id" :label="pt.name">
                <div class="provider-option">
                  <span>{{ pt.name }}</span>
                  <t-tag v-if="pt.free" theme="success" size="small" variant="light">{{ t('webSearchSettings.free') }}</t-tag>
                </div>
              </t-option>
            </t-select>
          </t-form-item>

          <t-form-item :label="t('webSearchSettings.providerNameLabel')" name="name">
            <t-input v-model="providerForm.name" :placeholder="selectedProviderType?.name || t('webSearchSettings.providerNamePlaceholder')" />
          </t-form-item>

          <t-form-item :label="t('webSearchSettings.providerDescLabel')" name="description">
            <t-input v-model="providerForm.description" :placeholder="t('webSearchSettings.providerDescPlaceholder')" />
          </t-form-item>

          <template v-if="selectedProviderType?.requires_api_key || selectedProviderType?.requires_engine_id">
            <div class="form-divider"></div>
            
            <div class="credentials-hint" v-if="selectedProviderType?.docs_url">
              <a :href="selectedProviderType.docs_url" target="_blank" rel="noopener noreferrer">
                {{ t('webSearchSettings.viewDocs') }} ↗
              </a>
            </div>
            
            <t-form-item v-if="selectedProviderType?.requires_api_key" :label="t('webSearchSettings.apiKeyLabel')" name="parameters.api_key">
              <t-input
                v-model="providerForm.parameters.api_key"
                type="password"
                :placeholder="editingProvider ? t('webSearchSettings.apiKeyUnchanged') : t('webSearchSettings.apiKeyPlaceholder')"
              />
            </t-form-item>
            <t-form-item v-if="selectedProviderType?.requires_engine_id" :label="t('webSearchSettings.engineIdLabel')" name="parameters.engine_id">
              <t-input v-model="providerForm.parameters.engine_id" :placeholder="t('webSearchSettings.engineIdLabel')" />
            </t-form-item>
          </template>

          <div class="form-divider"></div>

          <t-form-item :label="t('webSearchSettings.setAsDefault')" name="is_default">
            <template #help>
              <div class="switch-help">
                {{ t('webSearchSettings.setAsDefaultDesc') }}
              </div>
            </template>
            <t-switch v-model="providerForm.is_default" />
          </t-form-item>

          <div class="dialog-footer">
            <div class="footer-left">
              <t-button
                v-if="selectedProviderType && !selectedProviderType.free"
                theme="default"
                variant="outline"
                :loading="testing"
                @click="testConnection"
              >
                {{ testing ? t('webSearchSettings.testing') : t('webSearchSettings.testConnection') }}
              </t-button>
            </div>
            <div class="footer-right">
              <t-button theme="default" variant="base" @click="showAddProviderDialog = false">{{ t('common.cancel') }}</t-button>
              <t-button theme="primary" type="submit" :loading="saving">{{ t('common.save') }}</t-button>
            </div>
          </div>
        </t-form>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { AddIcon } from 'tdesign-icons-vue-next'
import {
  listWebSearchProviders,
  listWebSearchProviderTypes,
  createWebSearchProvider,
  updateWebSearchProvider,
  deleteWebSearchProvider as deleteWebSearchProviderAPI,
  testWebSearchProvider,
  type WebSearchProviderEntity,
  type WebSearchProviderTypeInfo,
} from '@/api/web-search-provider'

const { t } = useI18n()

// ===== State =====
const providerEntities = ref<WebSearchProviderEntity[]>([])
const providerTypes = ref<WebSearchProviderTypeInfo[]>([])
const showAddProviderDialog = ref(false)
const editingProvider = ref<WebSearchProviderEntity | null>(null)
const testing = ref(false)
const testingId = ref<string | null>(null)
const saving = ref(false)

const providerForm = ref<{
  name: string
  provider: string
  description: string
  parameters: { api_key?: string; engine_id?: string }
  is_default: boolean
}>({
  name: '',
  provider: 'duckduckgo',
  description: '',
  parameters: {},
  is_default: false,
})

// ===== Computed =====
const selectedProviderType = computed(() => {
  return providerTypes.value.find(pt => pt.id === providerForm.value.provider)
})

// ===== Methods =====
const onProviderTypeChange = () => {
  providerForm.value.parameters = {}
}

const loadProviderEntities = async () => {
  try {
    const response = await listWebSearchProviders()
    if (response.data && Array.isArray(response.data)) {
      providerEntities.value = response.data
    }
  } catch (error) {
    console.error('Failed to load provider entities:', error)
  }
}

const loadProviderTypes = async () => {
  try {
    providerTypes.value = await listWebSearchProviderTypes()
  } catch (error) {
    console.error('Failed to load provider types:', error)
  }
}

const openAddDialog = () => {
  editingProvider.value = null
  providerForm.value = { 
    name: '', 
    provider: providerTypes.value[0]?.id || 'duckduckgo', 
    description: '', 
    parameters: {}, 
    is_default: providerEntities.value.length === 0 
  }
  showAddProviderDialog.value = true
}

const editProvider = (entity: WebSearchProviderEntity) => {
  editingProvider.value = entity
  providerForm.value = {
    name: entity.name,
    provider: entity.provider,
    description: entity.description || '',
    parameters: {
      api_key: '',
      engine_id: entity.parameters?.engine_id || '',
    },
    is_default: entity.is_default || false,
  }
  showAddProviderDialog.value = true
}

const saveProvider = async ({ validateResult, firstError }: any) => {
  if (validateResult !== true && validateResult !== undefined) {
    MessagePlugin.warning(firstError || 'Please check the form fields')
    return
  }
  
  saving.value = true
  try {
    const data: Partial<WebSearchProviderEntity> = {
      name: providerForm.value.name.trim() || selectedProviderType.value?.name || providerForm.value.provider,
      provider: providerForm.value.provider as any,
      description: providerForm.value.description,
      parameters: { ...providerForm.value.parameters },
      is_default: providerForm.value.is_default,
    }
    
    if (editingProvider.value && !data.parameters!.api_key) {
      delete data.parameters!.api_key
    }

    if (editingProvider.value) {
      await updateWebSearchProvider(editingProvider.value.id!, data)
      MessagePlugin.success(t('webSearchSettings.toasts.providerUpdated'))
    } else {
      await createWebSearchProvider(data)
      MessagePlugin.success(t('webSearchSettings.toasts.providerCreated'))
    }
    showAddProviderDialog.value = false
    await loadProviderEntities()
  } catch (error: any) {
    MessagePlugin.error(error?.message || 'Failed to save provider')
  } finally {
    saving.value = false
  }
}

const deleteProvider = async (id: string) => {
  try {
    await deleteWebSearchProviderAPI(id)
    MessagePlugin.success(t('webSearchSettings.toasts.providerDeleted'))
    await loadProviderEntities()
  } catch (error: any) {
    MessagePlugin.error(error?.message || 'Failed to delete provider')
  }
}

const testConnection = async () => {
  testing.value = true
  try {
    const data = {
      provider: providerForm.value.provider,
      parameters: { ...providerForm.value.parameters },
    }
    
    if (editingProvider.value && !data.parameters.api_key) {
      const res = await testWebSearchProvider(editingProvider.value.id!)
      if (res.success) {
        MessagePlugin.success(t('webSearchSettings.toasts.testSuccess'))
      } else {
        MessagePlugin.error(res.error || t('webSearchSettings.toasts.testFailed'))
      }
    } else {
      const res = await testWebSearchProvider(undefined, data)
      if (res.success) {
        MessagePlugin.success(t('webSearchSettings.toasts.testSuccess'))
      } else {
        MessagePlugin.error(res.error || t('webSearchSettings.toasts.testFailed'))
      }
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('webSearchSettings.toasts.testFailed'))
  } finally {
    testing.value = false
  }
}

const testExistingConnection = async (entity: WebSearchProviderEntity) => {
  testingId.value = entity.id!
  try {
    const res = await testWebSearchProvider(entity.id!)
    if (res.success) {
      MessagePlugin.success(t('webSearchSettings.toasts.testSuccess'))
    } else {
      MessagePlugin.error(res.error || t('webSearchSettings.toasts.testFailed'))
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('webSearchSettings.toasts.testFailed'))
  } finally {
    testingId.value = null
  }
}

// ===== Init =====
onMounted(async () => {
  await Promise.all([loadProviderTypes(), loadProviderEntities()])
})
</script>

<style lang="less" scoped>
.websearch-settings {
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

.settings-group {
  display: flex;
  flex-direction: column;
}

.section-subheader {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;

  h3 {
    font-size: 16px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
  }
}

.provider-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.provider-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  transition: all 0.2s ease;

  &:hover {
    border-color: var(--td-brand-color);
  }
}

.item-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.item-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.item-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.item-desc {
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.item-actions {
  display: flex;
  gap: 4px;
  align-items: center;
}

.empty-providers {
  padding: 32px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  border: 1px dashed var(--td-component-stroke);
  border-radius: 8px;
  font-size: 14px;
}

.dialog-form-container {
  margin-top: 12px;
}

.provider-option {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
}

.form-divider {
  height: 1px;
  background: var(--td-component-border);
  margin: 20px 0;
}

.credentials-hint {
  margin-bottom: 12px;
  font-size: 13px;
  
  a {
    color: var(--td-brand-color);
    text-decoration: none;
    
    &:hover {
      text-decoration: underline;
    }
  }
}

.switch-help {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  margin-top: 4px;
  line-height: 1.4;
}

.dialog-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 32px;
  padding-top: 20px;
  border-top: 1px solid var(--td-component-border);

  .footer-right {
    display: flex;
    gap: 12px;
  }
}
</style>
