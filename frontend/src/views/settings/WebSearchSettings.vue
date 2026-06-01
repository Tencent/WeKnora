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
        :class="[`provider-card--${entity.provider}`, { 'provider-card--clickable': isProviderCardClickable() }]"
        :role="isProviderCardClickable() ? 'button' : undefined"
        :tabindex="isProviderCardClickable() ? 0 : undefined"
        @click="onProviderCardClick($event, entity)"
        @keydown.enter="onProviderCardClick($event, entity)"
      >
        <div
          class="provider-card__badge"
          :class="badgeClass(entity.provider)"
          :style="badgeStyle(entity.provider)"
          :aria-label="entity.provider"
        >
          <img
            v-if="resolveLogo(entity.provider)?.mode === 'color'"
            :src="resolveLogo(entity.provider)!.url"
            :alt="entity.provider"
            class="provider-card__badge-img"
          />
          <template v-else-if="!resolveLogo(entity.provider)">
            {{ providerInitial(entity.provider) }}
          </template>
        </div>
        <div class="provider-card__body">
          <div class="provider-card__header">
            <h3 class="provider-card__title" :title="entity.name">{{ entity.name }}</h3>
            <div
              v-if="getProviderOptions(entity).length > 0"
              class="provider-card__actions"
              @click.stop
            >
              <t-dropdown
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

    <!-- Add/Edit Drawer — 与 ModelEditorDialog / Parser / Storage 抽屉同款风格 -->
    <SettingDrawer
      v-model:visible="showAddProviderDialog"
      :title="editingProvider ? t('webSearchSettings.editProvider') : t('webSearchSettings.addProvider')"
      :class="drawerClass"
      :confirm-loading="saving"
      @confirm="saveProvider"
    >
      <!--
        Header icon — 与列表 .provider-card__badge 同款 logo/mono/fallback。
        - color logo（如 Bing/Google 彩色徽标）→ <img>，header 容器变白底 + 细边
        - mono logo（mask-image）→ ::before-style span，currentColor 染色
        - fallback：providerId 首字母 monogram
      -->
      <template v-if="selectedProviderType" #headerIcon>
        <img
          v-if="drawerLogo?.mode === 'color'"
          :src="drawerLogo.url"
          :alt="selectedProviderType.id"
          class="header-icon__img"
        />
        <span
          v-else-if="drawerLogo?.mode === 'mono'"
          class="header-icon__mono"
          :style="drawerLogoStyle"
        />
        <span v-else class="header-icon__text">{{ providerInitial(selectedProviderType.id) }}</span>
      </template>

      <!--
        Subtitle: provider 类型名 + 官方文档外链（若有）。
      -->
      <template v-if="selectedProviderType" #subtitle>
        <span>{{ selectedProviderType.name }}</span>
        <a
          v-if="selectedProviderType.docs_url"
          :href="selectedProviderType.docs_url"
          target="_blank"
          rel="noopener noreferrer"
          class="doc-link doc-link--inline"
        >
          {{ t('webSearchSettings.viewDocs') }}
          <t-icon name="link" class="link-icon" />
        </a>
      </template>

      <!--
        Test connection (footer-left, 与其他抽屉同款)。已经统一为唯一入口 —
        外层卡片菜单不再露出"测试连接"，所有测试都从这里发起。

        全部 provider 都显示按钮（包括 DuckDuckGo / SearXNG 这些"免费"的）—
        免费只是不要 api_key，不代表不需要测：DuckDuckGo 走外网可能被墙、
        SearXNG 是自托管要验 base_url 可达性。disabled 由 canTestConnection
        统一控制，缺哪个必填字段就置灰。
      -->
      <template v-if="selectedProviderType" #footer-left>
        <t-button
          variant="outline"
          :loading="testing"
          :disabled="!canTestConnection"
          @click="testConnection"
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
          {{ testing ? t('webSearchSettings.testing') : t('webSearchSettings.testConnection') }}
        </t-button>
      </template>

      <t-form ref="formRef" :data="providerForm" label-align="top" class="provider-form">
        <!-- Section 1 — 基本信息 -->
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('webSearchSettings.basicSection', '基本信息') }}</h4>

          <!-- providerType 选择器：仅在新建时可改 -->
          <div class="form-item">
            <label class="form-label required">{{ t('webSearchSettings.providerTypeLabel') }}</label>
            <t-select
              v-model="providerForm.provider"
              :disabled="!!editingProvider"
              @change="onProviderTypeChange"
            >
              <!--
                Just provider name in each option — we used to append a "免费"
                t-tag for providers that don't take an api_key, but the
                "免费"分类对用户决策没什么帮助（DuckDuckGo / SearXNG 也都
                需要可用的网络/自托管实例），反而占视觉空间。
              -->
              <t-option v-for="pt in providerTypes" :key="pt.id" :value="pt.id" :label="pt.name" />
            </t-select>
          </div>

          <div class="form-item">
            <label class="form-label">{{ t('webSearchSettings.providerNameLabel') }}</label>
            <t-input
              v-model="providerForm.name"
              :placeholder="selectedProviderType?.name || t('webSearchSettings.providerNamePlaceholder')"
            />
          </div>

          <div class="form-item">
            <label class="form-label">{{ t('webSearchSettings.providerDescLabel') }}</label>
            <t-input
              v-model="providerForm.description"
              :placeholder="t('webSearchSettings.providerDescPlaceholder')"
            />
          </div>
        </section>

        <!-- Section 2 — 连接配置（base url / api key / engine id），仅当任意字段需要时渲染 -->
        <section
          v-if="selectedProviderType?.requires_api_key || selectedProviderType?.requires_engine_id || selectedProviderType?.requires_base_url"
          class="setting-drawer__section"
        >
          <h4 class="setting-drawer__section-title">{{ t('webSearchSettings.credentialsSection', '连接配置') }}</h4>

          <div v-if="selectedProviderType?.requires_base_url" class="form-item">
            <label class="form-label required">{{ t('webSearchSettings.baseUrlLabel') }}</label>
            <t-input
              v-model="providerForm.parameters.base_url"
              :placeholder="t('webSearchSettings.baseUrlPlaceholder')"
            />
          </div>

          <!--
            Edit 模式下凭证由 CredentialResource 管理（独立的 /credentials
            子资源调用），不与本表单 submit 耦合；Create 模式下用 plain
            password input + lock prefix-icon，与 ModelEditorDialog 一致。
          -->
          <div v-if="selectedProviderType?.requires_api_key" class="form-item">
            <label class="form-label required">{{ t('webSearchSettings.apiKeyLabel') }}</label>
            <CredentialResource
              v-if="editingProvider?.id"
              :api="credentialApi"
              :fields="credentialFields"
              :meta="credentialMeta"
            />
            <t-input
              v-else
              v-model="providerForm.parameters.api_key"
              type="password"
              :placeholder="apiKeyPlaceholder"
            >
              <template #prefix-icon><t-icon name="lock-on" /></template>
            </t-input>
          </div>

          <div v-if="selectedProviderType?.requires_engine_id" class="form-item">
            <label class="form-label required">{{ t('webSearchSettings.engineIdLabel') }}</label>
            <t-input
              v-model="providerForm.parameters.engine_id"
              :placeholder="t('webSearchSettings.engineIdLabel')"
            />
          </div>
        </section>

        <!-- Section 3 — 选项（代理 / 默认） -->
        <section
          v-if="selectedProviderType?.supports_proxy || selectedProviderType"
          class="setting-drawer__section"
        >
          <h4 class="setting-drawer__section-title">{{ t('webSearchSettings.optionsSection', '选项') }}</h4>

          <div v-if="selectedProviderType?.supports_proxy" class="form-item">
            <label class="form-label">{{ t('webSearchSettings.proxyUrlLabel') }}</label>
            <t-input
              v-model="providerForm.parameters.proxy_url"
              :placeholder="t('webSearchSettings.proxyUrlPlaceholder')"
            />
            <p class="form-desc">{{ t('webSearchSettings.proxyUrlHelp') }}</p>
          </div>

          <div class="form-item">
            <label class="form-label">{{ t('webSearchSettings.setAsDefault') }}</label>
            <div class="vision-toggle">
              <t-switch v-model="providerForm.is_default" />
              <span class="form-desc form-desc--inline">{{ t('webSearchSettings.setAsDefaultDesc') }}</span>
            </div>
          </div>
        </section>
      </t-form>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
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
import { providerLogo } from './providerLogos'

