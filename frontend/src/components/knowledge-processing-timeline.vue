<script setup lang="ts">
import { ref, reactive, onMounted, onBeforeUnmount, watch, computed, nextTick } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { getKnowledgeSpans, reparseKnowledge, cancelKnowledgeParse, getKnowledgeDetails } from '@/api/knowledge-base/index'
import { knowledgeSpansPayloadHasTrace } from '@/utils/knowledgeTrace'
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'

interface SpanNode {
  span_id?: string
  parent_span_id?: string
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
  current_attempt?: number
  parse_status: string
  current_stage?: string
  trace: SpanNode
  last_error?: LastError | null
}

// IMPORTANT: Vue 3 coerces missing Boolean props to `false`, NOT
// `undefined` — so an optional boolean parent omits is indistinguishable
// from one explicitly set to false. That bit us: every parent that
// didn't pass auto-poll silently got polling disabled. Use withDefaults
// to make the intent explicit: polling is ON unless a parent opts out.
const props = withDefaults(
  defineProps<{
    knowledgeId: string
    parseStatus?: string
    autoPoll?: boolean
    compact?: boolean
    // gracePoll selects the polling rule. See shouldPollNow() for
    // the full semantics:
    //   true  — follow parse_status + any running subspan + a
    //           QUIESCE_GRACE_MS grace window after the tree quiesces.
    //           This is what the user-visible drawer mount wants:
    //           late-arriving subspans (wiki has a 30s debounce, etc.)
    //           surface without forcing a manual refresh.
    //   false — strict mode. Poll ONLY while parse_status itself is
    //           non-terminal; ignore post-pipeline async subspans.
    //           This is what background mounts (the hidden badge
    //           driver in doc-content.vue) want, so they stop firing
    //           /spans the instant parsing finishes — async wiki/
    //           summary work doesn't keep a headless poller alive
    //           after the user closed the trace drawer.
    gracePoll?: boolean
    /** Document title shown as the drawer primary heading. */
    docTitle?: string
    /** Show a close control (secondary trace drawer). */
    showClose?: boolean
  }>(),
  {
    autoPoll: true,
    compact: false,
    gracePoll: true,
    docTitle: '',
    showClose: false,
  },
)

const emit = defineEmits<{
  (e: 'update:hasSpans', has: boolean): void
  (e: 'update:summary', summary: { totalMs: number; status: string; stageIndex: number; stageTotal: number; stageLabel: string }): void
  (e: 'close'): void
}>()

const { t } = useI18n()

const STAGES = ['docreader', 'chunking', 'embedding', 'multimodal', 'postprocess'] as const
const POLL_INTERVAL_MS = 2000

const data = ref<SpansResponse | null>(null)
const processOverrides = ref<KnowledgeProcessOverrides | null>(null)
const loading = ref(false)
const refreshing = ref(false)
const selectedAttempt = ref<number | undefined>(undefined)
const expandedRows = ref<Set<string>>(new Set(['__root__']))
const selectedSpanId = ref<string | null>(null)
const expandedJsonKeys = ref<Set<string>>(new Set())
const nowTick = ref(Date.now())
const detailTab = ref<'overview' | 'input' | 'output' | 'metadata' | 'raw'>('overview')
const lastFetchedAt = ref<number>(0)
const scrollRef = ref<HTMLElement | null>(null)
// Stages the user has explicitly toggled (in either direction). Once
// a stage is in this set, the auto-expand-when-children-appear rule
// stops applying to it — the user's last action wins forever.
//
// Why this beats the old "firstLoadDone once" approach:
//   - Old logic only auto-expanded on the very first fetch. If a stage
//     had no subspans yet at that moment, it'd stay collapsed even
//     after children arrived later, forcing a manual click.
//   - New logic re-evaluates on every fetch: stages with children get
//     expanded unless the user has spoken. Discoverability + respect
//     for manual collapse, both.
// Tracks every row the user has manually expanded or collapsed (by
// row.key). Auto-expand checks this set so deeper subspans that the
// user explicitly hid stay hidden across polls, instead of being
// reopened by the next fetch's re-evaluation pass.
const userToggledRows = ref<Set<string>>(new Set())
// Tracks consecutive fetch failures so the "更新于" caption can surface
// staleness. When the parse_status is mid-flight but every fetch is
// hitting an error, the loop keeps going silently — without this
// indicator the user sees a spinning auto-refresh icon while the
// caption ages without explanation.
const failedAttempts = ref<number>(0)
const lastFetchOk = ref<boolean>(true)

const attemptStatuses = reactive<Map<number, string>>(new Map())

// ===== Polling, take N+1: brutally simple =====
//
// Prior incarnations tried "smart" polling (self-rescheduling chains,
// watchers, watchdogs, fetchInFlight gates routing through tick
// callbacks). They all had edge cases where the loop silently died
// while the LIVE badge kept showing.
//
// New design: ONE setInterval that fires every tick for the entire
// lifetime of the component. The tick callback checks state and
// either calls fetchSpans or no-ops. No re-arming, no clearing, no
// watchers, no chains. The interval cannot get "stranded" because
// nothing ever stops it until unmount.
//
// A 2-second no-op (3 ref reads) is free. The simplicity is the win.
let pollTimer: ReturnType<typeof setInterval> | null = null
let nowTimer: ReturnType<typeof setInterval> | null = null
let unmounted = false
// Drops overlapping fetches. Does NOT gate the polling decision —
// the tick callback owns scheduling. If fetchInFlight is stuck (e.g.
// network black hole), the worst case is one stuck request blocking
// retries until axios timeout (30s); the loop itself stays alive.
let fetchInFlight = false

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
  if (ms < 60000) return `${(ms / 1000).toFixed(2)}s`
  const mins = Math.floor(ms / 60000)
  const rem = ((ms % 60000) / 1000).toFixed(1)
  return `${mins}m${rem}s`
}

function formatRelativeTime(ts: number): string {
  if (!ts) return '—'
  const sec = Math.max(0, Math.floor((nowTick.value - ts) / 1000))
  if (sec < 1) return t('knowledgeStages.justNow')
  if (sec < 60) return t('knowledgeStages.secondsAgo', { n: sec })
  const min = Math.floor(sec / 60)
  return t('knowledgeStages.minutesAgo', { n: min })
}

function isPolling(status?: string): boolean {
  // finalizing is the post-process fan-out window — subspans
  // (summary / question / graph.chunk[*]) are still actively producing
  // events, so we must keep polling and drawing LIVE.
  return status === 'pending' || status === 'processing' || status === 'finalizing'
}

// Hard-terminal statuses override traceActive: even if child spans were
// left in 'running' state (e.g. a cancel raced ahead of a worker's
// FailSpan), the parse pipeline is definitively done for this attempt
// and we should stop polling instead of refreshing forever.
function isHardTerminal(status?: string): boolean {
  return status === 'cancelled' || status === 'failed' || status === 'completed'
}

// True if any node in the trace is still running/pending. Crucial for
// postprocess — its subspans (summary/question/graph.chunk[*]) run
// asynchronously AFTER the main pipeline closes, so parse_status
// flips to 'completed' while there's plenty of work still happening.
// Without checking the tree we'd stop polling at that moment and
// strand the user staring at half-rendered postprocess until they
// manually refresh.
function spanTreeActive(node?: SpanNode): boolean {
  if (!node) return false
  const s = node.status
  if (s === 'running' || s === 'pending') return true
  const kids = node.children || []
  for (let i = 0; i < kids.length; i++) {
    if (spanTreeActive(kids[i])) return true
  }
  return false
}

const traceActive = computed<boolean>(() => spanTreeActive(data.value?.trace))

// Drives BOTH the LIVE badge AND polling. Single source of truth:
//   1. If we have data: still live while parse_status is polling
//      OR the span tree has any running descendant.
//   2. If we don't have data yet (initial paint): trust the parent's
//      parseStatus hint so the UI shows LIVE immediately.
const isLive = computed<boolean>(() => {
  if (data.value) {
    // Hard-terminal parse_status wins over a stale traceActive: cancel
    // and irrecoverable failure can leave child spans stranded as
    // 'running' (worker process died, cancel raced FailSpan, etc.),
    // and we must NOT keep polling forever on those.
    if (isHardTerminal(data.value.parse_status)) return false
    return isPolling(data.value.parse_status) || traceActive.value
  }
  if (isHardTerminal(props.parseStatus)) return false
  return isPolling(props.parseStatus)
})

// Walk every node in the tree and return the freshest updated_at /
// finished_at timestamp we can see. Used by the quiescent grace
// window below to decide "did this trace finish recently, or is it
// just an old completed one we shouldn't waste polls on?"
function spanTreeLastActivity(node?: SpanNode): number {
  if (!node) return 0
  let max = 0
  const stamps: (string | null | undefined)[] = [
    (node as any).updated_at,
    node.finished_at,
    node.started_at,
    (node as any).created_at,
  ]
  for (const s of stamps) {
    const t = parseTime(s || undefined)
    if (t !== null && t > max) max = t
  }
  const kids = node.children || []
  for (let i = 0; i < kids.length; i++) {
    const t = spanTreeLastActivity(kids[i])
    if (t > max) max = t
  }
  return max
}

// Async post-pipeline tasks (summary, question, graph.chunk[*],
// wiki) open their postprocess.* subspans AFTER the parse pipeline
// has finalised — wiki in particular fires 30s after enqueue thanks
// to wikiIngestDelay. At that exact moment parse_status is already
// 'completed' AND every existing span is 'done', so isLive flips to
// false and polling stops. The user is then stranded watching a
// stale tree until they hit refresh.
//
// QUIESCE_GRACE_MS keeps the poll loop alive for a bounded window
// after the trace appears quiescent so late subspans surface
// automatically. Sized generously past wikiIngestDelay (30s) plus a
// typical ingest run, but short enough that opening an
// already-completed knowledge from days ago doesn't waste many
// polls before the loop falls silent.
const QUIESCE_GRACE_MS = 2 * 60 * 1000

const lastTraceActivityAt = computed<number>(() =>
  spanTreeLastActivity(data.value?.trace),
)

const isWithinQuiesceGrace = computed<boolean>(() => {
  if (isLive.value) return false
  const last = lastTraceActivityAt.value
  if (!last) return false
  return nowTick.value - last < QUIESCE_GRACE_MS
})

