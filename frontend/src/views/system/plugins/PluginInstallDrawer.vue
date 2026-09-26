<template>
  <SettingDrawer
    :visible="visible"
    :title="tenant ? t('pluginCenter.own.title') : t('pluginAdmin.install.title')"
    icon="download"
    width="640px"
    @update:visible="(v: boolean) => emit('update:visible', v)"
  >
    <template #header-extra>
      <ol class="install-steps">
        <li
          v-for="(s, i) in STEPS"
          :key="s"
          class="install-steps__step"
          :class="{ 'is-current': step === s, 'is-done': STEPS.indexOf(step) > i }"
        >
          <span class="install-steps__n">
            <t-icon v-if="STEPS.indexOf(step) > i" name="check" />
            <template v-else>{{ i + 1 }}</template>
          </span>
          {{ t(`pluginAdmin.install.steps.${s}`) }}
        </li>
      </ol>
    </template>

    <!-- 1. Choose a package -->
    <section v-if="step === 'source'" class="setting-drawer__section">
      <p class="form-desc form-desc--lead">{{ tenant ? t('pluginCenter.own.description') : t('pluginAdmin.install.description') }}</p>
      <div class="option-chips">
        <button
          v-for="m in modes"
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

      <button
        v-if="mode === 'upload'"
        type="button"
        class="drop-zone"
        :class="{ 'is-over': dragOver, 'is-busy': busy }"
        :disabled="busy"
        @click="fileInput?.click()"
        @dragover.prevent="dragOver = true"
        @dragleave.prevent="dragOver = false"
        @drop.prevent="onDrop"
      >
        <span class="drop-zone__icon">
          <t-loading v-if="busy" size="small" />
          <t-icon v-else name="upload" />
        </span>
        <span class="drop-zone__title">
          {{ busy ? t('pluginAdmin.install.inspecting') : file?.name ?? t('pluginAdmin.install.dropTitle') }}
        </span>
        <span class="drop-zone__hint">{{ t('pluginAdmin.install.dropHint') }}</span>
        <input ref="fileInput" type="file" accept=".wkp,.zip" hidden @change="onFile" />
      </button>

      <div v-else-if="mode === 'market'" class="form-item">
        <p v-if="marketLoading" class="form-desc">{{ t('pluginAdmin.market.loading') }}</p>
        <t-alert v-else-if="marketError" theme="error" :message="marketError" />
        <p v-else-if="market && !market.configured" class="notice">{{ t('pluginAdmin.market.notConfigured') }}</p>
        <template v-else-if="market">
          <t-input v-model="marketQuery" clearable :placeholder="t('pluginAdmin.market.search')">
            <template #prefix-icon><t-icon name="search" /></template>
          </t-input>
          <p v-if="marketList.length === 0" class="form-desc">{{ t('pluginAdmin.market.empty') }}</p>
          <ul v-else class="market-list">
            <li
              v-for="p in marketList"
              :key="p.id"
              class="market-list__item"
              :class="{ 'is-picked': picked?.id === p.id }"
            >
              <span class="market-list__badge" :class="{ 'market-list__badge--logo': !!p.icon }">
                <img v-if="p.icon" :src="p.icon" alt="" />
                <t-icon v-else name="extension" />
              </span>
              <div class="market-list__text">
                <div class="market-list__name">
                  {{ localizedText(p.name, locale) }}
                  <span v-if="p.installedVersion" class="market-list__installed">
                    {{ t('pluginAdmin.market.installed', { version: p.installedVersion }) }}
                  </span>
                </div>
                <div class="market-list__meta">
                  {{ p.publisher?.name || p.publisher?.id }}<template v-if="p.latest"> · v{{ p.latest.version }}</template>
                </div>
                <p v-if="localizedText(p.description, locale)" class="market-list__desc">
                  {{ localizedText(p.description, locale) }}
                </p>
                <p v-if="p.incompatible" class="market-list__warn">{{ p.incompatible }}</p>
              </div>
              <t-button
                size="small"
                :variant="marketAction(p) === 'installed' ? 'text' : 'outline'"
                :disabled="busy || marketAction(p) === 'unavailable' || marketAction(p) === 'installed'"
                :loading="busy && picked?.id === p.id"
                @click="pick(p)"
              >
                {{ t(`pluginAdmin.market.action.${marketAction(p)}`) }}
              </t-button>
            </li>
          </ul>
          <p class="form-desc">{{ t('pluginAdmin.market.source', { url: market.indexUrl }) }}</p>
        </template>
      </div>

      <div v-else class="form-item">
        <label class="form-label required">{{ t('pluginAdmin.install.urlLabel') }}</label>
        <t-input
          v-model="url"
          :disabled="busy"
          placeholder="https://example.com/acme-search-1.0.0.wkp"
          @enter="inspect"
        />
        <p class="form-desc">{{ t('pluginAdmin.install.urlHint') }}</p>
      </div>
    </section>

    <!-- 2. Review -->
    <template v-else-if="step === 'review' && preview">
      <section class="setting-drawer__section">
        <div class="review-hero">
          <PluginBadge :manifest="preview.manifest" size="lg" />
          <div class="review-hero__text">
            <div class="review-hero__name">{{ localizedText(preview.manifest.name, locale) }}</div>
            <div class="review-hero__meta">
              {{ preview.manifest.publisher.name || preview.manifest.publisher.id }} · {{ preview.manifest.id }} · {{ formatBytes(preview.size) }}
            </div>
          </div>
          <div class="review-hero__change" :class="`is-${preview.change}`">
            <strong>{{ t(`pluginAdmin.install.changeShort.${preview.change}`) }}</strong>
            <span>
              <template v-if="preview.installedVersion && preview.change !== 'reinstall'">v{{ preview.installedVersion }} → </template>v{{ preview.manifest.version }}
            </span>
          </div>
        </div>
        <p v-if="description" class="review-about">{{ description }}</p>
        <dl class="review-facts">
          <dt>{{ t('pluginAdmin.install.trustRow') }}</dt>
          <dd>
            <span class="trust" :class="`trust--${preview.trust.level}`">{{ t(`pluginAdmin.trust.${preview.trust.level}`) }}</span>
            <span class="review-facts__muted">{{ t(`pluginAdmin.trust.${signature.key}`, { key: signature.keyId }) }}</span>
          </dd>
          <dt>{{ t('pluginAdmin.detail.runtime') }}</dt>
          <dd>{{ runtimeLabel(preview.manifest.runtime?.type ?? '') }}</dd>
          <template v-if="preview.manifest.engines?.weknora">
            <dt>{{ t('pluginAdmin.detail.engines') }}</dt>
            <dd><code>{{ preview.manifest.engines.weknora }}</code></dd>
          </template>
          <dt>{{ t('pluginAdmin.install.digest') }}</dt>
          <dd class="review-facts__digest">
            <code :title="preview.digest">{{ shortDigest(preview.digest) }}…</code>
            <t-button size="small" variant="text" theme="primary" @click="copyWithToast(preview.digest, 'pluginAdmin.secret.copied')">
              {{ t('pluginAdmin.secret.copy') }}
            </t-button>
          </dd>
        </dl>
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('pluginAdmin.contributions') }}</h4>
        <PluginCapabilityList :manifest="preview.manifest" show="detail" />
      </section>

      <section class="setting-drawer__section">
        <h4 class="setting-drawer__section-title">{{ t('pluginAdmin.permissions') }}</h4>
        <p v-if="permissions.length === 0 && hosts.length === 0" class="form-desc">
          {{ t('pluginAdmin.install.noPermissions') }}
        </p>
        <ul v-else class="perm-list">
          <li v-for="h in hosts" :key="`host:${h}`">
            <span class="perm-list__kind">{{ t('pluginAdmin.permission.remote') }}</span>
            <code>{{ h }}</code>
          </li>
          <li
            v-for="p in permissions"
            :key="`${p.kind}:${p.value}`"
            :class="{ 'is-risky': p.kind === 'egress' && p.value === EGRESS_ANY_HOST }"
          >
            <template v-if="p.kind === 'egress' && p.value === EGRESS_ANY_HOST">
              <span class="perm-list__kind"><t-icon name="error-circle" /> {{ t('pluginAdmin.permission.anyHost') }}</span>
              <span>{{ t('pluginAdmin.install.anyHostHint') }}</span>
            </template>
            <template v-else>
              <span class="perm-list__kind">{{ t(`pluginAdmin.permission.${p.kind}`) }}</span>
              <code>{{ p.value }}</code>
            </template>
          </li>
        </ul>
      </section>

      <section v-if="isRemote" class="setting-drawer__section">
        <label class="form-label" :class="{ required: preview.change === 'install' }">
          {{ t('pluginAdmin.install.remoteUrlLabel') }}
        </label>
        <t-input v-model="remoteUrl" :disabled="busy" placeholder="https://plugins.example.com/acme-search" />
        <p class="form-desc">
          {{ t('pluginAdmin.install.remoteUrlHint') }}
          <template v-if="preview.change !== 'install'">{{ t('pluginAdmin.install.remoteUrlKeep') }}</template>
        </p>
      </section>

      <p class="notice">{{ notice }}</p>
    </template>

    <!-- 3. Done -->
    <section v-else-if="step === 'done' && done" class="install-done">
      <span class="install-done__icon"><t-icon name="check" /></span>
      <div class="install-done__title">{{ t('pluginAdmin.install.done', { name: done.name }) }}</div>
      <p class="install-done__hint">{{ doneHint }}</p>
    </section>

    <template #footer-left>
      <t-button v-if="step === 'review'" variant="outline" :disabled="busy" @click="back">
        {{ t('pluginAdmin.install.back') }}
      </t-button>
    </template>
    <template #footer-right>
      <template v-if="step === 'done'">
        <t-button theme="primary" @click="emit('update:visible', false)">{{ t('pluginAdmin.install.finish') }}</t-button>
      </template>
      <template v-else>
        <t-button variant="outline" :disabled="busy" @click="emit('update:visible', false)">{{ t('common.cancel') }}</t-button>
        <t-button v-if="step === 'source'" theme="primary" :loading="busy" :disabled="!source || busy" @click="inspect">
          {{ t('pluginAdmin.install.next') }}
        </t-button>
        <t-button v-else theme="primary" :loading="busy" :disabled="!canConfirm" @click="onConfirm">
          {{ confirmText }}
        </t-button>
      </template>
    </template>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'

