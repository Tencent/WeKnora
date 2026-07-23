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
  if (nodes.some(node => node.status === 'failed')) return 'failed'
  if (nodes.every(node => node.status === 'skipped')) return 'skipped'
  if (nodes.some(node => node.status === 'cancelled')) return 'cancelled'
  return 'done'
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
    output: { chunk_count: graphChildren.length, status_counts: counts },
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