// shouldPollNow encodes the per-tick "do I fetch?" decision. Split
// from isLive (which also drives the LIVE badge — its semantics are
// "trace is actually running") so the polling rules can diverge from
// the badge rules without entangling them.
//
// Two modes, selected by the gracePoll prop:
//
//   gracePoll = true  (default — for the visible drawer mount):
//     Follow the trace through everything the user might want to see
//     live: the main parse pipeline (parse_status), any running
//     subspan (traceActive), AND the QUIESCE_GRACE_MS window after
//     the tree quiesces (so wiki ingest's 30s-debounced subspan
//     surfaces on its own without a manual refresh).
//
//   gracePoll = false (for background mounts e.g. doc-content's
//     hidden badge driver):
//     Strict mode. Poll ONLY while the parse pipeline itself is
//     non-terminal. Post-pipeline async work (wiki / summary /
//     question / graph subspans) is intentionally ignored —
//     otherwise the hidden mount would keep firing /spans every
//     2s until those finish, even though the user has closed the
//     trace drawer and is only looking at the header badge (whose
//     job is "is parsing done yet?", not "is async post-work done
//     yet?"). The next user-initiated drawer-open will get a fresh
//     fetch via onMounted, picking up any post-pipeline progress.
function shouldPollNow(): boolean {
  if (!data.value) return isPolling(props.parseStatus)
  if (props.gracePoll) {
    return isLive.value || isWithinQuiesceGrace.value
  }
  return isPolling(data.value.parse_status)
}

async function fetchSpans(opts: { manual?: boolean } = {}) {
  if (!props.knowledgeId) return
  if (fetchInFlight) return
  fetchInFlight = true
  if (opts.manual) refreshing.value = true
  if (!data.value) loading.value = true
  let attemptOk = false
  try {
    const res: any = await getKnowledgeSpans(props.knowledgeId, selectedAttempt.value)
    if (res?.success && res.data) {
      data.value = res.data as SpansResponse
      attemptOk = true
      if (selectedAttempt.value === undefined) {
        selectedAttempt.value = data.value.attempt
      }
      // Auto-expand rule, applied on EVERY fetch and walked over
      // EVERY level of the tree (stage + subspan + sub-subspan, …):
      //   - Row has children + user hasn't toggled it → expand
      //   - Row has no children → leave whatever state it was in
      //   - User has toggled the row (either direction) → don't touch
      // Walking the full depth (not just stages) is what makes deep
      // subspans like postprocess.wiki — whose own children like
      // postprocess.wiki.extract / .summary / .classify / .page[*]
      // appear later in the run — surface automatically. Without this
      // the user would click the chevron on postprocess.wiki and see
      // nothing change, because the row would default to collapsed
      // and the auto-expand pass would skip it.
      const expanded = new Set(expandedRows.value)
      expanded.add('__root__')
      const autoExpand = (n: SpanNode) => {
        const key = n.span_id || `stage:${n.name}`
        const kids = n.children || []
        if (kids.length > 0 && !userToggledRows.value.has(key)) {
          expanded.add(key)
        }
        for (const c of kids) autoExpand(c)
      }
      for (const stage of data.value.trace?.children || []) autoExpand(stage)
      expandedRows.value = expanded
      const traceStatus = data.value.trace?.status || data.value.parse_status || 'running'
      attemptStatuses.set(data.value.attempt, traceStatus)
      ensureAttemptStatuses()
      emit('update:hasSpans', knowledgeSpansPayloadHasTrace(data.value))
    } else {
      emit('update:hasSpans', false)
    }
  } catch (e) {
    // Surface the error in the console — silent failures here is
    // exactly what hid the polling-stalled bug from us before.
    console.warn('[KnowledgeTimeline] fetchSpans failed', e)
    emit('update:hasSpans', false)
  } finally {
    // Track every attempt, not just successful ones — otherwise a
    // failing endpoint would leave "更新于 X 秒前" frozen forever while
    // the spinner spins. Pair with failedAttempts to render a "fetch
    // failed" hint when consecutive errors pile up.
    lastFetchedAt.value = Date.now()
    lastFetchOk.value = attemptOk
    if (attemptOk) {
      failedAttempts.value = 0
    } else {
      failedAttempts.value += 1
    }
    loading.value = false
    refreshing.value = false
    fetchInFlight = false
  }
}

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
      .catch(() => { })
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

async function copyValue(value: any) {
  try {
    const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2)
    await navigator.clipboard.writeText(text)
    MessagePlugin.success(t('knowledgeStages.copied'))
  } catch {
    MessagePlugin.error(t('knowledgeStages.copyDetails'))
  }
}

async function copySpan(node: SpanNode) {
  await copyValue(node)
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

async function onManualRefresh() {
  if (refreshing.value || loading.value) return
  await fetchSpans({ manual: true })
}

const cancelling = ref(false)

// Mirrors the backend CancelKnowledgeParse gate (pending / processing /
// finalizing). Uses the freshest status we have: live span data first,
// the parent's hint before the first fetch lands.
const canCancelParse = computed<boolean>(() => {
  const status = data.value?.parse_status ?? props.parseStatus
  return isPolling(status)
})

async function onCancelParseConfirm() {
  if (cancelling.value) return
  const id = props.knowledgeId
  if (!id) return
  cancelling.value = true
  try {
    await cancelKnowledgeParse(id)
    MessagePlugin.success(t('knowledgeBase.cancelParseSubmitted'))
    await fetchSpans({ manual: true })
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeBase.cancelParseFailed'))
  } finally {
    cancelling.value = false
  }
}

function onAttemptChange(n: number) {
  if (Number.isNaN(n)) return
  selectedAttempt.value = n
  selectedSpanId.value = null
  // New attempt: forget per-row user choices so the auto-expand
  // rule re-evaluates cleanly against the new attempt's tree.
  userToggledRows.value = new Set()
  expandedRows.value = new Set(['__root__'])
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
    userToggledRows.value = new Set()
    fetchSpans()
  },
)

function onKeydown(ev: KeyboardEvent) {
  if (ev.key === 'Escape' && selectedSpanId.value) {
    selectedSpanId.value = null
  }
}

async function fetchProcessOverrides() {
  if (props.compact || !props.knowledgeId) return
  try {
    const res: any = await getKnowledgeDetails(props.knowledgeId)
    if (res?.success && res.data) {
      processOverrides.value = res.data.metadata?.process_overrides ?? null
    }
  } catch {
    processOverrides.value = null
  }
}

onMounted(() => {
  fetchSpans()
  fetchProcessOverrides()
  // One permanent interval for the entire component lifetime. The
  // tick decides whether to actually fetch — no clearing, no
  // re-arming, no watchers wired into it. If this interval ever
  // stops firing, the entire JS event loop is wedged (in which case
  // nothing else would work either).
  if (props.autoPoll) {
    pollTimer = setInterval(() => {
      if (unmounted) return
      if (fetchInFlight) return
      if (!shouldPollNow()) return
      fetchSpans()
    }, POLL_INTERVAL_MS)
  }
  nowTimer = setInterval(() => {
    nowTick.value = Date.now()
  }, 1000)
  window.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  unmounted = true
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
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
  // Extend the right edge to "now" whenever the trace is still
  // actively producing spans — this covers both parse_status mid-flight
  // AND the postprocess-async case where the top-level status closes
  // but subspans keep ticking. Both conditions are captured by isLive.
  if (isLive.value) {
    const now = nowTick.value
    candidate = candidate === null ? now : Math.max(candidate, now)
  }
  return candidate
})

const totalMs = computed<number>(() => {
  if (t0.value === null || tEnd.value === null) return 0
  // The trace's own duration_ms only covers the parsing pipeline up to
  // FinalizeAttempt. Async post-processing subspans (summary / question /
  // graph) keep producing rows AFTER the root closes — so the time axis
  // must scale to the latest descendant end, otherwise their bars get
  // clipped past the right edge. Take the max of (root duration, observed
  // span tail) regardless of polling state.
  const observed = Math.max(0, tEnd.value - t0.value)
  const traceDur = data.value?.trace?.duration_ms
  if (typeof traceDur === 'number' && traceDur > 0) {
    return Math.max(traceDur, observed)
  }
  return observed
})

const showRuler = computed(() => totalMs.value >= 50)

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

// "Now" position on the waterfall scale, used to draw the live cursor
// while polling so the user can see time advancing even when the running
// stage's bar grows slowly toward the right edge.
const nowMarkerPct = computed<number | null>(() => {
  if (!isLive.value || !t0.value || !totalMs.value) return null
  const pct = ((nowTick.value - t0.value) / totalMs.value) * 100
  return Math.max(0, Math.min(100, pct))
})

interface FlatRow {
  key: string
  depth: number
  node: SpanNode
  hasChildren: boolean
  isRoot: boolean
  isStage: boolean
  parentKey?: string
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
      parentKey: rootKey,
    })
    if (!expandedRows.value.has(stageKey)) continue

    const walk = (n: SpanNode, depth: number, idxPath: string, parentKey: string) => {
      const key = rowKey(n, `${idxPath}:${n.name}`)
      const kids = n.children || []
      rows.push({
        key,
        depth,
        node: n,
        hasChildren: kids.length > 0,
        isRoot: false,
        isStage: false,
        parentKey,
      })
      // Honour the expand/collapse state for subspans too — without
      // this gate, clicking the chevron on a subspan (e.g.
      // postprocess.wiki) only rotated the icon but the children
      // were always rendered, so the toggle looked broken.
      if (!expandedRows.value.has(key)) return
      kids.forEach((c, i) => walk(c, depth + 1, `${idxPath}/${i}`, key))
    }
    stageChildren.forEach((c, i) => walk(c, 2, `${stageKey}/${i}`, stageKey))
  }

  return rows
})

const selectedRow = computed<FlatRow | null>(() => {
  const id = selectedSpanId.value
  if (!id) return null
  return flatRows.value.find((r) => r.key === id) || null
})

const detailOpen = computed(() => selectedSpanId.value !== null && selectedRow.value !== null)

function barStyle(node: SpanNode): Record<string, string> {
  const total = totalMs.value
  if (!total || t0.value === null) return { display: 'none' }
  const start = nodeStart(node)
  if (start === null) return { display: 'none' }
  // For a span with no recorded finished_at, use "now" as the end
  // whenever it's plausibly still running — either the trace overall
  // is live, or this individual span's status says it's in flight.
  // Without the second clause, postprocess subspans that survived past
  // parse_status='completed' would collapse to zero width.
  const liveBar = isLive.value || node.status === 'running' || node.status === 'pending'
  const end = nodeEnd(node) ?? (liveBar ? nowTick.value : start)
  const leftPct = ((start - t0.value) / total) * 100
  const widthPct = Math.max(0.4, ((end - start) / total) * 100)
  return {
    left: `${Math.max(0, Math.min(100, leftPct))}%`,
    width: `${Math.min(100 - Math.max(0, leftPct), widthPct)}%`,
  }
}

// Wrapping outline bar — when a span's children extend past the parent's
// own finished_at (typical for postprocess: stage closes in ~9ms but its
// async summary/question subspans run for tens of seconds), we render a
// faint outline from the parent's start to the latest descendant end.
// This makes "this stage's downstream work took N seconds total" visible
// at a glance without conflating it with the stage's self-duration.
function descendantMaxEnd(node: SpanNode): number | null {
  const ends: number[] = []
  for (const c of node.children || []) {
    collectEnds(c, ends)
  }
  if (ends.length === 0) return null
  return Math.max(...ends)
}

