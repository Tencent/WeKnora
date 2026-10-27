/**
 * Editors that can lose work register here, so whatever wants to move the
 * user elsewhere (the assistant opening a page) can check first.
 */
type UnsavedCheck = () => boolean

const checks = new Set<UnsavedCheck>()

/** Registers a check for unsaved changes; returns the unregister function. */
export function registerUnsavedChanges(check: UnsavedCheck): () => void {
  checks.add(check)
  return () => {
    checks.delete(check)
  }
}

/** Whether any open editor has changes that leaving would lose. */
export function hasUnsavedChanges(): boolean {
  for (const check of checks) {
    try {
      if (check()) return true
    } catch {
      // An editor tearing down cannot hold unsaved work.
    }
  }
  return false
}
