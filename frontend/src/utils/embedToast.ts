/** Lightweight toast for embed bundle (no TDesign). */
export function embedToast(message: string, type: 'error' | 'info' = 'error') {
  if (!message || typeof document === 'undefined') return
  const el = document.createElement('div')
  el.textContent = message
  el.setAttribute('role', 'alert')
  Object.assign(el.style, {
    position: 'fixed',
    left: '50%',
    top: '16px',
    transform: 'translateX(-50%)',
    zIndex: '2147483647',
    maxWidth: 'min(90vw, 420px)',
    padding: '10px 14px',
    borderRadius: '8px',
    fontSize: '14px',
    lineHeight: '1.4',
    boxShadow: '0 4px 16px rgba(0,0,0,.12)',
    background: type === 'error' ? '#fff1f0' : '#f6ffed',
    color: type === 'error' ? '#cf1322' : '#389e0d',
    border: `1px solid ${type === 'error' ? '#ffccc7' : '#b7eb8f'}`,
  })
  document.body.appendChild(el)
  window.setTimeout(() => el.remove(), 4000)
}
