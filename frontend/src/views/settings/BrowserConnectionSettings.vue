<template>
  <div class="browser-settings">
    <div class="section-header">
      <h2>{{ t('localBrowser.settingsTitle') }}</h2>
      <p class="section-description">{{ t('localBrowser.settingsDescription') }}</p>
    </div>

    <p v-if="error || refreshError" class="error" role="alert">{{ error || refreshError }}</p>
    <t-skeleton v-if="!loaded && !refreshError" animation="gradient"
      :row-col="[{ width: '100%', height: '132px', type: 'rect' }]" />
    <p v-else-if="loaded && !status.enabled" class="empty-hint">{{ t('localBrowser.unavailable') }}</p>

    <div v-else-if="loaded" class="browser-layout">
      <article class="connection-card">
        <div class="product-row">
          <img class="product-logo" :src="browserLogo" width="44" height="44" alt="BrowserSkill" />
          <div class="product-copy">
            <div class="product-heading">
              <div class="product-name">
                <strong>BrowserSkill</strong>
                <span v-if="status.connected && status.extension_version" class="product-version">v{{ status.extension_version }}</span>
              </div>
              <span class="status-pill" :class="{ online: status.connected, idle: !status.connected && status.device }">
                <i />{{ t(status.connected ? 'localBrowser.connected' : status.device ? 'localBrowser.offline' : 'localBrowser.notPaired') }}
              </span>
            </div>
            <p class="product-desc">
              {{ t('localBrowser.productDescription') }}
              <a class="product-link" href="https://github.com/Tencent/BrowserSkill" target="_blank"
                rel="noopener noreferrer">GitHub<t-icon name="jump" size="12px" /></a>
            </p>
          </div>
        </div>

        <template v-if="status.device">
          <div class="capabilities">
            <p v-if="!status.connected" class="capabilities-hint">
              <t-icon name="time" size="14px" />{{ t('localBrowser.reconnectHint') }}
            </p>
            <p v-else-if="extensionOutdated" class="capabilities-hint" role="status">
              <t-icon name="error-circle" size="14px" />
              {{ t('localBrowser.extensionOutdated', { current: status.extension_version, version: MIN_EXTENSION_VERSION }) }}
            </p>
            <span class="capabilities-title">{{ t('localBrowser.capabilitiesTitle') }}</span>
            <ul class="capabilities-grid">
              <li v-for="item in capabilities" :key="item.label">
                <t-icon :name="item.icon" size="16px" />{{ t(item.label) }}
              </li>
            </ul>
            <label class="sidebar-status-toggle">
              <span>{{ t('localBrowser.sidebarStatus') }}</span>
              <t-switch size="small" :value="uiStore.sidebarBrowserStatus"
                @change="(value: unknown) => uiStore.setSidebarBrowserStatus(value === true)" />
            </label>
          </div>
          <div class="device-block">
            <div class="device-meta">
              <span>{{ status.device.label }}</span>
              <span>{{ t('localBrowser.lastSeen') }} {{ formatDate(status.device.last_seen_at) }}</span>
            </div>
            <div class="device-actions">
              <t-button theme="default" variant="text" size="small" :disabled="busy" @click="pair">
                {{ t('localBrowser.replaceDevice') }}
              </t-button>
              <t-popconfirm theme="warning" :content="t('localBrowser.revokeConfirm')"
                :confirm-btn="{ content: t('localBrowser.revoke'), theme: 'danger' }"
                :cancel-btn="{ content: t('common.cancel') }" placement="bottom" @confirm="disconnect">
                <t-button class="revoke-action" theme="default" variant="text" size="small" :disabled="busy">
                  {{ t('localBrowser.revoke') }}
                </t-button>
              </t-popconfirm>
            </div>
          </div>
        </template>

        <ol v-else class="setup-steps">
          <li>
            <span class="step-index">1</span>
            <div class="step-copy">
              <strong>{{ t('localBrowser.usageStep1Title') }}</strong>
              <p>{{ t('localBrowser.extensionMinVersion', { version: MIN_EXTENSION_VERSION }) }}</p>
              <details class="install-guide">
                <summary>{{ t('localBrowser.manualInstall') }}<t-icon name="chevron-down" size="12px" /></summary>
                <div class="setup-help">
                  <p>{{ t('localBrowser.installHint') }}</p>
                  <p>{{ t('localBrowser.usageStep1Text') }}</p>
                  <t-button class="setup-action fallback-download" theme="default" variant="outline" size="small"
                    :disabled="!status.extension_available || downloading" @click="download">
                    <template #icon><t-icon name="download" size="13px" /></template>
                    {{ t('localBrowser.downloadExtension') }}
                  </t-button>
                  <p v-if="!status.extension_available" class="package-hint">{{ t('localBrowser.packageUnavailable') }}</p>
                </div>
              </details>
            </div>
            <div class="store-actions">
              <a v-for="store in EXTENSION_STORES" :key="store.label" class="setup-action setup-main-action store-action"
                :href="store.url" target="_blank" rel="noopener noreferrer">
                {{ t(store.label) }}<t-icon name="jump" size="13px" />
              </a>
            </div>
          </li>
          <li>
            <span class="step-index">2</span>
            <div class="step-copy">
              <strong>{{ t('localBrowser.pairBrowser') }}</strong>
              <details class="install-guide">
                <summary>{{ t('localBrowser.pairGuide') }}<t-icon name="chevron-down" size="12px" /></summary>
                <div class="setup-help"><p>{{ t('localBrowser.pairHint') }}</p></div>
              </details>
            </div>
            <button type="button" class="setup-action setup-main-action" :disabled="busy" @click="pair">
              {{ t(copied ? 'localBrowser.copyAgain' : 'localBrowser.copyPairing') }}
              <t-icon name="file-copy" size="13px" />
            </button>
          </li>
        </ol>

        <div v-if="pairing" class="pairing-feedback" role="status">
          <t-icon name="check-circle-filled" size="16px" />
          <span>{{ t(copyFallback ? 'localBrowser.manualCopy' : 'localBrowser.pairingReady') }}</span>
          <input v-if="copyFallback" type="password" readonly :value="pairing"
            :aria-label="t('localBrowser.copyPairing')" @focus="($event.target as HTMLInputElement).select()" />
        </div>
      </article>

      <div class="browser-side">
        <BrowserSearchPreferences />
      </div>

      <section class="usage">
        <h3>{{ t('localBrowser.usageTitle') }}</h3>
        <ol>
          <li>
            <span class="usage-index">1</span>
            <div class="usage-copy">
              <strong>{{ t('localBrowser.usageStep1Title') }}</strong>
              <p>{{ t('localBrowser.storeInstallHint') }}</p>
            </div>
          </li>
          <li>
            <span class="usage-index">2</span>
            <div class="usage-copy">
              <strong>{{ t('localBrowser.usageStep2Title') }}</strong>
              <p>{{ t('localBrowser.usageStep2Text') }}</p>
            </div>
          </li>
          <li>
            <span class="usage-index">3</span>
            <div class="usage-copy">
              <strong>{{ t('localBrowser.usageStep3Title') }}</strong>
              <p>{{ t('localBrowser.usageStep3Text') }}</p>
            </div>
          </li>
          <li>
            <span class="usage-index">4</span>
            <div class="usage-copy">
              <strong>{{ t('localBrowser.usageStep4Title') }}</strong>
              <p>{{ t('localBrowser.usageStep4Text') }}</p>
            </div>
          </li>
        </ol>
      </section>
    </div>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { get, post, getDown } from '@/utils/request'
