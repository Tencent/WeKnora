<template>
  <section class="browser-settings">
    <header><h2>{{ t('localBrowser.settingsTitle') }}</h2><p>{{ t('localBrowser.settingsDescription') }}</p></header>
    <p v-if="error || refreshError" class="error" role="alert">{{ error || refreshError }}</p>
    <t-skeleton v-if="!loaded && !refreshError" animation="gradient" :row-col="[{ width: '100%', height: '112px', type: 'rect' }]" />
    <p v-else-if="loaded && !status.enabled" class="hint">{{ t('localBrowser.unavailable') }}</p>
    <template v-else-if="loaded">
      <article class="connection-card">
        <div class="connection-heading">
          <img :src="browserLogo" alt="BrowserSkill" width="44" height="44" />
          <div class="product"><strong>BrowserSkill</strong><span>{{ t('localBrowser.productDescription') }}</span></div>
          <span class="state" :class="{ online: status.connected }"><i />{{ t(status.connected ? 'localBrowser.connected' : status.device ? 'localBrowser.offline' : 'localBrowser.notPaired') }}</span>
        </div>
        <template v-if="status.device">
          <div class="device-details"><strong>{{ status.device.label }}</strong><span>{{ t('localBrowser.lastSeen') }} {{ formatDate(status.device.last_seen_at) }}</span></div>
          <p class="hint">{{ t(status.connected ? 'localBrowser.readyHint' : 'localBrowser.reconnectHint') }}</p>
          <div class="device-actions"><t-button theme="default" variant="outline" size="small" :disabled="busy" @click="disconnect">{{ t('localBrowser.revoke') }}</t-button><t-button theme="default" variant="text" size="small" :disabled="busy" @click="pair">{{ t('localBrowser.replaceDevice') }}</t-button></div>
        </template>
        <template v-else>
          <ol class="setup-steps">
            <li><span class="step">1</span><div><strong>{{ t('localBrowser.installExtension') }}</strong><p>{{ t('localBrowser.installHint') }}</p></div><t-button theme="default" variant="outline" size="small" :disabled="!status.extension_available || downloading" @click="download">{{ t('localBrowser.downloadExtension') }}</t-button></li>
            <li><span class="step">2</span><div><strong>{{ t('localBrowser.pairBrowser') }}</strong><p>{{ t('localBrowser.pairHint') }}</p></div><t-button theme="primary" size="small" :disabled="busy" @click="pair">{{ t(copied ? 'localBrowser.copyAgain' : 'localBrowser.copyPairing') }}</t-button></li>
          </ol>
          <p v-if="!status.extension_available" class="hint package-hint">{{ t('localBrowser.packageUnavailable') }}</p>
        </template>
        <div v-if="pairing" class="pairing-feedback" role="status">
          <t-icon name="check-circle" size="16px" /><span>{{ t(copyFallback ? 'localBrowser.manualCopy' : 'localBrowser.pairingReady') }}</span>
          <input v-if="copyFallback" type="password" readonly :value="pairing" :aria-label="t('localBrowser.copyPairing')" @focus="($event.target as HTMLInputElement).select()" />
        </div>
      </article>
      <div class="usage-notes"><p><t-icon name="check-circle" />{{ t('localBrowser.connectionNote') }}</p><p><t-icon name="pause-circle" />{{ t('localBrowser.resumeNote') }}</p><p><t-icon name="secured" />{{ t('localBrowser.privacyNote') }}</p></div>
      <details class="help"><summary>{{ t('localBrowser.installHelp') }}</summary><p>{{ t('localBrowser.installSteps') }}</p><a href="https://github.com/Tencent/BrowserSkill" target="_blank" rel="noopener noreferrer">BrowserSkill <t-icon name="jump" size="13px" /></a></details>
    </template>
  </section>
