<template>
  <div class="websearch-settings">
    <div class="section-header">
      <h2>{{ t('webSearchSettings.title') }}</h2>
      <p class="section-description">{{ t('webSearchSettings.description') }}</p>
    </div>

    <div class="settings-toolbar">
      <h3>{{ t('webSearchSettings.providersTitle') }}</h3>
      <t-button v-if="authStore.hasRole('admin')" theme="primary" variant="outline" size="small" @click="openAddDialog">
        <template #icon><add-icon /></template>
        {{ t('webSearchSettings.addProvider') }}
      </t-button>
    </div>

    <!-- Provider List —— 与 ModelSettings 的卡片同形：左侧标识徽章 + 标题 / 副标题 / proxy URL 三段式。
         不复用 SettingCard 的原因和 Models 一样：每页有微妙不同的右上侧栏需求（这里没有控件，
         Mcp 有开关），SettingCard 仍服务于其它消费者。 -->
    <div v-if="providerEntities.length > 0" class="provider-grid">
      <div
        v-for="entity in providerEntities"
        :key="entity.id"
        class="provider-card"
        :class="`provider-card--${entity.provider}`"
      >
        <div class="provider-card__badge" :aria-label="entity.provider">
          {{ providerInitial(entity.provider) }}
        </div>
        <div class="provider-card__body">
          <div class="provider-card__header">
            <h3 class="provider-card__title" :title="entity.name">{{ entity.name }}</h3>
            <span v-if="entity.is_default" class="provider-card__pill provider-card__pill--success">
              {{ t('webSearchSettings.default') }}
            </span>
            <span v-if="isEntityFree(entity)" class="provider-card__pill provider-card__pill--warning">
              {{ t('webSearchSettings.free') }}
            </span>
            <t-dropdown
              v-if="getProviderOptions(entity).length > 0"
              :options="getProviderOptions(entity)"
              placement="bottom-right"
              attach="body"
              trigger="click"
              @click="(data: any) => handleMenuAction({ value: data.value }, entity)"
            >
              <t-button variant="text" shape="square" size="small" class="provider-card__more">
                <t-icon name="ellipsis" />
              </t-button>
            </t-dropdown>
          </div>
          <div class="provider-card__subtitle">
            <span class="provider-card__type">{{ providerTypeLabel(entity.provider) }}</span>
            <template v-if="entity.description">
              <span class="provider-card__sep">·</span>
              <span class="provider-card__desc" :title="entity.description">{{ entity.description }}</span>
            </template>
          </div>
          <div v-if="entity.parameters?.proxy_url" class="provider-card__url" :title="entity.parameters.proxy_url">
            {{ entity.parameters.proxy_url }}
          </div>
        </div>
      </div>
    </div>

    <!-- Empty State -->
    <div v-else class="empty-state">
      <t-empty :description="t('webSearchSettings.noProvidersDesc')">
        <t-button v-if="authStore.hasRole('admin')" theme="primary" variant="outline" size="small" @click="openAddDialog">
          <template #icon><add-icon /></template>
          {{ t('webSearchSettings.addProvider') }}
        </t-button>
      </t-empty>
    </div>

    <!-- Add/Edit Drawer -->
    <SettingDrawer v-model:visible="showAddProviderDialog"
      :title="editingProvider ? t('webSearchSettings.editProvider') : t('webSearchSettings.addProvider')"
      :confirm-loading="saving" @confirm="saveProvider">
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
          <t-input v-model="providerForm.name"
            :placeholder="selectedProviderType?.name || t('webSearchSettings.providerNamePlaceholder')" />
        </t-form-item>

        <t-form-item :label="t('webSearchSettings.providerDescLabel')" name="description">
          <t-input v-model="providerForm.description" :placeholder="t('webSearchSettings.providerDescPlaceholder')" />
        </t-form-item>

        <template
          v-if="selectedProviderType?.requires_api_key || selectedProviderType?.requires_engine_id || selectedProviderType?.requires_base_url">
          <div class="form-divider"></div>

          <div class="credentials-hint" v-if="selectedProviderType?.docs_url">
            <a :href="selectedProviderType.docs_url" target="_blank" rel="noopener noreferrer" class="doc-link">
              {{ t('webSearchSettings.viewDocs') }}
              <t-icon name="link" class="link-icon" />
            </a>
          </div>

          <t-form-item v-if="selectedProviderType?.requires_base_url" :label="t('webSearchSettings.baseUrlLabel')"
            name="parameters.base_url">
            <t-input v-model="providerForm.parameters.base_url"
              :placeholder="t('webSearchSettings.baseUrlPlaceholder')" />
          </t-form-item>
          <!--
            Edit mode: credential is managed by the shared <CredentialResource>
            card via the /credentials subresource. Create mode keeps a plain
            input so the initial api_key flows in with the first POST.

            API key is mandatory for every requires_api_key=true provider
            (validation in service/web_search_provider.go). The component's
            "Remove" action is still available because a user might want to
            rotate via remove + re-add, but in normal use they will use
            "Replace" instead.
          -->
          <div v-if="selectedProviderType?.requires_api_key" class="credential-field">
            <label class="credential-label">{{ t('webSearchSettings.apiKeyLabel') }}</label>
            <CredentialResource v-if="editingProvider?.id" :api="credentialApi" :fields="credentialFields"
              :meta="credentialMeta" />
            <t-input v-else v-model="providerForm.parameters.api_key" type="password"
              :placeholder="apiKeyPlaceholder" />
          </div>
          <t-form-item v-if="selectedProviderType?.requires_engine_id" :label="t('webSearchSettings.engineIdLabel')"
            name="parameters.engine_id">
            <t-input v-model="providerForm.parameters.engine_id" :placeholder="t('webSearchSettings.engineIdLabel')" />
          </t-form-item>
        </template>

        <t-form-item v-if="selectedProviderType?.supports_proxy" :label="t('webSearchSettings.proxyUrlLabel')"
          name="parameters.proxy_url">
          <t-input v-model="providerForm.parameters.proxy_url"
            :placeholder="t('webSearchSettings.proxyUrlPlaceholder')" />
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
        <t-button v-if="selectedProviderType && !isProviderFree(selectedProviderType)" theme="default" variant="outline"
          :loading="testing" @click="testConnection">
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
  putWebSearchProviderCredentials,
  deleteWebSearchProviderCredentialField,
  type WebSearchProviderEntity,
  type WebSearchProviderTypeInfo,
  type WebSearchCredentialField,
} from '@/api/web-search-provider'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import CredentialResource, {
  type CredentialFieldDef,
  type CredentialResourceApi,
} from '@/components/credentials/CredentialResource.vue'
import { useConfirmDelete } from '@/components/settings/useConfirmDelete'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const authStore = useAuthStore()
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
  parameters: { api_key?: string; engine_id?: string; base_url?: string; proxy_url?: string }
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

