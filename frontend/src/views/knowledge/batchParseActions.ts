export interface BatchParseKnowledgeItem {
  id: string
  parse_status?: string
}

/**
 * Return only selected documents that are still owned by the parse pipeline.
 * Missing rows are deliberately excluded so a stale selection cannot turn a
 * batch action into a request for an unrelated or already-finished document.
 */
export function getCancellableKnowledgeIds(
  items: readonly BatchParseKnowledgeItem[],
  selectedIds: Iterable<string>,
  isInFlight: (status?: string) => boolean,
): string[] {
  const itemsById = new Map(items.map((item) => [item.id, item]))
  return Array.from(selectedIds).filter((id) => {
    const item = itemsById.get(id)
    return item != null && isInFlight(item.parse_status)
  })
}
