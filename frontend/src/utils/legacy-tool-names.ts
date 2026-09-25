/**
 * Legacy agent tool name normalization.
 *
 * The backend consolidated the knowledge-retrieval tools:
 *   - knowledge_search / grep_chunks                      → search_knowledge
 *   - list_knowledge_chunks / get_document_info /
 *     wiki_read_source_doc                                → read_document
 *
 * Agent configs saved before that change still carry the retired names in
 * `allowed_tools`. The editor maps them to the new names on load so the tool
 * checkboxes render correctly; chat history rendering keeps accepting the old
 * names as-is (see `AgentStreamDisplay.vue`), so this helper is ONLY for
 * config-shaped tool lists, never for tool-call events.
 */
export const LEGACY_TOOL_NAME_MAP: Readonly<Record<string, string>> = Object.freeze({
  knowledge_search: 'search_knowledge',
  grep_chunks: 'search_knowledge',
  list_knowledge_chunks: 'read_document',
  get_document_info: 'read_document',
  wiki_read_source_doc: 'read_document',
})

/** Map a single tool name to its current name (identity for non-legacy names). */
export function normalizeLegacyToolName(toolName: string): string {
  return LEGACY_TOOL_NAME_MAP[toolName] ?? toolName
}

/**
 * Normalize an `allowed_tools`-style list: retired names are replaced by their
 * successors, duplicates are dropped (first occurrence wins), order otherwise
 * preserved. Non-string / empty entries are dropped. Always returns a new array.
 */
export function normalizeLegacyToolNames(
  tools: readonly unknown[] | undefined | null,
): string[] {
  if (!Array.isArray(tools) || tools.length === 0) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of tools) {
    if (typeof raw !== 'string') continue
    const name = normalizeLegacyToolName(raw.trim())
    if (!name || seen.has(name)) continue
    seen.add(name)
    out.push(name)
  }
  return out
}

/** True iff the list contains the tool, by current OR retired name. */
export function allowedToolsInclude(
  tools: readonly string[] | undefined | null,
  toolName: string,
): boolean {
  if (!tools || tools.length === 0) return false
  const target = normalizeLegacyToolName(toolName)
  return tools.some(t => normalizeLegacyToolName(t) === target)
}
