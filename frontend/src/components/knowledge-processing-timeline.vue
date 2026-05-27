<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount, watch, computed } from 'vue'
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
const selectedSpanId = ref<string | null>(null)
const expandedJsonKeys = ref<Set<string>>(new Set())
const nowTick = ref(Date.now())

// Per-attempt status cache for the tab strip. Lazy-fetched: when the strip
// first becomes visible, we hit /spans?attempt=n once per missing tab so we
// can colour the chip without polling all of them indefinitely.
const attemptStatuses = reactive<Map<number, string>>(new Map())

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
      // Always refresh the active tab's status from the freshest payload.
      const traceStatus = data.value.trace?.status || data.value.parse_status || 'running'
      attemptStatuses.set(data.value.attempt, traceStatus)
      ensureAttemptStatuses()
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

// Lazy per-attempt status discovery. We only fire one request per missing
// number; the active attempt is always populated via fetchSpans, so usually
// only the older attempts hit this path.
function ensureAttemptStatuses() {
  const latest = data.value?.latest_attempt || 0
  if (latest <= 1) return
  for (let n = 1; n <= latest; n++) {
    if (attemptStatuses.has(n)) continue
    getKnowledgeSpans(props.knowledgeId, n)
      .then((res: any) => {
        if (res?.success && res.data?.trace) {
          attemptStatuses.set(n, res.data.trace.status || res.data.parse_status || 'running')
        }
      })
      .catch(() => {})
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
    attemptStatuses.clear()
    selectedSpanId.value = null
    await fetchSpans()
  } catch {
    // ignore
  }
}

function onAttemptChange(n: number) {
  if (Number.isNaN(n)) return
  selectedAttempt.value = n
  selectedSpanId.value = null
  fetchSpans()
}

watch(
  () => props.knowledgeId,
  () => {
    selectedAttempt.value = undefined
    data.value = null
    expandedRows.value = new Set(['__root__'])
    selectedSpanId.value = null
    attemptStatuses.clear()
    fetchSpans()
  },
)

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape' && selectedSpanId.value) {
    selectedSpanId.value = null
  }
}

onMounted(() => {
  fetchSpans()
  nowTimer = setInterval(() => {
    nowTick.value = Date.now()
  }, 1000)
  window.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  unmounted = true
  clearTimer()
  if (nowTimer) {
    clearInterval(nowTimer)
    nowTimer = null
  }
  window.removeEventListener('keydown', onKeydown)
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
  const status = data.value?.parse_status
  if (isPolling(status)) {
    const now = nowTick.value
    candidate = candidate === null ? now : Math.max(candidate, now)
  }
  return candidate
})

const totalMs = computed<number>(() => {
  if (t0.value === null || tEnd.value === null) return 0
  // Prefer the trace's own reported duration when terminal — it's authoritative
  // now that the root span closes properly on done/failed.
  const traceDur = data.value?.trace?.duration_ms
  if (typeof traceDur === 'number' && traceDur > 0 && !isPolling(data.value?.parse_status)) {
    return traceDur
  }
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
      kids.forEach((c, i) => walk(c, depth + 1, `${idxPath}/${i}`))
    }
    stageChildren.forEach((c, i) => walk(c, 2, `${stageKey}/${i}`))
  }

  return rows
})

const selectedRow = computed<FlatRow | null>(() => {
  const id = selectedSpanId.value
  if (!id) return null
  return flatRows.value.find((r) => r.key === id) || null
})

const drawerOpen = computed(() => selectedSpanId.value !== null && selectedRow.value !== null)

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

function selectRow(row: FlatRow) {
  if (selectedSpanId.value === row.key) {
    selectedSpanId.value = null
  } else {
    selectedSpanId.value = row.key
  }
}

function closeDrawer() {
  selectedSpanId.value = null
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
  if (row.isRoot) return t('knowledgeStages.root')
  if (row.isStage) return t(`knowledgeStages.stage.${row.node.name}`)
  return row.node.name
}

function isMonoRowName(row: FlatRow): boolean {
  // Monospace only for free-form sub-spans (not root, not the canonical stages).
  return !row.isRoot && !row.isStage
}

