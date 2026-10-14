/** Letter shown beside the i-th option of a docked prompt: A, B, C… */
export function optionKey(index: number): string {
  return index >= 0 && index < 26 ? String.fromCharCode(65 + index) : ''
}

/** Index of the option a key press picks, or -1. */
export function optionIndexForKey(key: string): number {
  if (key.length !== 1) return -1
  const code = key.toUpperCase().charCodeAt(0)
  return code >= 65 && code <= 90 ? code - 65 : -1
}

/** Whether key presses on el are typing rather than shortcuts. */
export function isTextEntry(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false
  return el.isContentEditable || el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT'
}

/** Enter that submits: not composing an IME candidate, not a soft line break. */
export function isSubmitKey(e: KeyboardEvent): boolean {
  // Let focused controls perform their own action (especially Reject/Skip).
  // The dock receives bubbled keydowns from both its cards and its header.
  // A picked option keeps focus after the click, and Enter then submits it.
  const target = e.target as Element | null
  const control = target?.closest?.('button, a, [role="button"]')
  if (control && (control.getAttribute?.('role') !== 'option'
    || control.getAttribute?.('aria-selected') !== 'true')) return false
  return e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229
}