const { t } = useI18n()
const authStore = useAuthStore()
const confirmDelete = useConfirmDelete()

// ===== State =====
const providerEntities = ref<WebSearchProviderEntity[]>([])
const providerTypes = ref<WebSearchProviderTypeInfo[]>([])
const showAddProviderDialog = ref(false)
const editingProvider = ref<WebSearchProviderEntity | null>(null)
const testing = ref(false)
const saving = ref(false)
const formRef = ref<any>()

// Tri-state hint icon next to the test button: null=neutral, true=just
// succeeded, false=just failed. Cleared whenever the user changes the
// underlying connection inputs (see watch() below, set up after providerForm
// is initialized so the watch's source function doesn't trip on TDZ).
const lastTestOk = ref<boolean | null>(null)

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

// Invalidate the cached test result whenever the user edits a connection
// field. Set up after providerForm is declared so the watch's source
// function — which dereferences providerForm.value on first run — doesn't
// hit a TDZ ReferenceError. proxy_url is excluded because the upstream
// call doesn't actually use it for credential validation.
watch(
  () => [
    providerForm.value.provider,
    providerForm.value.parameters?.api_key,
    providerForm.value.parameters?.engine_id,
    providerForm.value.parameters?.base_url,
  ],
  () => { lastTestOk.value = null },
)

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