function wrapStyle(node: SpanNode): Record<string, string> | null {
  const total = totalMs.value
  if (!total || t0.value === null) return null
  const start = nodeStart(node)
  if (start === null) return null
  const selfEnd = nodeEnd(node) ?? start
  const childEnd = descendantMaxEnd(node)
  if (childEnd === null) return null
  // Only render the wrapping bar when descendants extend at least 50ms
  // past the parent — otherwise the outline is indistinguishable from
  // the solid self-bar and only adds visual noise.
  if (childEnd - selfEnd < 50) return null
  const leftPct = ((start - t0.value) / total) * 100
  const widthPct = Math.max(0.4, ((childEnd - start) / total) * 100)
  return {
    left: `${Math.max(0, Math.min(100, leftPct))}%`,
    width: `${Math.min(100 - Math.max(0, leftPct), widthPct)}%`,
  }
}

function wrapDurationMs(node: SpanNode): number {
  const start = nodeStart(node)
  const childEnd = descendantMaxEnd(node)
  if (start === null || childEnd === null) return 0
  return Math.max(0, childEnd - start)
}

function barOffsetPct(node: SpanNode): number | null {
  const total = totalMs.value
  if (!total || t0.value === null) return null
  const start = nodeStart(node)
  if (start === null) return null
  return Math.max(0, Math.min(100, ((start - t0.value) / total) * 100))
}

function barOffsetMs(node: SpanNode): number {
  const start = nodeStart(node)
  if (start === null || t0.value === null) return 0
  return Math.max(0, start - t0.value)
}

function liveElapsedMs(node: SpanNode): number {
  const s = nodeStart(node)
  if (s === null) return 0
  return Math.max(0, nowTick.value - s)
}

function isPlaceholder(node: SpanNode): boolean {
  return !node.span_id && !node.started_at
}

function isRowExpanded(key: string): boolean {
  return expandedRows.value.has(key)
}

function treeToggleAriaLabel(row: FlatRow): string {
  if (!row.hasChildren || row.isRoot) return ''
  return isRowExpanded(row.key)
    ? t('knowledgeStages.collapseBranch')
    : t('knowledgeStages.expandBranch')
}

function scrollRowIntoView(key: string) {
  nextTick(() => {
    const root = scrollRef.value
    if (!root) return
    const safeKey = typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(key) : key.replace(/"/g, '\\"')
    const el = root.querySelector(`.kp-row[data-span-key="${safeKey}"]`)
    el?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  })
}

function toggleTree(row: FlatRow, ev?: MouseEvent) {
  if (ev) ev.stopPropagation()
  if (!row.hasChildren) return
  const wasExpanded = expandedRows.value.has(row.key)
  const next = new Set(expandedRows.value)
  if (next.has(row.key)) next.delete(row.key)
  else next.add(row.key)
  expandedRows.value = next
  // Record the manual toggle at every depth so the next poll's
  // auto-expand pass leaves this row alone. Without this, a user
  // who collapsed a noisy subspan (e.g. postprocess.wiki with a
  // dozen page[*] children) would see it pop back open on the
  // next 2s tick.
  const touched = new Set(userToggledRows.value)
  touched.add(row.key)
  userToggledRows.value = touched
  // Expanding via chevron should also surface the detail panel for
  // this stage/span — otherwise the name column only toggles the tree.
  if (!wasExpanded && next.has(row.key)) {
    selectRow(row)
  }
}

function selectRow(row: FlatRow) {
  if (selectedSpanId.value === row.key) {
    return
  }
  selectedSpanId.value = row.key
  detailTab.value = 'overview'
  scrollRowIntoView(row.key)
}

function closeDetail() {
  selectedSpanId.value = null
}

function isObjectWithKeys(v: any): boolean {
  return v && typeof v === 'object' && !Array.isArray(v) && Object.keys(v).length > 0
}

function hasContent(v: any): boolean {
  if (v === null || v === undefined || v === '') return false
  if (Array.isArray(v)) return v.length > 0
  if (typeof v === 'object') return Object.keys(v).length > 0
  return true
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

function rowKindLabel(row: FlatRow): string {
  if (row.isRoot) return 'root'
  if (row.isStage) return 'stage'
  return row.node.kind || 'span'
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
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const ss = String(d.getSeconds()).padStart(2, '0')
  // Date prefix: omit year when same as current year to keep the row
  // compact, but always show month+day so traces from yesterday/last
  // week aren't ambiguous. The full ISO date is preserved in the
  // tooltip via the original string.
  const now = new Date()
  const yyyy = d.getFullYear()
  const mo = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  const datePart = yyyy === now.getFullYear() ? `${mo}-${dd}` : `${yyyy}-${mo}-${dd}`
  return `${datePart} ${hh}:${mm}:${ss}.${ms}`
}

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
  // True for short payloads — the panel skips the click-to-expand
  // affordance and renders the JSON inline directly so users see the
  // values without an extra click. Long payloads stay folded so the
  // panel doesn't blow up vertically when an output has hundreds of
  // entries.
  defaultExpanded: boolean
}

function buildKvEntries(obj: any): KvEntry[] {
  if (!isObjectWithKeys(obj)) return []
  const entries: KvEntry[] = []
  for (const [key, value] of Object.entries(obj)) {
    entries.push(toKvEntry(key, value))
  }
  return entries
}

// Threshold for inline auto-expansion. Short payloads — small arrays /
// shallow objects — render inline directly. Anything larger keeps the
// click-to-expand summary so the detail panel doesn't grow without bound.
const KV_INLINE_ARRAY_LIMIT = 8
const KV_INLINE_OBJECT_KEY_LIMIT = 8
const KV_INLINE_JSON_BYTES_LIMIT = 600

function shouldInlineExpand(value: any): boolean {
  if (Array.isArray(value)) {
    if (value.length > KV_INLINE_ARRAY_LIMIT) return false
    try {
      return JSON.stringify(value).length <= KV_INLINE_JSON_BYTES_LIMIT
    } catch {
      return false
    }
  }
  if (value && typeof value === 'object') {
    const keys = Object.keys(value)
    if (keys.length > KV_INLINE_OBJECT_KEY_LIMIT) return false
    try {
      return JSON.stringify(value).length <= KV_INLINE_JSON_BYTES_LIMIT
    } catch {
      return false
    }
  }
  return false
}

function toKvEntry(key: string, value: any): KvEntry {
  const label = humanizeKey(key)
  if (value === null || value === undefined) {
    return { key, label, kind: 'scalar', display: '—', raw: value, defaultExpanded: false }
  }
  if (typeof value === 'boolean') {
    return { key, label, kind: 'bool', display: value ? 'true' : 'false', raw: value, defaultExpanded: false }
  }
  if (typeof value === 'number') {
    return { key, label, kind: 'scalar', display: value.toLocaleString(), raw: value, defaultExpanded: false }
  }
  if (typeof value === 'string') {
    return { key, label, kind: 'scalar', display: value, raw: value, defaultExpanded: false }
  }
  if (Array.isArray(value)) {
    return {
      key, label, kind: 'array',
      display: `Array · ${value.length}`, raw: value,
      defaultExpanded: shouldInlineExpand(value),
    }
  }
  if (typeof value === 'object') {
    const n = Object.keys(value as object).length
    return {
      key, label, kind: 'object',
      display: `Object · ${n} keys`, raw: value,
      defaultExpanded: shouldInlineExpand(value),
    }
  }
  return { key, label, kind: 'scalar', display: String(value), raw: value, defaultExpanded: false }
}

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
      return { ch: '✓', cls: 'kp-glyph-done' }
    case 'failed':
      return { ch: '✗', cls: 'kp-glyph-failed' }
    case 'running':
    case 'pending':
    case 'processing':
      return { ch: '●', cls: 'kp-glyph-running' }
    default:
      return { ch: '–', cls: 'kp-glyph-unknown' }
  }
}

// True when the panel is showing the most recent attempt (or there's
// only one). Historical attempts must keep their own per-attempt
// trace.status; only the latest attempt's header should defer to the
// knowledge-level parse_status.
const viewingLatestAttempt = computed<boolean>(() => {
  const latest = data.value?.latest_attempt || 0
  if (latest <= 1) return true
  const active = selectedAttempt.value ?? data.value?.attempt ?? latest
  return active === latest
})

// Project the knowledge-level parse_status onto the trace-span status
// vocabulary localizedStatus() speaks, so the header badge reads the
// same whether it comes from the root span or from parse_status.
// 'finalizing' keeps its own label ("优化中") to match the doc card.
function parseStatusToTraceStatus(s?: string): string {
  switch (s) {
    case 'completed':
      return 'done'
    case 'processing':
      return 'running'
    case 'finalizing':
      return 'finalizing'
    default:
      return s || ''
  }
}

// The authoritative status for the header badge. During the async
// post-pipeline window (summary / question / graph / wiki), the latest
// attempt's ROOT span closes — so trace.status reads 'done' — while
// those subspans keep running and the row is still 'finalizing'.
// Trusting trace.status there flashes "已完成" mid-wiki even though the
// doc card (and LIVE badge) still say "优化中". Prefer parse_status while
// it is non-terminal on the latest attempt so all three agree.
const headerStatus = computed(() => {
  const parseStatus = data.value?.parse_status
  if (viewingLatestAttempt.value && isPolling(parseStatus)) {
    return parseStatusToTraceStatus(parseStatus)
  }
  return data.value?.trace?.status || parseStatusToTraceStatus(parseStatus)
})

const headerStatusText = computed(() => {
  const s = headerStatus.value
  return s ? localizedStatus(s) : ''
})

const headerStatusTheme = computed(() => {
  switch (headerStatus.value) {
    case 'done':
    case 'completed':
      return 'success'
    case 'failed':
      return 'danger'
    case 'running':
    case 'processing':
    case 'pending':
    case 'finalizing':
      return 'warning'
    default:
      return 'default'
  }
})

const stagesStatDisplay = computed(() => {
  const total = stages.value.length
  const doneCount = stages.value.filter((s) => s.status === 'done').length
  const inProgress = stages.value.some(
    (s) => s.status === 'running' || s.status === 'failed' || s.status === 'pending',
  )
  if (inProgress) {
    return {
      label: t('knowledgeStages.head.stagesProgress'),
      value: `${currentStageIndex.value}/${total}`,
    }
  }
  return {
    label: t('knowledgeStages.head.stagesDone'),
    value: `${doneCount}/${total}`,
  }
})

