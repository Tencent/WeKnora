<template>
  <SettingDrawer
    v-model:visible="drawerVisible"
    :title="t('modelSettings.observability.title')"
    :description="t('modelSettings.observability.description')"
    class="model-usage-drawer"
    :close-btn="renderCloseIcon"
    width="920px"
    :min-width="720"
    :max-width="1280"
    storage-key="setting-drawer:width:model-usage"
    hide-footer
  >
    <template #headerIcon><ChartNoAxesCombined size="22px" aria-hidden="true" /></template>
    <div class="usage-toolbar">
      <label class="usage-field usage-model-filter">
        <span>{{ t('modelSettings.observability.selectModel') }}</span>
        <select v-model="usageModelId" @change="loadUsage">
          <option value="">{{ t('modelSettings.observability.allModels') }}</option>
          <option v-for="model in models" :key="model.id" :value="model.id">{{ modelLabel(model) }}</option>
        </select>
      </label>
      <t-radio-group v-model="windowDays" variant="default-filled" @change="loadUsage">
        <t-radio-button :value="7">7 {{ t('modelSettings.observability.days') }}</t-radio-button>
        <t-radio-button :value="30">30 {{ t('modelSettings.observability.days') }}</t-radio-button>
        <t-radio-button :value="90">90 {{ t('modelSettings.observability.days') }}</t-radio-button>
        <t-radio-button :value="0">{{ t('modelSettings.observability.customRange') }}</t-radio-button>
      </t-radio-group>
      <t-button variant="outline" :disabled="loading" @click="loadUsage">
        <template #icon><RefreshCw size="16px" :class="{ 'icon-spinning': loading }" aria-hidden="true" /></template>
        {{ t('common.refresh') }}
      </t-button>
    </div>
    <div v-if="windowDays === 0" class="usage-range">
      <label class="usage-field">
        <span>{{ t('modelSettings.observability.from') }}</span>
        <input v-model="rangeFrom" type="datetime-local" @change="loadUsage" />
      </label>
      <label class="usage-field">
        <span>{{ t('modelSettings.observability.to') }}</span>
        <input v-model="rangeTo" type="datetime-local" @change="loadUsage" />
      </label>
    </div>
    <p class="usage-hint">{{ t('modelSettings.observability.timezoneHint') }}</p>
    <p v-if="usageError" class="usage-error" role="alert">{{ usageError }}</p>

    <div :aria-busy="loading">
      <div v-if="loading" class="usage-loading" role="status"><LoaderCircle size="22px" class="icon-spinning" aria-hidden="true" /><span>{{ t('evaluation.loading') }}</span></div>
      <div v-if="!loading && !usageError && rows.length === 0" class="usage-empty">
        <ChartNoAxesCombined size="40px" aria-hidden="true" />
        <p>{{ t('modelSettings.observability.empty') }}</p>
      </div>
      <div v-else class="usage-list">
        <article v-for="row in rows" :key="row.model_id" class="usage-card">
          <header class="usage-card__header">
            <div>
              <h4>{{ modelName(row.model_id) }}</h4>
              <span>{{ modelType(row.model_id) }}</span>
            </div>
            <div class="usage-card__cost">
              <span>{{ t('modelSettings.observability.recordedCost') }}</span>
              <strong>{{ formatCosts(row.costs) }}</strong>
              <small>{{ t('modelSettings.observability.accountedCalls', { complete: integer(row.accounting_complete_calls), total: integer(row.call_count) }) }}</small>
            </div>
          </header>
          <div class="usage-metrics">
            <div><span>{{ t('modelSettings.observability.calls') }}</span><strong>{{ integer(row.call_count) }}</strong></div>
            <div><span>{{ t('modelSettings.observability.tokens') }}</span><strong>{{ integer(row.total_tokens) }}</strong></div>
            <div><span>p50</span><strong>{{ latency(row.latency.p50_ms) }}</strong></div>
            <div><span>p95</span><strong>{{ latency(row.latency.p95_ms) }}</strong></div>
            <div><span>p99</span><strong>{{ latency(row.latency.p99_ms) }}</strong></div>
            <div><span>{{ t('modelSettings.observability.unpriced') }}</span><strong>{{ integer(row.unpriced_calls) }}</strong></div>
          </div>
          <dl class="call-status" :aria-label="t('modelSettings.observability.callStatus')">
            <div v-for="item in callStatuses" :key="item.key"><dt>{{ t(`modelSettings.observability.${item.label}`) }}</dt><dd>{{ integer(row[item.key]) }}</dd></div>
          </dl>
          <div class="cache-grid">
            <section>
              <h5><Layers3 size="15px" aria-hidden="true" />{{ t('modelSettings.observability.providerCache') }}</h5>
              <strong>{{ percent(row.provider_cache.hit_rate) }}</strong>
              <progress v-if="row.provider_cache.hit_rate != null" :value="row.provider_cache.hit_rate" :max="1" :aria-label="t('modelSettings.observability.providerCache')" />
              <p>{{ t('modelSettings.observability.providerDenominator', { value: integer(row.provider_cache.observed_tokens) }) }}</p>
            </section>
            <section>
              <h5><Database size="15px" aria-hidden="true" />{{ t('modelSettings.observability.applicationCache') }}</h5>
              <strong>{{ percent(row.application_cache.hit_rate) }}</strong>
              <progress v-if="row.application_cache.hit_rate != null" :value="row.application_cache.hit_rate" :max="1" :aria-label="t('modelSettings.observability.applicationCache')" />
              <p>{{ t('modelSettings.observability.applicationDenominator', { value: integer(row.application_cache.observed_items), bypass: integer(row.application_cache.bypass_items) }) }}</p>
            </section>
          </div>
        </article>
      </div>
    </div>

    <details v-if="canEditPricing" class="pricing-section">
      <summary>{{ t('modelSettings.observability.pricingTitle') }}</summary>
      <p>{{ t('modelSettings.observability.pricingDescription') }}</p>
      <div class="pricing-form">
        <label class="usage-field">
          <span>{{ t('modelSettings.observability.selectModel') }}</span>
          <select v-model="priceModelId" :disabled="savingPrice" @change="loadPrices">
            <option value="" disabled>{{ t('modelSettings.observability.selectModel') }}</option>
            <option v-for="model in models" :key="model.id" :value="model.id">{{ modelLabel(model) }}</option>
          </select>
        </label>
        <label class="usage-field"><span>{{ t('modelSettings.observability.currency') }}</span><input v-model="currency" maxlength="3" :disabled="savingPrice" /></label>
        <label class="usage-field"><span>{{ t('modelSettings.observability.inputPrice') }}</span><input v-model.number="inputPrice" type="number" min="0" step="0.000001" :disabled="savingPrice" /></label>
        <label class="usage-field"><span>{{ t('modelSettings.observability.outputPrice') }}</span><input v-model.number="outputPrice" type="number" min="0" step="0.000001" :disabled="savingPrice" /></label>
        <label class="usage-field"><span>{{ t('modelSettings.observability.validFrom') }}</span><input v-model="validFrom" type="datetime-local" :disabled="savingPrice" /></label>
        <label class="usage-field"><span>{{ t('modelSettings.observability.validTo') }}</span><input v-model="validTo" type="datetime-local" :disabled="savingPrice" /></label>
      </div>
      <fieldset class="cache-pricing-fields" :disabled="savingPrice">
        <legend>{{ t('modelSettings.observability.cachePricingTitle') }}</legend>
        <p id="cache-pricing-hint">{{ t('modelSettings.observability.cachePricingHint') }}</p>
        <div class="pricing-form">
          <label class="usage-field"><span>{{ t('modelSettings.observability.cacheReadPrice') }}</span><input v-model.number="cacheReadPrice" type="number" min="0" step="0.000001" aria-describedby="cache-pricing-hint" /></label>
          <label class="usage-field"><span>{{ t('modelSettings.observability.cacheWrite5mPrice') }}</span><input v-model.number="cacheWrite5mPrice" type="number" min="0" step="0.000001" aria-describedby="cache-pricing-hint" /></label>
          <label class="usage-field"><span>{{ t('modelSettings.observability.cacheWrite1hPrice') }}</span><input v-model.number="cacheWrite1hPrice" type="number" min="0" step="0.000001" aria-describedby="cache-pricing-hint" /></label>
        </div>
      </fieldset>
      <p v-if="priceValidationError" class="usage-error" role="alert">{{ priceValidationError }}</p>
      <div class="pricing-actions">
        <t-button theme="primary" :disabled="!canSavePrice" @click="savePrice">
          <template #icon><LoaderCircle v-if="savingPrice" size="16px" class="icon-spinning" aria-hidden="true" /><Plus v-else size="16px" aria-hidden="true" /></template>
          {{ t('modelSettings.observability.addPrice') }}
        </t-button>
      </div>
      <p v-if="priceError" class="usage-error" role="alert">{{ priceError }}</p>
      <LoaderCircle v-if="priceLoading" size="20px" class="icon-spinning" role="status" :aria-label="t('evaluation.loading')" />
      <div v-else-if="prices.length" class="price-history">
        <div v-for="price in prices" :key="price.id" class="price-version">
          <span>{{ new Date(price.valid_from).toLocaleString() }} → {{ price.valid_to ? new Date(price.valid_to).toLocaleString() : t('modelSettings.observability.openEnded') }}</span>
          <strong>{{ price.currency }} {{ micros(price.input_microunits_per_million) }} / {{ micros(price.output_microunits_per_million) }}</strong>
          <dl class="cache-price-history">
            <div v-for="field in cachePriceFields" :key="field.key"><dt>{{ t(`modelSettings.observability.${field.label}`) }}</dt><dd>{{ price.cache_pricing?.[field.key] == null ? t('modelSettings.observability.unknownPrice') : `${price.currency} ${micros(price.cache_pricing[field.key]!)}` }}</dd></div>
          </dl>
        </div>
      </div>
    </details>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { computed, h, onBeforeUnmount, ref, watch } from 'vue'