// Per-provider class on the drawer — the non-scoped CSS block at the
// bottom uses .websearch-drawer--{id} to color the header-icon container
// to match the matching list-card badge.
const drawerClass = computed(() => {
  const id = providerForm.value.provider
  return id
    ? `websearch-drawer websearch-drawer--${id}`
    : 'websearch-drawer'
})

// Reuses providerLogo() so the drawer header icon matches whatever the
// list card showed for the same provider id.
const drawerLogo = computed(() => {
  const id = providerForm.value.provider
  return id ? providerLogo('websearch', id) : null
})

const drawerLogoStyle = computed((): Record<string, string> => {
  const logo = drawerLogo.value
  if (!logo || logo.mode !== 'mono') return {}
  return { '--logo-url': `url("${logo.url}")` }
})

// Whether "Test connection" can fire. New-mode requires the user to have
// typed an api_key (and engine_id / base_url where applicable); edit-mode
// can fire with no fresh api_key because the backend will fall back to
// the stored credential. Free providers don't show the button at all.
const canTestConnection = computed(() => {
  const pt = selectedProviderType.value
  if (!pt) return false
  if (editingProvider.value) return true
  if (pt.requires_api_key && !providerForm.value.parameters.api_key) return false
  if (pt.requires_engine_id && !providerForm.value.parameters.engine_id) return false
  if (pt.requires_base_url && !providerForm.value.parameters.base_url) return false
  return true
})

// 卡片首字母徽章。复用 providerType 信息表，让多字节缩写也走同一处。
const providerInitial = (providerId: string) => {
  const label = providerTypes.value.find(p => p.id === providerId)?.name || providerId
  return (label.trim().charAt(0) || '?').toUpperCase()
}

// 见 VectorStoreSettings 的同名注释：返回 --logo-url 给 ::before 用 mask 渲染。
const resolveLogo = (providerId: string) => providerLogo('websearch', providerId)

const badgeClass = (providerId: string) => {
  const m = resolveLogo(providerId)?.mode
  return {
    'provider-card__badge--logo': !!m,
    'provider-card__badge--color': m === 'color',
    'provider-card__badge--mono': m === 'mono',
  }
}

const badgeStyle = (providerId: string): Record<string, string> => {
  const logo = resolveLogo(providerId)
  return logo?.mode === 'mono' ? { '--logo-url': `url("${logo.url}")` } : {}
}

const providerTypeLabel = (providerId: string) => {
  return providerTypes.value.find(p => p.id === providerId)?.name || providerId
}

// ===== Methods =====
const onProviderTypeChange = () => {
  providerForm.value.parameters = {}
  lastTestOk.value = null
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
  lastTestOk.value = null
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
  lastTestOk.value = null
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

    let ok = false
    if (editingProvider.value && !data.parameters.api_key) {
      const res = await testWebSearchProvider(editingProvider.value.id!)
      ok = !!res.success
      if (res.success) {
        MessagePlugin.success(t('webSearchSettings.toasts.testSuccess'))
      } else {
        MessagePlugin.error(res.error || t('webSearchSettings.toasts.testFailed'))
      }
    } else {
      const res = await testWebSearchProvider(undefined, data)
      ok = !!res.success
      if (res.success) {
        MessagePlugin.success(t('webSearchSettings.toasts.testSuccess'))
      } else {
        MessagePlugin.error(res.error || t('webSearchSettings.toasts.testFailed'))
      }
    }
    lastTestOk.value = ok
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('webSearchSettings.toasts.testFailed'))
    lastTestOk.value = false
  } finally {
    testing.value = false
  }
}

const isProviderCardClickable = () => authStore.hasRole('admin')

const onProviderCardClick = (event: Event, entity: WebSearchProviderEntity) => {
  if (!isProviderCardClickable()) return
  if (event.type === 'keydown') {
    const ke = event as KeyboardEvent
    if (ke.key !== 'Enter' && ke.key !== ' ') return
    ke.preventDefault()
  }
  const target = event.target as HTMLElement | null
  if (target?.closest('.provider-card__actions')) return
  editProvider(entity)
}

const getProviderOptions = (_entity: WebSearchProviderEntity) => {
  // Web search providers carry external API credentials; the backend
  // gates every mutation/test behind Admin+ (RegisterWebSearchProviderRoutes).
  // Hide the action menu entirely for non-Admins so they don't trip 403s.
  // 测试连接已挪到编辑抽屉的 footer，不再放在外层菜单里 — 单一入口减少
  // 用户疑惑（"为什么有两个测试入口，结果一样吗？"）。
  if (!authStore.hasRole('admin')) {
    return []
  }
  return [
    { content: t('common.edit'), value: 'edit' },
    { content: t('common.delete'), value: 'delete', theme: 'error' as const }
  ]
}

