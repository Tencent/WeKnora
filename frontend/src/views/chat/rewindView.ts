/**
 * Client-side rewind helpers. The HTTP call mutates the source session on
 * the server; these decide whether that result may touch the live chat view.
 */

export function shouldApplyRewindLocally(currentSessionId: string, sourceSessionId: string): boolean {
  return Boolean(currentSessionId) && currentSessionId === sourceSessionId
}

export function rewindPrefillText(role: unknown, content: unknown): string {
  if (role !== 'user') {
    return ''
  }
  return String(content ?? '')
}
