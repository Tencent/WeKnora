<template>
  <SettingDrawer
    :visible="visible"
    :title="plugin ? nameOf(plugin) : ''"
    width="620px"
    :confirm-loading="configSaving"
    :confirm-disabled="!configSchema || configSaving"
    :hide-footer="!configSchema || !canManage"
    @update:visible="(v: boolean) => emit('update:visible', v)"
    @confirm="saveConfig"
  >
    <template #headerIcon>
      <PluginBadge v-if="plugin" :manifest="plugin.manifest" size="md" />
    </template>
    <template #subtitle>
      <span v-if="plugin">{{ subtitle }}</span>
    </template>
    <template #header-actions>
      <div v-if="plugin" class="center-drawer__switch">
        <span>{{ plugin.manifest.required ? t('pluginCenter.required') : t('pluginCenter.detail.enabledHere') }}</span>
        <t-switch
          v-if="!plugin.manifest.required"
          size="small"
          :model-value="plugin.enabled"
          :disabled="!canManage || pending"
          :loading="pending"
          @update:model-value="(v: boolean) => emit('toggle', v)"
        />
      </div>
    </template>

    <template v-if="plugin">
      <section class="setting-drawer__section">
        <p v-if="description" class="center-drawer__about">{{ description }}</p>
        <PluginCapabilityList :manifest="plugin.manifest" />
      </section>

      <div v-if="configLoading" class="center-drawer__state"><t-loading size="small" /></div>

      <section v-if="configSchema && canManage" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('pluginCenter.detail.config') }}</h4>
        <p class="center-drawer__hint">{{ t('pluginCenter.configDescription') }}</p>
        <SchemaForm v-model="configValues" :schema="configSchema" :errors="configErrors" />
      </section>

      <section v-if="webhooks.length && canManage" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('pluginCenter.webhooks') }}</h4>
        <p class="center-drawer__hint">{{ t('pluginCenter.webhooksHint') }}</p>
        <div v-for="hook in webhooks" :key="hook.id" class="center-drawer__webhook">
          <div v-if="webhooks.length > 1" class="center-drawer__webhook-name">{{ localizedText(hook.name, locale) }}</div>
          <div class="center-drawer__copy">
            <code>{{ webhookUrl(hook, origin) }}</code>
            <t-button size="small" variant="text" theme="primary"
              @click="copyWithToast(webhookUrl(hook, origin), 'pluginCenter.webhookCopied')">
              {{ t('pluginCenter.copy') }}
            </t-button>
          </div>
        </div>
      </section>

      <section v-if="owned && canManage" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('pluginCenter.own.section') }}</h4>
        <div class="center-drawer__row">
          <div class="center-drawer__row-text">
            <div>{{ t('pluginAdmin.detail.remoteUrl') }}</div>
            <code v-if="!editingUrl">{{ owned.remote_url }}</code>
            <t-input v-else v-model="urlDraft" size="small" :disabled="urlSaving" @enter="saveUrl" />
          </div>
          <template v-if="editingUrl">
            <t-button size="small" theme="primary" :loading="urlSaving" :disabled="!isPackageUrl(urlDraft)" @click="saveUrl">
              {{ t('common.save') }}
            </t-button>
            <t-button size="small" variant="text" :disabled="urlSaving" @click="editingUrl = false">{{ t('common.cancel') }}</t-button>
          </template>
          <t-button v-else size="small" variant="outline" @click="startEditUrl">{{ t('pluginCenter.own.editUrl') }}</t-button>
        </div>
        <div class="center-drawer__row">
          <div class="center-drawer__row-text">
            <div>{{ t('pluginAdmin.detail.rotateSecret') }}</div>
            <p>{{ t('pluginAdmin.detail.rotateHint') }}</p>
          </div>
          <t-popconfirm :content="t('pluginAdmin.detail.rotateConfirm')" @confirm="rotate">
            <t-button size="small" variant="outline" :loading="rotating">{{ t('pluginAdmin.detail.rotateSecret') }}</t-button>
          </t-popconfirm>
        </div>
        <div class="center-drawer__row">
          <div class="center-drawer__row-text">
            <div>{{ t('pluginCenter.own.update') }}</div>
            <p>{{ t('pluginCenter.own.updateHint') }}</p>
          </div>
          <t-button size="small" variant="outline" :disabled="!ownAllowed" @click="emit('update-package')">
            {{ t('pluginCenter.own.update') }}
          </t-button>
        </div>
        <div class="center-drawer__row">
          <div class="center-drawer__row-text">
            <div>{{ t('pluginCenter.own.remove') }}</div>
            <p>{{ t('pluginCenter.own.removeConfirm') }}</p>
          </div>
          <t-popconfirm theme="danger" :content="t('pluginCenter.own.removeConfirm')" @confirm="remove">
            <t-button size="small" theme="danger" variant="outline" :loading="removing">{{ t('pluginCenter.own.remove') }}</t-button>
          </t-popconfirm>
        </div>
      </section>
    </template>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'

