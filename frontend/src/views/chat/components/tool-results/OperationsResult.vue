<template>
  <div class="catalog-result">
    <div v-if="plainError" class="catalog-error" role="alert">
      <t-icon name="error-circle" />
      <pre>{{ plainError }}</pre>
    </div>

    <template v-else-if="mode === 'find'">
      <div class="catalog-summary">
        <span>{{ $t('agentStream.operations.found', { count: operations.length }) }}</span>
        <span v-if="unavailable.length"> · {{ $t('agentStream.operations.unavailableCount', { count: unavailable.length }) }}</span>
      </div>
      <p v-if="!operations.length && !unavailable.length" class="catalog-empty">{{ $t('agentStream.operations.empty') }}</p>
      <div v-if="operations.length" class="catalog-list">
        <article v-for="(op, index) in operations" :key="`${op.id}-${index}`" class="catalog-item">
          <div class="catalog-item__head catalog-item__head--static">
            <span class="catalog-item__index">#{{ index + 1 }}</span>
            <code class="catalog-item__name">{{ op.id }}</code>
            <span v-if="op.risk" class="catalog-risk" :data-risk="op.risk">{{ riskLabel(op.risk) }}</span>
            <span class="catalog-item__summary">{{ op.summary }}</span>
          </div>
          <div v-if="op.expandable" class="catalog-item__body">
            <p v-if="op.description">{{ op.description }}</p>
            <div v-if="op.params.length" class="catalog-params">
              <div class="catalog-params__label">{{ $t('agentStream.operations.parameters') }}</div>
              <div v-for="param in op.params" :key="param.name" class="catalog-param">
                <div class="catalog-param__title">
                  <code>{{ param.name }}</code>
                  <span v-if="param.type">{{ param.type }}</span>
                  <span v-if="param.required" class="catalog-required">{{ $t('agentStream.operations.required') }}</span>
                </div>
                <p v-if="param.description">{{ param.description }}</p>
                <p v-if="param.enumText" class="catalog-param__meta">{{ $t('agentStream.operations.enum') }}：{{ param.enumText }}</p>
                <p v-if="param.range" class="catalog-param__meta">{{ $t('agentStream.operations.range', param.range) }}</p>
              </div>
            </div>
          </div>
        </article>
      </div>
      <div v-if="unavailable.length" class="catalog-unavailable">
        <div class="catalog-summary">{{ $t('agentStream.operations.unavailableHeading') }}</div>
        <div class="catalog-list">
          <article v-for="(op, index) in unavailable" :key="`${op.id}-${index}`" class="catalog-item">
            <div class="catalog-item__head catalog-item__head--static">
              <span class="catalog-item__index">#{{ index + 1 }}</span>
              <code class="catalog-item__name">{{ op.id }}</code>
              <span v-if="op.risk" class="catalog-risk" :data-risk="op.risk">{{ riskLabel(op.risk) }}</span>
              <span class="catalog-item__summary">{{ op.summary }}</span>
            </div>
            <p v-if="op.reason" class="catalog-item__reason">{{ op.reason }}</p>
          </article>
        </div>
      </div>
    </template>

    <template v-else>
      <div v-if="run.operation || run.truncated" class="catalog-summary">
        <code v-if="run.operation">{{ run.operation }}</code>
        <span v-if="run.truncated">{{ $t('agentStream.operations.truncated') }}</span>
      </div>
      <p v-if="resultEmpty" class="catalog-empty">{{ $t('agentStream.operations.emptyResult') }}</p>
      <CatalogValue v-else :value="run.data" />
      <div v-if="hasMeta" class="catalog-meta">
        <div class="catalog-summary">{{ $t('agentStream.operations.meta') }}</div>
        <CatalogValue :value="run.meta" />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  isEmptyCatalogValue,
  operationRows,
  parseCatalogPayload,
  runOperationView,
  unavailableOperationRows,
} from '@/utils/catalogToolDisplay'
import CatalogValue from './CatalogValue.vue'

