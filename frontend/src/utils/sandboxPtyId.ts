/**
 * Persist the remote shell PID across a tab refresh / panel remount.
 *
 * Reattach-capable providers leave bash in the sandbox so Pty.Connect can
 * reconnect. lastPid used to live only in the
 * composable closure, so a refresh created a new shell and left the old
 * one orphaned. sessionStorage is tab-scoped: a new tab starts clean, this
 * tab's F5 does not.
 */

const KEY_PREFIX = 'weknora_sandbox_pty:'

export function sandboxPtyStorageKey(sessionId: string): string {
  return `${KEY_PREFIX}${sessionId.trim()}`
}

export function readStoredPtyId(sessionId: string): number | null {
  const sid = sessionId.trim()
  if (!sid || typeof sessionStorage === 'undefined') return null
  try {
    const raw = sessionStorage.getItem(sandboxPtyStorageKey(sid))
    const pid = Number(raw)
    if (!Number.isInteger(pid) || pid <= 0) return null
    return pid
  } catch {
    return null
  }
}

export function writeStoredPtyId(sessionId: string, pid: number | null): void {
  const sid = sessionId.trim()
  if (!sid || typeof sessionStorage === 'undefined') return
  try {
    const key = sandboxPtyStorageKey(sid)
    if (pid == null || pid <= 0) {
      sessionStorage.removeItem(key)
      return
    }
    sessionStorage.setItem(key, String(pid))
  } catch {
    // Private mode can throw; the provider then degrades to Create.
  }
}