const headMetaParts = computed(() => {
  if (!data.value) return []
  const parts: string[] = [t('knowledgeStages.title')]
  if (totalMs.value > 0) {
    parts.push(t('knowledgeStages.total', { d: formatDuration(totalMs.value) }))
  }
  const st = stagesStatDisplay.value
  parts.push(`${st.label} ${st.value}`)
  if (attemptTabs.value.length === 0 && data.value.current_attempt) {
    parts.push(t('knowledgeStages.attempt', { n: data.value.current_attempt }))
  }
  if (lastFetchedAt.value && isLive.value) {
    let updated = formatRelativeTime(lastFetchedAt.value)
    if (!lastFetchOk.value) {
      updated += ` (${t('knowledgeStages.fetchFailedShort')})`
    }
    parts.push(`${t('knowledgeStages.head.updated')} ${updated}`)
  }
  return parts
})

const primaryHeadTitle = computed(() => props.docTitle || t('knowledgeStages.title'))

// Emit summary upstream so the doc-content drawer can show a one-line
// status pill without mounting a second copy of the tree.
watch(
  [
    () => totalMs.value,
    () => data.value?.parse_status,
    () => currentStageIndex.value,
    () => currentStageLabel.value,
  ],
  () => {
    emit('update:summary', {
      totalMs: totalMs.value,
      status: data.value?.trace?.status || data.value?.parse_status || '',
      stageIndex: currentStageIndex.value,
      stageTotal: stages.value.length,
      stageLabel: currentStageLabel.value,
    })
  },
  { immediate: true },
)

function tabHasContent(tab: 'input' | 'output' | 'metadata'): boolean {
  const node = selectedRow.value?.node
  if (!node) return false
  return hasContent((node as any)[tab])
}

const traceMetadata = computed(() => {
  const m = data.value?.trace?.metadata
  return hasContent(m) ? m : null
})

watch([selectedSpanId, detailTab], () => {
  if (detailTab.value === 'metadata' && !tabHasContent('metadata')) {
    detailTab.value = 'overview'
  }
})

// Identity / lineage info for the Overview tab. Surfaces fields that
// were previously buried in the raw payload so the panel doesn't feel
// thin even when the span has no input/output/metadata.
interface IdentityField {
  key: string
  label: string
  value: string
  mono: boolean
  copyable: boolean
}

function identityFields(row: FlatRow): IdentityField[] {
  const out: IdentityField[] = []
  const node = row.node as any
  out.push({ key: 'name', label: t('knowledgeStages.detail.name'), value: rowLabel(row), mono: false, copyable: false })
  out.push({ key: 'kind', label: t('knowledgeStages.detail.kind'), value: rowKindLabel(row), mono: true, copyable: false })
  out.push({ key: 'status', label: t('knowledgeStages.detail.status'), value: localizedStatus(row.node.status), mono: false, copyable: false })
  if (row.isStage) {
    const idx = stages.value.findIndex((s) => s.name === row.node.name)
    if (idx >= 0) {
      out.push({ key: 'stageIndex', label: t('knowledgeStages.detail.stageOrder'), value: `${idx + 1} / ${stages.value.length}`, mono: true, copyable: false })
    }
  }
  if (row.hasChildren) {
    out.push({ key: 'children', label: t('knowledgeStages.detail.childCount'), value: String((row.node.children || []).length), mono: true, copyable: false })
  }
  if (node.span_id) out.push({ key: 'span_id', label: 'span_id', value: node.span_id, mono: true, copyable: true })
  if (node.parent_span_id) out.push({ key: 'parent_span_id', label: 'parent_span_id', value: node.parent_span_id, mono: true, copyable: true })
  if (data.value?.knowledge_id) out.push({ key: 'knowledge_id', label: 'knowledge_id', value: data.value.knowledge_id, mono: true, copyable: true })
  if (data.value?.current_attempt) out.push({ key: 'attempt', label: t('knowledgeStages.head.attempt'), value: `#${data.value.current_attempt}`, mono: true, copyable: false })
  return out
}

// Quick stage-by-stage breakdown shown inside root's overview.
interface StageRowSummary {
  name: string
  label: string
  status: string
  duration_ms?: number
  pct: number
}

const stageBreakdown = computed<StageRowSummary[]>(() => {
  const total = totalMs.value || 1
  return stages.value.map((s) => ({
    name: s.name,
    label: t(`knowledgeStages.stage.${s.name}`),
    status: s.status,
    duration_ms: s.duration_ms,
    pct: typeof s.duration_ms === 'number' && s.duration_ms > 0 ? Math.min(100, (s.duration_ms / total) * 100) : 0,
  }))
})

// Human-readable summary of the per-upload parse overrides stored in
// knowledge.metadata.process_overrides. Empty overrides → KB defaults.
const processConfigLines = computed<string[]>(() => {
  const o = processOverrides.value
  if (!o) return [t('knowledgeStages.processConfig.kbDefault')]
  const k = (s: string) => `knowledgeStages.processConfig.${s}`
  const onOff = (v: boolean) => (v ? t(k('on')) : t(k('off')))
  const lines: string[] = []

  const cc = o.chunking_config
  if (cc) {
    const parts: string[] = []
    if (cc.chunk_size != null) parts.push(t(k('chunkSize'), { n: cc.chunk_size }))
    if (cc.enable_parent_child != null) {
      parts.push(cc.enable_parent_child ? t(k('parentChildOn')) : t(k('parentChildOff')))
    }
    if (parts.length) lines.push(`${t(k('chunking'))}: ${parts.join(' · ')}`)
  }

  const rules = o.parser_engine_rules || cc?.parser_engine_rules
  if (rules?.length) {
    lines.push(`${t(k('parser'))}: ${rules.map(r => `${r.file_types.join('/')}→${r.engine}`).join(', ')}`)
  }

  const mm = o.vlm_config?.enabled ?? o.enable_multimodel
  if (mm != null) lines.push(`${t(k('multimodal'))}: ${onOff(mm)}`)

  if (o.asr_config?.enabled != null) lines.push(`${t(k('asr'))}: ${onOff(o.asr_config.enabled)}`)

  const qg = o.question_generation_config
  if (qg?.enabled != null) {
    lines.push(`${t(k('question'))}: ${qg.enabled ? t(k('questionOn'), { n: qg.question_count ?? 3 }) : t(k('off'))}`)
  }

  const graph = o.graph_enabled ?? o.extract_config?.enabled
  if (graph != null) lines.push(`${t(k('graph'))}: ${onOff(graph)}`)

  return lines.length ? lines : [t('knowledgeStages.processConfig.kbDefault')]
})
</script>