// Create-mode placeholder (edit mode replaces the input with
// <CredentialResource>, which has its own placeholder).
const apiKeyPlaceholder = computed(() => t('webSearchSettings.apiKeyPlaceholder'))

const credentialFields = computed<CredentialFieldDef<WebSearchCredentialField>[]>(() => [
  { key: 'api_key', label: t('webSearchSettings.apiKeyLabel') as string },
])

const credentialApi = computed<CredentialResourceApi<WebSearchCredentialField>>(() => {
  const id = editingProvider.value?.id ?? ''
  return {
    save: async (patch) => {
      const meta = await putWebSearchProviderCredentials(id, patch)
      return meta.fields
    },
    remove: async (field) => {
      await deleteWebSearchProviderCredentialField(id, field)
    },
  }
})

// Initial configured? from the main provider response (embedded server-side
// in dto.WebSearchProviderResponse.Credentials).
const credentialMeta = computed(() => editingProvider.value?.credentials ?? {
  api_key: { configured: false },
})

const isProviderFree = (providerType: WebSearchProviderTypeInfo) => {
  // "Free" here means no upstream-paid credentials are required. Self-hosted
  // providers (requires_base_url) are still free to use even though they need
  // an instance URL, so they should keep the free badge.
  return !providerType.requires_api_key && !providerType.requires_engine_id
}

const isEntityFree = (entity: WebSearchProviderEntity) => {
  const pt = providerTypes.value.find(p => p.id === entity.provider)
  return pt ? isProviderFree(pt) : false
}

