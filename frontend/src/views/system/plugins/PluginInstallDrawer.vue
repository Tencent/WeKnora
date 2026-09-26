<template>
  <SettingDrawer
    :visible="visible"
    :title="t('pluginAdmin.install.title')"
    :description="t('pluginAdmin.install.description')"
    icon="download"
    width="640px"
    :confirm-text="confirmText"
    :confirm-loading="busy"
    :confirm-disabled="!canConfirm"
    @update:visible="(v: boolean) => emit('update:visible', v)"
    @confirm="onConfirm"
  >
    <section class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ t('pluginAdmin.install.sourceSection') }}</h4>
      <div class="option-chips">
        <button
          v-for="m in (['upload', 'url'] as const)"
          :key="m"
          type="button"
          class="option-chip"
          :class="{ 'option-chip--active': mode === m }"
          :disabled="busy"
          @click="setMode(m)"
        >
          {{ t(`pluginAdmin.install.mode.${m}`) }}
        </button>
      </div>

      <div v-if="mode === 'upload'" class="form-item">
        <label class="form-label required">{{ t('pluginAdmin.install.fileLabel') }}</label>
        <div class="package-picker">
          <t-button variant="outline" :disabled="busy" @click="fileInput?.click()">
            <template #icon><t-icon name="upload" /></template>
            {{ t('pluginAdmin.install.chooseFile') }}
          </t-button>
          <span class="package-picker__name">{{ file?.name ?? t('pluginAdmin.install.noFile') }}</span>
          <input ref="fileInput" type="file" accept=".wkp,.zip" hidden @change="onFile" />
        </div>
        <p class="form-desc">{{ t('pluginAdmin.install.fileHint') }}</p>
      </div>

      <div v-else class="form-item">
        <label class="form-label required">{{ t('pluginAdmin.install.urlLabel') }}</label>
        <t-input
          v-model="url"
          :disabled="busy"
          placeholder="https://example.com/acme-search-1.0.0.wkp"
          @change="preview = null"
        />
        <p class="form-desc">{{ t('pluginAdmin.install.urlHint') }}</p>
      </div>
    </section>

    <section v-if="preview" class="setting-drawer__section">
      <h4 class="setting-drawer__section-title">{{ t('pluginAdmin.install.reviewSection') }}</h4>
      <div class="review-head">
        <div class="review-head__badge">{{ initial }}</div>
        <div class="review-head__text">
          <div class="review-head__name">
            {{ localizedText(preview.manifest.name, locale) }}
            <t-tag size="small" variant="light" :theme="changeTheme">
              {{ t(`pluginAdmin.change.${preview.change}`, { from: preview.installedVersion ?? '' }) }}
            </t-tag>
          </div>
          <div class="review-head__meta">
            {{ preview.manifest.id }} · v{{ preview.manifest.version }} · {{ formatBytes(preview.size) }}
          </div>
          <div class="review-head__meta">
            {{ t('pluginAdmin.publisher') }}: {{ preview.manifest.publisher.name || preview.manifest.publisher.id }}
          </div>
          <p v-if="description" class="review-head__desc">{{ description }}</p>
        </div>
      </div>

      <div class="review-block">
        <div class="review-block__title">{{ t('pluginAdmin.contributions') }}</div>
        <ul class="review-list">
          <li v-for="c in contributions" :key="c.id">
            <span class="review-list__point">{{ t(`pluginCenter.points.${c.point}`) }}</span>
            <span class="review-list__name">{{ c.name }}</span>
            <code v-if="c.detail" class="review-list__detail">{{ c.detail }}</code>
          </li>
        </ul>
      </div>

      <div class="review-block">
        <div class="review-block__title">{{ t('pluginAdmin.permissions') }}</div>
        <p v-if="permissions.length === 0 && hosts.length === 0" class="form-desc">
          {{ t('pluginAdmin.install.noPermissions') }}
        </p>
        <ul v-else class="review-list">
          <li v-for="h in hosts" :key="`host:${h}`">
            <span class="review-list__point">{{ t('pluginAdmin.permission.remote') }}</span>
            <code class="review-list__detail">{{ h }}</code>
          </li>
          <li v-for="p in permissions" :key="`${p.kind}:${p.value}`">
            <span class="review-list__point">{{ t(`pluginAdmin.permission.${p.kind}`) }}</span>
            <strong v-if="p.kind === 'egress' && p.value === EGRESS_ANY_HOST" class="review-list__warn">
              {{ t('pluginAdmin.permission.anyHost') }}
            </strong>
            <code v-else class="review-list__detail">{{ p.value }}</code>
          </li>
        </ul>
      </div>

      <t-alert
        v-if="needsConfig"
        theme="info"
        class="review-alert"
        :message="t('pluginAdmin.install.configNotice')"
      />
      <t-alert theme="warning" class="review-alert" :message="t('pluginAdmin.install.tenantNotice')" />
      <p class="form-desc">{{ t('pluginAdmin.install.digest') }}: <code>{{ preview.digest }}</code></p>
    </section>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'

import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  inspectPluginPackage,
  installPluginPackage,
  type InstalledPlugin,
  type PackageSource,
  type PluginPreview,
} from '@/api/system/plugins'
import { localizedText } from '@/utils/localizedText'