import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import PluginBadge from '@/components/plugins/PluginBadge.vue'
import PluginCapabilityList from '@/components/plugins/PluginCapabilityList.vue'
import { tenantPackages } from '@/api/tenantPlugins'
import {
  listMarketPlugins,
  platformPackages,
  type InstalledPlugin,
  type MarketListing,
  type MarketPlugin,
  type PackageSource,
  type PluginPreview,
} from '@/api/system/plugins'
import { copyWithToast } from '@/utils/clipboard'
import { localizedText } from '@/utils/localizedText'

import {
  EGRESS_ANY_HOST,
  filterMarket,
  marketAction,
  formatBytes,
  hasSystemConfig,
  isPackageUrl,
  permissionLines,
  remoteHosts,
  remoteUrlReady,
  shortDigest,
  trustNote,
} from '../pluginManagementState'
import { hasTenantConfig } from '../../settings/pluginCenterState'

// Installing is two steps on purpose: inspect shows what the package would
// add and reach, and install sends the reviewed digest so the server refuses
// a package that changed in between (a URL that now serves something else).
// scope tenant registers the workspace's own remote plugin instead of
// installing one for the platform: no marketplace, the tenant endpoints.
const props = defineProps<{ visible: boolean; scope?: 'system' | 'tenant' }>()
const emit = defineEmits<{
  'update:visible': [value: boolean]
  installed: [plugin: InstalledPlugin]
}>()

