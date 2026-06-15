export type CitationChunkCacheValue = { content: string; error?: string }

const chunkCache = new Map<string, CitationChunkCacheValue>()

export function makeCitationCacheKey(scope: string, chunkId: string): string {
  return `${scope}::${chunkId}`
}

export function getCitationChunkCache(
  scope: string,
  chunkId: string,
): CitationChunkCacheValue | undefined {
  return chunkCache.get(makeCitationCacheKey(scope, chunkId))
}

export function setCitationChunkCache(
  scope: string,
  chunkId: string,
  value: CitationChunkCacheValue,
): void {
  chunkCache.set(makeCitationCacheKey(scope, chunkId), value)
}

export function clearCitationChunkCache(scope?: string): void {
  if (scope === undefined) {
    chunkCache.clear()
    return
  }
  const prefix = `${scope}::`
  for (const key of chunkCache.keys()) {
    if (key.startsWith(prefix)) {
      chunkCache.delete(key)
    }
  }
}
