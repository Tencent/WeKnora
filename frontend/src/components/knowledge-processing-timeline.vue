<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch, computed } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { getKnowledgeSpans, reparseKnowledge } from '@/api/knowledge-base/index'

interface SpanNode {
  span_id?: string
  name: string
  kind: string
  status: string
  started_at?: string | null
  finished_at?: string | null
  duration_ms?: number
  error_code?: string
  error_message?: string
  input?: any
  output?: any
  metadata?: any
  children?: SpanNode[]
}

interface LastError {
  name: string
  error_code: string
  error_message: string
  finished_at?: string
}

interface SpansResponse {
  knowledge_id: string
  attempt: number
  latest_attempt: number
  parse_status: string
  current_stage?: string
  trace: SpanNode
  last_error?: LastError | null
}

const props = defineProps<{
  knowledgeId: string
  parseStatus?: string
  autoPoll?: boolean
  compact?: boolean
}>()

const { t } = useI18n()

const STAGES = ['docreader', 'chunking', 'embedding', 'multimodal', 'postprocess'] as const

const data = ref<SpansResponse | null>(null)
const loading = ref(false)
const expandedStage = ref<string>('')
const selectedAttempt = ref<number | undefined>(undefined)
let pollTimer: ReturnType<typeof setTimeout> | null = null
let unmounted = false

const stages = computed<SpanNode[]>(() => {
  const trace = data.value?.trace
  const children = trace?.children || []
  const byName = new Map<string, SpanNode>()
  for (const c of children) {
    if (c && c.kind === 'stage' && c.name) byName.set(c.name, c)
  }
  return STAGES.map((n) => byName.get(n) || ({ name: n, kind: 'stage', status: 'pending' } as SpanNode))
})

const currentStageLabel = computed(() => {
  const running = stages.value.find((s) => s.status === 'running')
  const failed = stages.value.find((s) => s.status === 'failed')
  const target = failed || running || stages.value.find((s) => s.status === 'pending') || stages.value[stages.value.length - 1]
  return target ? t(`knowledgeStages.stage.${target.name}`) : ''
})

const currentStageIndex = computed(() => {
  const idx = stages.value.findIndex((s) => s.status === 'running' || s.status === 'failed')
  if (idx >= 0) return idx + 1
  const done = stages.value.filter((s) => s.status === 'done').length
  return Math.min(done + 1, stages.value.length)
})