<template>
  <div class="kp-timeline" :class="{ 'kp-compact': compact }">
    <!-- =========================================================
         COMPACT MODE — used by the card hover popover. Untouched.
         ========================================================= -->
    <template v-if="compact">
      <div class="kp-compact-row">
        <span v-for="s in stages" :key="s.name" class="kp-dot" :class="['kp-dot-' + s.status]"
          :title="t(`knowledgeStages.stage.${s.name}`) + ' · ' + t(`knowledgeStages.status.${s.status}`)" />
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

    <!-- =========================================================
         FULL MODE — Langfuse-style waterfall, lives inside the
         secondary drawer. Bottom-docked detail panel.
         ========================================================= -->
    <template v-else>
      <div class="kp-shell">
        <!-- ============== HEADER ============== -->
        <div class="kp-head">
          <div class="kp-head-toolbar">
            <h2 class="kp-head-doc-title" :title="primaryHeadTitle">{{ primaryHeadTitle }}</h2>
            <t-tag v-if="data && headerStatusText" size="small" :theme="headerStatusTheme" variant="light"
              class="kp-head-status-tag">
              {{ headerStatusText }}
            </t-tag>
            <span v-if="isLive" class="kp-live-badge" :title="t('knowledgeStages.liveTooltip')">
              <span class="kp-live-dot" />
              <span class="kp-live-text">{{ t('knowledgeStages.live') }}</span>
            </span>
            <div class="kp-head-actions">
              <t-popup trigger="hover" placement="bottom-right" :overlay-style="{ maxWidth: '340px' }">
                <button type="button" class="kp-icon-btn"
                  :title="t('knowledgeStages.processConfig.title')"
                  :aria-label="t('knowledgeStages.processConfig.title')">
                  <t-icon name="info-circle" size="14px" />
                </button>
                <template #content>
                  <div class="kp-proccfg-pop">
                    <div class="kp-proccfg-title">{{ t('knowledgeStages.processConfig.title') }}</div>
                    <div v-for="(line, i) in processConfigLines" :key="i" class="kp-proccfg-line">{{ line }}</div>
                  </div>
                </template>
              </t-popup>
              <button type="button" class="kp-icon-btn" :class="{
                'kp-icon-btn-spin': refreshing,
                'kp-icon-btn-autoflow': isLive && !refreshing,
              }" :disabled="loading || refreshing"
                :title="isLive ? t('knowledgeStages.autoRefreshOn') : t('knowledgeStages.refresh')"
                :aria-label="isLive ? t('knowledgeStages.autoRefreshOn') : t('knowledgeStages.refresh')"
                @click="onManualRefresh">
                <t-icon name="refresh" size="14px" />
              </button>
              <t-popconfirm
                v-if="canCancelParse"
                theme="warning"
                :content="t('knowledgeBase.cancelParseConfirmBody', { title: props.docTitle || props.knowledgeId })"
                :confirm-btn="{ content: t('knowledgeBase.cancelParse'), theme: 'danger' }"
                :cancel-btn="{ content: t('common.cancel') }"
                placement="bottom"
                @confirm="onCancelParseConfirm"
              >
                <button type="button" class="kp-icon-btn kp-icon-btn-danger"
                  :class="{ 'kp-icon-btn-spin': cancelling }" :disabled="cancelling"
                  :title="t('knowledgeBase.cancelParse')" :aria-label="t('knowledgeBase.cancelParse')"
                  @click.stop>
                  <t-icon :name="cancelling ? 'loading' : 'close-circle'" size="15px" />
                </button>
              </t-popconfirm>
              <t-button v-if="data?.parse_status === 'failed'" size="small" theme="primary" variant="outline"
                @click="onRetry">
                <t-icon name="refresh" size="14px" />
                <span style="margin-left: 4px">{{ t('knowledgeStages.retry') }}</span>
              </t-button>
              <button v-if="showClose" type="button" class="kp-icon-btn" :aria-label="t('knowledgeStages.close')"
                :title="t('knowledgeStages.close')" @click="emit('close')">
                <t-icon name="close" size="16px" />
              </button>
            </div>
          </div>

          <p v-if="headMetaParts.length > 0" class="kp-head-meta">
            <template v-for="(part, idx) in headMetaParts" :key="idx">
              <span v-if="idx > 0" class="kp-head-meta-sep" aria-hidden="true">·</span>
              <span class="kp-head-meta-part">{{ part }}</span>
            </template>
          </p>

          <div v-if="attemptTabs.length > 0" class="kp-attempts">
            <button v-for="tab in attemptTabs" :key="tab.n" type="button" class="kp-attempt"
              :class="{ 'kp-attempt-active': tab.active }" @click="onAttemptChange(tab.n)">
              <span class="kp-attempt-num kp-mono">#{{ tab.n }}</span>
              <span class="kp-attempt-glyph" :class="attemptGlyph(tab.status).cls">{{ attemptGlyph(tab.status).ch
                }}</span>
            </button>
          </div>
        </div>

        <!-- ============== BODY (Waterfall) ============== -->
        <div class="kp-body" :class="{ 'kp-body-with-detail': detailOpen }">
          <div v-if="loading && !data" class="kp-state">
            <t-loading size="small" />
          </div>
          <div v-else-if="!data && !loading" class="kp-state kp-state-empty">
            <span>{{ t('knowledgeStages.noActivity') }}</span>
          </div>

          <template v-else-if="data">
            <!-- Ruler sits outside the scroll region so the time axis never
                 scrolls away or fights position:sticky inside overflow. -->
            <div v-if="showRuler" class="kp-ruler">
              <div class="kp-ruler-spacer-name" />
              <div class="kp-ruler-spacer-meta" />
              <div class="kp-ruler-track">
                <span v-for="(tick, i) in rulerTicks" :key="i" class="kp-tick"
                  :class="{ 'kp-tick-first': i === 0, 'kp-tick-last': i === rulerTicks.length - 1 }"
                  :style="{ left: tick.left }">
                  <span class="kp-tick-line" />
                  <span class="kp-tick-label kp-mono">{{ tick.label }}</span>
                </span>
              </div>
            </div>

            <div ref="scrollRef" class="kp-scroll">
            <div class="kp-rows">
              <div v-for="row in flatRows" :key="row.key" class="kp-row" :data-span-key="row.key" :class="{
                'kp-row-active': selectedSpanId === row.key,
                'kp-row-root': row.isRoot,
                'kp-row-stage': row.isStage,
                'kp-row-span': !row.isRoot && !row.isStage,
                'kp-row-expandable': row.hasChildren && !row.isRoot,
              }" :title="row.hasChildren && !row.isRoot ? t('knowledgeStages.rowSelectHint') : undefined"
                @click="selectRow(row)">
                <div class="kp-cell-name">
                  <div class="kp-name-inner" :style="{ paddingLeft: row.depth * 16 + 'px' }">
                    <button v-if="row.hasChildren && !row.isRoot" type="button" class="kp-tree-toggle"
                      :aria-expanded="isRowExpanded(row.key)" :aria-label="treeToggleAriaLabel(row)"
                      @click="toggleTree(row, $event)">
                      <t-icon :name="isRowExpanded(row.key) ? 'chevron-down' : 'chevron-right'" size="14px" />
                    </button>
                    <span v-else class="kp-tree-toggle-spacer" />
                    <span class="kp-status-dot"
                      :class="['kp-dot-' + row.node.status, { 'kp-dot-placeholder': isPlaceholder(row.node) }]" />
                    <span class="kp-name-text"
                      :class="{ 'kp-name-root': row.isRoot, 'kp-name-mono': !row.isRoot && !row.isStage }">{{
                        rowLabel(row) }}</span>
                    <span class="kp-name-kind">{{ rowKindLabel(row) }}</span>
                  </div>
                </div>

                <div class="kp-cell-dur kp-mono">
                  <template v-if="row.node.status === 'running'">
                    <span class="kp-running-time">{{ formatDuration(liveElapsedMs(row.node)) }}</span>
                  </template>
                  <template v-else>
                    {{ formatDuration(row.node.duration_ms) }}
                  </template>
                </div>

                <div class="kp-cell-bar">
                  <span v-if="nowMarkerPct !== null && row.isRoot" class="kp-now-marker"
                    :style="{ left: nowMarkerPct + '%' }" />
                  <div v-if="isPlaceholder(row.node)" class="kp-bar kp-bar-placeholder" />
                  <template v-else>
                    <!-- Wrapping outline: descendants extend past this
                         span's own end (e.g. async postprocess subspans
                         under a closed stage). Renders behind the solid
                         self-bar so both are visible. -->
                    <div v-if="wrapStyle(row.node)" class="kp-bar-wrap" :class="['kp-bar-wrap-' + row.node.status]"
                      :style="wrapStyle(row.node) || {}">
                      <span class="kp-bar-tip">
                        <span class="kp-bar-tip-name">{{ rowLabel(row) }}</span>
                        <span class="kp-bar-tip-sep">·</span>
                        <span class="kp-mono">{{ formatDuration(wrapDurationMs(row.node)) }}</span>
                        <span class="kp-bar-tip-sep">·</span>
                        <span>{{ t('knowledgeStages.detail.includingChildren') }}</span>
                      </span>
                    </div>
                    <div class="kp-bar"
                      :class="['kp-bar-' + row.node.status, { 'kp-bar-running-anim': row.node.status === 'running' }]"
                      :style="barStyle(row.node)">
                      <span class="kp-bar-tip">
                        <span class="kp-bar-tip-name">{{ rowLabel(row) }}</span>
                        <span class="kp-bar-tip-sep">·</span>
                        <span class="kp-mono">{{ formatDuration(row.node.status === 'running' ? liveElapsedMs(row.node)
                          : row.node.duration_ms) }}</span>
                        <span class="kp-bar-tip-sep">·</span>
                        <span>{{ localizedStatus(row.node.status) }}</span>
                      </span>
                    </div>
                    <span v-if="barOffsetPct(row.node) !== null && barOffsetMs(row.node) > 0"
                      class="kp-bar-offset kp-mono" :style="{ left: barOffsetPct(row.node) + '%' }">
                      +{{ formatDuration(barOffsetMs(row.node)) }}
                    </span>
                  </template>
                </div>
              </div>
            </div>

            <div v-if="data?.last_error && data?.parse_status === 'failed'" class="kp-last-error">
            <div class="kp-last-error-bar" />
            <div class="kp-last-error-body">
              <div class="kp-last-error-row">
                <span class="kp-last-error-glyph">!</span>
                <span class="kp-last-error-title">{{ localizedErrorTitle(data.last_error.error_code) }}</span>
                <span v-if="data.last_error.error_code" class="kp-last-error-code kp-mono">{{ data.last_error.error_code
                  }}</span>
              </div>
              <div class="kp-last-error-suggestion">{{ localizedErrorSuggestion(data.last_error.error_code) }}</div>
              <div v-if="data.last_error.error_message" class="kp-last-error-raw kp-mono">{{
                data.last_error.error_message }}
              </div>
            </div>
            </div>
            </div>
          </template>
        </div>

        <!-- ============== DETAIL PANEL ============== -->
        <div class="kp-detail" :class="{ 'kp-detail-open': detailOpen }">
          <template v-if="selectedRow">
            <div class="kp-detail-head">
              <div class="kp-detail-title">
                <span class="kp-status-dot kp-detail-dot" :class="['kp-dot-' + selectedRow.node.status]" />
                <span class="kp-detail-name">{{ rowLabel(selectedRow) }}</span>
                <span class="kp-detail-kind">{{ rowKindLabel(selectedRow) }}</span>
                <span class="kp-status-chip" :class="'kp-chip-' + selectedRow.node.status">
                  {{ localizedStatus(selectedRow.node.status) }}
                </span>
              </div>
              <div class="kp-detail-actions">
                <button type="button" class="kp-icon-btn" :title="t('knowledgeStages.copyDetails')"
                  @click.stop="copySpan(selectedRow.node)">
                  <t-icon name="copy" size="18px" />
                </button>
                <button type="button" class="kp-icon-btn" :title="t('knowledgeStages.close')" @click="closeDetail">
                  <t-icon name="close" size="18px" />
                </button>
              </div>
            </div>

            <div class="kp-tabs">
              <button type="button" class="kp-tab" :class="{ 'kp-tab-active': detailTab === 'overview' }"
                @click="detailTab = 'overview'">{{ t('knowledgeStages.tab.overview') }}</button>
              <button type="button" class="kp-tab"
                :class="{ 'kp-tab-active': detailTab === 'input', 'kp-tab-empty': !tabHasContent('input') }"
                @click="detailTab = 'input'">{{ t('knowledgeStages.detail.input') }}</button>
              <button type="button" class="kp-tab"
                :class="{ 'kp-tab-active': detailTab === 'output', 'kp-tab-empty': !tabHasContent('output') }"
                @click="detailTab = 'output'">{{ t('knowledgeStages.detail.output') }}</button>
              <button v-if="tabHasContent('metadata')" type="button" class="kp-tab"
                :class="{ 'kp-tab-active': detailTab === 'metadata' }" @click="detailTab = 'metadata'">{{
                  t('knowledgeStages.detail.metadata') }}</button>
              <button type="button" class="kp-tab" :class="{ 'kp-tab-active': detailTab === 'raw' }"
                @click="detailTab = 'raw'">{{ t('knowledgeStages.tab.raw') }}</button>
            </div>

            <div class="kp-detail-body">
              <!-- Overview tab -->
              <template v-if="detailTab === 'overview'">
                <!-- Timing -->
                <div class="kp-section">
                  <div class="kp-section-title">{{ t('knowledgeStages.detail.timing') }}</div>
                  <div class="kp-kv">
                    <div class="kp-kv-row">
                      <span class="kp-kv-key">{{ t('knowledgeStages.detail.started') }}</span>
                      <span class="kp-kv-val kp-mono">{{ formatTime(selectedRow.node.started_at) }}</span>
                    </div>
                    <div class="kp-kv-row">
                      <span class="kp-kv-key">{{ t('knowledgeStages.detail.finished') }}</span>
                      <span class="kp-kv-val kp-mono">
                        <template v-if="selectedRow.node.status === 'running'">
                          <span class="kp-kv-running">{{ t('knowledgeStages.detail.inProgress') }}</span>
                        </template>
                        <template v-else>
                          {{ formatTime(selectedRow.node.finished_at) }}
                        </template>
                      </span>
                    </div>
                    <div class="kp-kv-row">
                      <span class="kp-kv-key">{{ t('knowledgeStages.detail.duration') }}</span>
                      <span class="kp-kv-val kp-mono">
                        <template v-if="selectedRow.node.status === 'running'">
                          {{ formatDuration(liveElapsedMs(selectedRow.node)) }}
                          <span class="kp-kv-tag-live">{{ t('knowledgeStages.detail.elapsed') }}</span>
                        </template>
                        <template v-else>
                          {{ formatDuration(selectedRow.node.duration_ms) }}
                        </template>
                      </span>
                    </div>
                    <div v-if="!selectedRow.isRoot && barOffsetMs(selectedRow.node) > 0" class="kp-kv-row">
                      <span class="kp-kv-key">{{ t('knowledgeStages.detail.offset') }}</span>
                      <span class="kp-kv-val kp-mono">+{{ formatDuration(barOffsetMs(selectedRow.node)) }}</span>
                    </div>
                  </div>
                </div>

                <!-- Identity / lineage -->
                <div class="kp-section">
                  <div class="kp-section-title">{{ t('knowledgeStages.detail.identity') }}</div>
                  <div class="kp-kv">
                    <div v-for="entry in identityFields(selectedRow)" :key="entry.key" class="kp-kv-row">
                      <span class="kp-kv-key">{{ entry.label }}</span>
                      <span class="kp-kv-val" :class="{ 'kp-mono': entry.mono, 'kp-kv-truncate': entry.copyable }">
                        <span class="kp-kv-text">{{ entry.value }}</span>
                        <button v-if="entry.copyable" type="button" class="kp-kv-copy"
                          :title="t('knowledgeStages.copy')" @click.stop="copyValue(entry.value)">
                          <t-icon name="copy" size="14px" />
                        </button>
                      </span>
                    </div>
                  </div>
                </div>

                <div v-if="traceMetadata" class="kp-section">
                  <div class="kp-section-title">{{ t('knowledgeStages.detail.traceMetadata') }}</div>
                  <p class="kp-section-desc">{{ t('knowledgeStages.detail.metadataHint') }}</p>
                  <div class="kp-kv">
                    <div v-for="entry in buildKvEntries(traceMetadata)" :key="entry.key" class="kp-kv-row kp-kv-row-multiline">
                      <span class="kp-kv-key kp-mono">{{ entry.key }}</span>
                      <span class="kp-kv-val kp-kv-scalar">{{ entry.display }}</span>
                    </div>
                  </div>
                </div>

                <!-- Stage breakdown (root only) -->
                <div v-if="selectedRow.isRoot" class="kp-section">
                  <div class="kp-section-title">{{ t('knowledgeStages.detail.stageBreakdown') }}</div>
                  <div class="kp-breakdown">
                    <div v-for="s in stageBreakdown" :key="s.name" class="kp-breakdown-row"
                      :class="['kp-breakdown-' + s.status]">
                      <span class="kp-breakdown-label">
                        <span class="kp-status-dot" :class="['kp-dot-' + s.status]" />
                        {{ s.label }}
                      </span>
                      <div class="kp-breakdown-track">
                        <div class="kp-breakdown-bar" :class="['kp-bar-' + s.status]" :style="{ width: s.pct + '%' }" />
                      </div>
                      <span class="kp-breakdown-dur kp-mono">{{ formatDuration(s.duration_ms) }}</span>
                    </div>
                  </div>
                </div>

                <!-- Error -->
                <div
                  v-if="(selectedRow.node.status === 'failed' || selectedRow.node.status === 'cancelled') && (selectedRow.node.error_code || selectedRow.node.error_message)"
                  class="kp-error-block">
                  <div class="kp-error-head">
                    <span class="kp-error-glyph">!</span>
                    <span class="kp-error-title">{{ localizedErrorTitle(selectedRow.node.error_code) ||
                      t('knowledgeStages.detail.error') }}</span>
                    <span v-if="selectedRow.node.error_code" class="kp-error-code kp-mono">{{
                      selectedRow.node.error_code }}</span>
                  </div>
                  <pre v-if="selectedRow.node.error_message"
                    class="kp-error-msg kp-mono">{{ selectedRow.node.error_message }}</pre>
                </div>

                <div v-if="!selectedRow.node.span_id && !selectedRow.node.started_at" class="kp-detail-hint">
                  {{ t('knowledgeStages.detail.placeholderHint') }}
                </div>
              </template>

              <!-- Input / Output / Metadata tabs -->
              <template v-else-if="detailTab === 'input' || detailTab === 'output' || detailTab === 'metadata'">
                <div v-if="!tabHasContent(detailTab)" class="kp-detail-empty">
                  <span>{{ detailTab === 'metadata' ? t('knowledgeStages.detail.metadataEmpty') :
                    t('knowledgeStages.detail.empty') }}</span>
                </div>
                <template v-else>
                  <div class="kp-section">
                    <div class="kp-section-bar">
                      <span class="kp-section-title">{{ t('knowledgeStages.detail.' + detailTab) }}</span>
                      <button type="button" class="kp-section-action"
                        @click="copyValue((selectedRow.node as any)[detailTab])">
                        <t-icon name="copy" size="14px" />
                        <span>{{ t('knowledgeStages.copy') }}</span>
                      </button>
                    </div>

                    <div v-if="isObjectWithKeys((selectedRow.node as any)[detailTab])" class="kp-kv">
                      <div v-for="entry in buildKvEntries((selectedRow.node as any)[detailTab])" :key="entry.key"
                        class="kp-kv-row kp-kv-row-multiline">
                        <span class="kp-kv-key kp-mono">{{ entry.key }}</span>
                        <div class="kp-kv-val kp-kv-multiline">
                          <span v-if="entry.kind === 'bool'" class="kp-mono"
                            :class="{ 'kp-bool-true': entry.raw, 'kp-bool-false': !entry.raw }">{{ entry.display
                            }}</span>
                          <span v-else-if="entry.kind === 'scalar'" class="kp-kv-scalar">{{ entry.display }}</span>
                          <!-- Short payloads render inline so the user
                               sees the data without an extra click. The
                               summary chip ("Array · 3") is shown above
                               the JSON for context. -->
                          <div v-else-if="entry.defaultExpanded" class="kp-kv-inline">
                            <span class="kp-kv-summary kp-mono kp-kv-summary-static">{{ entry.display }}</span>
                            <pre class="kp-json kp-mono">{{ prettyJSON(entry.raw) }}</pre>
                          </div>
                          <div v-else class="kp-kv-collapsible">
                            <button type="button" class="kp-kv-toggle"
                              @click.stop="toggleJsonKey(detailTab, entry.key)">
                              <span class="kp-kv-summary kp-mono">{{ entry.display }}</span>
                              <span class="kp-kv-toggle-label">{{
                                isJsonExpanded(detailTab, entry.key)
                                  ? t('knowledgeStages.detail.hideJson')
                                  : t('knowledgeStages.detail.showJson')
                              }}</span>
                            </button>
                            <pre v-if="isJsonExpanded(detailTab, entry.key)"
                              class="kp-json kp-mono">{{ prettyJSON(entry.raw) }}</pre>
                          </div>
                        </div>
                      </div>
                    </div>
                    <pre v-else class="kp-json kp-mono">{{ prettyJSON((selectedRow.node as any)[detailTab]) }}</pre>
                  </div>
                </template>
              </template>

              <!-- Raw JSON tab -->
              <template v-else-if="detailTab === 'raw'">
                <div class="kp-section">
                  <div class="kp-section-bar">
                    <span class="kp-section-title">{{ t('knowledgeStages.tab.raw') }}</span>
                    <button type="button" class="kp-section-action" @click="copyValue(selectedRow.node)">
                      <t-icon name="copy" size="14px" />
                      <span>{{ t('knowledgeStages.copy') }}</span>
                    </button>
                  </div>
                  <pre class="kp-json kp-mono kp-json-large">{{ prettyJSON(selectedRow.node) }}</pre>
                </div>
              </template>
            </div>
          </template>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped lang="less">
