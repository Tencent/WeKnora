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

    <template v-else-if="loaded">
      <article class="connection-card">
        <div class="product-row">
          <img class="product-logo" :src="browserLogo" width="44" height="44" alt="BrowserSkill" />
          <div class="product-copy">
            <div class="product-heading">
              <div class="product-name">
                <strong>BrowserSkill</strong>
                <a class="product-link" href="https://github.com/Tencent/BrowserSkill" target="_blank"
                  rel="noopener noreferrer" aria-label="BrowserSkill">
                  <t-icon name="jump" size="14px" />
                </a>
              </div>
              <span class="status-pill" :class="{ online: status.connected, idle: !status.connected && status.device }">
                <i />{{ t(status.connected ? 'localBrowser.connected' : status.device ? 'localBrowser.offline' : 'localBrowser.notPaired') }}
              </span>
            </div>
            <p class="product-desc">{{ t('localBrowser.productDescription') }}</p>
          </div>
        </div>

        <template v-if="status.device">
          <div class="device-block">
            <div class="device-meta">
              <strong>{{ status.device.label }}</strong>
              <span>{{ t('localBrowser.lastSeen') }} {{ formatDate(status.device.last_seen_at) }}</span>
            </div>
            <p v-if="!status.connected" class="device-hint">{{ t('localBrowser.reconnectHint') }}</p>
            <div class="device-actions">
              <t-popconfirm theme="warning" :content="t('localBrowser.revokeConfirm')"
                :confirm-btn="{ content: t('localBrowser.revoke'), theme: 'danger' }"
                :cancel-btn="{ content: t('common.cancel') }" placement="bottom" @confirm="disconnect">
                <t-button theme="default" variant="outline" size="small" :disabled="busy">
                  <template #icon><t-icon name="rollback" /></template>
                  {{ t('localBrowser.revoke') }}
                </t-button>
              </t-popconfirm>
              <t-button theme="default" variant="outline" size="small" :disabled="busy" @click="pair">
                <template #icon><t-icon name="refresh" /></template>
                {{ t('localBrowser.replaceDevice') }}
              </t-button>
            </div>
          </div>
        </template>

        <ol v-else class="setup-steps">
          <li>
            <span class="step-index">1</span>
            <div class="step-copy">
              <strong>{{ t('localBrowser.installExtension') }}</strong>
              <p>{{ t('localBrowser.installHint') }}</p>
            </div>
            <t-button theme="default" variant="outline" size="small"
              :disabled="!status.extension_available || downloading" @click="download">
              <template #icon><t-icon name="download" /></template>
              {{ t('localBrowser.downloadExtension') }}
            </t-button>
          </li>
          <li>
            <span class="step-index">2</span>
            <div class="step-copy">
              <strong>{{ t('localBrowser.pairBrowser') }}</strong>
              <p>{{ t('localBrowser.pairHint') }}</p>
            </div>
            <t-button theme="primary" size="small" :disabled="busy" @click="pair">
              <template #icon><t-icon name="file-copy" /></template>
              {{ t(copied ? 'localBrowser.copyAgain' : 'localBrowser.copyPairing') }}
            </t-button>
          </li>
        </ol>
        <p v-if="!status.device && !status.extension_available" class="package-hint">
          {{ t('localBrowser.packageUnavailable') }}
        </p>

        <div v-if="pairing" class="pairing-feedback" role="status">
          <t-icon name="check-circle-filled" size="16px" />
          <span>{{ t(copyFallback ? 'localBrowser.manualCopy' : 'localBrowser.pairingReady') }}</span>
          <input v-if="copyFallback" type="password" readonly :value="pairing"
            :aria-label="t('localBrowser.copyPairing')" @focus="($event.target as HTMLInputElement).select()" />
        </div>
      </article>

      <BrowserSearchPreferences />

      <section class="usage" :aria-label="t('localBrowser.usageTitle')">
        <h3>{{ t('localBrowser.usageTitle') }}</h3>
        <ol>
          <li>
            <span class="usage-index">1</span>
            <div class="usage-copy">
              <strong>{{ t('localBrowser.usageStep1Title') }}</strong>
              <p>{{ t('localBrowser.usageStep1Text') }}</p>
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
    </template>
  </div>