function formatDuration(ms?: number): string {
  if (!ms || ms < 0) return '—'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function statusIcon(status: string): string {
  switch (status) {
    case 'done': return 'check'
    case 'running': return 'loading'
    case 'failed': return 'close'
    case 'cancelled': return 'minus-circle'
    case 'skipped': return 'minus'
    default: return 'circle'
  }
}

function isPolling(status?: string): boolean {
  return status === 'pending' || status === 'processing'
}

function clearTimer() {
  if (pollTimer) {
    clearTimeout(pollTimer)
    pollTimer = null
  }
}

async function fetchSpans() {
  if (!props.knowledgeId) return
  loading.value = true
  try {
    const res: any = await getKnowledgeSpans(props.knowledgeId, selectedAttempt.value)
    if (res?.success && res.data) {
      data.value = res.data as SpansResponse
      if (selectedAttempt.value === undefined) {
        selectedAttempt.value = data.value.attempt
      }
    }
  } catch (e) {
    // Swallow — surface via empty state
  } finally {
    loading.value = false
  }

  if (unmounted) return
  if (props.autoPoll !== false && data.value && isPolling(data.value.parse_status)) {
    clearTimer()
    pollTimer = setTimeout(fetchSpans, 2000)
  } else {
    clearTimer()
  }
}

function localizedErrorTitle(code?: string): string {
  if (!code) return ''
  const key = `knowledgeStages.errorCode.${code}`
  const localized = t(key)
  // vue-i18n returns the key when missing
  return localized === key ? code : localized
}

function localizedErrorSuggestion(code?: string): string {
  if (!code) return ''
  const key = `knowledgeStages.errorCode.${code}_SUGGESTION`
  const localized = t(key)
  if (localized !== key) return localized
  const fallback = t('knowledgeStages.errorCode.UNKNOWN_SUGGESTION')
  return fallback === 'knowledgeStages.errorCode.UNKNOWN_SUGGESTION' ? '' : fallback
}

function toggleExpand(stage: SpanNode) {
  if (stage.status !== 'failed' && stage.status !== 'cancelled') return
  expandedStage.value = expandedStage.value === stage.name ? '' : stage.name
}

async function copySpan(stage: SpanNode) {
  try {
    await navigator.clipboard.writeText(JSON.stringify(stage, null, 2))
    MessagePlugin.success(t('knowledgeStages.copied'))
  } catch {
    MessagePlugin.error(t('knowledgeStages.copyDetails'))
  }
}

async function onRetry() {
  if (!props.knowledgeId) return
  try {
    await reparseKnowledge(props.knowledgeId)
    selectedAttempt.value = undefined
    await fetchSpans()
  } catch {
    // ignore
  }
}

function onAttemptChange(value: any) {
  const n = Number(value)
  if (!Number.isNaN(n)) {
    selectedAttempt.value = n
    fetchSpans()
  }
}

const attemptOptions = computed(() => {
  const latest = data.value?.latest_attempt || 1
  const opts: { label: string; value: number }[] = []
  for (let i = latest; i >= 1; i--) {
    opts.push({
      label: i === latest ? t('knowledgeStages.attemptLatest', { n: i }) : t('knowledgeStages.attempt', { n: i }),
      value: i,
    })
  }
  return opts
})

watch(
  () => props.knowledgeId,
  () => {
    selectedAttempt.value = undefined
    data.value = null
    expandedStage.value = ''
    fetchSpans()
  },
)

onMounted(() => {
  fetchSpans()
})

onBeforeUnmount(() => {
  unmounted = true
  clearTimer()
})
</script>

<template>
  <div class="kp-timeline" :class="{ 'kp-compact': compact }">
    <template v-if="compact">
      <div class="kp-compact-row">
        <span
          v-for="s in stages"
          :key="s.name"
          class="kp-dot"
          :class="['kp-dot-' + s.status]"
          :title="t(`knowledgeStages.stage.${s.name}`) + ' · ' + t(`knowledgeStages.status.${s.status}`)"
        />
      </div>
      <div class="kp-compact-caption">
        <span>{{ t('knowledgeStages.title') }}：</span>
        <span class="kp-stage-emph">{{ currentStageIndex }}/{{ stages.length }}</span>
        <span> · {{ currentStageLabel }}</span>
      </div>
    </template>

    <template v-else>
      <div class="kp-header">
        <span class="kp-title">{{ t('knowledgeStages.title') }}</span>
        <select
          v-if="(data?.latest_attempt || 0) > 1"
          class="kp-attempt-select"
          :value="selectedAttempt"
          @change="onAttemptChange(($event.target as HTMLSelectElement).value)"
        >
          <option v-for="opt in attemptOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
        </select>
      </div>

      <div class="kp-steps">
        <template v-for="(s, idx) in stages" :key="s.name">
          <div
            class="kp-step"
            :class="['kp-step-' + s.status, { 'kp-clickable': s.status === 'failed' || s.status === 'cancelled' }]"
            @click="toggleExpand(s)"
          >
            <div class="kp-icon-wrap">
              <t-icon :name="statusIcon(s.status)" size="14px" />
            </div>
            <div class="kp-step-name">{{ t(`knowledgeStages.stage.${s.name}`) }}</div>
            <div class="kp-step-dur">{{ formatDuration(s.duration_ms) }}</div>
          </div>
          <div v-if="idx < stages.length - 1" class="kp-connector" :class="'kp-connector-' + s.status" />
        </template>
      </div>

      <div v-for="s in stages" :key="'err-' + s.name">
        <div v-if="expandedStage === s.name" class="kp-error-card">
          <div class="kp-error-title">
            <t-icon name="error-circle" size="14px" />
            <span>{{ localizedErrorTitle(s.error_code) || t(`knowledgeStages.status.${s.status}`) }}</span>
          </div>
          <div v-if="s.error_message" class="kp-error-msg">{{ s.error_message }}</div>
          <t-button size="small" variant="outline" @click="copySpan(s)">
            <t-icon name="copy" size="14px" />
            <span style="margin-left: 4px">{{ t('knowledgeStages.copyDetails') }}</span>
          </t-button>
        </div>
      </div>

      <div v-if="data?.last_error" class="kp-last-error">
        <div class="kp-last-error-row">
          <t-icon name="error-circle-filled" size="16px" class="kp-last-error-icon" />
          <div class="kp-last-error-body">
            <div class="kp-last-error-title">{{ localizedErrorTitle(data.last_error.error_code) }}</div>
            <div class="kp-last-error-suggestion">{{ localizedErrorSuggestion(data.last_error.error_code) }}</div>
            <div v-if="data.last_error.error_message" class="kp-last-error-raw">{{ data.last_error.error_message }}</div>
          </div>
          <t-button
            v-if="data.parse_status === 'failed'"
            size="small"
            theme="primary"
            @click="onRetry"
          >
            <t-icon name="refresh" size="14px" />
            <span style="margin-left: 4px">{{ t('knowledgeStages.retry') }}</span>
          </t-button>
        </div>
      </div>

      <div v-else-if="!loading && !data" class="kp-empty">{{ t('knowledgeStages.noActivity') }}</div>
    </template>
  </div>
</template>

<style scoped>
.kp-timeline {
  font-size: 13px;
  color: var(--td-text-color-primary);
}

/* ============ FULL MODE ============ */
.kp-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.kp-title {
  font-weight: 600;
  font-size: 14px;
}
.kp-attempt-select {
  font-size: 12px;
  padding: 2px 8px;
  border: 1px solid var(--td-component-border, #dcdcdc);
  border-radius: 4px;
  background: var(--td-bg-color-component);
  color: var(--td-text-color-primary);
  cursor: pointer;
}

.kp-steps {
  display: flex;
  align-items: flex-start;
  gap: 4px;
  width: 100%;
}
.kp-step {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  flex: 0 0 auto;
  min-width: 72px;
  padding: 4px 2px;
  border-radius: 6px;
  transition: background-color 0.15s;
}
.kp-clickable {
  cursor: pointer;
}
.kp-clickable:hover {
  background: var(--td-bg-color-container-hover, rgba(0, 0, 0, 0.04));
}
.kp-icon-wrap {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  background: var(--td-text-color-placeholder, #c0c0c0);
}
.kp-step-name {
  font-size: 12px;
  text-align: center;
  color: var(--td-text-color-primary);
}
.kp-step-dur {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}
.kp-connector {
  flex: 1 1 auto;
  height: 2px;
  margin-top: 11px;
  background: var(--td-component-border, #e0e0e0);
  min-width: 12px;
}
.kp-connector-done {
  background: var(--td-success-color);
}
.kp-connector-failed {
  background: var(--td-error-color);
}

.kp-step-done .kp-icon-wrap {
  background: var(--td-success-color);
}
.kp-step-running .kp-icon-wrap {
  background: var(--td-brand-color);
  animation: kpPulse 1.4s ease-in-out infinite;
}
.kp-step-failed .kp-icon-wrap {
  background: var(--td-error-color);
}
.kp-step-cancelled .kp-icon-wrap {
  background: transparent;
  color: var(--td-text-color-placeholder);
  border: 1px dashed var(--td-text-color-placeholder, #c0c0c0);
}
.kp-step-skipped .kp-icon-wrap {
  background: var(--td-text-color-placeholder, #c0c0c0);
}
.kp-step-pending .kp-icon-wrap {
  background: transparent;
  color: var(--td-text-color-placeholder);
  border: 1px solid var(--td-component-border, #dcdcdc);
}

@keyframes kpPulse {
  0%, 100% { box-shadow: 0 0 0 0 var(--td-brand-color-light, rgba(0, 102, 255, 0.4)); }
  50% { box-shadow: 0 0 0 6px transparent; }
}

.kp-error-card {
  margin-top: 12px;
  padding: 10px 12px;
  background: var(--td-bg-color-component);
  border: 1px solid var(--td-error-color);
  border-radius: 6px;
}
.kp-error-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  color: var(--td-error-color);
  margin-bottom: 6px;
}
.kp-error-msg {
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--td-text-color-secondary);
  margin-bottom: 8px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.kp-last-error {
  margin-top: 12px;
  padding: 10px 12px;
  background: var(--td-bg-color-component);
  border-left: 3px solid var(--td-error-color);
  border-radius: 4px;
}
.kp-last-error-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}
.kp-last-error-icon {
  color: var(--td-error-color);
  flex-shrink: 0;
  margin-top: 2px;
}
.kp-last-error-body {
  flex: 1 1 auto;
  min-width: 0;
}
.kp-last-error-title {
  font-weight: 600;
  color: var(--td-text-color-primary);
}
.kp-last-error-suggestion {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  margin-top: 2px;
}
.kp-last-error-raw {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  margin-top: 4px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  white-space: pre-wrap;
  word-break: break-word;
}

.kp-empty {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  padding: 8px 0;
}

/* ============ COMPACT MODE ============ */
.kp-compact {
  max-width: 320px;
}
.kp-compact-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.kp-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--td-text-color-placeholder, #c0c0c0);
  display: inline-block;
}
.kp-dot-done { background: var(--td-success-color); }
.kp-dot-running {
  background: var(--td-brand-color);
  animation: kpPulse 1.4s ease-in-out infinite;
}
.kp-dot-failed { background: var(--td-error-color); }
.kp-dot-cancelled {
  background: transparent;
  border: 1px dashed var(--td-text-color-placeholder, #c0c0c0);
}
.kp-dot-skipped { background: var(--td-text-color-placeholder, #c0c0c0); opacity: 0.6; }
.kp-dot-pending {
  background: transparent;
  border: 1px solid var(--td-component-border, #dcdcdc);
}
.kp-compact-caption {
  margin-top: 4px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.kp-stage-emph {
  color: var(--td-brand-color);
  font-weight: 600;
}
</style>