const props = withDefaults(defineProps<{
  mode: 'find' | 'run'
  output?: string
  data?: Record<string, unknown>
  success?: boolean
}>(), { output: '', success: undefined })

const { t, te } = useI18n()
const payload = computed(() => parseCatalogPayload(props.output, props.data))
const operations = computed(() => operationRows(payload.value))
const unavailable = computed(() => unavailableOperationRows(payload.value))
const run = computed(() => runOperationView(payload.value))
const resultEmpty = computed(() => isEmptyCatalogValue(run.value.data))
const hasMeta = computed(() => !isEmptyCatalogValue(run.value.meta))
const plainError = computed(() => {
  if (props.success !== false) return ''
  if (props.mode === 'find' && (operations.value.length || unavailable.value.length)) return ''
  if (props.mode === 'run' && !resultEmpty.value) return ''
  return props.output.trim() || t('agentStream.operations.failed')
})

function riskLabel(risk: string) {
  const key = `agentStream.operations.risk.${risk}`
  return te(key) ? t(key) : risk
}
</script>

<style lang="less" scoped>
.catalog-result { min-width: 0; font-size: var(--app-text-sm); color: var(--td-text-color-primary); }
.catalog-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
  margin-bottom: 8px;
  color: var(--td-text-color-secondary);
}
.catalog-summary code { font-weight: 600; color: var(--td-text-color-primary); }
.catalog-empty { margin: 0; color: var(--td-text-color-placeholder); }
.catalog-list {
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  overflow: hidden;
  background: var(--td-bg-color-container);
  max-height: 360px;
  overflow-y: auto;
}
.catalog-item + .catalog-item { border-top: 1px solid var(--td-component-stroke); }
.catalog-item__head {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  margin: 0;
  padding: 8px 10px;
  border: 0;
  background: transparent;
  font: inherit;
  color: inherit;
  text-align: left;
}
.catalog-item__index {
  flex-shrink: 0;
  min-width: 22px;
  font-size: var(--app-text-xs);
  font-weight: 600;
  color: var(--td-text-color-placeholder);
}
.catalog-item__name {
  flex-shrink: 0;
  max-width: 46%;
  font: var(--app-text-sm)/1.5 var(--app-font-family-mono, monospace);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.catalog-item__summary {
  flex: 1;
  min-width: 0;
  color: var(--td-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.catalog-item__body, .catalog-item__reason {
  margin: 0;
  padding: 0 10px 10px 40px;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.catalog-item__body p { margin: 0 0 8px; }
.catalog-risk {
  flex-shrink: 0;
  padding: 0 6px;
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-xs);
  line-height: 1.6;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
}
.catalog-risk[data-risk='write'] { color: var(--td-warning-color); }
.catalog-risk[data-risk='destructive'] { color: var(--td-error-color); }
.catalog-params { border: 1px solid var(--td-component-stroke); border-radius: var(--app-radius-md); overflow: hidden; }
.catalog-params__label {
  padding: 6px 12px;
  font-size: var(--app-text-xs);
  font-weight: 600;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-secondarycontainer);
}
.catalog-param { padding: 8px 12px; }
.catalog-param + .catalog-param { border-top: 1px solid var(--td-component-stroke); }
.catalog-param__title { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.catalog-param__title code { font-weight: 600; }
.catalog-param__title span, .catalog-param__meta { color: var(--td-text-color-secondary); }
.catalog-required { color: var(--td-brand-color); }
.catalog-param p { margin: 4px 0 0; }
.catalog-unavailable, .catalog-meta { margin-top: 12px; }
.catalog-error { display: flex; align-items: flex-start; gap: 8px; color: var(--td-error-color); }
.catalog-error pre, .catalog-result :deep(pre) { margin: 0; }
.catalog-error pre {
  flex: 1;
  min-width: 0;
  padding: 0;
  background: transparent;
  font: inherit;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