import { ChartAnalyticsIcon as ChartNoAxesCombined, ServerIcon as Database, LayersIcon as Layers3, LoadingIcon as LoaderCircle, AddIcon as Plus, RefreshIcon as RefreshCw, CloseIcon as X } from 'tdesign-icons-vue-next'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  listModelPrices,
  listModelUsage,
  putModelPrice,
  type ModelConfig,
  type ModelCostTotal,
  type ModelPriceVersion,
  type ModelUsageStatistics,
} from '@/api/model'

const props = defineProps<{ visible: boolean; models: ModelConfig[]; canEditPricing: boolean }>()
const emit = defineEmits<{ (e: 'update:visible', value: boolean): void }>()
const { t } = useI18n()
const renderCloseIcon = () => h(X, { size: '20px', 'aria-hidden': 'true' })

const drawerVisible = computed({ get: () => props.visible, set: value => emit('update:visible', value) })
const loading = ref(false)
const windowDays = ref(30)
const usageModelId = ref('')
const rangeFrom = ref(localDateTime(new Date(Date.now() - 30 * 86400000)))
const rangeTo = ref(localDateTime(new Date()))
const usageError = ref('')
const rows = ref<ModelUsageStatistics[]>([])
const priceModelId = ref('')
const prices = ref<ModelPriceVersion[]>([])
const inputPrice = ref<number | string>('')
const outputPrice = ref<number | string>('')
const cacheReadPrice = ref<number | string>('')
const cacheWrite5mPrice = ref<number | string>('')
const cacheWrite1hPrice = ref<number | string>('')
const currency = ref('USD')
const validFrom = ref(localDateTime(new Date()))
const validTo = ref('')
const savingPrice = ref(false)
const priceLoading = ref(false)
const priceError = ref('')
let usageSequence = 0
let priceSequence = 0
let sessionSequence = 0

