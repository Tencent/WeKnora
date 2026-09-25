<template>
  <SettingDrawer
    :visible="visible"
    :title="title"
    :description="plugin ? `${plugin.id} · v${plugin.active_version}` : ''"
    icon="app"
    width="640px"
    :confirm-text="t('common.save')"
    :confirm-loading="saving"
    :confirm-disabled="!systemSchema || saving"
    :hide-footer="!systemSchema"
    @update:visible="(v: boolean) => emit('update:visible', v)"
    @confirm="saveConfig"
  >
    <template v-if="plugin">
      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('pluginAdmin.detail.overview') }}</h4>
        <dl class="facts">
          <dt>{{ t('pluginAdmin.publisher') }}</dt>
          <dd>{{ manifest?.publisher.name || manifest?.publisher.id }}</dd>
          <dt>{{ t('pluginAdmin.detail.runtime') }}</dt>
          <dd>{{ te(`pluginAdmin.runtime.${plugin.runtime}`) ? t(`pluginAdmin.runtime.${plugin.runtime}`) : plugin.runtime }}</dd>
          <dt>{{ t('pluginAdmin.detail.source') }}</dt>
          <dd>
            {{ t(`pluginAdmin.source.${plugin.source?.kind ?? 'upload'}`) }}
            <code v-if="plugin.source?.url" class="facts__code">{{ plugin.source.url }}</code>
          </dd>
          <template v-if="manifest?.homepage">
            <dt>{{ t('pluginAdmin.detail.homepage') }}</dt>
            <dd><a :href="manifest.homepage" target="_blank" rel="noopener noreferrer">{{ manifest.homepage }}</a></dd>
          </template>
          <template v-if="manifest?.license">
            <dt>{{ t('pluginAdmin.detail.license') }}</dt>
            <dd>{{ manifest.license }}</dd>
          </template>
          <template v-if="manifest?.engines?.weknora">
            <dt>{{ t('pluginAdmin.detail.engines') }}</dt>
            <dd><code class="facts__code">{{ manifest.engines.weknora }}</code></dd>
          </template>
        </dl>
        <ul v-if="contributions.length" class="line-list">
          <li v-for="c in contributions" :key="c.id">
            <span class="line-list__tag">{{ t(`pluginCenter.points.${c.point}`) }}</span>
            <span>{{ c.name }}</span>
            <code v-if="c.detail" class="line-list__muted">{{ c.detail }}</code>
          </li>
        </ul>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('pluginAdmin.detail.nodes') }}</h4>
        <p v-if="instanceError" class="form-desc">{{ instanceError }}</p>
        <p v-else-if="instances.length === 0" class="form-desc">{{ t('pluginAdmin.detail.noNodes') }}</p>
        <ul v-else class="line-list">
          <li v-for="n in instances" :key="n.node">
            <t-tag size="small" variant="light" :theme="n.state === 'ready' ? 'success' : 'danger'">
              {{ t(`pluginAdmin.nodeState.${n.state}`) }}
            </t-tag>
            <code>{{ n.node }}</code>
            <span class="line-list__muted">v{{ n.version }}</span>
            <span v-if="n.error" class="line-list__error">{{ n.error }}</span>
          </li>
        </ul>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('pluginAdmin.detail.versions') }}</h4>
        <ul class="line-list">
          <li v-for="v in versions" :key="v.version">
            <span class="version">v{{ v.version }}</span>
            <code class="line-list__muted">{{ shortDigest(v.digest) }}</code>
            <span class="line-list__muted">{{ formatBytes(v.size) }} · {{ formatDate(v.created_at) }}</span>
            <t-tag v-if="v.version === plugin.active_version" size="small" variant="light" theme="primary">
              {{ t('pluginAdmin.detail.active') }}
            </t-tag>
            <t-popconfirm
              v-else
              :content="t('pluginAdmin.detail.activateConfirm', { version: v.version })"
              @confirm="activate(v.version)"
            >
              <t-button size="small" variant="text" theme="primary" :loading="activating === v.version">
                {{ compareVersions(v.version, plugin.active_version) < 0 ? t('pluginAdmin.detail.rollback') : t('pluginAdmin.detail.activate') }}
              </t-button>
            </t-popconfirm>
          </li>
        </ul>
      </section>

      <section v-if="systemSchema" class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('pluginAdmin.detail.systemConfig') }}</h4>
        <p class="form-desc">{{ t('pluginAdmin.detail.systemConfigHint') }}</p>
        <SchemaForm v-model="configValues" :schema="systemSchema" :errors="configErrors" />
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('pluginAdmin.detail.danger') }}</h4>
        <div class="danger-row">
          <span class="form-desc">{{ t('pluginAdmin.detail.uninstallHint') }}</span>
          <t-popconfirm theme="danger" :content="t('pluginAdmin.detail.uninstallConfirm')" @confirm="uninstall">
            <t-button theme="danger" variant="outline" :loading="uninstalling">
              {{ t('pluginAdmin.detail.uninstall') }}
            </t-button>
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

