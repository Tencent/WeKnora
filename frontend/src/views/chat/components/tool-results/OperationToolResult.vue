<template>
  <div class="assistant-tool-result operation-result" :aria-label="toolText(data.operation || toolArguments.operation)">
    <p v-if="success === false || data.error" class="result-error" role="alert">{{ toolText(data.error) || output || $t('agentStream.assistantTools.failed') }}</p>
    <template v-else-if="discovery && Array.isArray(data.operations)">
      <p class="result-meta">{{ $t('agentStream.assistantTools.available', { count: operations.length }) }}</p>
      <p v-if="!operations.length && !unavailable.length" class="result-meta">{{ $t('agentStream.assistantTools.empty') }}</p>
      <article v-for="(op, index) in operations" :key="index" class="result-card">
        <div class="result-heading"><code>{{ op.id }}</code><span class="risk" :class="toolText(op.risk)">{{ riskLabel(op.risk) }}</span></div>
        <p>{{ op.summary }}</p>
        <details v-if="op.description || toolRows(op.params).length">
          <summary>{{ $t('agentStream.assistantTools.details') }}</summary>
          <p v-if="op.description && op.description !== op.summary">{{ op.description }}</p>
          <div v-for="(param, paramIndex) in toolRows(op.params)" :key="paramIndex" class="parameter">
            <div class="result-heading"><code>{{ param.name }}</code><span>{{ param.type }}</span><span v-if="param.required === true">{{ $t('agentStream.assistantTools.required') }}</span></div>
            <p v-if="param.description">{{ param.description }}</p>
            <p v-if="Array.isArray(param.enum)">{{ param.enum.join(' · ') }}</p>
            <p v-if="param.minimum !== undefined || param.maximum !== undefined">{{ param.minimum ?? '—' }} … {{ param.maximum ?? '—' }}</p>
          </div>
        </details>
      </article>
      <template v-if="unavailable.length">
        <p class="result-meta">{{ $t('agentStream.assistantTools.unavailable', { count: unavailable.length }) }}</p>
        <article v-for="(op, index) in unavailable" :key="index" class="result-card unavailable-operation">
          <div class="result-heading"><code>{{ op.id }}</code><span class="risk" :class="toolText(op.risk)">{{ riskLabel(op.risk) }}</span></div>
          <p>{{ op.summary }}</p><p class="result-warning">{{ op.reason }}</p>
        </article>
      </template>
    </template>
    <template v-else-if="!discovery && 'data' in result">
      <div class="result-body"><OperationValue :value="result.data" /></div>
      <p v-if="result.truncated === true" class="result-meta">{{ $t('agentStream.assistantTools.truncated') }}</p>
      <details v-if="Object.keys(toolRecord(result.meta)).length">
        <summary>{{ $t('agentStream.assistantTools.metadata') }}</summary>
        <OperationValue :value="result.meta" />
      </details>
    </template>
    <pre v-else class="result-body">{{ output || $t('agentStream.assistantTools.empty') }}</pre>
    <p v-if="data.hint" class="result-meta">{{ data.hint }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { parseAssistantToolOutput, toolRecord, toolRows, toolText } from '@/utils/assistantToolDisplay'
import OperationValue from './OperationValue.vue'

const props = withDefaults(defineProps<{
  discovery: boolean; output: string; data?: Record<string, unknown>; arguments?: Record<string, unknown>; success?: boolean
}>(), { success: undefined })
const { t } = useI18n()
const data = computed(() => parseAssistantToolOutput(props.output, props.data))
const toolArguments = computed(() => props.arguments || {})
const operations = computed(() => toolRows(data.value.operations))
const unavailable = computed(() => toolRows(data.value.unavailable))
const result = computed(() => toolRecord(data.value.result))
const riskLabel = (risk: unknown) => ['read', 'write', 'destructive'].includes(toolText(risk))
  ? t(`agentStream.assistantTools.risk.${risk}`) : toolText(risk)
</script>

<style scoped lang="less">
@import './assistant-tool-result.less';
.result-heading code { color: var(--td-text-color-primary); font-size: var(--app-text-xs); }
.risk { font-size: var(--app-text-xs); color: var(--td-text-color-placeholder); }
.risk.write { color: var(--td-warning-color); }
.risk.destructive { color: var(--td-error-color); }
.parameter { border-top: 1px solid var(--td-component-stroke); padding-top: 8px; margin-top: 8px; }
</style>
