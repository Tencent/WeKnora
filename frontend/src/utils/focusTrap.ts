const FOCUSABLE_SELECTOR = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

export function getFocusableElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR))
    .filter((element) => {
      if (element.hidden || element.getClientRects().length === 0) return false
      if (element.getAttribute('aria-disabled') === 'true') return false
      if (element.closest('[inert], [aria-hidden="true"]')) return false
      const style = window.getComputedStyle(element)
      return style.display !== 'none' && style.visibility !== 'hidden' && style.visibility !== 'collapse'
    })
}

export function focusFirstElement(container: HTMLElement, preferredSelector?: string) {
  const focusable = getFocusableElements(container)
  const preferred = preferredSelector
    ? container.querySelector<HTMLElement>(preferredSelector)
    : null
  const target = preferred && focusable.includes(preferred)
    ? preferred
    : focusable[0] ?? container
  if (!container.hasAttribute('tabindex')) container.setAttribute('tabindex', '-1')
  target.focus({ preventScroll: true })
}

export function trapTabKey(event: KeyboardEvent, container: HTMLElement): boolean {
  if (event.key !== 'Tab') return false
  const focusable = getFocusableElements(container)
  if (!focusable.length) {
    event.preventDefault()
    container.focus({ preventScroll: true })
    return true
  }

  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  const active = document.activeElement
  if (event.shiftKey && (active === first || !container.contains(active))) {
    event.preventDefault()
    last.focus({ preventScroll: true })
    return true
  }
  if (!event.shiftKey && (active === last || !container.contains(active))) {
    event.preventDefault()
    first.focus({ preventScroll: true })
    return true
  }
  return false
}