import {
  EGRESS_ANY_HOST,
  contributionLines,
  formatBytes,
  hasSystemConfig,
  isPackageUrl,
  permissionLines,
  remoteHosts,
} from '../pluginManagementState'
import { hasTenantConfig } from '../../settings/pluginCenterState'

// Installing is two steps on purpose: inspect shows what the package would
// add and reach, and install sends the reviewed digest so the server refuses
// a package that changed in between (a URL that now serves something else).
const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  installed: [plugin: InstalledPlugin]
}>()

const { t, locale } = useI18n()

const mode = ref<'upload' | 'url'>('upload')
const file = ref<File | null>(null)
const url = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
const preview = ref<PluginPreview | null>(null)
const busy = ref(false)

watch(
  () => props.visible,
  (v) => {
    if (!v) return
    mode.value = 'upload'
    file.value = null
    url.value = ''
    preview.value = null
  },
)

function setMode(m: 'upload' | 'url') {
  mode.value = m
  preview.value = null
}

function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  file.value = input.files?.[0] ?? null
  preview.value = null
  input.value = ''
  if (file.value) void inspect()
}

const source = computed<PackageSource | null>(() => {
  if (mode.value === 'upload') return file.value ? { file: file.value } : null
  return isPackageUrl(url.value) ? { url: url.value.trim() } : null
})

const canConfirm = computed(() => !!source.value && !busy.value)
const confirmText = computed(() =>
  preview.value ? t(`pluginAdmin.install.confirm.${preview.value.change}`) : t('pluginAdmin.install.inspect'),
)

const contributions = computed(() => (preview.value ? contributionLines(preview.value.manifest, locale.value) : []))
const permissions = computed(() => permissionLines(preview.value?.manifest.permissions))
const hosts = computed(() => (preview.value ? remoteHosts(preview.value.manifest) : []))
const description = computed(() => (preview.value ? localizedText(preview.value.manifest.description, locale.value) : ''))
const initial = computed(() =>
  (preview.value ? localizedText(preview.value.manifest.name, locale.value) : '?').trim().charAt(0).toUpperCase(),
)
const needsConfig = computed(
  () => !!preview.value && (hasSystemConfig(preview.value.manifest) || hasTenantConfig(preview.value.manifest)),
)
const changeTheme = computed(() => {
  switch (preview.value?.change) {
    case 'upgrade':
      return 'success'
    case 'downgrade':
      return 'warning'
    default:
      return 'primary'
  }
})

async function inspect() {
  if (!source.value) return
  busy.value = true
  try {
    const res = await inspectPluginPackage(source.value)
    preview.value = res.data
  } catch (e: any) {
    preview.value = null
    MessagePlugin.error(e?.message || t('pluginAdmin.install.inspectFailed'))
  } finally {
    busy.value = false
  }
}

async function onConfirm() {
  if (!preview.value) {
    await inspect()
    return
  }
  if (!source.value) return
  busy.value = true
  try {
    const res = await installPluginPackage(source.value, preview.value.digest)
    MessagePlugin.success(t('pluginAdmin.install.done', { name: localizedText(preview.value.manifest.name, locale.value) }))
    emit('installed', res.data)
    emit('update:visible', false)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginAdmin.install.failed'))
  } finally {
    busy.value = false
  }
}
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/provider-card.less';

.option-chips {
  display: inline-flex;
  align-self: flex-start;
  gap: 4px;
  padding: 3px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
}

.option-chip {
  border: none;
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  padding: 5px 12px;
  border-radius: var(--app-radius-sm);
  cursor: pointer;

  &--active {
    background: var(--td-bg-color-container);
    color: var(--td-brand-color);
    font-weight: 500;
    box-shadow: var(--td-shadow-1);
  }

  &:disabled {
    cursor: not-allowed;
  }
}

.form-label {
  display: block;
  margin-bottom: 6px;
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);

  &.required::before {
    content: '*';
    color: var(--td-error-color);
    margin-right: 4px;
  }
}

.form-desc {
  margin: 4px 0 0;
  font-size: var(--app-text-sm);
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
  word-break: break-all;
}

.package-picker {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;

  &__name {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.review-head {
  display: flex;
  gap: 12px;

  &__badge {
    .provider-card-badge();
    .provider-card-badge-color(#0052d9);
  }

  &__text {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  &__name {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: var(--app-text-lg);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  &__meta {
    font-size: var(--app-text-xs);
    font-family: var(--app-font-family-mono);
    color: var(--td-text-color-placeholder);
  }

  &__desc {
    margin: 4px 0 0;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
  }
}

.review-block {
  display: flex;
  flex-direction: column;
  gap: 6px;

  &__title {
    font-size: var(--app-text-sm);
    font-weight: 500;
    color: var(--td-text-color-primary);
  }
}

.review-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;

  li {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 8px;
    font-size: var(--app-text-sm);
  }

  &__point {
    flex: none;
    font-size: var(--app-text-xs);
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
    border-radius: var(--app-radius-xs);
    padding: 1px 6px;
  }

  &__name {
    color: var(--td-text-color-primary);
  }

  &__detail {
    font-size: var(--app-text-xs);
    color: var(--td-text-color-placeholder);
    word-break: break-all;
  }

  &__warn {
    font-size: var(--app-text-sm);
    font-weight: 500;
    color: var(--td-warning-color);
  }
}

.review-alert {
  margin-top: 4px;
}
</style>
