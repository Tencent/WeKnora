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
                  <t-tag v-if="isProviderFree(pt)" theme="success" size="small" variant="light">{{ t('webSearchSettings.free') }}</t-tag>
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
            
            <!--
              Credential field: when a value is currently stored the placeholder
              swaps to the shared "bullets + Enter new value to replace" hint
              so the input itself signals "something is there". The destructive
              clear checkbox sits on its own row beneath.
            -->
            <div v-if="selectedProviderType?.requires_api_key" class="credential-field">
              <label class="credential-label">{{ t('webSearchSettings.apiKeyLabel') }}</label>
              <t-input
                v-model="providerForm.parameters.api_key"
                type="password"
                :disabled="clearApiKey"
                :placeholder="apiKeyPlaceholder"
              />
              <t-checkbox
                v-if="editingProvider && hasExistingApiKey"
                v-model="clearApiKey"
                class="clear-credential"
              >
                {{ t('secret.clearHint') }}
              </t-checkbox>
            </div>
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

          <div class="dialog-footer">
            <div class="footer-left">
              <t-button
                v-if="selectedProviderType && !isProviderFree(selectedProviderType)"
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
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
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

// Fixed placeholder returned by the server for redacted secrets. Must match
// internal/types/secret.go → RedactedSecretPlaceholder.
const REDACTED_PLACEHOLDER = '***'

// Explicit "remove stored api_key" flag. Reset on every dialog open and on
// provider type change.
const clearApiKey = ref(false)

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

// "Is an API key currently stored?" is signaled by the server returning the
// fixed REDACTED_PLACEHOLDER in the response. Drive the label badge from this.
const hasExistingApiKey = computed(() => {
  return editingProvider.value?.parameters?.api_key === REDACTED_PLACEHOLDER
})

// In edit mode with a stored key, swap the placeholder to the shared
// "bullets + Enter new value to replace" hint. Otherwise fall back to the
// provider-specific placeholder (creation) or the "leave blank to keep"
// copy used when no key is stored yet.
const apiKeyPlaceholder = computed(() => {
  if (!editingProvider.value) return t('webSearchSettings.apiKeyPlaceholder')
  return hasExistingApiKey.value
    ? t('secret.storedPlaceholder')
    : t('webSearchSettings.apiKeyUnchanged')
})

const isProviderFree = (providerType: WebSearchProviderTypeInfo) => {
  return !providerType.requires_api_key && !providerType.requires_engine_id
}

// ===== Methods =====
const onProviderTypeChange = () => {
  providerForm.value.parameters = {}
  clearApiKey.value = false
}

// Prompt the user before irrevocable credential removal.
const confirmClearIfNeeded = (): Promise<boolean> => {
  if (!clearApiKey.value) return Promise.resolve(true)
  return new Promise((resolve) => {
    const d = DialogPlugin.confirm({
      header: t('secret.confirmClearTitle'),
      body: t('secret.confirmClearBody'),
      confirmBtn: { content: t('common.confirm'), theme: 'danger' },
      cancelBtn: t('common.cancel'),
      onConfirm: () => { d.hide(); resolve(true) },
      onCancel: () => { d.hide(); resolve(false) },
      onClose: () => { d.hide(); resolve(false) },
    })
  })
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
  clearApiKey.value = false
  showAddProviderDialog.value = true
}

const editProvider = (entity: WebSearchProviderEntity) => {
  editingProvider.value = entity
  providerForm.value = {
    name: entity.name,
    provider: entity.provider,
    description: entity.description || '',
    parameters: {
      // Never pre-fill the api_key — even the redacted placeholder from the
      // server is ignored so that "non-empty means user typed it" holds.
      api_key: '',
      engine_id: entity.parameters?.engine_id || '',
      proxy_url: entity.parameters?.proxy_url || '',
    },
    is_default: entity.is_default || false,
  }
  clearApiKey.value = false
  showAddProviderDialog.value = true
}

const saveProvider = async ({ validateResult, firstError }: any) => {
  if (validateResult !== true && validateResult !== undefined) {
    MessagePlugin.warning(firstError || 'Please check the form fields')
    return
  }

  const proceed = await confirmClearIfNeeded()
  if (!proceed) return

  saving.value = true
  try {
    // Build the parameters payload using three-state semantics:
    //   - clearApiKey → send clear_api_key: true, omit api_key
    //   - user typed a value → send api_key
    //   - empty + editing → omit api_key (server preserves)
    //   - empty + creating → omit api_key (no secret to store yet)
    const paramsOut: WebSearchProviderEntity['parameters'] = {
      engine_id: providerForm.value.parameters.engine_id,
      proxy_url: providerForm.value.parameters.proxy_url,
    }
    if (clearApiKey.value) {
      paramsOut.clear_api_key = true
    } else if (providerForm.value.parameters.api_key) {
      paramsOut.api_key = providerForm.value.parameters.api_key
    }

    const data: Partial<WebSearchProviderEntity> = {
      name: providerForm.value.name.trim() || selectedProviderType.value?.name || providerForm.value.provider,
      provider: providerForm.value.provider as any,
      description: providerForm.value.description,
      parameters: paramsOut,
      is_default: providerForm.value.is_default,
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

/**
 * Credential field: stacks the label row, password input, and the optional
 * "Remove this credential" checkbox vertically. Matches the pattern in
 * McpServiceDialog and ModelEditorDialog so the whole UI reads consistently.
 */
.credential-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 20px;
}

.credential-label {
  display: block;
  font-size: 14px;
  color: var(--td-text-color-primary);
}

.clear-credential {
  :deep(.t-checkbox__label) {
    color: var(--td-error-color);
    font-size: 13px;
  }
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
