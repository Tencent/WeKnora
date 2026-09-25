/**
 * Helpers for cached API payloads that vary by Accept-Language
 * (built-in agent names/descriptions, shared-agent lists, …).
 *
 * The request must stamp the locale it was *started* with. Stamping
 * getCurrentLanguage() after await lets a zh-CN response be marked as the
 * en-US snapshot when the user switches language while the call is in flight.
 */

/** A localized snapshot is only usable for the UI language it was fetched in. */
export function isLocalizedSnapshotUsable(
  loaded: boolean,
  loadedLocale: string,
  currentLocale: string,
): boolean {
  return loaded && loadedLocale !== '' && loadedLocale === currentLocale
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