.kp-timeline {
  font-family: var(--app-font-family);
  font-size: 13px;
  color: var(--td-text-color-primary);
  width: 100%;
  height: 100%;
  /* Defensive: never let waterfall rows / detail panel push past the
     drawer's visible bounds even if the host width is unexpectedly
     narrow. The horizontal scroll inside .kp-body handles legit cases
     where labels are long. */
  overflow: hidden;
}

.kp-shell {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
  width: 100%;
  min-height: 0;
  min-width: 0;
  background: var(--td-bg-color-container);
  overflow: hidden;
}

/* ============== HEADER ============== */
.kp-head {
  flex: 0 0 auto;
  padding: 14px 20px 10px;
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

.kp-head-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.kp-head-doc-title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  line-height: 1.35;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kp-head-status-tag {
  flex-shrink: 0;
}

/* LIVE badge — sits next to the title while polling, telegraphs the
   pipeline is actively refreshing. Pulsing dot + uppercase mono label. */
.kp-live-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  border-radius: var(--td-radius-medium);
  background: var(--td-warning-color-light);
  color: var(--td-warning-color);
  font-size: 10px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  line-height: 1;
}

.kp-live-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--td-warning-color);
  animation: kpLivePulse 1.4s ease-in-out infinite;
}

.kp-live-text {
  font-family: var(--app-font-family-mono);
}

@keyframes kpLivePulse {

  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }

  50% {
    opacity: 0.45;
    transform: scale(0.8);
  }
}

.kp-head-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  margin-left: auto;
}

.kp-head-meta {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  word-break: break-word;
}

.kp-head-meta-sep {
  margin: 0 6px;
  color: var(--td-text-color-placeholder);
}

.kp-head-meta-part {
  display: inline;
}

.kp-icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border: none;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  border-radius: var(--td-radius-default);
  transition: background 150ms ease, color 150ms ease;
}

.kp-icon-btn:hover:not(:disabled) {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}

.kp-icon-btn:disabled {
  cursor: not-allowed;
  opacity: 0.4;
}

/* Stop-parse control — stays a quiet placeholder icon until hover, then
   reveals its destructive intent with the error tint. Matches the other
   header icon buttons rather than shouting with a full outline button. */
.kp-icon-btn-danger:hover:not(:disabled) {
  background: var(--td-error-color-light);
  color: var(--td-error-color);
}

.kp-icon-btn-spin :deep(.t-icon) {
  animation: kpSpin 0.9s linear infinite;
}

/* Slow rotation while auto-polling — visually distinct from the
   manual-refresh fast spin. Tells the user "refresh is happening on
   its own" without an extra label or badge. */
.kp-icon-btn-autoflow :deep(.t-icon) {
  animation: kpSpin 4s linear infinite;
  color: var(--td-warning-color);
}

@keyframes kpSpin {
  to {
    transform: rotate(360deg);
  }
}

.kp-meta-glyph {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 12px;
  height: 12px;
  font-size: 11px;
  line-height: 1;
}

.kp-glyph-done {
  color: var(--td-success-color);
}

.kp-glyph-failed {
  color: var(--td-error-color);
}

.kp-glyph-running {
  color: var(--td-warning-color);
  animation: kpLivePulse 1.4s ease-in-out infinite;
}

.kp-glyph-unknown {
  color: var(--td-text-color-placeholder);
}

/* Attempts strip */
.kp-attempts {
  display: flex;
  gap: 6px;
  margin-top: 10px;
  overflow-x: auto;
  padding-bottom: 2px;
}

.kp-attempt {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 10px;
  border: 1px solid var(--td-component-border);
  border-radius: var(--td-radius-default);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 1.4;
  cursor: pointer;
  white-space: nowrap;
  transition: background 150ms ease, border-color 150ms ease, color 150ms ease;
}

.kp-attempt:hover {
  background: var(--td-bg-color-secondarycontainer);
  border-color: var(--td-text-color-placeholder);
  color: var(--td-text-color-primary);
}

.kp-attempt-active {
  background: var(--td-brand-color);
  color: var(--td-text-color-anti);
  border-color: var(--td-brand-color);
}

