import { MessagePlugin } from 'tdesign-vue-next'
import i18n from '@/i18n'

/**
 * Copy text to the clipboard, falling back to a hidden textarea +
 * document.execCommand('copy') when the async Clipboard API is unavailable
 * (e.g. on non-secure HTTP origins) or rejects (e.g. permission denied).
 *
 * Returns true on success, false otherwise. This helper is UI-agnostic —
 * callers own any feedback. Use copyWithToast for the common "copy + toast"
 * case; call this directly when you need custom feedback (e.g. inline DOM
 * state) or none at all.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  if (!text) return false

  // Prefer the async Clipboard API where available (HTTPS / localhost).
  if (navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Fall through to the legacy path (e.g. permission denied).
    }
  }

  // Legacy fallback that also works on non-secure (HTTP) origins.
  try {
    const textArea = document.createElement('textarea')
    textArea.value = text
    textArea.style.position = 'fixed'
    textArea.style.top = '0'
    textArea.style.left = '0'
    textArea.style.opacity = '0'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(textArea)
    return ok
  } catch {
    return false
  }
}

/**
 * Copy text and surface the result via a toast. Returns the underlying
 * copyToClipboard result so callers can run extra side effects on success
 * (e.g. closing a dialog). Empty / null / undefined text is a silent no-op
 * (no toast), so callers can pass optional values (e.g. obj.value?.id)
 * without a separate guard. successKey is required; failureKey defaults to
 * the common message.
 */
export async function copyWithToast(
  text: string | null | undefined,
  successKey: string,
  failureKey: string = 'common.copyFailed',
): Promise<boolean> {
  if (!text) return false
  const ok = await copyToClipboard(text)
  if (ok) {
    MessagePlugin.success(i18n.global.t(successKey))
  } else {
    MessagePlugin.error(i18n.global.t(failureKey))
  }
  return ok
}