import { getPluginConfig, listPluginWebhooks, updatePluginConfig, type PluginWebhook, type TenantPlugin } from '@/api/plugin'
import type { InstalledPlugin } from '@/api/system/plugins'
import { rotateTenantPluginSecret, setTenantPluginRemoteUrl, uninstallTenantPlugin } from '@/api/tenantPlugins'
import PluginBadge from '@/components/plugins/PluginBadge.vue'
import PluginCapabilityList from '@/components/plugins/PluginCapabilityList.vue'
import SchemaForm from '@/components/schema-form/SchemaForm.vue'
import { pluginFormSource } from '@/components/schema-form/pluginSource'
import { provideSchemaFormSource } from '@/components/schema-form/source'
import { validateConfig, type ConfigSchema, type ConfigValue, type FieldError } from '@/components/schema-form/schema'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { copyWithToast } from '@/utils/clipboard'
import { localizedText } from '@/utils/localizedText'
import { isPackageUrl } from '@/views/system/pluginManagementState'

import { hasTenantConfig, hasWebhooks, webhookUrl } from '../pluginCenterState'

// One plugin in the workspace: what it adds and where to find it, its
// workspace configuration, its webhook addresses, and — for the workspace's
// own remote plugins — where it runs and its signing secret.
const props = defineProps<{
  visible: boolean
  plugin: TenantPlugin | null
  owned?: InstalledPlugin
  ownAllowed: boolean
  canManage: boolean
  pending: boolean
}>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  toggle: [enabled: boolean]
  'update-package': []
  changed: [view?: InstalledPlugin]
  removed: []
}>()

const { t, locale } = useI18n()
const origin = window.location.origin

const nameOf = (p: TenantPlugin) => localizedText(p.manifest.name, locale.value) || p.manifest.id
const description = computed(() => (props.plugin ? localizedText(props.plugin.manifest.description, locale.value) : ''))
const subtitle = computed(() => {
  const m = props.plugin?.manifest
  if (!m) return ''
  const kind = m.builtin ? t('pluginCenter.builtin') : props.owned ? t('pluginCenter.own.tag') : t('pluginCenter.installed')
  return [m.publisher.name || m.publisher.id, m.builtin ? '' : `v${m.version}`, kind].filter(Boolean).join(' · ')
})

// The workspace's own remote plugin.
const editingUrl = ref(false)
const urlDraft = ref('')
const urlSaving = ref(false)
const rotating = ref(false)
const removing = ref(false)

const configSchema = ref<ConfigSchema | null>(null)
const configValues = ref<ConfigValue>({})
const configErrors = ref<FieldError[]>([])
const configLoading = ref(false)
const configSaving = ref(false)
const webhooks = ref<PluginWebhook[]>([])