import { useBrowserConnectionStore } from '@/stores/browserConnection'
import { useUIStore } from '@/stores/ui'
import browserLogo from '@/assets/browserskill/logo.png'
import BrowserSearchPreferences from './BrowserSearchPreferences.vue'
interface Device { id: string; label: string; last_seen_at: string }
interface Connection { enabled: boolean; connected: boolean; device?: Device; extension_available: boolean; extension_version?: string }
const { t, locale } = useI18n()
const browserConnection = useBrowserConnectionStore()
const uiStore = useUIStore()
const status = ref<Connection>({ enabled: false, connected: false, extension_available: false })
const busy = ref(false), loaded = ref(false), error = ref(''), refreshError = ref(''), pairing = ref(''), copied = ref(false), copyFallback = ref(false), downloading = ref(false)
const endpoint = '/api/v1/me/browser'
const controller = new AbortController()
let revision = 0
let alive = true, timer: ReturnType<typeof setTimeout> | undefined, expiry: ReturnType<typeof setTimeout> | undefined, pairExpires = 0
const MIN_EXTENSION_VERSION = '0.3.1'
const EXTENSION_STORES = [
  { label: 'localBrowser.storeInstall', url: 'https://chromewebstore.google.com/detail/hhcmgoofomhgciiibhipgmgkgnoenaoi' },
  { label: 'localBrowser.edgeStoreInstall', url: 'https://microsoftedge.microsoft.com/addons/detail/browserskill/emacgiaaaiojkkpkddmmdfhmokgmnikg' },
]
const versionParts = (value: string) => value.replace(/^v/, '').split(/[.-]/).slice(0, 3).map((part) => Number.parseInt(part, 10) || 0)
const extensionOutdated = computed(() => {
  const current = status.value.connected ? status.value.extension_version : ''
  if (!current) return false
  const [a, b] = [versionParts(current), versionParts(MIN_EXTENSION_VERSION)]
  for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i]! < b[i]!
  return false
})
const capabilities = [
  { icon: 'link', label: 'localBrowser.openPage' },
  { icon: 'file-search', label: 'localBrowser.readPage' },
  { icon: 'cursor', label: 'localBrowser.clickPage' },
  { icon: 'edit-1', label: 'localBrowser.fillPage' },
  { icon: 'screenshot', label: 'localBrowser.captureScreenshot' },
  { icon: 'layers', label: 'localBrowser.listTabs' },
]
const formatDate = (value: string) => new Date(value).toLocaleString(locale.value, { dateStyle: 'short', timeStyle: 'short' })
function clearPairing() { pairing.value = ''; copied.value = false; copyFallback.value = false; clearTimeout(expiry) }
async function refresh() {
  try {
    if (!busy.value && !document.hidden) {
      const current = revision
      const result = await get<{ data: Connection }>(endpoint, { signal: controller.signal })
      if (!alive) return
      if (current !== revision) { timer = setTimeout(refresh, 5000); return }
      if (result.data.device?.id && result.data.device.id !== status.value.device?.id) clearPairing()
      status.value = result.data; loaded.value = true; refreshError.value = ''
      browserConnection.apply(result.data)
    }
  } catch (e: any) { if (alive) { refreshError.value = e?.message || t('localBrowser.failed') } }
  if (alive) timer = setTimeout(refresh, 5000)
}
async function pair() {
  if (busy.value) return
  busy.value = true; error.value = ''; revision++
  try {
    if (!pairing.value || Date.now() >= pairExpires) {
      const result = await post<{ data: { pairing_link: string } }>(endpoint, { action: 'pair', origin: window.location.origin }, { timeout: 15000, signal: controller.signal })
      if (!alive) return
      pairing.value = result.data.pairing_link; pairExpires = Date.now() + 5 * 60 * 1000
      clearTimeout(expiry); expiry = setTimeout(clearPairing, 5 * 60 * 1000)
    }
    try { await navigator.clipboard.writeText(pairing.value); if (alive) { copied.value = true; copyFallback.value = false } }
    catch { if (alive) copyFallback.value = true }
  } catch (e: any) { if (alive) error.value = e?.message || t('localBrowser.failed') }
  finally { if (alive) busy.value = false }
}
async function disconnect() {
  if (busy.value) return
  busy.value = true; error.value = ''; revision++
  try {
    const result = await post<{ data: Connection }>(endpoint, { action: 'revoke' }, { signal: controller.signal })
    if (alive) { status.value = result.data; clearPairing(); browserConnection.apply(result.data) }
  } catch (e: any) { if (alive) error.value = e?.message || t('localBrowser.failed') }
  finally { if (alive) busy.value = false }
}
async function download() {
  downloading.value = true; error.value = ''
  try {
    // Bypass archives cached before the server started sending no-store.
    const blob = await getDown(`${endpoint}/extension?t=${Date.now()}`)
    if (!alive) return
    const url = URL.createObjectURL(blob), link = document.createElement('a')
    link.href = url; link.download = 'browser-skill-weknora.zip'; link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (e: any) { if (alive) error.value = e?.message || t('localBrowser.failed') }
  finally { if (alive) downloading.value = false }
}
onMounted(() => { void refresh() })
onBeforeUnmount(() => { alive = false; controller.abort(); clearTimeout(timer); clearPairing() })
</script>
<style lang="less" scoped>
@import (reference) '@/components/css/settings-section.less';

.browser-settings {
  width: 100%;
  container: browser-settings / inline-size;
}

// Stacked in the settings dialog; side by side once a full page gives it room.
@container browser-settings (min-width: 960px) {
  .browser-layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    column-gap: 20px;
  }

  .connection-card {
    display: flex;
    flex-direction: column;

    > .capabilities {
      flex: 1;
    }
  }

  .browser-side {
    border-radius: var(--app-radius-xl);
    padding: 24px;
    background: color-mix(in srgb, var(--td-bg-color-secondarycontainer) 65%, var(--td-bg-color-container));

    > .browser-search-preferences {
      margin-top: 0;
    }
  }

  .usage {
    grid-column: 1 / -1;
    padding: 0 4px;
  }
}