</template>
<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { get, post, getDown } from '@/utils/request'
import { useBrowserConnectionStore } from '@/stores/browserConnection'
import browserLogo from '@/assets/browserskill/logo.png'
import BrowserSearchPreferences from './BrowserSearchPreferences.vue'
interface Device { id: string; label: string; last_seen_at: string }
interface Connection { enabled: boolean; connected: boolean; device?: Device; extension_available: boolean }
const { t, locale } = useI18n()
const browserConnection = useBrowserConnectionStore()
const status = ref<Connection>({ enabled: false, connected: false, extension_available: false })
const busy = ref(false), loaded = ref(false), error = ref(''), refreshError = ref(''), pairing = ref(''), copied = ref(false), copyFallback = ref(false), downloading = ref(false)
const endpoint = '/api/v1/me/browser'
const controller = new AbortController()
let revision = 0
let alive = true, timer: ReturnType<typeof setTimeout> | undefined, expiry: ReturnType<typeof setTimeout> | undefined, pairExpires = 0
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
    const blob = await getDown(`${endpoint}/extension`)
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
.browser-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 24px;

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

.connection-card {
  border: 1px solid var(--td-component-stroke);
  border-radius: 12px;
  padding: 20px;
  background: var(--td-bg-color-container);
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.product-row {
  display: flex;
  align-items: flex-start;
  gap: 14px;
}

.product-logo {
  flex-shrink: 0;
  width: 44px;
  height: 44px;
  border-radius: 12px;
  object-fit: cover;
  box-shadow:
    0 1px 2px rgba(15, 23, 42, 0.08),
    0 6px 16px rgba(234, 88, 12, 0.16);
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
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
    color: var(--td-text-color-primary);
  }
}

.product-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-placeholder);
  line-height: 1;

  &:hover,
  &:focus-visible {
    color: var(--td-text-color-primary);
  }
}

.product-desc {
  margin: 4px 0 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
  padding: 3px 9px;
  border-radius: 999px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 12px;
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
    background: color-mix(in srgb, var(--td-warning-color) 12%, transparent);
  }

  &.online {
    color: var(--td-success-color);
    background: color-mix(in srgb, var(--td-success-color) 12%, transparent);

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
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  span {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }
}

.device-hint {
  margin: 8px 0 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.device-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
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
  margin: 18px 0 0;
  padding: 16px 0 0;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.setup-steps li {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.step-index {
  width: 22px;
  height: 22px;
  margin-top: 1px;
  display: grid;
  place-items: center;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  font-weight: 600;
}

.step-copy {
  flex: 1;
  min-width: 0;

  strong {
    display: block;
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  p {
    margin: 4px 0 0;
    font-size: 12px;
    line-height: 1.6;
    color: var(--td-text-color-secondary);
  }
}

.package-hint,
.empty-hint {
  margin: 12px 0 0;
  font-size: 13px;
  line-height: 1.65;
  color: var(--td-text-color-secondary);
}

.pairing-feedback {
  margin-top: 16px;
  padding: 10px 12px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--td-success-color) 10%, transparent);
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  color: var(--td-text-color-primary);
  font-size: 12px;
  line-height: 1.55;

  :deep(.t-icon) {
    color: var(--td-success-color);
  }

  input {
    width: 100%;
    padding: 8px;
    border: 1px solid var(--td-component-border);
    border-radius: 6px;
    background: var(--td-bg-color-container);
  }
}

.usage {
  margin-top: 28px;

  h3 {
    margin: 0 0 16px;
    font-size: 15px;
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
  font-size: 12px;
  font-weight: 600;
  z-index: 1;
}

.usage-copy {
  flex: 1;
  min-width: 0;
  padding-top: 1px;

  strong {
    display: block;
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  p {
    margin: 4px 0 0;
    font-size: 13px;
    line-height: 1.65;
    color: var(--td-text-color-secondary);
  }
}

.error {
  color: var(--td-error-color);
  font-size: 13px;
  margin: 0 0 16px;
}

@media (max-width: 600px) {
  .setup-steps li {
    flex-wrap: wrap;
  }

  .setup-steps li :deep(.t-button) {
    margin-left: 34px;
  }

  .status-pill {
    margin-left: 0;
  }

  .product-heading {
    flex-wrap: wrap;
  }
}
</style>
