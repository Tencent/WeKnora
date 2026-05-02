/**
 * Shared localStorage utilities for per-user UI preferences (theme, fonts).
 *
 * Storage layout: WeKnora_${userId}_${suffix}, where userId is the active
 * user's id or "anon" before login. Read paths are intentionally narrow —
 * no cross-namespace fallbacks — so one user's preferences cannot bleed
 * into another user's session.
 *
 * One-shot migration adopts pre-existing values into the current user's
 * namespace at login time:
 *   - Legacy un-namespaced keys (WeKnora_${suffix}) from earlier branch
 *     versions are inherited and removed.
 *   - The "anon" namespace (used while no user is logged in) is also
 *     adopted and cleared, so the next user to log in cannot inherit it.
 */

const PREFERENCE_SUFFIXES = [
  'theme',
  'font_sans',
  'font_mono',
  'font_size',
] as const

export function readUserId(): string {
  try {
    const raw = localStorage.getItem('weknora_user')
    if (!raw) return 'anon'
    const parsed = JSON.parse(raw)
    return parsed?.id ? String(parsed.id) : 'anon'
  } catch {
    return 'anon'
  }
}

export function safeGetItem(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

export function safeSetItem(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch (err) {
    // Quota exceeded, disabled storage, private mode — surface in DevTools
    // so the issue is at least diagnosable, but don't break the UI.
    console.warn(`[WeKnora] failed to persist preference "${key}":`, err)
  }
}

export function safeRemoveItem(key: string): void {
  try {
    localStorage.removeItem(key)
  } catch {
    // Same conditions as setItem; silent best-effort.
  }
}

export function userKey(suffix: string): string {
  return `WeKnora_${readUserId()}_${suffix}`
}

export function loadPreference(suffix: string): string | null {
  return safeGetItem(userKey(suffix))
}

export function savePreference(suffix: string, value: string): void {
  safeSetItem(userKey(suffix), value)
}

let migratedForUser: string | null = null

/**
 * Adopt legacy and anon preferences into the current user's namespace, then
 * remove the source keys. Idempotent per session per user — repeat calls for
 * the same userId are no-ops. Safe to call before the user is logged in
 * (it returns early when userId === "anon").
 */
export function migratePreferencesIntoUser(): void {
  const userId = readUserId()
  if (userId === 'anon') return
  if (migratedForUser === userId) return
  migratedForUser = userId

  for (const suffix of PREFERENCE_SUFFIXES) {
    const target = `WeKnora_${userId}_${suffix}`
    const targetExists = safeGetItem(target) !== null

    const anonKey = `WeKnora_anon_${suffix}`
    const legacyKey = `WeKnora_${suffix}`

    if (!targetExists) {
      const anonValue = safeGetItem(anonKey)
      if (anonValue !== null) {
        safeSetItem(target, anonValue)
      } else {
        const legacyValue = safeGetItem(legacyKey)
        if (legacyValue !== null) {
          safeSetItem(target, legacyValue)
        }
      }
    }

    // Always clean up source keys so subsequent users cannot inherit them.
    safeRemoveItem(anonKey)
    safeRemoveItem(legacyKey)
  }
}

/** Resets the per-session migration latch (used when the active user changes). */
export function resetMigrationLatch(): void {
  migratedForUser = null
}

// Run migration once at module load so the composables that read from
// storage see post-migration values when initialising their refs.
migratePreferencesIntoUser()
