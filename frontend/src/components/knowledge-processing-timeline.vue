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
const selectedAttempt = ref<number | undefined>(undefined)
const expandedRows = ref<Set<string>>(new Set(['__root__']))
const detailRow = ref<string | null>(null)
const nowTick = ref(Date.now())
let pollTimer: ReturnType<typeof setTimeout> | null = null
let nowTimer: ReturnType<typeof setInterval> | null = null
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
  if (ms === undefined || ms === null || isNaN(ms) || ms < 0) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(1)}s`
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

async function copySpan(node: SpanNode) {
  try {
    await navigator.clipboard.writeText(JSON.stringify(node, null, 2))
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
    expandedRows.value = new Set(['__root__'])
    detailRow.value = null
    fetchSpans()
  },
)

onMounted(() => {
  fetchSpans()
  nowTimer = setInterval(() => {
    nowTick.value = Date.now()
  }, 1000)
})

onBeforeUnmount(() => {
  unmounted = true
  clearTimer()
  if (nowTimer) {
    clearInterval(nowTimer)
    nowTimer = null
  }
})

// ---------- Waterfall helpers ----------

function rowKey(node: SpanNode, fallback: string): string {
  return node.span_id || fallback
}

function parseTime(s?: string | null): number | null {
  if (!s) return null
  const t = Date.parse(s)
  return Number.isNaN(t) ? null : t
}

function nodeStart(node: SpanNode): number | null {
  return parseTime(node.started_at || undefined)
}

function nodeEnd(node: SpanNode): number | null {
  const e = parseTime(node.finished_at || undefined)
  if (e !== null) return e
  const s = nodeStart(node)
  if (s !== null && typeof node.duration_ms === 'number' && node.duration_ms > 0) {
    return s + node.duration_ms
  }
  return null
}

function collectStarts(node: SpanNode | undefined, out: number[]) {
  if (!node) return
  const s = nodeStart(node)
  if (s !== null) out.push(s)
  for (const c of node.children || []) collectStarts(c, out)
}

function collectEnds(node: SpanNode | undefined, out: number[]) {
  if (!node) return
  const e = nodeEnd(node)
  if (e !== null) out.push(e)
  for (const c of node.children || []) collectEnds(c, out)
}

const traceRoot = computed<SpanNode | null>(() => {
  const trace = data.value?.trace
  if (!trace) return null
  // Build a synthesized root that contains the canonical 5 stages in order,
  // so the waterfall always shows the full pipeline shape.
  const synthChildren: SpanNode[] = stages.value.map((stage) => stage)
  return {
    ...trace,
    name: trace.name || 'knowledge_processing',
    kind: trace.kind || 'root',
    children: synthChildren,
  }
})

const t0 = computed<number | null>(() => {
  const root = traceRoot.value
  if (!root) return null
  const direct = nodeStart(root)
  if (direct !== null) return direct
  const all: number[] = []
  collectStarts(root, all)
  if (all.length === 0) return null
  return Math.min(...all)
})

const tEnd = computed<number | null>(() => {
  const root = traceRoot.value
  if (!root) return null
  const direct = parseTime(root.finished_at || undefined)
  const all: number[] = []
  collectEnds(root, all)
  let candidate: number | null = direct
  if (all.length > 0) {
    const max = Math.max(...all)
    candidate = candidate === null ? max : Math.max(candidate, max)
  }
  // If the trace is still running, extend t_end to "now" so the running bar
  // grows visibly.
  const status = data.value?.parse_status
  if (isPolling(status)) {
    const now = nowTick.value
    candidate = candidate === null ? now : Math.max(candidate, now)
  }
  return candidate
})

const totalMs = computed<number>(() => {
  if (t0.value === null || tEnd.value === null) return 0
  return Math.max(0, tEnd.value - t0.value)
})

const showRuler = computed(() => totalMs.value >= 100)

const rulerTicks = computed(() => {
  if (!showRuler.value) return [] as { left: string; label: string }[]
  const total = totalMs.value
  const fmt = (ms: number) => formatDuration(ms)
  return [
    { left: '0%', label: fmt(0) },
    { left: '25%', label: fmt(total * 0.25) },
    { left: '50%', label: fmt(total * 0.5) },
    { left: '75%', label: fmt(total * 0.75) },
    { left: '100%', label: fmt(total) },
  ]
})

interface FlatRow {
  key: string
  depth: number
  node: SpanNode
  hasChildren: boolean
  isRoot: boolean
  isStage: boolean
}

function isStageName(name?: string): boolean {
  return !!name && (STAGES as readonly string[]).includes(name)
}

const flatRows = computed<FlatRow[]>(() => {
  const root = traceRoot.value
  if (!root) return []
  const rows: FlatRow[] = []

  const rootKey = rowKey(root, '__root__')
  rows.push({
    key: rootKey,
    depth: 0,
    node: root,
    hasChildren: (root.children || []).length > 0,
    isRoot: true,
    isStage: false,
  })
  if (!expandedRows.value.has(rootKey) && !expandedRows.value.has('__root__')) {
    return rows
  }

  for (const stage of root.children || []) {
    const stageKey = rowKey(stage, `stage:${stage.name}`)
    const stageChildren = stage.children || []
    rows.push({
      key: stageKey,
      depth: 1,
      node: stage,
      hasChildren: stageChildren.length > 0,
      isRoot: false,
      isStage: true,
    })
    if (!expandedRows.value.has(stageKey)) continue

    // Walk descendants depth-first; stage children are already at depth 2.
    const walk = (n: SpanNode, depth: number, idxPath: string) => {
      const key = rowKey(n, `${idxPath}:${n.name}`)
      const kids = n.children || []
      rows.push({
        key,
        depth,
        node: n,
        hasChildren: kids.length > 0,
        isRoot: false,
        isStage: false,
      })
      if (kids.length === 0) return
      // Auto-expand non-stage descendants — only stage rows gate further depth.
      kids.forEach((c, i) => walk(c, depth + 1, `${idxPath}/${i}`))
    }
    stageChildren.forEach((c, i) => walk(c, 2, `${stageKey}/${i}`))
  }

  return rows
})

function barStyle(node: SpanNode): Record<string, string> {
  const total = totalMs.value
  if (!total || t0.value === null) return { display: 'none' }
  const start = nodeStart(node)
  if (start === null) return { display: 'none' }
  const end = nodeEnd(node) ?? (isPolling(data.value?.parse_status) ? nowTick.value : start)
  const leftPct = ((start - t0.value) / total) * 100
  const widthPct = Math.max(0.5, ((end - start) / total) * 100)
  return {
    left: `${Math.max(0, Math.min(100, leftPct))}%`,
    width: `${Math.min(100 - Math.max(0, leftPct), widthPct)}%`,
  }
}

function isPlaceholder(node: SpanNode): boolean {
  return !node.span_id && !node.started_at
}

function toggleTree(row: FlatRow, ev?: MouseEvent) {
  if (ev) ev.stopPropagation()
  if (!row.hasChildren) return
  const next = new Set(expandedRows.value)
  if (next.has(row.key)) next.delete(row.key)
  else next.add(row.key)
  expandedRows.value = next
}

function toggleDetail(row: FlatRow) {
  detailRow.value = detailRow.value === row.key ? null : row.key
}

function isObjectWithKeys(v: any): boolean {
  return v && typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length > 0
}

function prettyJSON(v: any): string {
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}

function localizedStatus(status: string): string {
  const key = `knowledgeStages.status.${status}`
  const localized = t(key)
  return localized === key ? status : localized
}

function rowLabel(row: FlatRow): string {
  if (row.isRoot) return row.node.name || 'knowledge_processing'
  if (row.isStage) return t(`knowledgeStages.stage.${row.node.name}`)
  return row.node.name
}
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
        <template v-if="totalMs > 0">
          {{ t('knowledgeStages.totalDuration', { d: formatDuration(totalMs) }) }}
        </template>
        <template v-else>
          <span>{{ t('knowledgeStages.title') }}：</span>
          <span class="kp-stage-emph">{{ currentStageIndex }}/{{ stages.length }}</span>
          <span> · {{ currentStageLabel }}</span>
        </template>
      </div>
    </template>

    <template v-else>
      <div class="kp-header">
        <span class="kp-title">{{ t('knowledgeStages.title') }}</span>
        <div class="kp-header-actions">
          <select
            v-if="(data?.latest_attempt || 0) > 1"
            class="kp-attempt-select"
            :value="selectedAttempt"
            @change="onAttemptChange(($event.target as HTMLSelectElement).value)"
          >
            <option v-for="opt in attemptOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
          </select>
          <t-button
            v-if="data?.parse_status === 'failed'"
            size="small"
            theme="primary"
            variant="outline"
            @click="onRetry"
          >
            <t-icon name="refresh" size="14px" />
            <span style="margin-left: 4px">{{ t('knowledgeStages.retry') }}</span>
          </t-button>
        </div>
      </div>

      <div v-if="data" class="kp-status-line">
        <span v-if="totalMs > 0">{{ t('knowledgeStages.totalDuration', { d: formatDuration(totalMs) }) }}</span>
        <span v-if="totalMs > 0" class="kp-status-sep">·</span>
        <span>status: {{ localizedStatus(data.parse_status) }}</span>
      </div>

      <div v-if="!data && !loading" class="kp-empty">{{ t('knowledgeStages.noActivity') }}</div>

      <template v-else-if="data">
        <div v-if="showRuler" class="kp-ruler-row">
          <div class="kp-ruler-spacer-name" />
          <div class="kp-ruler-spacer-meta" />
          <div class="kp-ruler-track">
            <span
              v-for="(tick, i) in rulerTicks"
              :key="i"
              class="kp-ruler-tick"
              :style="{ left: tick.left }"
            >
              <span class="kp-ruler-tick-line" />
              <span class="kp-ruler-tick-label">{{ tick.label }}</span>
            </span>
          </div>
        </div>

        <div class="kp-rows">
          <template v-for="row in flatRows" :key="row.key">
            <div
              class="kp-row"
              :class="{ 'kp-row-active': detailRow === row.key }"
              @click="toggleDetail(row)"
            >
              <div class="kp-cell-name">
                <div class="kp-name-inner" :style="{ paddingLeft: row.depth * 16 + 'px' }">
                  <button
                    v-if="row.hasChildren"
                    type="button"
                    class="kp-tree-toggle"
                    :class="{ 'kp-tree-toggle-open': expandedRows.has(row.key) }"
                    :aria-label="row.key"
                    @click="toggleTree(row, $event)"
                  >
                    <t-icon name="chevron-right" size="12px" />
                  </button>
                  <span v-else class="kp-tree-toggle-spacer" />
                  <span
                    class="kp-status-dot"
                    :class="['kp-dot-' + row.node.status, { 'kp-dot-placeholder': isPlaceholder(row.node) }]"
                  />
                  <span class="kp-name-text" :class="{ 'kp-name-root': row.isRoot }">{{ rowLabel(row) }}</span>
                </div>
              </div>
              <div class="kp-cell-meta">
                <span class="kp-meta-status">{{ localizedStatus(row.node.status) }}</span>
                <span class="kp-meta-dur">{{ formatDuration(row.node.duration_ms) }}</span>
              </div>
              <div class="kp-cell-bar">
                <div v-if="isPlaceholder(row.node)" class="kp-bar kp-bar-placeholder" />
                <div
                  v-else
                  class="kp-bar"
                  :class="[
                    'kp-bar-' + row.node.status,
                    { 'kp-bar-running': row.node.status === 'running' },
                  ]"
                  :style="barStyle(row.node)"
                />
              </div>
            </div>

            <div v-if="detailRow === row.key" class="kp-detail" :class="{ 'kp-detail-error': row.node.status === 'failed' || row.node.status === 'cancelled' }">
              <div class="kp-detail-grid">
                <div class="kp-detail-field">
                  <span class="kp-detail-label">Status</span>
                  <span class="kp-status-chip" :class="'kp-chip-' + row.node.status">{{ localizedStatus(row.node.status) }}</span>
                </div>
                <div class="kp-detail-field">
                  <span class="kp-detail-label">Started</span>
                  <span class="kp-detail-value">{{ row.node.started_at || '—' }}</span>
                </div>
                <div class="kp-detail-field">
                  <span class="kp-detail-label">Finished</span>
                  <span class="kp-detail-value">{{ row.node.finished_at || '—' }}</span>
                </div>
                <div class="kp-detail-field">
                  <span class="kp-detail-label">Duration</span>
                  <span class="kp-detail-value">{{ formatDuration(row.node.duration_ms) }}</span>
                </div>
              </div>

              <div v-if="row.node.error_code || row.node.error_message" class="kp-detail-error-block">
                <div class="kp-detail-error-title">
                  <t-icon name="error-circle" size="14px" />
                  <span>{{ localizedErrorTitle(row.node.error_code) || localizedStatus(row.node.status) }}</span>
                </div>
                <div v-if="row.node.error_message" class="kp-detail-error-msg">{{ row.node.error_message }}</div>
              </div>

              <div v-if="isObjectWithKeys(row.node.input)" class="kp-detail-section">
                <div class="kp-detail-label">input</div>
                <pre class="kp-json">{{ prettyJSON(row.node.input) }}</pre>
              </div>
              <div v-if="isObjectWithKeys(row.node.output)" class="kp-detail-section">
                <div class="kp-detail-label">output</div>
                <pre class="kp-json">{{ prettyJSON(row.node.output) }}</pre>
              </div>
              <div v-if="isObjectWithKeys(row.node.metadata)" class="kp-detail-section">
                <div class="kp-detail-label">metadata</div>
                <pre class="kp-json">{{ prettyJSON(row.node.metadata) }}</pre>
              </div>

              <div class="kp-detail-actions">
                <t-button size="small" variant="outline" @click.stop="copySpan(row.node)">
                  <t-icon name="copy" size="14px" />
                  <span style="margin-left: 4px">{{ t('knowledgeStages.copyDetails') }}</span>
                </t-button>
              </div>
            </div>
          </template>
        </div>
      </template>

      <div v-if="data?.last_error && data?.parse_status === 'failed'" class="kp-last-error">
        <div class="kp-last-error-row">
          <t-icon name="error-circle-filled" size="16px" class="kp-last-error-icon" />
          <div class="kp-last-error-body">
            <div class="kp-last-error-title">{{ localizedErrorTitle(data.last_error.error_code) }}</div>
            <div class="kp-last-error-suggestion">{{ localizedErrorSuggestion(data.last_error.error_code) }}</div>
            <div v-if="data.last_error.error_message" class="kp-last-error-raw">{{ data.last_error.error_message }}</div>
          </div>
          <t-button size="small" theme="primary" @click="onRetry">
            <t-icon name="refresh" size="14px" />
            <span style="margin-left: 4px">{{ t('knowledgeStages.retry') }}</span>
          </t-button>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.kp-timeline {
  font-size: 13px;
  color: var(--td-text-color-primary);
  width: 100%;
}

/* ============ HEADER ============ */
.kp-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--td-component-border, #e7e7e7);
  margin-bottom: 8px;
}
.kp-title {
  font-weight: 600;
  font-size: 14px;
}
.kp-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
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
.kp-status-line {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  margin-bottom: 10px;
  display: flex;
  gap: 6px;
  align-items: center;
}
.kp-status-sep {
  color: var(--td-text-color-placeholder);
}

/* ============ RULER ============ */
.kp-ruler-row {
  display: grid;
  grid-template-columns: minmax(180px, 30%) minmax(110px, 14%) 1fr;
  align-items: center;
  margin-bottom: 4px;
}
.kp-ruler-track {
  position: relative;
  height: 16px;
}
.kp-ruler-tick {
  position: absolute;
  top: 0;
  transform: translateX(-50%);
  display: flex;
  flex-direction: column;
  align-items: center;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
}
.kp-ruler-tick:first-child { transform: translateX(0); align-items: flex-start; }
.kp-ruler-tick:last-child { transform: translateX(-100%); align-items: flex-end; }
.kp-ruler-tick-line {
  width: 1px;
  height: 4px;
  background: var(--td-component-border, #e7e7e7);
}
.kp-ruler-tick-label {
  margin-top: 1px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

/* ============ ROWS GRID ============ */
.kp-rows {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 6px;
  overflow: hidden;
  background: var(--td-bg-color-container, #fff);
}
.kp-row {
  display: grid;
  grid-template-columns: minmax(180px, 30%) minmax(110px, 14%) 1fr;
  align-items: center;
  min-height: 28px;
  cursor: pointer;
  border-bottom: 1px solid var(--td-component-border, #f0f0f0);
  transition: background-color 0.12s;
}
.kp-row:last-child { border-bottom: none; }
.kp-row:hover { background: var(--td-bg-color-container-hover, rgba(0, 0, 0, 0.025)); }
.kp-row-active { background: var(--td-bg-color-container-hover, rgba(0, 0, 0, 0.04)); }

.kp-cell-name {
  padding: 4px 8px 4px 8px;
  min-width: 0;
}
.kp-name-inner {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.kp-tree-toggle {
  width: 14px;
  height: 14px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  padding: 0;
  cursor: pointer;
  color: var(--td-text-color-placeholder);
  transition: transform 0.15s;
}
.kp-tree-toggle-open { transform: rotate(90deg); }
.kp-tree-toggle-spacer { width: 14px; height: 14px; display: inline-block; }
.kp-status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--td-text-color-placeholder, #c0c0c0);
}
.kp-name-text {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.kp-name-root { font-weight: 600; }

.kp-cell-meta {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  padding: 4px 12px 4px 4px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}
.kp-meta-status {
  font-size: 11px;
  text-transform: lowercase;
  color: var(--td-text-color-placeholder);
}
.kp-meta-dur {
  width: 56px;
  text-align: right;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--td-text-color-secondary);
}

.kp-cell-bar {
  position: relative;
  height: 18px;
  margin-right: 12px;
}
.kp-bar {
  position: absolute;
  top: 5px;
  height: 8px;
  border-radius: 2px;
  background: var(--td-text-color-placeholder, #c0c0c0);
  min-width: 2px;
}
.kp-bar-done { background: var(--td-success-color); }
.kp-bar-failed { background: var(--td-error-color); }
.kp-bar-cancelled {
  background: transparent;
  border: 1px dashed var(--td-text-color-placeholder, #c0c0c0);
}
.kp-bar-skipped {
  background: var(--td-text-color-placeholder);
  opacity: 0.5;
}
.kp-bar-pending {
  background: var(--td-component-border, #e7e7e7);
}
.kp-bar-running {
  background: var(--td-brand-color);
  background-image: linear-gradient(
    45deg,
    rgba(255, 255, 255, 0.25) 25%,
    transparent 25%,
    transparent 50%,
    rgba(255, 255, 255, 0.25) 50%,
    rgba(255, 255, 255, 0.25) 75%,
    transparent 75%,
    transparent
  );
  background-size: 12px 12px;
  animation: kpStripes 0.8s linear infinite;
}
.kp-bar-placeholder {
  right: 0;
  top: 6px;
  height: 6px;
  width: 12px;
  background: transparent;
  border: 1px dashed var(--td-component-border, #e7e7e7);
  border-radius: 2px;
}

@keyframes kpStripes {
  from { background-position: 0 0; }
  to { background-position: 12px 0; }
}

/* ============ STATUS DOTS (shared) ============ */
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
.kp-dot-skipped { background: var(--td-text-color-placeholder, #c0c0c0); opacity: 0.5; }
.kp-dot-pending {
  background: transparent;
  border: 1px solid var(--td-component-border, #dcdcdc);
}
.kp-dot-placeholder {
  background: transparent;
  border: 1px dashed var(--td-component-border, #dcdcdc);
}

@keyframes kpPulse {
  0%, 100% { box-shadow: 0 0 0 0 var(--td-brand-color-light, rgba(0, 102, 255, 0.4)); }
  50% { box-shadow: 0 0 0 4px transparent; }
}

/* ============ DETAIL ROW ============ */
.kp-detail {
  grid-column: 1 / -1;
  padding: 12px 16px;
  background: var(--td-bg-color-secondarycontainer, #fafafa);
  border-bottom: 1px solid var(--td-component-border, #f0f0f0);
  font-size: 12px;
}
.kp-detail-error {
  border-left: 3px solid var(--td-error-color);
}
.kp-detail-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 8px 16px;
  margin-bottom: 8px;
}
.kp-detail-field {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.kp-detail-label {
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--td-text-color-placeholder);
}
.kp-detail-value {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--td-text-color-secondary);
  overflow-wrap: anywhere;
}
.kp-status-chip {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 10px;
  font-size: 11px;
  background: var(--td-bg-color-component, #eee);
  color: var(--td-text-color-primary);
  width: fit-content;
}
.kp-chip-done { background: rgba(0, 168, 112, 0.12); color: var(--td-success-color); }
.kp-chip-running { background: rgba(0, 102, 255, 0.12); color: var(--td-brand-color); }
.kp-chip-failed { background: rgba(217, 48, 37, 0.12); color: var(--td-error-color); }
.kp-chip-cancelled { background: var(--td-bg-color-component, #eee); color: var(--td-text-color-secondary); }
.kp-chip-skipped { background: var(--td-bg-color-component, #eee); color: var(--td-text-color-placeholder); }
.kp-chip-pending { background: var(--td-bg-color-component, #eee); color: var(--td-text-color-secondary); }

.kp-detail-error-block {
  margin: 6px 0 10px;
  padding: 8px 10px;
  border: 1px solid var(--td-error-color);
  border-radius: 4px;
  background: rgba(217, 48, 37, 0.04);
}
.kp-detail-error-title {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--td-error-color);
  font-weight: 600;
  margin-bottom: 4px;
}
.kp-detail-error-msg {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}

.kp-detail-section {
  margin-top: 8px;
}
.kp-json {
  margin: 4px 0 0;
  padding: 8px 10px;
  background: var(--td-bg-color-container, #fff);
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 4px;
  max-height: 240px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--td-text-color-primary);
}
.kp-detail-actions {
  margin-top: 8px;
  display: flex;
  justify-content: flex-end;
}

/* ============ EMPTY ============ */
.kp-empty {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  padding: 20px 0;
  text-align: center;
}

/* ============ LAST-ERROR (failed only) ============ */
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