const priceValidationError = computed(() => {
  if (!validRange(validFrom.value, validTo.value, true)) return t('modelSettings.observability.invalidPriceDates')
  if (!/^[A-Za-z]{3}$/.test(currency.value.trim())) return t('modelSettings.observability.invalidCurrency')
  if ([inputPrice.value, outputPrice.value, cacheReadPrice.value, cacheWrite5mPrice.value, cacheWrite1hPrice.value].some(value => value !== '' && !validPrice(value))) return t('modelSettings.observability.invalidPrice')
  return ''
})
const canSavePrice = computed(() => props.canEditPricing && !savingPrice.value && Boolean(
  priceModelId.value && validPrice(inputPrice.value) && validPrice(outputPrice.value) && !priceValidationError.value,
))

function invalidateRequests() {
  usageSequence += 1
  priceSequence += 1
  sessionSequence += 1
  loading.value = false
  priceLoading.value = false
  rows.value = []
  prices.value = []
  usageError.value = ''
  priceError.value = ''
}

watch(() => props.visible, visible => {
  if (visible) {
    if (!priceModelId.value && props.models[0]?.id) priceModelId.value = props.models[0].id
    void loadUsage()
    if (props.canEditPricing) void loadPrices()
  } else {
    invalidateRequests()
  }
}, { immediate: true })
onBeforeUnmount(invalidateRequests)