.section-header {
  .settings-section-header();
}

.connection-card {
  border-radius: var(--app-radius-xl);
  padding: 24px;
  background: color-mix(in srgb, var(--td-bg-color-secondarycontainer) 65%, var(--td-bg-color-container));
}

.product-row {
  display: flex;
  align-items: center;
  gap: 14px;
}

.product-logo {
  flex-shrink: 0;
  width: 40px;
  height: 40px;
  border-radius: 11px;
  object-fit: cover;
}

.product-copy {
  flex: 1;
  min-width: 0;
}

.product-heading {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.product-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;

  strong {
    font-size: var(--app-text-lg);
    font-weight: 600;
    line-height: 24px;
    color: var(--td-text-color-primary);
  }
}

.product-link {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin-left: 6px;
  color: var(--td-brand-color);
  text-decoration: none;
  white-space: nowrap;

  &:hover,
  &:focus-visible {
    text-decoration: underline;
  }
}

.product-version {
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-weight: 400;
  font-variant-numeric: tabular-nums;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 12ch;
}

.product-desc {
  margin: 4px 0 0;
  font-size: var(--app-text-md);
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  padding: 3px 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-weight: 500;
  line-height: 18px;
  flex-shrink: 0;

  i {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: currentColor;
    opacity: 0.55;
  }

  &.idle {
    color: var(--td-warning-color);
  }

  &.online {
    color: var(--td-success-color);

    i {
      opacity: 1;
    }
  }
}

