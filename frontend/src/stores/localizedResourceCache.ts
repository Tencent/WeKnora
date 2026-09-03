/**
 * Helpers for short-TTL caches of API payloads that vary by Accept-Language
 * (built-in agent names/descriptions, shared-agent lists, …).
 *
 * The request must stamp the locale it was *started* with. Stamping
 * getCurrentLanguage() after await lets a zh-CN response be marked fresh for
 * en-US when the user switches language while the call is in flight.
 */

export function isLocalizedCacheFresh(
  loadedAt: number,
  loadedLocale: string,
  currentLocale: string,
  ttlMs: number,
  now = Date.now(),
): boolean {
  return loadedAt > 0 && now - loadedAt < ttlMs && loadedLocale === currentLocale
}

/** Reuse an in-flight list request only when it was started for this UI language. */
export function shouldReuseLocalizedInflight(
  hasInflight: boolean,
  inflightLocale: string,
  currentLocale: string,
): boolean {
  return hasInflight && inflightLocale !== '' && inflightLocale === currentLocale
}

/**
 * Force a follow-up fetch when the in-flight request was started for a
 * different UI language. versionedRequestCoordinator reuses in-flight on
 * non-force fetch, so locale mismatch must opt into force.
 */
export function shouldForceLocalizedRefetch(
  hasInflight: boolean,
  inflightLocale: string,
  currentLocale: string,
): boolean {
  return hasInflight && inflightLocale !== '' && inflightLocale !== currentLocale
}

/** Drop a completed payload when a newer generation (or language switch) superseded it. */
export function shouldCommitLocalizedGeneration(
  requestGen: number,
  currentGen: number,
): boolean {
  return requestGen === currentGen
}