async function loadUsage() {
  const sequence = ++usageSequence
  rows.value = []
  usageError.value = ''
  loading.value = false
  if (!props.visible) return
  if (windowDays.value === 0 && !validRange(rangeFrom.value, rangeTo.value)) {
    usageError.value = t('modelSettings.observability.invalidRange')
    return
  }
  loading.value = true
  try {
    const to = windowDays.value === 0 ? new Date(rangeTo.value) : new Date()
    const from = windowDays.value === 0 ? new Date(rangeFrom.value) : new Date(to.getTime() - windowDays.value * 86400000)
    const result = await listModelUsage({ from: from.toISOString(), to: to.toISOString(), modelIds: usageModelId.value ? [usageModelId.value] : undefined })
    if (sequence === usageSequence) rows.value = result.items ?? []
  } catch (error: any) {
    if (sequence === usageSequence) usageError.value = error?.message || t('modelSettings.observability.loadFailed')
  } finally {
    if (sequence === usageSequence) loading.value = false
  }
}

async function loadPrices() {
  const sequence = ++priceSequence
  prices.value = []
  priceError.value = ''
  priceLoading.value = false
  if (!props.visible || !props.canEditPricing || !priceModelId.value) return
  priceLoading.value = true
  try {
    const result = await listModelPrices(priceModelId.value)
    if (sequence === priceSequence) prices.value = result
  } catch (error: any) {
    if (sequence === priceSequence) priceError.value = error?.message || t('modelSettings.observability.priceLoadFailed')
  } finally {
    if (sequence === priceSequence) priceLoading.value = false
  }
}

async function savePrice() {
  if (!canSavePrice.value) return
  const session = sessionSequence
  const modelId = priceModelId.value
  savingPrice.value = true
  try {
    await putModelPrice(modelId, {
      valid_from: new Date(validFrom.value).toISOString(),
      valid_to: validTo.value ? new Date(validTo.value).toISOString() : undefined,
      input_microunits_per_million: Math.round(Number(inputPrice.value) * 1_000_000),
      output_microunits_per_million: Math.round(Number(outputPrice.value) * 1_000_000),
      currency: currency.value.trim().toUpperCase(),
      cache_pricing: [cacheReadPrice.value, cacheWrite5mPrice.value, cacheWrite1hPrice.value].some(value => value !== '') ? {
        version: 1,
        read_microunits_per_million: optionalPriceMicros(cacheReadPrice.value),
        write_5m_microunits_per_million: optionalPriceMicros(cacheWrite5mPrice.value),
        write_1h_microunits_per_million: optionalPriceMicros(cacheWrite1hPrice.value),
      } : undefined,
    })
    if (session === sessionSequence && props.visible) {
      MessagePlugin.success(t('modelSettings.observability.priceSaved'))
      if (modelId === priceModelId.value) await loadPrices()
    }
  } catch (error: any) {
    if (session === sessionSequence && props.visible) MessagePlugin.error(error?.message || t('modelSettings.observability.priceSaveFailed'))
  } finally {
    savingPrice.value = false
  }
}