</template>
<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { get, post, getDown } from '@/utils/request'
import browserLogo from '@/assets/browserskill/logo.png'
interface Device { id: string; label: string; last_seen_at: string }
interface Connection { enabled: boolean; connected: boolean; device?: Device; extension_available: boolean }
const { t, locale } = useI18n()
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
    }
  } catch (e: any) { if (alive) { refreshError.value = e?.message || t('localBrowser.failed') } }
  if (alive) timer = setTimeout(refresh, 5000)
}
async function pair() {
  if (busy.value) return
  busy.value = true; error.value = ''; revision++
  try {
    if (!pairing.value || Date.now() >= pairExpires) {
      const result = await post<{ data: { pairing_link: string } }>(endpoint, { action: 'pair' }, { timeout: 15000, signal: controller.signal })
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
    if (alive) { status.value = result.data; clearPairing() }
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
<style scoped>
.browser-settings { max-width: 800px; color: var(--td-text-color-primary); }
header { margin-bottom: 24px; } h2 { margin: 0 0 8px; font-size: 20px; font-weight: 600; }
header p, .hint { color: var(--td-text-color-secondary); font-size: 13px; line-height: 1.65; margin: 0; }
.connection-card { border: 1px solid var(--td-component-border); border-radius: 12px; padding: 22px 24px; background: var(--td-bg-color-container); }
.connection-heading { display: flex; align-items: center; gap: 12px; }
.product { flex: 1; display: grid; gap: 5px; }.product strong { font-size: 16px; font-weight: 600; }.product span { font-size: 12px; color: var(--td-text-color-secondary); }
.state { display: inline-flex; gap: 6px; align-items: center; font-size: 12px; color: var(--td-text-color-secondary); }.state i { width: 6px; height: 6px; border-radius: 50%; background: var(--td-text-color-placeholder); }.state.online { color: var(--td-success-color); }.state.online i { background: currentColor; }
.setup-steps { list-style: none; margin: 22px 0 0; padding: 0; border-top: 1px solid var(--td-component-border); }.setup-steps li { display: flex; align-items: center; gap: 12px; padding-top: 20px; }.step { width: 24px; height: 24px; display: grid; place-items: center; background: var(--td-bg-color-secondarycontainer); border-radius: 50%; font-size: 12px; color: var(--td-text-color-secondary); flex-shrink: 0; }.setup-steps li div { flex: 1; }.setup-steps strong { font-size: 13px; font-weight: 500; }.setup-steps p { font-size: 12px; line-height: 1.6; color: var(--td-text-color-secondary); margin: 5px 0 0; }
.device-details { margin: 22px 0 8px; display: flex; flex-wrap: wrap; gap: 12px; align-items: center; }.device-details strong { font-size: 13px; font-weight: 500; }.device-details span { font-size: 12px; color: var(--td-text-color-placeholder); }.device-actions { display: flex; gap: 8px; margin-top: 18px; }
.pairing-feedback { margin-top: 18px; padding: 10px 12px; background: var(--td-success-color-1); border-radius: 6px; display: flex; flex-wrap: wrap; align-items: center; gap: 8px; color: var(--td-text-color-primary); font-size: 12px; }.pairing-feedback :deep(.t-icon) { color: var(--td-success-color); } input { width: 100%; padding: 8px; border: 1px solid var(--td-component-border); border-radius: 5px; background: var(--td-bg-color-container); }
.usage-notes { padding: 12px 2px 0; }.usage-notes p { display: flex; align-items: start; gap: 8px; margin: 12px 0; font-size: 12px; line-height: 1.65; color: var(--td-text-color-secondary); }.usage-notes :deep(.t-icon) { margin-top: 3px; flex-shrink: 0; }
.help { margin-top: 18px; font-size: 12px; color: var(--td-text-color-secondary); }.help summary { cursor: pointer; width: fit-content; }.help p { line-height: 1.8; max-width: 66ch; }.help a { color: var(--td-brand-color); text-decoration: none; }.package-hint { margin-top: 12px; font-size: 12px; }.error { color: var(--td-error-color); font-size: 13px; }
@media(max-width:600px){.connection-card { padding: 18px; }.setup-steps li { flex-wrap: wrap; }.setup-steps li :deep(.t-button) { margin-left: 36px; }.state { align-self: start; }}
</style>