.device-block,
.setup-steps {
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--td-component-stroke);
}

.device-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px 12px;

  strong {
    font-size: var(--app-text-base);
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  span {
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);
    font-variant-numeric: tabular-nums;
  }
}

.capabilities {
  margin-top: 18px;
}

.capabilities-hint {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin: 0 0 14px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-md);
  line-height: 1.55;

  .t-icon {
    flex-shrink: 0;
    margin-top: 3px;
    color: var(--td-warning-color);
  }
}

.sidebar-status-toggle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 14px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  cursor: pointer;
}

.capabilities-title {
  display: block;
  margin-bottom: 10px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
}

.capabilities-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;

  li {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    padding: 8px 10px;
    border-radius: var(--app-radius-md);
    background: var(--td-bg-color-container);
    color: var(--td-text-color-primary);
    font-size: var(--app-text-md);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;

    .t-icon {
      flex-shrink: 0;
      color: var(--td-brand-color);
    }
  }
}

.device-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-left: auto;
}

.device-block {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-top: 20px;
  padding-top: 14px;
}

.revoke-action {
  color: var(--td-text-color-secondary);
  &:hover { color: var(--td-error-color); }
}

.connection-card :deep(.t-button--variant-outline.t-button--theme-default) {
  &:hover,
  &:focus-visible,
  &:active {
    color: var(--td-text-color-primary);
    border-color: var(--td-component-border);
    background-color: var(--td-bg-color-container-hover);
  }
}

.setup-steps {
  list-style: none;
  margin: 20px 0 0;
  padding: 20px 0 0;
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.setup-steps li {
  display: grid;
  grid-template-columns: 22px minmax(0, 1fr) auto;
  align-items: flex-start;
  gap: 12px;
}

.store-actions {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 6px;
}

.connection-card .setup-action {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  box-sizing: border-box;
  height: 30px;
  padding: 3px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  color: var(--setup-action-color, var(--td-text-color-primary));
  font-family: inherit;
  font-size: var(--app-text-sm);
  font-weight: 500;
  line-height: 22px;
  text-decoration: none;
  cursor: pointer;
  transition: background-color .16s, border-color .16s;
  &:disabled { opacity: .5; cursor: not-allowed; }
  &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; }
  :deep(.t-icon) { color: currentColor; margin: 0; flex-shrink: 0; }
  // TDesign adds 8px here; use the same flex gap as the other actions.
  :deep(.t-icon + .t-button__text:not(:empty)) { margin-left: 0; }
}