function localDateTime(date: Date): string {
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
}
function validRange(from: string, to: string, optionalEnd = false): boolean {
  const start = new Date(from).getTime()
  return Number.isFinite(start) && ((optionalEnd && !to) || (Number.isFinite(new Date(to).getTime()) && new Date(to).getTime() > start))
}
function optionalPriceMicros(value: number | string): number | undefined {
  return value === '' ? undefined : Math.round(Number(value) * 1_000_000)
}
function validPrice(value: number | string): boolean {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 && Number.isSafeInteger(Math.round(value * 1_000_000))
}

const callStatuses = [
  {key: 'started_calls', label: 'started'}, {key: 'success_calls', label: 'success'},
  {key: 'error_calls', label: 'error'}, {key: 'canceled_calls', label: 'canceled'},
] as const

const cachePriceFields = [
  {key: 'read_microunits_per_million', label: 'cacheReadPrice'},
  {key: 'write_5m_microunits_per_million', label: 'cacheWrite5mPrice'},
  {key: 'write_1h_microunits_per_million', label: 'cacheWrite1hPrice'},
] as const

const modelFor = (id: string) => props.models.find(model => model.id === id)
const modelLabel = (model: ModelConfig) => model.display_name || model.name
const modelName = (id: string) => modelFor(id) ? modelLabel(modelFor(id)!) : `${id} · ${t('modelSettings.observability.nameUnavailable')}`
const modelType = (id: string) => modelFor(id)?.type || ''
const integer = (value: number) => new Intl.NumberFormat().format(value || 0)
const decimal = (value: number) => new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(value || 0)
const latency = (value: number | null) => value == null ? '—' : `${decimal(value)} ms`
const percent = (value: number | null) => value == null ? '—' : `${(value * 100).toFixed(1)}%`
const micros = (value: number) => (value / 1_000_000).toFixed(6).replace(/0+$/, '').replace(/\.$/, '')
const formatCosts = (costs: ModelCostTotal[]) => costs?.length
  ? costs.map(item => `${item.currency} ${micros(item.cost_microunits)}`).join(' · ')
  : t('modelSettings.observability.noPricedCost')
</script>