const { t, te, locale } = useI18n()
const runtimeLabel = (rt: string) => (te(`pluginAdmin.runtime.${rt}`) ? t(`pluginAdmin.runtime.${rt}`) : rt)

const STEPS = ['source', 'review', 'done'] as const
type Step = (typeof STEPS)[number]

type Mode = 'upload' | 'url' | 'market'
const mode = ref<Mode>('upload')
const tenant = computed(() => props.scope === 'tenant')
const api = computed(() => (tenant.value ? tenantPackages : platformPackages))
const modes = computed<Mode[]>(() => (tenant.value ? ['upload', 'url'] : ['upload', 'url', 'market']))
const file = ref<File | null>(null)
const url = ref('')
const remoteUrl = ref('')
const fileInput = ref<HTMLInputElement | null>(null)
const dragOver = ref(false)
const preview = ref<PluginPreview | null>(null)
const done = ref<{ name: string; needsConfig: boolean } | null>(null)
const busy = ref(false)
const market = ref<MarketListing | null>(null)
const marketLoading = ref(false)
const marketError = ref('')
const marketQuery = ref('')
const picked = ref<MarketPlugin | null>(null)
const marketList = computed(() => filterMarket(market.value?.plugins ?? [], marketQuery.value, locale.value))

const step = computed<Step>(() => (done.value ? 'done' : preview.value ? 'review' : 'source'))