.connection-card .setup-main-action {
  min-width: 144px;
}

.connection-card .store-action {
  --setup-action-color: color-mix(in srgb, var(--td-brand-color) 75%, var(--td-text-color-primary));
  color: var(--setup-action-color);
}

.connection-card .setup-action:not(:disabled) {
  &:hover,
  &:active {
    color: var(--setup-action-color, var(--td-text-color-primary));
    background: var(--td-bg-color-container-hover);
    border-color: var(--td-component-border);
  }
}

.step-index {
  width: 22px;
  height: 22px;
  margin-top: 1px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  border-radius: 6px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-weight: 600;
}

.step-copy {
  flex: 1;
  min-width: 0;

  strong {
    display: block;
    font-size: var(--app-text-base);
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  p {
    margin: 4px 0 0;
    font-size: var(--app-text-sm);
    line-height: 1.6;
    color: var(--td-text-color-secondary);
  }
}

.package-hint,
.empty-hint {
  margin: 12px 0 0;
  font-size: var(--app-text-md);
  line-height: 1.65;
  color: var(--td-text-color-secondary);
}

.fallback-download {
  margin-top: 12px;
}

.install-guide {
  margin-top: 6px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 1.6;

  summary {
    cursor: pointer;
    width: fit-content;
    display: flex;
    align-items: center;
    gap: 5px;
    list-style: none;
    &::-webkit-details-marker { display: none; }
    &:hover { color: var(--td-text-color-primary); }
    &:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 3px; }
  }

  &[open] summary :deep(.t-icon) { transform: rotate(180deg); }
}

.setup-help {
  margin-top: 10px;
  padding-left: 12px;
  border-left: 2px solid var(--td-component-stroke);
  p + p { margin-top: 8px; }
}

.pairing-feedback {
  margin-top: 16px;
  padding: 10px 12px;
  border-radius: var(--app-radius-md);
  background: color-mix(in srgb, var(--td-success-color) 10%, transparent);
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-sm);
  line-height: 1.55;

  :deep(.t-icon) {
    color: var(--td-success-color);
  }

  input {
    width: 100%;
    padding: 8px;
    border: 1px solid var(--td-component-border);
    border-radius: var(--app-radius-sm);
    background: var(--td-bg-color-container);
  }
}

.usage {
  margin-top: 32px;

  h3 {
    margin: 0 0 16px;
    font-size: var(--app-text-lg);
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  ol {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  li {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    position: relative;
    padding-bottom: 18px;

    &:last-child {
      padding-bottom: 0;
    }

    &:not(:last-child)::before {
      content: '';
      position: absolute;
      left: 10px;
      top: 22px;
      bottom: 0;
      width: 1px;
      background: var(--td-component-stroke);
    }
  }
}

.usage-index {
  width: 22px;
  height: 22px;
  margin-top: 1px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-weight: 600;
  z-index: 1;
}

.usage-copy {
  flex: 1;
  min-width: 0;
  padding-top: 1px;

  strong {
    display: block;
    font-size: var(--app-text-base);
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  p {
    margin: 4px 0 0;
    font-size: var(--app-text-md);
    line-height: 1.65;
    color: var(--td-text-color-secondary);
  }
}

.error {
  color: var(--td-error-color);
  font-size: var(--app-text-md);
  margin: 0 0 16px;
}

@media (max-width: 600px) {
  .connection-card { padding: 18px; }
  .device-actions { margin-left: 0; }
  .setup-steps li {
    grid-template-columns: 22px minmax(0, 1fr);
  }

  .setup-steps li > .setup-action,
  .setup-steps li > .store-actions {
    grid-column: 2;
    justify-self: start;
  }

  .store-actions {
    flex-direction: row;
  }

  .status-pill {
    margin-left: 0;
  }

  .product-heading {
    flex-wrap: wrap;
  }
}
</style>