// x-options / x-oauth fields of the workspace configuration ask the plugin.
provideSchemaFormSource(pluginFormSource({
  target: () => (props.plugin ? { pluginId: props.plugin.manifest.id, scope: 'tenant' } : undefined),
  values: () => configValues.value,
}))

watch(
  () => [props.visible, props.plugin?.manifest.id] as const,
  async ([visible]) => {
    editingUrl.value = false
    if (!visible || !props.plugin) return
    const m = props.plugin.manifest
    configSchema.value = null
    configValues.value = {}
    configErrors.value = []
    webhooks.value = []
    if (!props.canManage || (!hasTenantConfig(m) && !hasWebhooks(m))) return
    configLoading.value = true
    try {
      const [config, hooks] = await Promise.all([
        hasTenantConfig(m) ? getPluginConfig(m.id) : null,
        hasWebhooks(m) ? listPluginWebhooks(m.id) : null,
      ])
      if (config) {
        configSchema.value = config.data.schema
        configValues.value = config.data.values ?? {}
      }
      webhooks.value = hooks?.data ?? []
    } catch (e: any) {
      MessagePlugin.error(e?.message || t('pluginCenter.configLoadFailed'))
    } finally {
      configLoading.value = false
    }
  },
  { immediate: true },
)

async function saveConfig() {
  const p = props.plugin
  if (!p || !configSchema.value) return
  configErrors.value = validateConfig(configSchema.value, configValues.value)
  if (configErrors.value.length) return
  configSaving.value = true
  try {
    const res = await updatePluginConfig(p.manifest.id, configValues.value)
    configValues.value = res.data.values ?? {}
    MessagePlugin.success(t('pluginCenter.configSaved'))
    emit('update:visible', false)
  } catch (e: any) {
    const details = e?.error?.details
    if (Array.isArray(details)) configErrors.value = details
    MessagePlugin.error(e?.message || t('pluginCenter.configSaveFailed'))
  } finally {
    configSaving.value = false
  }
}

function startEditUrl() {
  urlDraft.value = props.owned?.remote_url ?? ''
  editingUrl.value = true
}

async function saveUrl() {
  const id = props.owned?.id
  if (!id || !isPackageUrl(urlDraft.value)) return
  urlSaving.value = true
  try {
    emit('changed', (await setTenantPluginRemoteUrl(id, urlDraft.value.trim())).data)
    editingUrl.value = false
    MessagePlugin.success(t('pluginAdmin.detail.urlSaved'))
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginAdmin.detail.urlSaveFailed'))
  } finally {
    urlSaving.value = false
  }
}

async function rotate() {
  const id = props.owned?.id
  if (!id) return
  rotating.value = true
  try {
    emit('changed', (await rotateTenantPluginSecret(id)).data)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginAdmin.detail.rotateFailed'))
  } finally {
    rotating.value = false
  }
}

async function remove() {
  const id = props.owned?.id
  if (!id) return
  removing.value = true
  try {
    await uninstallTenantPlugin(id)
    MessagePlugin.success(t('pluginCenter.own.removed'))
    emit('removed')
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginCenter.own.removeFailed'))
  } finally {
    removing.value = false
  }
}
</script>

<style lang="less" scoped>
.center-drawer__switch {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
}

.center-drawer__about {
  margin: 0 0 12px;
  font-size: var(--app-text-md);
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.center-drawer__state {
  display: flex;
  justify-content: center;
  padding: 24px 0;
}

.center-drawer__hint {
  margin: 0 0 12px;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.center-drawer__webhook + .center-drawer__webhook {
  margin-top: 12px;
}

.center-drawer__webhook-name {
  margin-bottom: 4px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
}

.center-drawer__copy {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 6px 6px 10px;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);

  code {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
  }
}

.center-drawer__row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 0;

  & + & {
    border-top: 1px solid var(--td-component-stroke);
  }
}

.center-drawer__row-text {
  flex: 1;
  min-width: 0;
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);

  p,
  code {
    display: block;
    margin: 2px 0 0;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}
</style>