watch(
  () => props.visible,
  (v) => {
    if (!v) return
    mode.value = 'upload'
    file.value = null
    url.value = ''
    remoteUrl.value = ''
    preview.value = null
    done.value = null
    picked.value = null
    marketQuery.value = ''
    market.value = null
  },
)

function setMode(m: Mode) {
  mode.value = m
  preview.value = null
  picked.value = null
  if (m === 'market' && !market.value && !marketLoading.value) void loadMarket()
}

function back() {
  preview.value = null
  picked.value = null
}

async function loadMarket() {
  marketLoading.value = true
  marketError.value = ''
  try {
    const res = await listMarketPlugins()
    market.value = res.data
  } catch (e: any) {
    marketError.value = e?.message || t('pluginAdmin.market.loadFailed')
  } finally {
    marketLoading.value = false
  }
}

function pick(p: MarketPlugin) {
  picked.value = p
  preview.value = null
  void inspect()
}

function choose(f: File | null) {
  file.value = f
  preview.value = null
  if (f) void inspect()
}

function onFile(e: Event) {
  const input = e.target as HTMLInputElement
  choose(input.files?.[0] ?? null)
  input.value = ''
}

function onDrop(e: DragEvent) {
  dragOver.value = false
  if (busy.value) return
  choose(e.dataTransfer?.files?.[0] ?? null)
}

const source = computed<PackageSource | null>(() => {
  if (mode.value === 'upload') return file.value ? { file: file.value } : null
  if (mode.value === 'market') {
    const v = picked.value?.latest
    return v ? { url: v.url, digest: v.digest } : null
  }
  return isPackageUrl(url.value) ? { url: url.value.trim() } : null
})

const isRemote = computed(() => preview.value?.manifest.runtime?.type === 'remote')
const canConfirm = computed(
  () => !!source.value && !busy.value && !!preview.value && remoteUrlReady(preview.value, remoteUrl.value),
)
const confirmText = computed(() => {
  const p = preview.value
  if (!p) return ''
  if (p.change === 'upgrade' || p.change === 'downgrade') {
    return t(`pluginAdmin.install.confirmTo.${p.change}`, { version: p.manifest.version })
  }
  return t(`pluginAdmin.install.confirm.${p.change}`)
})