<style scoped lang="less">
.usage-toolbar, .pricing-section__heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.usage-toolbar { margin-bottom: 12px; flex-wrap: wrap; align-items: flex-end; }
.usage-model-filter { flex: 1; min-width: 180px; }
.usage-field { display: grid; gap: 6px; min-width: 0; color: var(--td-text-color-secondary); font-size: 12px; }
.usage-field input, .usage-field select { box-sizing: border-box; width: 100%; min-width: 0; height: 34px; padding: 0 10px; border: 1px solid var(--td-component-border); border-radius: 6px; color: var(--td-text-color-primary); background: var(--td-bg-color-container); font: inherit; }
.usage-field input:focus-visible, .usage-field select:focus-visible, summary:focus-visible { outline: 2px solid var(--td-brand-color); outline-offset: 2px; }
.usage-range { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.usage-hint { margin: 10px 0 18px; color: var(--td-text-color-secondary); font-size: 12px; }
.usage-error { padding: 10px 12px; border-radius: 6px; color: var(--td-error-color); background: var(--td-error-color-1); font-size: 12px; }
.usage-empty { padding: 44px 0; }
.usage-list { display: grid; gap: 14px; }
.usage-card { border: 1px solid var(--td-component-border); border-radius: 16px; padding: 22px; background: var(--td-bg-color-container); box-shadow: 0 6px 24px rgba(16, 46, 36, .035); animation: usage-enter .45s cubic-bezier(.22, 1, .36, 1) both; }
.usage-card__header { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 16px; align-items: flex-start; }
.usage-card__header h4, .pricing-section h4 { margin: 0; font-size: 15px; }
.usage-card__header span, .pricing-section p, .cache-grid p { color: var(--td-text-color-secondary); font-size: 12px; margin: 4px 0 0; }
.usage-card__header > div { min-width: 0; }
.usage-card__header h4 { overflow-wrap: anywhere; }
.usage-card__cost { display: grid; gap: 4px; text-align: right; }
.usage-card__cost strong { overflow-wrap: anywhere; color: var(--td-brand-color); font-size: 22px; letter-spacing: -.04em; font-variant-numeric: tabular-nums; }
.usage-card__cost small { font-size: 11px; color: var(--td-text-color-secondary); }
.usage-metrics { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; margin-top: 14px; }
.usage-metrics div, .cache-grid section { min-width: 0; overflow-wrap: anywhere; padding: 10px 12px; border-radius: 8px; background: var(--td-bg-color-secondarycontainer); }
.usage-metrics span { display: block; color: var(--td-text-color-secondary); font-size: 12px; }
.usage-metrics strong { display: block; margin-top: 5px; font-size: 20px; letter-spacing: -.035em; font-variant-numeric: tabular-nums; }
.cache-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-top: 10px; }
.cache-grid h5 { display: flex; align-items: center; gap: 7px; margin: 0 0 12px; font-size: 12px; color: var(--td-text-color-secondary); }
.cache-grid strong { font-size: 20px; }
.cache-grid progress { display: block; width: 100%; height: 6px; margin-top: 10px; accent-color: var(--td-brand-color); }
.pricing-section { margin-top: 24px; padding-top: 20px; border-top: 1px solid var(--td-component-stroke); }
.pricing-section summary { cursor: pointer; font-weight: 600; font-size: 14px; }
.pricing-form { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin-top: 14px; }
.pricing-actions { display: flex; justify-content: flex-end; margin-top: 14px; }
.price-history { margin-top: 12px; border-top: 1px solid var(--td-component-stroke); }
.price-version { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 12px; padding: 9px 0; font-size: 12px; border-bottom: 1px solid var(--td-component-stroke); }
.call-status { display: flex; flex-wrap: wrap; gap: 8px 20px; margin: 16px 0 0; padding: 0; font-size: 12px; }
.call-status > div { display: flex; gap: 7px; }
.call-status dt { color: var(--td-text-color-secondary); }
.call-status dd { margin: 0; font-variant-numeric: tabular-nums; font-weight: 600; }
.cache-pricing-fields { margin: 22px 0 0; padding: 16px; border: 1px solid var(--td-component-stroke); border-radius: 10px; }
.cache-pricing-fields legend { padding: 0 6px; font-size: 13px; font-weight: 600; }
.cache-price-history { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); flex-basis: 100%; gap: 10px; margin: 0; color: var(--td-text-color-secondary); }
.cache-price-history dd { margin: 4px 0 0; font-variant-numeric: tabular-nums; color: var(--td-text-color-primary); }
@media (max-width: 540px) { .usage-card { padding: 16px; } .cache-price-history { grid-template-columns: 1fr; } }
.usage-empty, .usage-loading { display: flex; align-items: center; justify-content: center; gap: 12px; min-height: 120px; color: var(--td-text-color-secondary); font-size: 13px; }
.usage-empty { flex-direction: column; }
.icon-spinning { animation: icon-spin 1s linear infinite; }
@keyframes icon-spin { to { transform: rotate(360deg); } }
@keyframes usage-enter { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: translateY(0); } }
@media (prefers-reduced-motion: reduce) { .usage-card, .icon-spinning { animation: none; } }
@media (max-width: 840px) { .usage-metrics { grid-template-columns: 1fr 1fr; } .pricing-form { grid-template-columns: 1fr 1fr; } }
@media (max-width: 540px) { .usage-toolbar { gap: 12px; } .usage-model-filter { flex-basis: 100%; } .cache-grid, .usage-range, .pricing-form { grid-template-columns: 1fr; } .usage-card__header, .price-version { flex-direction: column; } .usage-card__cost { text-align: left; } }
</style>

<style lang="less">
.model-usage-drawer .t-drawer__content-wrapper { max-width: 100vw; }
.model-usage-drawer { --td-brand-color: #087b59; --td-brand-color-1: #e9f5ef; }
.model-usage-drawer .setting-drawer__header-block { padding: 6px 0; }
.model-usage-drawer .setting-drawer__title { font-size: 25px; letter-spacing: -.04em; }
.model-usage-drawer .setting-drawer__header-icon { background: #e9f5ef; color: #087b59; }
@media (max-width: 540px) { .model-usage-drawer .setting-drawer__title { font-size: 20px; } }
@media (prefers-reduced-motion: reduce) { .model-usage-drawer .t-drawer__content-wrapper, .model-usage-drawer .t-drawer__mask { transition: none !important; animation: none !important; } }
</style>