// 卡片首字母徽章。复用 providerType 信息表，让多字节缩写也走同一处。
const providerInitial = (providerId: string) => {
  const label = providerTypes.value.find(p => p.id === providerId)?.name || providerId
  return (label.trim().charAt(0) || '?').toUpperCase()
}

const providerTypeLabel = (providerId: string) => {
  return providerTypes.value.find(p => p.id === providerId)?.name || providerId
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
      // Never pre-fill the api_key — even the redacted placeholder from the
      // server is ignored so that "non-empty means user typed it" holds.
      api_key: '',
      engine_id: entity.parameters?.engine_id || '',
      base_url: entity.parameters?.base_url || '',
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
    // Build the parameters payload. api_key only flows in on initial
    // create — edit mode commits credentials through <CredentialResource>
    // (a dedicated PUT /credentials call) before this save runs.
    const paramsOut: WebSearchProviderEntity['parameters'] = {
      engine_id: providerForm.value.parameters.engine_id,
      base_url: providerForm.value.parameters.base_url,
      proxy_url: providerForm.value.parameters.proxy_url,
    }
    if (!editingProvider.value && providerForm.value.parameters.api_key) {
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
  // Web search providers carry external API credentials; the backend
  // gates every mutation/test behind Admin+ (RegisterWebSearchProviderRoutes).
  // Hide the action menu entirely for non-Admins so they don't trip 403s.
  if (!authStore.hasRole('admin')) {
    return []
  }
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
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
}

// 卡片视觉与 ModelSettings 的 model-card 同构（徽章 + 标题 / 副标题 / url 三段式）。
// 现阶段两份样式各自维护避免过度抽象；如果后续 Mcp / 第四个消费者出现，
// 再把共用片段抽到 components/settings/ 下的基类。
.provider-card {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 14px 14px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
  min-width: 0;

  &:hover {
    border-color: var(--td-brand-color-3, var(--td-brand-color));
    box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
  }
}

.provider-card__badge {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: 1px;
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.02em;
  // 默认色，被 provider 修饰覆盖
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}

// 各搜索源的徽章配色 —— 不强求与官方 logo 一致，挑同色系低饱和版即可。
.provider-card--duckduckgo .provider-card__badge {
  background: rgba(222, 88, 51, 0.12);
  color: #DE5833;
}
.provider-card--bing .provider-card__badge {
  background: rgba(0, 137, 255, 0.12);
  color: #0089FF;
}
.provider-card--google .provider-card__badge {
  background: rgba(66, 133, 244, 0.12);
  color: #4285F4;
}
.provider-card--tavily .provider-card__badge {
  background: rgba(98, 53, 187, 0.12);
  color: #6235BB;
}
.provider-card--baidu .provider-card__badge {
  background: rgba(225, 38, 38, 0.12);
  color: #E12626;
}
.provider-card--searxng .provider-card__badge {
  background: rgba(33, 86, 137, 0.12);
  color: #215689;
}
.provider-card--ollama .provider-card__badge {
  background: rgba(70, 70, 70, 0.12);
  color: #464646;
}

.provider-card__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.provider-card__header {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.provider-card__title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.provider-card__pill {
  flex-shrink: 0;
  padding: 1px 6px;
  font-size: 11px;
  font-weight: 500;
  line-height: 16px;
  border-radius: 3px;

  &--success {
    color: var(--td-success-color-7, #118053);
    background: var(--td-success-color-1, #E7F7F2);
  }

  &--warning {
    color: var(--td-warning-color-7, #B85C00);
    background: var(--td-warning-color-1, #FEF3E6);
  }
}

.provider-card__more {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  padding: 2px;
  opacity: 0;
  transition: opacity 0.15s ease;

  &:hover,
  &:focus-visible {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.provider-card:hover .provider-card__more,
.provider-card:focus-within .provider-card__more {
  opacity: 1;
}

.provider-card__subtitle {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
  min-width: 0;
}

.provider-card__type {
  font-weight: 500;
}

.provider-card__sep {
  color: var(--td-text-color-placeholder);
}

.provider-card__desc {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.provider-card__url {
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 11px;
  line-height: 1.4;
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
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
</style>