import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import SchemaForm from '@/components/schema-form/SchemaForm.vue'
import { validateConfig, type ConfigSchema, type ConfigValue, type FieldError } from '@/components/schema-form/schema'
import { getPlugin, type PluginInstance } from '@/api/plugin'
import {
  activatePluginVersion,
  getPluginSystemConfig,
  uninstallPlugin,
  updatePluginSystemConfig,
  type InstalledPlugin,
} from '@/api/system/plugins'
import { localizedText } from '@/utils/localizedText'

import {
  compareVersions,
  contributionLines,
  formatBytes,
  hasSystemConfig,
  shortDigest,
  sortVersions,
} from '../pluginManagementState'

const props = defineProps<{ visible: boolean; plugin: InstalledPlugin | null }>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  changed: [plugin: InstalledPlugin]
  removed: [id: string]
}>()

const { t, te, locale } = useI18n()

const manifest = computed(() => props.plugin?.manifest)
const title = computed(() => (manifest.value ? localizedText(manifest.value.name, locale.value) : props.plugin?.id ?? ''))
const contributions = computed(() => (manifest.value ? contributionLines(manifest.value, locale.value) : []))
const versions = computed(() => sortVersions(props.plugin?.versions ?? []))

const instances = ref<PluginInstance[]>([])
const instanceError = ref('')
const systemSchema = ref<ConfigSchema | null>(null)
const configValues = ref<ConfigValue>({})
const configErrors = ref<FieldError[]>([])
const saving = ref(false)
const activating = ref('')
const uninstalling = ref(false)

const formatDate = (s: string) => (s ? new Date(s).toLocaleString(locale.value) : '')

async function loadNodes(id: string) {
  instanceError.value = ''
  try {
    const res = await getPlugin(id)
    instances.value = res.data.instances ?? []
    instanceError.value = res.data.instanceError ?? ''
  } catch {
    // A plugin disabled platform-wide is not in the catalog; it runs nowhere.
    instances.value = []
  }
}

async function loadConfig(id: string) {
  systemSchema.value = null
  configErrors.value = []
  if (!hasSystemConfig(manifest.value)) return
  try {
    const res = await getPluginSystemConfig(id)
    systemSchema.value = res.data.schema
    configValues.value = res.data.values ?? {}
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginAdmin.detail.configLoadFailed'))
  }
}

watch(
  () => [props.visible, props.plugin?.id, props.plugin?.active_version, props.plugin?.desired_state] as const,
  ([visible, id]) => {
    if (!visible || !id) return
    void loadNodes(id)
    void loadConfig(id)
  },
  { immediate: true },
)

async function saveConfig() {
  if (!props.plugin || !systemSchema.value) return
  configErrors.value = validateConfig(systemSchema.value, configValues.value, { skipSecrets: true })
  if (configErrors.value.length) return
  saving.value = true
  try {
    const res = await updatePluginSystemConfig(props.plugin.id, configValues.value)
    configValues.value = res.data.values ?? {}
    MessagePlugin.success(t('pluginAdmin.detail.configSaved'))
  } catch (e: any) {
    const details = e?.details ?? e?.error?.details
    if (Array.isArray(details)) configErrors.value = details
    MessagePlugin.error(e?.message || t('pluginAdmin.detail.configSaveFailed'))
  } finally {
    saving.value = false
  }
}

async function activate(version: string) {
  if (!props.plugin) return
  activating.value = version
  try {
    const res = await activatePluginVersion(props.plugin.id, version)
    emit('changed', res.data)
    MessagePlugin.success(t('pluginAdmin.detail.activated', { version }))
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginAdmin.detail.activateFailed'))
  } finally {
    activating.value = ''
  }
}

async function uninstall() {
  if (!props.plugin) return
  const id = props.plugin.id
  uninstalling.value = true
  try {
    await uninstallPlugin(id)
    MessagePlugin.success(t('pluginAdmin.detail.uninstalled'))
    emit('removed', id)
    emit('update:visible', false)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginAdmin.detail.uninstallFailed'))
  } finally {
    uninstalling.value = false
  }
}
</script>

<style lang="less" scoped>
.facts {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 6px 16px;
  margin: 0;
  font-size: var(--app-text-sm);

  dt {
    color: var(--td-text-color-secondary);
  }

  dd {
    margin: 0;
    color: var(--td-text-color-primary);
    word-break: break-all;
  }

  &__code {
    font-size: var(--app-text-xs);
    color: var(--td-text-color-placeholder);
    margin-left: 6px;
  }
}

.line-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;

  li {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-primary);
  }

  &__tag {
    font-size: var(--app-text-xs);
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
    border-radius: var(--app-radius-xs);
    padding: 1px 6px;
  }

  &__muted {
    font-size: var(--app-text-xs);
    color: var(--td-text-color-placeholder);
    word-break: break-all;
  }

  &__error {
    flex-basis: 100%;
    font-size: var(--app-text-xs);
    color: var(--td-error-color);
  }
}

.version {
  font-family: var(--app-font-family-mono);
  font-weight: 500;
}

.form-desc {
  margin: 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.danger-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
</style>
