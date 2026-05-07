<template>
  <div class="websearch-settings">
    <div class="section-header">
      <h2>{{ t('webSearchSettings.title') }}</h2>
      <p class="section-description">{{ t('webSearchSettings.description') }}</p>
    </div>

    <div class="settings-toolbar">
      <h3>{{ t('webSearchSettings.providersTitle') }}</h3>
      <t-button theme="primary" variant="outline" size="small" @click="openAddDialog">
        <template #icon><add-icon /></template>
        {{ t('webSearchSettings.addProvider') }}
      </t-button>
    </div>

    <!-- Provider List -->
    <div v-if="providerEntities.length > 0" class="provider-grid">
      <SettingCard
        v-for="entity in providerEntities"
        :key="entity.id"
        :title="entity.name"
        :description="entity.description || ''"
        :actions="getProviderOptions(entity)"
        @action="(value: string) => handleMenuAction({ value }, entity)"
      >
        <template #tags>
          <t-tag theme="primary" size="small" variant="light">
            {{ entity.provider }}
          </t-tag>
          <t-tag v-if="entity.is_default" theme="success" size="small" variant="light">
            {{ t('webSearchSettings.default') }}
          </t-tag>
          <t-tag v-if="isEntityFree(entity)" theme="warning" size="small" variant="light">
            {{ t('webSearchSettings.free') }}
          </t-tag>
        </template>
        <template #meta>
          <span v-if="entity.parameters?.proxy_url" class="provider-meta-item" :title="entity.parameters.proxy_url">
            <t-icon name="internet" size="12px" />
            <span class="provider-meta-text">{{ entity.parameters.proxy_url }}</span>
          </span>
        </template>
      </SettingCard>
    </div>

    <!-- Empty State -->
    <div v-else class="empty-state">
      <t-empty :description="t('webSearchSettings.noProvidersDesc')">
        <t-button theme="primary" variant="outline" size="small" @click="openAddDialog">
          <template #icon><add-icon /></template>
          {{ t('webSearchSettings.addProvider') }}
        </t-button>
      </t-empty>
    </div>

    <!-- Add/Edit Drawer -->
    <SettingDrawer
      v-model:visible="showAddProviderDialog"
      :title="editingProvider ? t('webSearchSettings.editProvider') : t('webSearchSettings.addProvider')"
      :confirm-loading="saving"
      @confirm="saveProvider"
    >
      <t-form ref="formRef" :data="providerForm" label-align="top" class="provider-form">
        <t-form-item :label="t('webSearchSettings.providerTypeLabel')" name="provider">
          <t-select v-model="providerForm.provider" :disabled="!!editingProvider" @change="onProviderTypeChange">
            <t-option v-for="pt in providerTypes" :key="pt.id" :value="pt.id" :label="pt.name">
              <div class="provider-option">
                <span>{{ pt.name }}</span>
                <t-tag v-if="isProviderFree(pt)" theme="success" size="small" variant="light">
                  {{ t('webSearchSettings.free') }}
                </t-tag>
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
            <a :href="selectedProviderType.docs_url" target="_blank" rel="noopener noreferrer" class="doc-link">
              {{ t('webSearchSettings.viewDocs') }}
              <t-icon name="link" class="link-icon" />
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

        <t-form-item v-if="selectedProviderType?.supports_proxy" :label="t('webSearchSettings.proxyUrlLabel')" name="parameters.proxy_url">
          <t-input
            v-model="providerForm.parameters.proxy_url"
            :placeholder="t('webSearchSettings.proxyUrlPlaceholder')"
          />
          <template #help>
            <span class="switch-help">{{ t('webSearchSettings.proxyUrlHelp') }}</span>
          </template>
        </t-form-item>

        <div class="form-divider"></div>

        <t-form-item :label="t('webSearchSettings.setAsDefault')" name="is_default">
          <template #help>
            <div class="switch-help">
              {{ t('webSearchSettings.setAsDefaultDesc') }}
            </div>
          </template>
          <t-switch v-model="providerForm.is_default" />
        </t-form-item>
      </t-form>

      <template #footer-left>
        <t-button
          v-if="selectedProviderType && !isProviderFree(selectedProviderType)"
          theme="default"
          variant="outline"
          :loading="testing"
          @click="testConnection"
        >
          {{ testing ? t('webSearchSettings.testing') : t('webSearchSettings.testConnection') }}
        </t-button>
      </template>
    </SettingDrawer>
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
import SettingCard from '@/components/settings/SettingCard.vue'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'

const { t } = useI18n()
const confirmDelete = useConfirmDelete()

// ===== State =====
const providerEntities = ref<WebSearchProviderEntity[]>([])
const providerTypes = ref<WebSearchProviderTypeInfo[]>([])
const showAddProviderDialog = ref(false)
const editingProvider = ref<WebSearchProviderEntity | null>(null)
const testing = ref(false)
const testingId = ref<string | null>(null)
const saving = ref(false)
const formRef = ref<any>()

const providerForm = ref<{
  name: string
  provider: string
  description: string
  parameters: { api_key?: string; engine_id?: string; proxy_url?: string }
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

const isProviderFree = (providerType: WebSearchProviderTypeInfo) => {
  return !providerType.requires_api_key && !providerType.requires_engine_id
}

const isEntityFree = (entity: WebSearchProviderEntity) => {
  const pt = providerTypes.value.find(p => p.id === entity.provider)
  return pt ? isProviderFree(pt) : false
}

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
      proxy_url: entity.parameters?.proxy_url || '',
    },
    is_default: entity.is_default || false,
  }
  showAddProviderDialog.value = true
}

const saveProvider = async () => {
  const validateResult = await formRef.value?.validate()
  if (validateResult !== true && validateResult !== undefined) {
    const firstError = typeof validateResult === 'object' ? Object.values(validateResult)[0] : ''
    MessagePlugin.warning(typeof firstError === 'string' ? firstError : 'Please check the form fields')
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

const deleteProvider = (entity: WebSearchProviderEntity) => {
  confirmDelete({
    body: t('webSearchSettings.deleteConfirm'),
    onConfirm: async () => {
      try {
        await deleteWebSearchProviderAPI(entity.id!)
        MessagePlugin.success(t('webSearchSettings.toasts.providerDeleted'))
        await loadProviderEntities()
      } catch (error: any) {
        MessagePlugin.error(error?.message || 'Failed to delete provider')
      }
    }
  })
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

const getProviderOptions = (_entity: WebSearchProviderEntity) => {
  return [
    { content: t('webSearchSettings.testConnection'), value: 'test' },
    { content: t('common.edit'), value: 'edit' },
    { content: t('common.delete'), value: 'delete', theme: 'error' as const }
  ]
}

const handleMenuAction = (data: { value: string }, entity: WebSearchProviderEntity) => {
  switch (data.value) {
    case 'test':
      testExistingConnection(entity)
      break
    case 'edit':
      editProvider(entity)
      break
    case 'delete':
      deleteProvider(entity)
      break
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

.settings-toolbar {
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

.provider-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.provider-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  overflow: hidden;

  .provider-meta-text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.empty-state {
  padding: 64px 0;
  text-align: center;

  :deep(.t-empty__description) {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    margin-bottom: 16px;
  }
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
</style>
