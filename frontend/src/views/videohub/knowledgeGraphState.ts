export const GRAPH_FILTER_TYPES: Record<string, string> = {
  实体: 'entity',
  概念: 'concept',
  方法论: 'methodology',
  案例: 'case',
  洞察: 'insight',
}

export function graphTypesForAttribute(attribute: string): string[] | undefined {
  const value = GRAPH_FILTER_TYPES[attribute]
  return attribute === 'all' || !value ? undefined : [value]
}

export function isWikiPageId(value: string): boolean {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value)
}

export function createRequestGate() {
  let activeSequence = 0

  return {
    next(): number {
      activeSequence += 1
      return activeSequence
    },
    invalidate(): void {
      activeSequence += 1
    },
    isCurrent(sequence: number): boolean {
      return sequence === activeSequence
    },
  }
}

export function graphContainsSelection(
  payload: { nodes: Array<{ id: string }>; wiki_pages?: Array<{ id: string }> },
  selectedNodeId: string | null,
): boolean {
  if (!selectedNodeId) return true
  return payload.nodes.some(node => node.id === selectedNodeId)
    || payload.wiki_pages?.some(page => `wiki:${page.id}` === selectedNodeId)
    || false
}
