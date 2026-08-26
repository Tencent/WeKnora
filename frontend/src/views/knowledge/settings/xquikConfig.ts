export const XQUIK_DEFAULT_RESULTS_PER_QUERY = 100
export const XQUIK_MAX_RESULTS_PER_QUERY = 1000
export const XQUIK_MAX_QUERIES = 20
export const XQUIK_MAX_QUERY_LENGTH = 512

export type XquikValidationError =
  | 'queriesRequired'
  | 'tooManyQueries'
  | 'queryTooLong'
  | 'resultsOutOfRange'

export function xquikQueries(value: unknown): string[] {
  const seen = new Set<string>()
  const queries: string[] = []
  for (const line of String(value ?? '').replaceAll('\r\n', '\n').split('\n')) {
    const query = line.trim()
    if (!query || seen.has(query)) continue
    seen.add(query)
    queries.push(query)
  }
  return queries
}

export function validateXquikSettings(settings: Record<string, unknown>): XquikValidationError | null {
  const queries = xquikQueries(settings.queries)
  if (queries.length === 0) return 'queriesRequired'
  if (queries.length > XQUIK_MAX_QUERIES) return 'tooManyQueries'
  if (queries.some(query => Array.from(query).length > XQUIK_MAX_QUERY_LENGTH)) {
    return 'queryTooLong'
  }

  const results = Number(settings.results_per_query ?? XQUIK_DEFAULT_RESULTS_PER_QUERY)
  if (!Number.isInteger(results) || results < 1 || results > XQUIK_MAX_RESULTS_PER_QUERY) {
    return 'resultsOutOfRange'
  }
  return null
}

export function xquikSettingsSignature(settings: Record<string, unknown>): string {
  return JSON.stringify({
    queries: xquikQueries(settings.queries),
    resultsPerQuery: Number(settings.results_per_query ?? XQUIK_DEFAULT_RESULTS_PER_QUERY),
  })
}

export function xquikResourceList(settings: Record<string, unknown>): Array<{
  external_id: string
  name: string
  type: string
  description: string
  url: string
}> {
  return xquikQueries(settings.queries).map(query => ({
    external_id: query,
    name: query,
    type: 'search_query',
    description: 'Import matching public X posts through Xquik',
    url: '',
  }))
}

export function xquikValidationCredentials(
  credentials: Record<string, unknown>,
  settings: Record<string, unknown>,
): Record<string, unknown> {
  return {
    ...credentials,
    queries: settings.queries,
    results_per_query: settings.results_per_query ?? XQUIK_DEFAULT_RESULTS_PER_QUERY,
  }
}