const handleMenuAction = (data: { value: string }, entity: WebSearchProviderEntity) => {
  switch (data.value) {
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

  &--clickable {
    cursor: pointer;

    &:hover {
      border-color: var(--td-brand-color-3, var(--td-brand-color));
      box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }
  }
}

.provider-card__actions {
  flex-shrink: 0;
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

// 真实品牌 logo：白底 + 细边，logo 用 mask-image 染成 currentColor（沿用品牌色）。
// 多套一层 .provider-card 以胜过 `.provider-card--<id> .provider-card__badge` 的具体规则。
.provider-card .provider-card__badge--logo {
  background: var(--td-bg-color-container, #fff);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.provider-card .provider-card__badge--mono::before {
  content: '';
  width: 22px;
  height: 22px;
  background-color: currentColor;
  -webkit-mask-image: var(--logo-url);
  -webkit-mask-position: center;
  -webkit-mask-repeat: no-repeat;
  -webkit-mask-size: contain;
  mask-image: var(--logo-url);
  mask-position: center;
  mask-repeat: no-repeat;
  mask-size: contain;
}

.provider-card__badge-img {
  width: 24px;
  height: 24px;
  object-fit: contain;
  display: block;
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
  // 百度官方主色（搜索框 du 标识那个蓝），#2932E1。低饱和版用 12% alpha
  // 浅底，跟其他 provider 一致。之前误填红色（混淆了百度地图等子产品）。
  background: rgba(41, 50, 225, 0.12);
  color: #2932E1;
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
.provider-card:focus-within .provider-card__more,
.provider-card__actions:focus-within .provider-card__more {
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

// 隐藏 t-form 默认的 form-item 容器 — 我们走自定义 .form-item / .form-label。
:deep(.t-form) .t-form-item {
  display: none;
}

.vision-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
}

// ---- footer-left 测试按钮的状态 icon（与 ModelEditorDialog/MCP 同款） ----
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

// ---- Header 图标徽章 ----
.header-icon__img {
  width: 24px;
  height: 24px;
  object-fit: contain;
  display: block;
}

.header-icon__mono {
  display: inline-block;
  width: 22px;
  height: 22px;
  background-color: currentColor;
  -webkit-mask-image: var(--logo-url);
  -webkit-mask-position: center;
  -webkit-mask-repeat: no-repeat;
  -webkit-mask-size: contain;
  mask-image: var(--logo-url);
  mask-position: center;
  mask-repeat: no-repeat;
  mask-size: contain;
}

.header-icon__text {
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

.doc-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-brand-color);
  text-decoration: none;
  transition: color 0.15s ease;

  &:hover {
    color: var(--td-brand-color-active);
  }

  .link-icon {
    font-size: 14px;
  }

  &--inline {
    margin-left: 6px;
    font-size: 12px;
    font-weight: 500;
    vertical-align: baseline;

    .link-icon {
      font-size: 12px;
    }
  }
}
</style>

<!--
  Non-scoped block: per-provider header-icon coloring + color-logo
  background tweak. Same pattern as Storage/Parser drawers — these rules
  must be global so they always reach the t-drawer panel even if its
  scoped data-attribute is dropped in some builds. Each rule mirrors the
  matching .provider-card--{id} .provider-card__badge from the scoped
  block above so list-card → drawer hand-off stays visually continuous.
-->
<style lang="less">
// 彩色 logo 时给 header-icon 容器一个白底 + 1px 边，避免品牌色浅底压在
// 彩色图标上影响对比度。
.websearch-drawer .setting-drawer__header-icon:has(.header-icon__img) {
  background: var(--td-bg-color-container, #fff);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.websearch-drawer--duckduckgo .setting-drawer__header-icon {
  background: rgba(222, 88, 51, 0.12);
  color: #DE5833;
}
.websearch-drawer--bing .setting-drawer__header-icon {
  background: rgba(0, 137, 255, 0.12);
  color: #0089FF;
}
.websearch-drawer--google .setting-drawer__header-icon {
  background: rgba(66, 133, 244, 0.12);
  color: #4285F4;
}
.websearch-drawer--tavily .setting-drawer__header-icon {
  background: rgba(98, 53, 187, 0.12);
  color: #6235BB;
}
.websearch-drawer--baidu .setting-drawer__header-icon {
  background: rgba(41, 50, 225, 0.12);
  color: #2932E1;
}
.websearch-drawer--searxng .setting-drawer__header-icon {
  background: rgba(33, 86, 137, 0.12);
  color: #215689;
}
.websearch-drawer--ollama .setting-drawer__header-icon {
  background: rgba(70, 70, 70, 0.12);
  color: #464646;
}
</style>