const permissions = computed(() => permissionLines(preview.value?.manifest.permissions))
const hosts = computed(() => (preview.value ? remoteHosts(preview.value.manifest) : []))
const description = computed(() => (preview.value ? localizedText(preview.value.manifest.description, locale.value) : ''))
const signature = computed(() => trustNote(preview.value?.trust ?? { level: 'community' }))
const needsConfig = computed(
  () => !!preview.value && (hasSystemConfig(preview.value.manifest) || hasTenantConfig(preview.value.manifest)),
)
// One sentence per fact, joined without the gap a template line break leaves.
const joined = (...parts: Array<string | false>) => parts.filter(Boolean).join(locale.value.startsWith('zh') || locale.value.startsWith('ja') ? '' : ' ')
const notice = computed(() =>
  joined(
    tenant.value ? t('pluginCenter.own.notice') : t('pluginAdmin.install.tenantNotice'),
    needsConfig.value && t('pluginAdmin.install.configNotice'),
  ),
)
const doneHint = computed(() =>
  tenant.value
    ? t('pluginCenter.own.notice')
    : joined(t('pluginAdmin.install.doneHint'), !!done.value?.needsConfig && t('pluginAdmin.install.doneConfig')),
)

async function inspect() {
  if (!source.value) return
  busy.value = true
  try {
    const res = await api.value.inspect(source.value)
    preview.value = res.data
  } catch (e: any) {
    preview.value = null
    MessagePlugin.error(e?.message || t('pluginAdmin.install.inspectFailed'))
  } finally {
    busy.value = false
  }
}

async function onConfirm() {
  if (!preview.value || !source.value) return
  busy.value = true
  try {
    const res = await api.value.install(
      source.value,
      preview.value.digest,
      isRemote.value ? remoteUrl.value.trim() : undefined,
    )
    done.value = {
      name: localizedText(preview.value.manifest.name, locale.value),
      needsConfig: hasSystemConfig(preview.value.manifest),
    }
    emit('installed', res.data)
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('pluginAdmin.install.failed'))
  } finally {
    busy.value = false
  }
}
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/option-chips.less';

.install-steps {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 4px 0 0;
  padding: 0;
  list-style: none;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);

  &__step {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    white-space: nowrap;

    & + &::before {
      content: '';
      width: 32px;
      height: 1px;
      margin-right: 4px;
      background: var(--td-component-stroke);
    }

    &.is-current {
      color: var(--td-text-color-primary);
      font-weight: 500;
    }

    &.is-done {
      color: var(--td-brand-color);
    }
  }

  &__n {
    width: 20px;
    height: 20px;
    display: inline-grid;
    place-items: center;
    border: 1px solid currentColor;
    border-radius: 50%;
    font-size: var(--app-text-xs);
    font-weight: 400;
    line-height: 1;

    .is-current & {
      border-color: var(--td-brand-color);
      background: var(--td-brand-color);
      color: var(--td-text-color-anti);
    }
  }
}

.option-chips {
  .option-chips();
  align-self: flex-start;
}

.option-chip {
  .option-chip();
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

  &--lead {
    margin: 0;
    font-size: var(--app-text-md);
    color: var(--td-text-color-secondary);
    word-break: normal;
  }
}

