/** Pure helpers for chat folder-scope selection. No Vue/Pinia deps. */

/** Collect folder ids from @mention items (filter + dedupe, drop empty ids). */
export function collectFolderIdsFromMentions(items: { type: string; id?: string }[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const item of items) {
    if (item.type !== 'folder' || !item.id) continue
    if (seen.has(item.id)) continue
    seen.add(item.id)
    out.push(item.id)
  }
  return out
}

/** Build the `folder_ids` request field: undefined when empty so it is omitted. */
export function buildFolderIdsField(ids: string[]): string[] | undefined {
  return ids.length > 0 ? ids : undefined
}

/** Partition folder ids into those resolvable (valid) vs not (invalid, e.g. deleted). */
export function partitionValidFolderIds(
  folderIds: string[],
  resolvableIds: Set<string>,
): { valid: string[]; invalid: string[] } {
  const valid: string[] = []
  const invalid: string[] = []
  for (const id of folderIds) {
    if (resolvableIds.has(id)) valid.push(id)
    else invalid.push(id)
  }
  return { valid, invalid }
}