// ---------- Detail key/value rendering ----------

function humanizeKey(k: string): string {
  return k
    .replace(/[_-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/\b([a-z])/g, (_, c: string) => c.toUpperCase())
}

interface KvEntry {
  key: string
  label: string
  kind: 'scalar' | 'bool' | 'array' | 'object'
  display: string
  raw: any
}

function buildKvEntries(obj: any): KvEntry[] {
  if (!isObjectWithKeys(obj)) return []
  const entries: KvEntry[] = []
  for (const [key, value] of Object.entries(obj)) {
    entries.push(toKvEntry(key, value))
  }
  return entries
}

function toKvEntry(key: string, value: any): KvEntry {
  const label = humanizeKey(key)
  if (value === null || value === undefined) {
    return { key, label, kind: 'scalar', display: '—', raw: value }
  }
  if (typeof value === 'boolean') {
    return { key, label, kind: 'bool', display: value ? '✓' : '✗', raw: value }
  }
  if (typeof value === 'number') {
    return { key, label, kind: 'scalar', display: value.toLocaleString(), raw: value }
  }
  if (typeof value === 'string') {
    return { key, label, kind: 'scalar', display: value, raw: value }
  }
  if (Array.isArray(value)) {
    return { key, label, kind: 'array', display: `[Array length=${value.length}]`, raw: value }
  }
  if (typeof value === 'object') {
    const n = Object.keys(value as object).length
    return { key, label, kind: 'object', display: `{Object · ${n} keys}`, raw: value }
  }
  return { key, label, kind: 'scalar', display: String(value), raw: value }
}

function jsonExpandKey(section: string, key: string): string {
  return `${selectedSpanId.value || ''}::${section}::${key}`
}

function toggleJsonKey(section: string, key: string) {
  const k = jsonExpandKey(section, key)
  const next = new Set(expandedJsonKeys.value)
  if (next.has(k)) next.delete(k)
  else next.add(k)
  expandedJsonKeys.value = next
}

function isJsonExpanded(section: string, key: string): boolean {
  return expandedJsonKeys.value.has(jsonExpandKey(section, key))
}

function formatTime(s?: string | null): string {
  if (!s) return '—'
  const ts = Date.parse(s)
  if (Number.isNaN(ts)) return s
  const d = new Date(ts)
  const ms = String(d.getMilliseconds()).padStart(3, '0')
  return `${d.toLocaleTimeString()}.${ms}`
}

// ---------- Attempt strip helpers ----------

interface AttemptTab {
  n: number
  status: string
  active: boolean
}

const attemptTabs = computed<AttemptTab[]>(() => {
  const latest = data.value?.latest_attempt || 0
  if (latest <= 1) return []
  const active = selectedAttempt.value ?? data.value?.attempt ?? latest
  const out: AttemptTab[] = []
  for (let n = 1; n <= latest; n++) {
    out.push({
      n,
      status: attemptStatuses.get(n) || 'unknown',
      active: n === active,
    })
  }
  return out
})

function attemptGlyph(status: string): { ch: string; cls: string } {
  switch (status) {
    case 'done':
      return { ch: '✓', cls: 'kp-tab-glyph-done' }
    case 'failed':
      return { ch: '✗', cls: 'kp-tab-glyph-failed' }
    case 'running':
    case 'pending':
    case 'processing':
      return { ch: '●', cls: 'kp-tab-glyph-running' }
    default:
      return { ch: '–', cls: 'kp-tab-glyph-unknown' }
  }
}

const headerStatusGlyph = computed(() => {
  const s = data.value?.trace?.status || data.value?.parse_status || ''
  return attemptGlyph(s)
})

const headerStatusText = computed(() => {
  const s = data.value?.trace?.status || data.value?.parse_status || ''
  return s ? localizedStatus(s) : ''
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
      <div class="kp-shell">
        <div class="kp-main">
          <div class="kp-header">
            <div class="kp-header-left">
              <span class="kp-title">{{ t('knowledgeStages.title') }}</span>
            </div>
            <div class="kp-header-actions">
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
            <span v-if="totalMs > 0" class="kp-total">{{ t('knowledgeStages.total', { d: formatDuration(totalMs) }) }}</span>
            <span v-if="totalMs > 0 && headerStatusText" class="kp-status-sep">·</span>
            <span v-if="headerStatusText" class="kp-status-cluster">
              <span class="kp-status-glyph" :class="headerStatusGlyph.cls">{{ headerStatusGlyph.ch }}</span>
              <span>{{ headerStatusText }}</span>
            </span>
          </div>

          <div v-if="loading && !data" class="kp-loading">
            <t-loading size="small" />
          </div>
          <div v-else-if="!data && !loading" class="kp-empty">{{ t('knowledgeStages.noActivity') }}</div>

          <template v-else-if="data">
            <div v-if="attemptTabs.length > 0" class="kp-tab-strip">
              <button
                v-for="tab in attemptTabs"
                :key="tab.n"
                type="button"
                class="kp-tab"
                :class="{ 'kp-tab-active': tab.active }"
                @click="onAttemptChange(tab.n)"
              >
                <span class="kp-tab-num">#{{ tab.n }}</span>
                <span class="kp-tab-glyph" :class="attemptGlyph(tab.status).cls">{{ attemptGlyph(tab.status).ch }}</span>
              </button>
            </div>

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
              <div
                v-for="row in flatRows"
                :key="row.key"
                class="kp-row"
                :class="{ 'kp-row-active': selectedSpanId === row.key }"
                @click="selectRow(row)"
              >
                <div class="kp-cell-name">
                  <div class="kp-name-inner" :style="{ paddingLeft: row.depth * 14 + 'px' }">
                    <button
                      v-if="row.hasChildren && !row.isRoot"
                      type="button"
                      class="kp-tree-toggle"
                      :class="{ 'kp-tree-toggle-open': expandedRows.has(row.key) }"
                      :aria-label="row.key"
                      @click="toggleTree(row, $event)"
                    >▸</button>
                    <span v-else class="kp-tree-toggle-spacer" />
                    <span
                      class="kp-status-dot"
                      :class="['kp-dot-' + row.node.status, { 'kp-dot-placeholder': isPlaceholder(row.node) }]"
                    />
                    <span
                      class="kp-name-text"
                      :class="{ 'kp-name-root': row.isRoot, 'kp-name-mono': isMonoRowName(row) }"
                    >{{ rowLabel(row) }}</span>
                  </div>
                </div>
                <div class="kp-cell-dur">{{ formatDuration(row.node.duration_ms) }}</div>
                <div class="kp-cell-bar">
                  <div v-if="isPlaceholder(row.node)" class="kp-bar kp-bar-placeholder" />
                  <div
                    v-else
                    class="kp-bar"
                    :class="['kp-bar-' + row.node.status, { 'kp-bar-running': row.node.status === 'running' }]"
                    :style="barStyle(row.node)"
                  />
                </div>
              </div>
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
        </div>

        <!-- Drawer (slides in within the shell) -->
        <div class="kp-drawer" :class="{ 'kp-drawer-open': drawerOpen }">
          <template v-if="selectedRow">
            <div class="kp-drawer-header">
              <div class="kp-drawer-title">
                <span
                  class="kp-status-dot kp-drawer-dot"
                  :class="['kp-dot-' + selectedRow.node.status]"
                />
                <span class="kp-drawer-name">{{ rowLabel(selectedRow) }}</span>
              </div>
              <div class="kp-drawer-meta">
                <span class="kp-status-chip" :class="'kp-chip-' + selectedRow.node.status">
                  {{ localizedStatus(selectedRow.node.status) }}
                </span>
                <button type="button" class="kp-drawer-close" @click="closeDrawer" aria-label="close">×</button>
              </div>
            </div>

            <div class="kp-drawer-body">
              <div class="kp-section">
                <div class="kp-kv-grid">
                  <div class="kp-kv-row">
                    <span class="kp-kv-key">{{ t('knowledgeStages.detail.started') }}</span>
                    <span class="kp-kv-val kp-mono">{{ formatTime(selectedRow.node.started_at) }}</span>
                  </div>
                  <div class="kp-kv-row">
                    <span class="kp-kv-key">{{ t('knowledgeStages.detail.finished') }}</span>
                    <span class="kp-kv-val kp-mono">{{ formatTime(selectedRow.node.finished_at) }}</span>
                  </div>
                  <div class="kp-kv-row">
                    <span class="kp-kv-key">{{ t('knowledgeStages.detail.duration') }}</span>
                    <span class="kp-kv-val kp-mono">{{ formatDuration(selectedRow.node.duration_ms) }}</span>
                  </div>
                </div>
              </div>

              <div
                v-if="(selectedRow.node.status === 'failed' || selectedRow.node.status === 'cancelled') && (selectedRow.node.error_code || selectedRow.node.error_message)"
                class="kp-error-block"
              >
                <div class="kp-error-title">
                  <t-icon name="error-circle" size="14px" />
                  <span>{{ localizedErrorTitle(selectedRow.node.error_code) || t('knowledgeStages.detail.error') }}</span>
                  <span v-if="selectedRow.node.error_code" class="kp-error-code">{{ selectedRow.node.error_code }}</span>
                </div>
                <div v-if="selectedRow.node.error_message" class="kp-error-msg">{{ selectedRow.node.error_message }}</div>
              </div>

              <template v-for="section in (['input','output','metadata'] as const)" :key="section">
                <div v-if="isObjectWithKeys((selectedRow.node as any)[section])" class="kp-section">
                  <div class="kp-section-title">{{ t('knowledgeStages.detail.' + section) }}</div>
                  <div class="kp-kv-grid">
                    <div
                      v-for="entry in buildKvEntries((selectedRow.node as any)[section])"
                      :key="entry.key"
                      class="kp-kv-row"
                    >
                      <span class="kp-kv-key">{{ entry.label }}</span>
                      <span v-if="entry.kind === 'bool'" class="kp-kv-val kp-bool" :class="{ 'kp-bool-true': entry.raw, 'kp-bool-false': !entry.raw }">{{ entry.display }}</span>
                      <span v-else-if="entry.kind === 'scalar'" class="kp-kv-val">{{ entry.display }}</span>
                      <div v-else class="kp-kv-val kp-kv-collapsible">
                        <button
                          type="button"
                          class="kp-kv-toggle"
                          @click.stop="toggleJsonKey(section, entry.key)"
                        >
                          <span class="kp-kv-summary">{{ entry.display }}</span>
                          <span class="kp-kv-toggle-label">{{
                            isJsonExpanded(section, entry.key)
                              ? t('knowledgeStages.detail.hideJson')
                              : t('knowledgeStages.detail.showJson')
                          }}</span>
                        </button>
                        <pre v-if="isJsonExpanded(section, entry.key)" class="kp-json">{{ prettyJSON(entry.raw) }}</pre>
                      </div>
                    </div>
                  </div>
                </div>
              </template>
            </div>

            <div class="kp-drawer-footer">
              <t-button size="small" variant="outline" @click.stop="copySpan(selectedRow.node)">
                <t-icon name="copy" size="14px" />
                <span style="margin-left: 4px">{{ t('knowledgeStages.copyDetails') }}</span>
              </t-button>
            </div>
          </template>
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

/* Shell wraps both timeline and drawer so the drawer is bounded by the
   component, not the viewport. */
.kp-shell {
  position: relative;
  overflow: hidden;
  border-radius: 8px;
}
.kp-main {
  width: 100%;
}

/* ============ HEADER ============ */
.kp-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding-bottom: 6px;
}
.kp-title {
  font-weight: 600;
  font-size: 14px;
  color: var(--td-text-color-primary);
}
.kp-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.kp-status-line {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  margin-bottom: 12px;
  display: flex;
  gap: 6px;
  align-items: center;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--td-component-border, #e7e7e7);
}
.kp-total { color: var(--td-text-color-primary); }
.kp-status-sep { color: var(--td-text-color-placeholder); }
.kp-status-cluster {
  display: inline-flex;
  gap: 4px;
  align-items: center;
}
.kp-status-glyph {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 12px;
  height: 12px;
  font-size: 11px;
  line-height: 1;
}

/* ============ ATTEMPT TAB STRIP ============ */
.kp-tab-strip {
  display: flex;
  gap: 8px;
  overflow-x: auto;
  padding-bottom: 10px;
  margin-bottom: 6px;
  border-bottom: 1px solid var(--td-component-border, #e7e7e7);
}
.kp-tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border: 1px solid var(--td-component-border, #dcdcdc);
  border-radius: 999px;
  background: var(--td-bg-color-container, #fff);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  white-space: nowrap;
  transition: background-color 0.15s, border-color 0.15s, color 0.15s;
}
.kp-tab:hover {
  background: var(--td-bg-color-container-hover, rgba(0, 0, 0, 0.03));
}
.kp-tab-active {
  background: var(--td-brand-color-light, rgba(0, 102, 255, 0.08));
  border-color: var(--td-brand-color);
  color: var(--td-brand-color);
}
.kp-tab-num { font-weight: 600; }
.kp-tab-glyph {
  font-size: 10px;
  line-height: 1;
}
.kp-tab-glyph-done { color: var(--td-success-color); }
.kp-tab-glyph-failed { color: var(--td-error-color); }
.kp-tab-glyph-running {
  color: var(--td-brand-color);
  animation: kpPulse 1.4s ease-in-out infinite;
}
.kp-tab-glyph-unknown { color: var(--td-text-color-placeholder); }

/* ============ RULER ============ */
.kp-ruler-row {
  display: grid;
  grid-template-columns: minmax(220px, 38%) 64px 1fr;
  align-items: center;
  margin-bottom: 4px;
  padding: 0 8px;
}
.kp-ruler-spacer-name,
.kp-ruler-spacer-meta { height: 16px; }
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
  background: var(--td-bg-color-container, #fff);
}
.kp-row {
  display: grid;
  grid-template-columns: minmax(220px, 38%) 64px 1fr;
  align-items: center;
  height: 32px;
  cursor: pointer;
  position: relative;
  transition: background-color 0.12s, box-shadow 0.12s;
  padding: 0 8px;
}
.kp-row:hover { background: var(--td-bg-color-container-hover, rgba(0, 0, 0, 0.03)); }
.kp-row-active {
  background: var(--td-bg-color-container-hover, rgba(0, 0, 0, 0.04));
  box-shadow: inset 3px 0 0 var(--td-brand-color);
}

.kp-cell-name { min-width: 0; }
.kp-name-inner {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.kp-tree-toggle {
  width: 12px;
  height: 12px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  padding: 0;
  cursor: pointer;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  line-height: 1;
  transition: transform 0.15s;
  flex-shrink: 0;
}
.kp-tree-toggle-open { transform: rotate(90deg); }
.kp-tree-toggle-spacer { width: 12px; height: 12px; display: inline-block; flex-shrink: 0; }
.kp-status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--td-text-color-placeholder, #c0c0c0);
  margin-right: 2px;
}
.kp-name-text {
  font-size: 12px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.kp-name-mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.kp-name-root { font-weight: 600; }

.kp-cell-dur {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  text-align: right;
  padding-right: 8px;
}

.kp-cell-bar {
  position: relative;
  height: 32px;
}
.kp-bar {
  position: absolute;
  top: 13px;
  height: 6px;
  border-radius: 3px;
  background: var(--td-text-color-placeholder, #c0c0c0);
  min-width: 2px;
}
.kp-bar-done { background: var(--td-success-color); }
.kp-bar-failed { background: var(--td-error-color); }
.kp-bar-cancelled {
  background: transparent;
  border: 1px dashed var(--td-error-color-light, rgba(207, 55, 62, 0.5));
}
.kp-bar-skipped {
  background: var(--td-text-color-placeholder);
  opacity: 0.5;
}
.kp-bar-pending { display: none; }
.kp-bar-running {
  background: var(--td-brand-color);
  background-image: linear-gradient(
    45deg,
    rgba(255, 255, 255, 0.3) 25%,
    transparent 25%,
    transparent 50%,
    rgba(255, 255, 255, 0.3) 50%,
    rgba(255, 255, 255, 0.3) 75%,
    transparent 75%,
    transparent
  );
  background-size: 12px 12px;
  animation: kpStripes 0.8s linear infinite;
}
.kp-bar-placeholder {
  right: 0;
  top: 13px;
  height: 4px;
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

/* ============ DRAWER ============ */
.kp-drawer {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: 42%;
  min-width: 320px;
  max-width: 480px;
  background: var(--td-bg-color-page, #fff);
  border-left: 1px solid var(--td-component-border, #e7e7e7);
  box-shadow: -4px 0 16px rgba(0, 0, 0, 0.06);
  transform: translateX(100%);
  transition: transform 200ms ease;
  display: flex;
  flex-direction: column;
  z-index: 2;
}
.kp-drawer-open { transform: translateX(0); }

.kp-drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--td-component-border, #e7e7e7);
}
.kp-drawer-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.kp-drawer-dot { width: 10px; height: 10px; }
.kp-drawer-name {
  font-weight: 600;
  font-size: 13px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.kp-drawer-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}
.kp-drawer-close {
  border: none;
  background: transparent;
  cursor: pointer;
  font-size: 18px;
  line-height: 1;
  color: var(--td-text-color-placeholder);
  padding: 0 4px;
}
.kp-drawer-close:hover { color: var(--td-text-color-primary); }

.kp-drawer-body {
  flex: 1;
  overflow-y: auto;
  padding: 12px 16px;
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.kp-drawer-footer {
  padding: 10px 14px;
  border-top: 1px solid var(--td-component-border, #e7e7e7);
  display: flex;
  justify-content: flex-end;
  background: var(--td-bg-color-container, #fff);
}

.kp-section { display: flex; flex-direction: column; gap: 6px; }
.kp-section-title {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--td-text-color-placeholder);
  font-weight: 600;
}
.kp-kv-grid {
  display: flex;
  flex-direction: column;
  gap: 4px;
  background: var(--td-bg-color-secondarycontainer, #fafafa);
  border-radius: 6px;
  padding: 8px 10px;
}
.kp-kv-row {
  display: grid;
  grid-template-columns: 110px 1fr;
  gap: 12px;
  align-items: baseline;
  font-size: 12px;
  min-width: 0;
}
.kp-kv-key {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.kp-kv-val {
  color: var(--td-text-color-primary);
  overflow-wrap: anywhere;
  word-break: break-word;
}
.kp-mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 11px; }
.kp-bool-true { color: var(--td-success-color); }
.kp-bool-false { color: var(--td-error-color); }

.kp-kv-collapsible { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.kp-kv-toggle {
  border: none;
  background: transparent;
  padding: 0;
  text-align: left;
  cursor: pointer;
  display: flex;
  align-items: baseline;
  gap: 8px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  flex-wrap: wrap;
}
.kp-kv-summary {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--td-text-color-secondary);
}
.kp-kv-toggle-label {
  font-size: 11px;
  color: var(--td-brand-color);
}
.kp-kv-toggle:hover .kp-kv-toggle-label { text-decoration: underline; }

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

.kp-error-block {
  padding: 10px 12px;
  border: 1px solid var(--td-error-color);
  border-radius: 6px;
  background: rgba(217, 48, 37, 0.05);
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.kp-error-title {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--td-error-color);
  font-weight: 600;
  font-size: 12px;
}
.kp-error-code {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  background: rgba(217, 48, 37, 0.12);
  border-radius: 999px;
  padding: 1px 8px;
  margin-left: auto;
}
.kp-error-msg {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  word-break: break-word;
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

/* ============ EMPTY / LOADING ============ */
.kp-empty {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  padding: 20px 0;
  text-align: center;
}
.kp-loading {
  display: flex;
  justify-content: center;
  padding: 24px 0;
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

/* ============ COMPACT MODE (untouched) ============ */
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
