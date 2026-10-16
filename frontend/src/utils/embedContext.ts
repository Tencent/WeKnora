/** Opens the host-context envelope produced by buildQueryWithHostContext. */
export const HOST_CONTEXT_MARKER = '[Host context]\n'

/** Prefix host-injected context onto the user query for embed chat. */
export function buildQueryWithHostContext(
  query: string,
  hostContext?: Record<string, unknown>,
): string {
  if (!hostContext || !Object.keys(hostContext).length) return query
  const lines = Object.entries(hostContext)
    .filter(([, v]) => v !== undefined && v !== null && v !== '')
    .map(([k, v]) => `${k}: ${typeof v === 'string' ? v : JSON.stringify(v)}`)
  if (!lines.length) return query
  return `${HOST_CONTEXT_MARKER}${lines.join('\n')}\n\n${query}`
}

/**
 * Strip the host-context envelope from a message before showing it.
 *
 * buildQueryWithHostContext folds the injected context into the same string as
 * the visitor's question, so that composed text is what the server stores as
 * the user message and echoes back in history. Rendering it verbatim exposes
 * internal fields (userId, page paths, ...) in the visitor's bubble, so every
 * embed surface that displays a user message — or derives an artifact from
 * one — goes through here first. Text without the envelope is returned as is.
 *
 * The envelope closes on the first blank line, matching the
 * "<marker><lines>\n\n<query>" shape the builder always emits.
 */
export function stripHostContextEnvelope(query: string): string {
  if (!query.startsWith(HOST_CONTEXT_MARKER)) return query
  const close = query.indexOf('\n\n', HOST_CONTEXT_MARKER.length)
  if (close === -1) return query
  return query.slice(close + 2)
}
