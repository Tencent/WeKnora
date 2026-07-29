/** Whether GET /knowledge/:id/spans returned a real trace (not legacy placeholder-only). */
export function knowledgeSpansPayloadHasTrace(
  data: { trace?: { span_id?: string }; current_attempt?: number } | null | undefined,
): boolean {
  if (!data?.trace) return false
  return !!(data.trace.span_id || (data.current_attempt ?? 0) > 0)
}

export interface KnowledgeTraceNode {
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
  input?: unknown
  output?: unknown
  metadata?: unknown
  children?: KnowledgeTraceNode[]
}

export interface PostprocessTaskSummary {
  running: number
  failed: number
  completed: number
  other: number
  total: number
}

const graphChunkName = /^postprocess\.graph\.chunk\[(\d+)\]$/

function timestamp(value?: string | null): number | null {
  if (!value) return null
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? null : parsed
}

function nodeEnd(node: KnowledgeTraceNode): number | null {
  const finished = timestamp(node.finished_at)
  if (finished !== null) return finished
  const started = timestamp(node.started_at)
  if (started !== null && typeof node.duration_ms === 'number' && node.duration_ms >= 0) {
    return started + node.duration_ms
  }
  return null
}

function aggregateStatus(nodes: KnowledgeTraceNode[]): string {
  if (nodes.some(node => node.status === 'running' || node.status === 'pending')) return 'running'
  const hasSucceeded = nodes.some(node => node.status === 'done' || node.status === 'partial')
  if (nodes.some(node => node.status === 'failed')) return hasSucceeded ? 'partial' : 'failed'
  if (nodes.some(node => node.status === 'partial')) return 'partial'
  if (nodes.every(node => node.status === 'skipped')) return 'skipped'
  if (nodes.some(node => node.status === 'cancelled')) return 'cancelled'
  return 'done'
}

const graphStatisticKeys = [
  'items_received',
  'items_rejected',
  'nodes_extracted',
  'nodes_valid',
  'nodes_merged',
  'nodes_succeeded',
  'nodes_added',
  'nodes_failed',
  'relations_extracted',
  'relations_valid',
  'relations_merged',
  'relations_succeeded',
  'relations_added',
  'relations_failed',
  'validation_failed',
  'write_failed',
  'failure_count',
] as const

function objectOutput(node: KnowledgeTraceNode): Record<string, unknown> | null {
  return node.output && typeof node.output === 'object' && !Array.isArray(node.output)
    ? node.output as Record<string, unknown>
    : null
}

function aggregateGraphOutput(
  graphChildren: KnowledgeTraceNode[],
  counts: Record<string, number>,
): Record<string, unknown> {
  const output: Record<string, unknown> = {
    chunk_count: graphChildren.length,
    status_counts: counts,
  }
  const totals: Record<string, number> = {}
  const failures: Record<string, unknown>[] = []

  graphChildren.forEach((child) => {
    const childOutput = objectOutput(child)
    if (childOutput) {
      for (const key of graphStatisticKeys) {
        const value = childOutput[key]
        if (typeof value === 'number' && Number.isFinite(value)) {
          totals[key] = (totals[key] || 0) + value
        }
      }
    }
    const chunkMatch = graphChunkName.exec(child.name)
    const chunkIndex = chunkMatch ? Number(chunkMatch[1]) : undefined
    const childFailures = childOutput?.failures
    if (Array.isArray(childFailures)) {
      for (const failure of childFailures) {
        if (failures.length >= 50) break
        if (!failure || typeof failure !== 'object' || Array.isArray(failure)) continue
        failures.push({
          ...(failure as Record<string, unknown>),
          chunk_index: chunkIndex,
        })
      }
    }
    // A fatal chunk stores its reason on the span rather than in output.
    // Include it in the virtual group so users do not need to open every
    // child row to discover which chunk failed and why.
    if (child.status === 'failed' && (child.error_code || child.error_message)) {
      totals.failure_count = (totals.failure_count || 0) + 1
      if (failures.length < 50) {
        failures.push({
          stage: 'graph_chunk',
          kind: 'chunk',
          chunk_index: chunkIndex,
          error_code: child.error_code,
          reason: child.error_message || child.error_code,
        })
      }
    }
  })

  Object.assign(output, totals)
  if (failures.length > 0) output.failures = failures
  return output
}

/**
 * Groups persisted postprocess.graph.chunk[i] spans into one derived graph
 * node. The derived duration is wall-clock time from the first graph worker
 * start to the final graph worker finish; children retain per-chunk detail.
 */
export function groupPostprocessGraphSpans(
  stage: KnowledgeTraceNode,
): KnowledgeTraceNode {
  const children = stage.children || []
  const graphChildren = children.filter(child => graphChunkName.test(child.name))
  if (graphChildren.length === 0) return stage

  const starts = graphChildren
    .map(child => timestamp(child.started_at))
    .filter((value): value is number => value !== null)
  const ends = graphChildren
    .map(nodeEnd)
    .filter((value): value is number => value !== null)
  const status = aggregateStatus(graphChildren)
  const start = starts.length > 0 ? Math.min(...starts) : null
  const terminal = status !== 'running'
  const end = terminal && ends.length > 0 ? Math.max(...ends) : null
  const counts = graphChildren.reduce<Record<string, number>>((result, child) => {
    result[child.status] = (result[child.status] || 0) + 1
    return result
  }, {})

  const group: KnowledgeTraceNode = {
    span_id: `virtual:postprocess.graph:${stage.span_id || 'stage'}`,
    parent_span_id: stage.span_id,
    name: 'postprocess.graph',
    kind: 'group',
    status,
    started_at: start === null ? null : new Date(start).toISOString(),
    finished_at: end === null ? null : new Date(end).toISOString(),
    duration_ms: start !== null && end !== null ? Math.max(0, end - start) : undefined,
    input: { chunk_count: graphChildren.length },
    output: aggregateGraphOutput(graphChildren, counts),
    children: graphChildren,
  }

  let inserted = false
  const groupedChildren: KnowledgeTraceNode[] = []
  for (const child of children) {
    if (graphChunkName.test(child.name)) {
      if (!inserted) {
        groupedChildren.push(group)
        inserted = true
      }
      continue
    }
    groupedChildren.push(child)
  }

  return { ...stage, children: groupedChildren }
}

/**
 * Counts leaf postprocess spans so the UI can distinguish the five main
 * pipeline stages from asynchronous enrichment work.
 */
export function summarizePostprocessTasks(
  trace?: KnowledgeTraceNode,
): PostprocessTaskSummary {
  const summary: PostprocessTaskSummary = {
    running: 0,
    failed: 0,
    completed: 0,
    other: 0,
    total: 0,
  }
  if (!trace) return summary

  const postprocess = trace.name === 'postprocess'
    ? trace
    : (trace.children || []).find(child => child.name === 'postprocess')
  if (!postprocess) return summary

  const countLeaves = (node: KnowledgeTraceNode) => {
    const children = node.children || []
    if (children.length > 0) {
      children.forEach(countLeaves)
      return
    }

    summary.total++
    switch (node.status) {
      case 'running':
      case 'pending':
      case 'processing':
      case 'finalizing':
        summary.running++
        break
      case 'failed':
        summary.failed++
        break
      case 'done':
      case 'completed':
        summary.completed++
        break
      default:
        summary.other++
    }
  }

  ;(postprocess.children || []).forEach(countLeaves)
  return summary
}
