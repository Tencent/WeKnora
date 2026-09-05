export interface EmbedChatPayload {
  query: string
  prompt_context?: string
}

/** Build an embed chat payload without mixing host context into the retrieval query. */
export function buildEmbedChatPayload(
  query: string,
  hostContext?: Record<string, unknown>,
): EmbedChatPayload {
  if (!hostContext || !Object.keys(hostContext).length) return { query }
  const entries = Object.entries(hostContext)
    .filter(([, v]) => v !== undefined && v !== null && v !== '')
  if (!entries.length) return { query }

  return {
    query,
    prompt_context: `[Host context]\n${JSON.stringify(Object.fromEntries(entries), null, 2)}`,
  }
}