.drop-zone {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  width: 100%;
  padding: 36px 16px;
  border: 1px dashed var(--td-component-border);
  border-radius: var(--app-radius-lg);
  background: none;
  font: inherit;
  cursor: pointer;
  transition: border-color var(--app-motion-fast) ease, background var(--app-motion-fast) ease;

  &:hover:not(:disabled),
  &.is-over {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }

  &:disabled {
    cursor: progress;
  }

  &__icon {
    width: 36px;
    height: 36px;
    display: grid;
    place-items: center;
    margin-bottom: 6px;
    border-radius: 50%;
    background: var(--td-brand-color-1);
    color: var(--td-brand-color);
    font-size: var(--app-text-2xl);
  }

  &__title {
    font-size: var(--app-text-base);
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  &__hint {
    font-size: var(--app-text-md);
    color: var(--td-text-color-secondary);
  }
}

.review-hero {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);

  &__text {
    flex: 1;
    min-width: 0;
  }

  &__name {
    font-size: var(--app-text-xl);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  &__meta {
    margin-top: 2px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: var(--app-text-md);
    color: var(--td-text-color-secondary);
  }

  &__change {
    flex: none;
    text-align: right;
    font-size: var(--app-text-md);
    color: var(--td-text-color-secondary);

    strong {
      display: block;
      font-weight: 500;
      color: var(--td-brand-color);
    }

    &.is-downgrade strong {
      color: var(--td-warning-color);
    }
  }
}

.review-about {
  margin: 0;
  font-size: var(--app-text-md);
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.review-facts {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 10px 20px;
  margin: 0;
  font-size: var(--app-text-md);

  dt {
    color: var(--td-text-color-secondary);
  }

  dd {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    margin: 0;
    min-width: 0;
    color: var(--td-text-color-primary);
  }

  code {
    font-family: var(--app-font-family-mono);
    font-size: var(--app-text-sm);
  }

  &__muted {
    color: var(--td-text-color-placeholder);
  }

  &__digest {
    code {
      color: var(--td-text-color-secondary);
    }

    .t-button {
      margin: -4px 0;
    }
  }
}

.trust {
  &--official {
    color: var(--td-success-color);
  }

  &--verified {
    color: var(--td-brand-color);
  }
}

.perm-list {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);

  li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 12px;
    font-size: var(--app-text-md);
    color: var(--td-text-color-primary);

    & + li {
      border-top: 1px solid var(--td-component-stroke);
    }

    code {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      font-family: var(--app-font-family-mono);
      font-size: var(--app-text-sm);
      color: var(--td-text-color-secondary);
    }

    &.is-risky {
      background: var(--td-warning-color-1);
      color: var(--td-text-color-secondary);

      .perm-list__kind {
        color: var(--td-warning-color-7);
        font-weight: 500;
      }
    }
  }

  &__kind {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    flex: none;
  }
}

// One neutral note at the end of the review, not a stack of alerts.
.notice {
  margin: 0;
  padding: 10px 12px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
  font-size: var(--app-text-md);
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.install-done {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 48px 24px;
  text-align: center;

  &__icon {
    width: 48px;
    height: 48px;
    display: grid;
    place-items: center;
    margin-bottom: 8px;
    border-radius: 50%;
    background: var(--td-success-color-1);
    color: var(--td-success-color);
    font-size: var(--app-text-3xl);
  }

  &__title {
    font-size: var(--app-text-xl);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  &__hint {
    max-width: 420px;
    margin: 0;
    font-size: var(--app-text-md);
    line-height: 1.6;
    color: var(--td-text-color-secondary);
  }
}

.market-list {
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
  max-height: 420px;
  overflow-y: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);

  &__item {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 12px;

    & + & {
      border-top: 1px solid var(--td-component-stroke);
    }

    &.is-picked {
      background: var(--td-brand-color-light);
    }
  }

  &__badge {
    flex: none;
    width: 32px;
    height: 32px;
    display: grid;
    place-items: center;
    border-radius: var(--app-radius-md);
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-xl);

    &--logo {
      background: var(--td-bg-color-container);
      border: 1px solid var(--td-component-stroke);
    }

    img {
      width: 20px;
      height: 20px;
      object-fit: contain;
    }
  }

  &__text {
    flex: 1;
    min-width: 0;
  }

  &__name {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: var(--app-text-md);
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  &__installed {
    font-size: var(--app-text-sm);
    font-weight: 400;
    color: var(--td-text-color-placeholder);
  }

  &__meta {
    margin-top: 2px;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-placeholder);
  }

  &__desc,
  &__warn {
    margin: 4px 0 0;
    font-size: var(--app-text-sm);
    line-height: 1.5;
    color: var(--td-text-color-secondary);
  }

  &__warn {
    color: var(--td-warning-color);
  }
}
</style>