.kp-attempt-active .kp-attempt-glyph {
  color: var(--td-text-color-anti) !important;
}

.kp-attempt-num {
  font-weight: 600;
  font-size: 11px;
}

.kp-attempt-glyph {
  font-size: 9px;
  line-height: 1;
}

/* ============== BODY (Waterfall) ============== */
.kp-body {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--td-bg-color-container);
}

.kp-scroll {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  overflow-x: auto;
  padding-bottom: 16px;
}

.kp-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  flex: 1 1 auto;
  padding: 56px 20px;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

/* Ruler — pinned above .kp-scroll, not inside the scroller */
.kp-ruler {
  flex: 0 0 auto;
  display: grid;
  grid-template-columns: minmax(220px, 42%) 64px 1fr;
  height: 24px;
  align-items: end;
  padding: 12px 20px 6px;
  background: var(--td-bg-color-container);
  border-bottom: 1px dashed var(--td-component-stroke);
  box-shadow: 0 4px 8px -6px rgba(0, 0, 0, 0.12);
}

.kp-ruler-spacer-name,
.kp-ruler-spacer-meta {
  height: 100%;
}

.kp-ruler-track {
  position: relative;
  height: 100%;
  margin-right: 16px;
}

.kp-tick {
  position: absolute;
  bottom: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  transform: translateX(-50%);
  font-size: 10px;
  color: var(--td-text-color-placeholder);
}

.kp-tick-first {
  transform: translateX(0);
  align-items: flex-start;
}

.kp-tick-last {
  transform: translateX(-100%);
  align-items: flex-end;
}

.kp-tick-line {
  width: 1px;
  height: 5px;
  background: var(--td-component-border);
}

.kp-tick-label {
  margin-top: 2px;
  font-size: 10px;
  letter-spacing: 0.02em;
}

/* Rows */
.kp-rows {
  display: flex;
  flex-direction: column;
}

.kp-row {
  display: grid;
  grid-template-columns: minmax(220px, 42%) 64px 1fr;
  align-items: center;
  height: 32px;
  cursor: pointer;
  position: relative;
  padding: 0 20px;
  transition: background 150ms ease;
}

.kp-row::before {
  content: "";
  position: absolute;
  left: 0;
  top: 4px;
  bottom: 4px;
  width: 2px;
  background: transparent;
  border-radius: 0 2px 2px 0;
  transition: background 150ms ease;
}

.kp-row:hover {
  background: var(--td-bg-color-secondarycontainer);
}

.kp-row-active {
  background: var(--td-bg-color-secondarycontainer);
}

.kp-row-active::before {
  background: var(--td-brand-color);
}

.kp-row-root {
  font-weight: 600;
}

.kp-row-stage:not(.kp-row-active) {
  background: color-mix(in srgb, var(--td-bg-color-secondarycontainer) 55%, transparent);
}

.kp-row-span:not(.kp-row-active):not(:hover) {
  background: color-mix(in srgb, var(--td-bg-color-container) 92%, var(--td-bg-color-secondarycontainer));
}

.kp-row-expandable:hover .kp-tree-toggle {
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container-hover);
}

/* Name cell */
.kp-cell-name {
  min-width: 0;
}

.kp-name-inner {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}

.kp-tree-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  padding: 0;
  cursor: pointer;
  color: var(--td-text-color-placeholder);
  width: 22px;
  height: 22px;
  margin: -3px 0;
  transition: color 120ms ease, background 150ms ease;
  flex-shrink: 0;
  border-radius: var(--td-radius-default);
}

.kp-tree-toggle:hover {
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container-hover);
}

.kp-tree-toggle-spacer {
  width: 22px;
  height: 22px;
  display: inline-block;
  flex-shrink: 0;
  margin: -3px 0;
}

.kp-status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--td-text-color-placeholder);
}

.kp-name-text {
  font-size: 12px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.kp-name-mono {
  font-family: var(--app-font-family-mono);
  font-size: 11px;
}

.kp-name-root {
  font-weight: 600;
  font-size: 13px;
}

.kp-name-kind {
  font-family: var(--app-font-family-mono);
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--td-text-color-placeholder);
  margin-left: auto;
  padding-left: 8px;
  flex-shrink: 0;
}

/* Duration cell */
.kp-cell-dur {
  font-size: 11px;
  color: var(--td-text-color-secondary);
  text-align: right;
  padding-right: 12px;
  letter-spacing: 0.02em;
}

.kp-running-time {
  color: var(--td-warning-color);
  font-weight: 600;
}

/* Bar cell */
.kp-cell-bar {
  position: relative;
  height: 32px;
  margin-right: 16px;
}

/* Vertical "now" cursor — animates left during polling so the user can
   visually confirm time is advancing even when the running bar grows
   slowly toward the right edge of the trace. */
.kp-now-marker {
  position: absolute;
  top: 4px;
  bottom: 4px;
  width: 1px;
  background: var(--td-warning-color);
  opacity: 0.65;
  z-index: 1;
  pointer-events: none;
  transition: left 1s linear;
}

.kp-now-marker::before {
  content: "";
  position: absolute;
  top: -2px;
  left: -3px;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--td-warning-color);
  animation: kpLivePulse 1.4s ease-in-out infinite;
}

.kp-bar {
  position: absolute;
  top: 12px;
  height: 8px;
  border-radius: var(--td-radius-small);
  background: var(--td-text-color-placeholder);
  min-width: 2px;
  /* Smooth left/width changes so polling-driven re-renders feel like
     the bar is growing, not jumping. The 1s easing matches the nowTick
     1Hz cadence. */
  transition: left 800ms cubic-bezier(0.2, 0.8, 0.2, 1),
    width 800ms cubic-bezier(0.2, 0.8, 0.2, 1),
    filter 150ms ease;
  z-index: 2;
}

.kp-row:hover .kp-bar {
  filter: brightness(1.05);
}

.kp-bar-tip {
  position: absolute;
  bottom: calc(100% + 8px);
  left: 50%;
  transform: translateX(-50%);
  background: var(--td-text-color-primary);
  color: var(--td-text-color-anti);
  font-size: 11px;
  padding: 4px 8px;
  border-radius: var(--td-radius-default);
  white-space: nowrap;
  opacity: 0;
  pointer-events: none;
  transition: opacity 150ms ease;
  z-index: 10;
  display: flex;
  align-items: center;
  gap: 4px;
}

.kp-bar-tip-name {
  font-weight: 500;
}

.kp-bar-tip-sep {
  color: var(--td-font-white-3);
}

.kp-bar:hover .kp-bar-tip {
  opacity: 1;
}

/* ROOT row is the first row under the fixed ruler. The default
   `bottom: calc(100% + 8px)` tooltip placement can clip against the
   ruler band, so flip tooltips below the bar for the root row. */
.kp-row-root .kp-bar-tip {
  bottom: auto;
  top: calc(100% + 8px);
}

/* Status palette — NOT all green. The project brand color happens
   to be green, which made done/running visually identical (both
   solid green). Done stays green (universal "success" semantic);
   running goes amber + striped (CI-style "in progress" — recognized
   everywhere from GitHub Actions to Jenkins). The two are now
   unmistakably different at a glance. */
.kp-bar-done {
  background: var(--td-success-color);
}

.kp-bar-failed {
  background: var(--td-error-color);
}

.kp-bar-cancelled {
  background: transparent;
  border: 1px dashed var(--td-error-color);
  height: 6px;
  top: 13px;
}

.kp-bar-skipped {
  background: var(--td-text-color-placeholder);
  opacity: 0.4;
}

.kp-bar-pending {
  display: none;
}

.kp-bar-running {
  /* Muted amber base. The diagonal stripes do the "in flight"
     signaling — the tone just supplies a subtle hint. Earlier
     iteration used full --td-warning-color + halo shadow + hard
     white stripes; users found that visually screaming. */
  background-color: var(--td-warning-color-3);
  background-image: linear-gradient(135deg,
      rgba(255, 255, 255, 0.22) 25%,
      transparent 25%,
      transparent 50%,
      rgba(255, 255, 255, 0.22) 50%,
      rgba(255, 255, 255, 0.22) 75%,
      transparent 75%,
      transparent);
  background-size: 14px 14px;
  animation: kpStripes 1.6s linear infinite;
}

@keyframes kpStripes {
  to {
    background-position: 14px 0;
  }
}

/* Wrapping outline bar — shows the full window from this span's start
   to the latest descendant end. Used when async children extend past
   the parent's own finished_at (e.g. postprocess stage closes fast but
   its summary/question subspans run for a long time). */
.kp-bar-wrap {
  position: absolute;
  top: 9px;
  height: 14px;
  border: 1px dashed var(--td-component-border);
  border-radius: var(--td-radius-small);
  background: transparent;
  min-width: 4px;
  z-index: 1;
  pointer-events: auto;
  transition: left 800ms cubic-bezier(0.2, 0.8, 0.2, 1),
    width 800ms cubic-bezier(0.2, 0.8, 0.2, 1);
}

.kp-bar-wrap:hover {
  border-color: var(--td-text-color-secondary);
}

.kp-bar-wrap:hover .kp-bar-tip {
  opacity: 1;
}

.kp-bar-wrap-done {
  border-color: rgba(7, 192, 95, 0.35);
}

.kp-bar-wrap-failed {
  border-color: rgba(229, 87, 64, 0.5);
}

.kp-bar-wrap-running {
  border-color: rgba(250, 157, 59, 0.5);
}

.kp-bar-wrap-cancelled {
  border-color: rgba(229, 87, 64, 0.3);
}

/* Indeterminate sweep on the running bar — gives obvious motion while
   waiting for the next poll. */
.kp-bar-running-anim {
  position: relative;
  overflow: hidden;
}

.kp-bar-running-anim::after {
  content: "";
  position: absolute;
  inset: 0;
  background: linear-gradient(90deg,
      transparent 0%,
      rgba(255, 255, 255, 0.5) 50%,
      transparent 100%);
  animation: kpSweep 1.6s linear infinite;
}

.kp-bar-placeholder {
  right: 4px;
  top: 13px;
  height: 6px;
  width: 14px;
  background: transparent;
  border: 1px dashed var(--td-component-border);
  border-radius: var(--td-radius-small);
  position: absolute;
}

.kp-bar-offset {
  position: absolute;
  bottom: -1px;
  font-size: 9px;
  color: var(--td-text-color-placeholder);
  pointer-events: none;
  white-space: nowrap;
  transform: translateX(-50%);
  opacity: 0;
  transition: opacity 150ms ease;
}

.kp-row:hover .kp-bar-offset,
.kp-row-active .kp-bar-offset {
  opacity: 1;
}

@keyframes kpSweep {
  0% {
    transform: translateX(-100%);
  }

  100% {
    transform: translateX(100%);
  }
}

/* Status dots (shared with compact mode) */
.kp-dot-done {
  background: var(--td-success-color);
}

.kp-dot-running {
  background: var(--td-warning-color);
  animation: kpLivePulse 1.4s ease-in-out infinite;
}

.kp-dot-failed {
  background: var(--td-error-color);
}

.kp-dot-cancelled {
  background: transparent;
  border: 1px dashed var(--td-text-color-placeholder);
}

.kp-dot-skipped {
  background: var(--td-text-color-placeholder);
  opacity: 0.4;
}

.kp-dot-pending {
  background: transparent;
  border: 1px solid var(--td-component-border);
}

.kp-dot-placeholder {
  background: transparent;
  border: 1px dashed var(--td-component-border);
}

.kp-dot-completed {
  background: var(--td-success-color);
}

.kp-dot-processing {
  background: var(--td-warning-color);
  animation: kpLivePulse 1.4s ease-in-out infinite;
}

.kp-dot-unknown {
  background: var(--td-text-color-placeholder);
}

/* Last error block */
.kp-last-error {
  margin: 14px 20px 4px;
  display: flex;
  background: var(--td-error-color-light);
  border-radius: var(--td-radius-medium);
  overflow: hidden;
  border: 1px solid var(--td-error-color-3);
}

.kp-last-error-bar {
  width: 3px;
  background: var(--td-error-color);
  flex-shrink: 0;
}

.kp-last-error-body {
  flex: 1;
  padding: 10px 14px;
  min-width: 0;
}

.kp-last-error-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.kp-last-error-glyph {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  background: var(--td-error-color);
  color: var(--td-text-color-anti);
  border-radius: 50%;
  font-size: 11px;
  font-weight: 700;
  flex-shrink: 0;
}

.kp-last-error-title {
  font-weight: 600;
  font-size: 12px;
  color: var(--td-error-color);
}

.kp-last-error-code {
  font-size: 10px;
  background: var(--td-error-color);
  color: var(--td-text-color-anti);
  padding: 1px 6px;
  border-radius: var(--td-radius-small);
  margin-left: auto;
}

.kp-last-error-suggestion {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  margin-bottom: 4px;
}

.kp-last-error-raw {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  white-space: pre-wrap;
  word-break: break-word;
}

/* ============== DETAIL PANEL ============== */
.kp-detail {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  height: 0;
  overflow: hidden;
  transition: height 240ms cubic-bezier(0.2, 0.8, 0.2, 1);
}

.kp-detail-open {
  height: 50%;
  min-height: 320px;
}

.kp-detail-head {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 20px 10px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.kp-detail-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.kp-detail-dot {
  width: 8px;
  height: 8px;
  flex-shrink: 0;
}

.kp-detail-name {
  font-weight: 600;
  font-size: 13px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.kp-detail-kind {
  font-family: var(--app-font-family-mono);
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

/* Status chip — soft tinted background using the brand/error/success
   palette. Matches TDesign tag aesthetics. */
.kp-status-chip {
  display: inline-flex;
  align-items: center;
  padding: 1px 8px;
  border-radius: var(--td-radius-default);
  font-size: 11px;
  font-weight: 500;
  background: var(--td-bg-color-component);
  color: var(--td-text-color-primary);
  flex-shrink: 0;
}

.kp-chip-done {
  background: var(--td-success-color-light);
  color: var(--td-success-color);
}

.kp-chip-running {
  background: var(--td-warning-color-light);
  color: var(--td-warning-color);
}

.kp-chip-failed {
  background: var(--td-error-color-light);
  color: var(--td-error-color);
}

.kp-chip-cancelled {
  background: var(--td-bg-color-component);
  color: var(--td-text-color-secondary);
}

.kp-chip-skipped {
  background: var(--td-bg-color-component);
  color: var(--td-text-color-placeholder);
}

.kp-chip-pending {
  background: var(--td-bg-color-component);
  color: var(--td-text-color-secondary);
}

.kp-detail-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}

/* Tabs */
.kp-tabs {
  flex: 0 0 auto;
  display: flex;
  gap: 0;
  padding: 0 20px;
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

.kp-tab {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 9px 14px 10px;
  border: none;
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  cursor: pointer;
  position: relative;
  transition: color 150ms ease;
}

.kp-tab:hover {
  color: var(--td-text-color-primary);
}

.kp-tab-active {
  color: var(--td-text-color-primary);
  font-weight: 600;
}

.kp-tab-active::after {
  content: "";
  position: absolute;
  left: 14px;
  right: 14px;
  bottom: -1px;
  height: 2px;
  background: var(--td-brand-color);
  border-radius: 2px 2px 0 0;
}

.kp-tab-empty {
  color: var(--td-text-color-placeholder);
}

.kp-detail-body {
  flex: 1 1 auto;
  overflow-y: auto;
  padding: 16px 20px 18px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.kp-detail-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 48px 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.kp-detail-hint {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  padding: 10px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: var(--td-radius-medium);
  border-left: 2px solid var(--td-component-border);
}

/* Sections */
.kp-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.kp-section-title {
  font-size: 11px;
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--td-text-color-secondary);
}

.kp-section-desc {
  margin: 4px 0 8px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

.kp-section-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.kp-section-action {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: 1px solid var(--td-component-border);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: 11px;
  padding: 3px 8px;
  border-radius: var(--td-radius-default);
  cursor: pointer;
  transition: background 150ms ease, color 150ms ease, border-color 150ms ease;
}

.kp-section-action:hover {
  background: var(--td-brand-color);
  color: var(--td-text-color-anti);
  border-color: var(--td-brand-color);
}

.kp-section-action:hover :deep(.t-icon) {
  color: var(--td-text-color-anti);
}

/* KV grid — TDesign description list aesthetic. White card on the gray
   page bg, soft 1px row separators, key/label color hierarchy. */
.kp-kv {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium);
  background: var(--td-bg-color-container);
  overflow: hidden;
}

.kp-kv-row {
  display: grid;
  grid-template-columns: 130px 1fr;
  gap: 12px;
  align-items: center;
  font-size: 12px;
  min-width: 0;
  padding: 8px 12px;
  background: var(--td-bg-color-container);
}

.kp-kv-row+.kp-kv-row {
  border-top: 1px solid var(--td-bg-color-secondarycontainer);
}

.kp-kv-row-multiline {
  align-items: flex-start;
}

.kp-kv-key {
  color: var(--td-text-color-secondary);
  font-size: 11px;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.kp-kv-val {
  color: var(--td-text-color-primary);
  overflow-wrap: anywhere;
  word-break: break-word;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.kp-kv-truncate {
  overflow: hidden;
}

.kp-kv-truncate .kp-kv-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  flex: 1;
}

.kp-kv-multiline {
  display: flex;
  flex-direction: column;
  gap: 4px;
  align-items: stretch;
}

.kp-kv-scalar {
  font-size: 12px;
}

.kp-kv-running {
  color: var(--td-warning-color);
  font-style: italic;
  font-family: var(--app-font-family);
  font-size: 11px;
}

.kp-kv-tag-live {
  display: inline-block;
  margin-left: 6px;
  padding: 0 6px;
  font-size: 9px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  background: var(--td-warning-color-light);
  color: var(--td-warning-color);
  border-radius: var(--td-radius-small);
  font-family: var(--app-font-family);
}

.kp-kv-copy {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border: none;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  border-radius: var(--td-radius-small);
  flex-shrink: 0;
}

.kp-kv-copy:hover {
  background: var(--td-bg-color-container-hover);
  color: var(--td-brand-color);
}

.kp-mono {
  font-family: var(--app-font-family-mono);
  font-size: 11px;
  letter-spacing: 0;
}

.kp-bool-true {
  color: var(--td-success-color);
  font-weight: 500;
}

.kp-bool-false {
  color: var(--td-error-color);
  font-weight: 500;
}

.kp-kv-collapsible {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

/* Same vertical layout as collapsible, but with no toggle button —
   used for short payloads that auto-expand inline. */
.kp-kv-inline {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.kp-kv-summary-static {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

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
  color: var(--td-text-color-secondary);
}

.kp-kv-toggle-label {
  font-size: 11px;
  color: var(--td-brand-color);
  font-weight: 500;
}

.kp-kv-toggle:hover .kp-kv-toggle-label {
  text-decoration: underline;
}

/* Stage breakdown table inside root overview */
.kp-breakdown {
  display: flex;
  flex-direction: column;
  gap: 6px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium);
  background: var(--td-bg-color-container);
  padding: 10px 12px;
}

.kp-breakdown-row {
  display: grid;
  grid-template-columns: 110px 1fr 64px;
  gap: 10px;
  align-items: center;
  font-size: 12px;
}

.kp-breakdown-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-primary);
}

.kp-breakdown-track {
  position: relative;
  height: 6px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: var(--td-radius-small);
  overflow: hidden;
}

.kp-breakdown-bar {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  border-radius: var(--td-radius-small);
  transition: width 800ms cubic-bezier(0.2, 0.8, 0.2, 1);
}

.kp-breakdown-row.kp-breakdown-pending .kp-breakdown-bar {
  display: none;
}

.kp-breakdown-dur {
  text-align: right;
  color: var(--td-text-color-secondary);
}

/* Error block in overview */
.kp-error-block {
  border: 1px solid var(--td-error-color-3);
  border-radius: var(--td-radius-medium);
  background: var(--td-error-color-light);
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.kp-error-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.kp-error-glyph {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  background: var(--td-error-color);
  color: var(--td-text-color-anti);
  border-radius: 50%;
  font-size: 11px;
  font-weight: 700;
  flex-shrink: 0;
}

.kp-error-title {
  font-weight: 600;
  font-size: 12px;
  color: var(--td-error-color);
}

.kp-error-code {
  margin-left: auto;
  font-size: 10px;
  background: var(--td-error-color);
  color: var(--td-text-color-anti);
  padding: 1px 6px;
  border-radius: var(--td-radius-small);
}

.kp-error-msg {
  margin: 0;
  font-size: 11px;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 160px;
  overflow: auto;
  padding: 8px 10px;
  background: var(--td-bg-color-container);
  border-radius: var(--td-radius-default);
  border: 1px solid var(--td-component-stroke);
}

/* JSON viewer */
.kp-json {
  margin: 0;
  padding: 10px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium);
  max-height: 360px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 11px;
  color: var(--td-text-color-primary);
  line-height: 1.6;
}

.kp-json-large {
  max-height: 480px;
}

/* ============== COMPACT MODE (untouched) ============== */
.kp-compact {
  max-width: 320px;
  height: auto;
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
  background: var(--td-text-color-placeholder);
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

.kp-proccfg-pop {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 2px 0;
  max-width: 320px;
}

.kp-proccfg-title {
  font-weight: 600;
  font-size: 13px;
  color: var(--td-text-color-primary);
  margin-bottom: 2px;
}

.kp-proccfg-line {
  font-size: 12px;
  line-height: 1.6;
  color: var(--td-text-color-secondary);
  word-break: break-word;
}
</style>
